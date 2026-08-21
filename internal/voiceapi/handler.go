// Package voiceapi adapts the versioned voice WebSocket protocol to a coordinator.
package voiceapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/androidupdate"
	"github.com/lkarlslund/koder/internal/deviceauth"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/version"
	"github.com/lkarlslund/koder/internal/voice"
)

const protocolVersion = "voice.v1"
const readLimit = 256 * 1024
const sessionRequestLimit = 4 * 1024
const delegationTimeout = 30 * time.Minute

type backend interface {
	voice.Backend
	voice.SpeechBackend
	voice.VoiceSessionBackend
	voice.ArtifactBackend
}

type androidUpdateSource interface {
	Manifest() (androidupdate.Manifest, bool, error)
	OpenAPK() (fs.File, error)
}

// Handler serves voice calls for one Koder process.
type Handler struct {
	Backend backend
	Token   string
	Lease   *voice.CallLease
	Updates androidUpdateSource
	Devices *phonedevice.Hub
	Auth    *deviceauth.Registry
	turns   *turnRegistry
	browser browserTickets
}

// ChatPresence describes the live device holding a voice chat lease.
type ChatPresence struct {
	Occupied       bool      `json:"occupied"`
	OwnedByBrowser bool      `json:"owned_by_browser"`
	DeviceKind     string    `json:"device_kind,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
}

// VoiceChatPresence reports whether a selected voice chat is currently in use.
func (h *Handler) VoiceChatPresence(chatID, browserClientID string) ChatPresence {
	chatID = strings.TrimSpace(chatID)
	browserDeviceID := "browser:" + strings.TrimSpace(browserClientID)
	for _, active := range h.Lease.Snapshots() {
		if active.VoiceSessionID != chatID {
			continue
		}
		kind := "phone"
		if strings.HasPrefix(active.DeviceID, "browser:") {
			kind = "browser"
		}
		return ChatPresence{
			Occupied:       true,
			OwnedByBrowser: active.DeviceID == browserDeviceID,
			DeviceKind:     kind,
			StartedAt:      active.StartedAt,
		}
	}
	return ChatPresence{}
}

type browserDeviceContextKey struct{}

// MintBrowserTicket creates a short-lived, single-use WebSocket credential
// for an already-connected browser client.
func (h *Handler) MintBrowserTicket(clientID string) (string, time.Time, error) {
	return h.browser.mint("browser:" + strings.TrimSpace(clientID))
}

// NewHandler creates a process-scoped voice protocol handler.
func NewHandler(backend backend, token string) *Handler {
	handler := &Handler{Backend: backend, Token: token, Lease: voice.NewCallLease(), Updates: androidupdate.Embedded(), turns: newTurnRegistry()}
	if source, ok := backend.(interface{ PhoneDeviceHub() *phonedevice.Hub }); ok {
		handler.Devices = source.PhoneDeviceHub()
	}
	return handler
}

type clientFrame struct {
	Type              string                          `json:"type"`
	Protocol          string                          `json:"protocol,omitempty"`
	UtteranceID       string                          `json:"utterance_id,omitempty"`
	Text              string                          `json:"text,omitempty"`
	SessionID         string                          `json:"session_id,omitempty"`
	ChatID            string                          `json:"chat_id,omitempty"`
	VoiceSessionID    string                          `json:"voice_session_id,omitempty"`
	Title             string                          `json:"title,omitempty"`
	AudioFormat       *voice.AudioFormat              `json:"audio_format,omitempty"`
	Languages         []string                        `json:"languages,omitempty"`
	BeforeID          string                          `json:"before_id,omitempty"`
	Limit             int                             `json:"limit,omitempty"`
	Query             string                          `json:"query,omitempty"`
	ResponsePacing    string                          `json:"response_pacing,omitempty"`
	AudioEncodings    []string                        `json:"audio_encodings,omitempty"`
	InputTransport    *voice.AudioTransportPreference `json:"input_transport_preference,omitempty"`
	OutputTransport   *voice.AudioTransportPreference `json:"output_transport_preference,omitempty"`
	Backend           domain.ChatBackend              `json:"backend,omitempty"`
	WorkflowRole      domain.WorkflowRole             `json:"workflow_role,omitempty"`
	ProviderID        string                          `json:"provider_id,omitempty"`
	ModelID           string                          `json:"model_id,omitempty"`
	PermissionProfile string                          `json:"permission_profile,omitempty"`
	MilestoneKey      string                          `json:"milestone_key,omitempty"`
	TaskRef           string                          `json:"task_ref,omitempty"`
	ToolStates        domain.ToolStates               `json:"tool_states,omitempty"`
}

type serverFrame struct {
	Type          string                         `json:"type"`
	Protocol      string                         `json:"protocol"`
	UtteranceID   string                         `json:"utterance_id,omitempty"`
	CallState     *voice.CallState               `json:"call_state,omitempty"`
	AudioConfig   *voice.AudioConfig             `json:"audio_config,omitempty"`
	AudioFormat   *voice.AudioFormat             `json:"audio_format,omitempty"`
	Transcript    string                         `json:"transcript,omitempty"`
	State         string                         `json:"state,omitempty"`
	WorkingOn     *voice.Session                 `json:"working_on,omitempty"`
	Message       *voice.Message                 `json:"message,omitempty"`
	Parts         []voice.Part                   `json:"parts,omitempty"`
	Error         string                         `json:"error,omitempty"`
	ErrorCode     string                         `json:"error_code,omitempty"`
	ServerTime    time.Time                      `json:"server_time,omitempty"`
	AppUpdate     *androidupdate.Manifest        `json:"app_update,omitempty"`
	History       []voice.TranscriptEntry        `json:"history,omitempty"`
	SearchResults []voice.TranscriptSearchResult `json:"search_results,omitempty"`
	HasMore       bool                           `json:"has_more,omitempty"`
}

type sessionsResponse struct {
	Protocol      string                    `json:"protocol"`
	Session       *voice.Session            `json:"session,omitempty"`
	Sessions      []voice.Session           `json:"sessions,omitempty"`
	Chat          *voice.Chat               `json:"chat,omitempty"`
	Chats         []voice.Chat              `json:"chats,omitempty"`
	VoiceSession  *voice.Session            `json:"voice_session,omitempty"`
	VoiceSessions []voice.Session           `json:"voice_sessions"`
	AppUpdate     *androidupdate.Manifest   `json:"app_update,omitempty"`
	ChatBackends  []voice.ChatBackendOption `json:"chat_backends,omitempty"`
}

type serverInfoResponse struct {
	Protocol              string     `json:"protocol"`
	ServerTime            time.Time  `json:"server_time"`
	Version               string     `json:"version"`
	Commit                string     `json:"commit"`
	Dirty                 string     `json:"dirty"`
	BuildTime             string     `json:"build_time"`
	StartedAt             time.Time  `json:"started_at"`
	UptimeSeconds         int64      `json:"uptime_seconds"`
	Platform              string     `json:"platform"`
	GoVersion             string     `json:"go_version"`
	LogicalCPUs           int        `json:"logical_cpus"`
	MaxProcs              int        `json:"max_procs"`
	Goroutines            int        `json:"goroutines"`
	HeapAllocBytes        uint64     `json:"heap_alloc_bytes"`
	HeapSysBytes          uint64     `json:"heap_sys_bytes"`
	HeapObjects           uint64     `json:"heap_objects"`
	GCCycles              uint32     `json:"gc_cycles"`
	SessionCount          int        `json:"session_count"`
	VoiceSessionCount     int        `json:"voice_session_count"`
	VoiceConnectionCount  int        `json:"voice_connection_count"`
	VoiceConnectionActive bool       `json:"voice_connection_active"`
	VoiceConnectionSince  *time.Time `json:"voice_connection_since,omitempty"`
	TokenRequired         bool       `json:"token_required"`
}

type createSessionRequest struct {
	domain.ChatCreateSpec
}

type updateSessionRequest struct {
	Title    *string `json:"title,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
	Pinned   *bool   `json:"pinned,omitempty"`
	Favorite *bool   `json:"favorite,omitempty"`
	Deleted  *bool   `json:"deleted,omitempty"`
}

type updateChatRequest struct {
	Title      *string `json:"title,omitempty"`
	Archived   *bool   `json:"archived,omitempty"`
	ProviderID *string `json:"provider_id,omitempty"`
	ModelID    *string `json:"model_id,omitempty"`
}

type bindDeviceRequest struct {
	Code   string                `json:"code"`
	Device deviceauth.DeviceInfo `json:"device"`
}

type bindDeviceResponse struct {
	Protocol string             `json:"protocol"`
	Binding  deviceauth.Binding `json:"binding"`
}

type incomingAudio struct {
	utteranceID  string
	format       voice.AudioFormat
	transport    voice.AudioFormat
	decoder      audioPacketDecoder
	nextSequence uint32
	pcm          []byte
	languages    []string
}

func (h *Handler) startTurn(callID, utteranceID, fingerprint string, state voice.CallState, run func(*cachedTurn)) (*cachedTurn, bool, error) {
	return h.turns.start(callID, utteranceID, fingerprint, state.VoiceSessionID, state.SessionID, state.ChatID, run)
}

func normalizeLanguages(values []string) ([]string, error) {
	if len(values) > 8 {
		return nil, fmt.Errorf("audio_start accepts at most 8 language hints")
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		language := strings.ToLower(strings.TrimSpace(value))
		if len(language) != 2 || language[0] < 'a' || language[0] > 'z' || language[1] < 'a' || language[1] > 'z' {
			return nil, fmt.Errorf("invalid ISO 639-1 language hint %q", value)
		}
		if _, ok := seen[language]; ok {
			continue
		}
		seen[language] = struct{}{}
		normalized = append(normalized, language)
	}
	slices.Sort(normalized)
	return normalized, nil
}

func parseTurnResume(r *http.Request) (turnResume, error) {
	query := r.URL.Query()
	resume := turnResume{utteranceID: strings.TrimSpace(query.Get("resume_utterance_id"))}
	for key, target := range map[string]*bool{
		"resume_transcript": &resume.transcriptReceived,
		"resume_message":    &resume.messageReceived,
	} {
		value := strings.TrimSpace(query.Get(key))
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return turnResume{}, fmt.Errorf("invalid %s: %w", key, err)
		}
		*target = parsed
	}
	if value := strings.TrimSpace(query.Get("resume_output_sequence")); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return turnResume{}, fmt.Errorf("invalid resume_output_sequence: %w", err)
		}
		resume.outputSequence = uint32(parsed)
	}
	if resume.utteranceID == "" && (resume.transcriptReceived || resume.messageReceived || resume.outputSequence != 0) {
		return turnResume{}, errors.New("resume cursor requires resume_utterance_id")
	}
	return resume, nil
}

// ServeHTTP accepts one authenticated voice call.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Backend == nil {
		http.Error(w, "voice backend unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/voice/v1/bind" {
		h.serveBindDevice(w, r)
		return
	}
	if r.URL.Path == "/voice/v1/android/koder.apk" && h.Auth != nil && h.Auth.InvitationValid(r.URL.Query().Get("bind_code")) {
		h.serveAndroidAPK(w, r)
		return
	}
	browserDeviceID, browserAuthorized := h.browser.consumeProtocol(r.Header.Get("Sec-WebSocket-Protocol"))
	if !authorized(r, h.Token, h.Auth) && !browserAuthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="koder-voice"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if browserAuthorized {
		r = r.WithContext(context.WithValue(r.Context(), browserDeviceContextKey{}, browserDeviceID))
	}
	if r.URL.Path == "/voice/v1/sessions" {
		h.serveSessions(w, r)
		return
	}
	if r.URL.Path == "/voice/v1/sessions/temporary" {
		h.serveTemporaryVoiceSession(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/voice/v1/sessions/") {
		h.serveVoiceSession(w, r)
		return
	}
	if r.URL.Path == "/voice/v1/server-info" {
		h.serveServerInfo(w, r)
		return
	}
	if r.URL.Path == "/voice/v1/device" {
		h.servePhoneDevice(w, r)
		return
	}
	if r.URL.Path == "/voice/v1/readiness" {
		h.serveReadiness(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/voice/v1/artifacts/") {
		h.serveArtifact(w, r)
		return
	}
	if r.URL.Path == "/voice/v1/android/koder.apk" {
		h.serveAndroidAPK(w, r)
		return
	}
	if r.URL.Path != "/voice/v1" {
		http.NotFound(w, r)
		return
	}
	if h.Lease == nil {
		http.Error(w, "voice call lease unavailable", http.StatusServiceUnavailable)
		return
	}
	callID := strings.TrimSpace(r.URL.Query().Get("call_id"))
	if callID == "" {
		callID = string(id.New())
	}
	resume, err := parseTurnResume(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	requestedSessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	requestedChatID := strings.TrimSpace(r.URL.Query().Get("chat_id"))
	if requestedChatID == "" {
		requestedChatID = strings.TrimSpace(r.URL.Query().Get("voice_chat_id"))
	}
	var call *voice.Call
	leaseTarget := ""
	if requestedSessionID != "" || requestedChatID != "" {
		backend, ok := h.Backend.(voice.SessionChatBackend)
		if !ok {
			http.Error(w, "session chat backend is unavailable", http.StatusServiceUnavailable)
			return
		}
		selected, selectErr := backend.EnsureVoiceChat(r.Context(), requestedSessionID, requestedChatID)
		if selectErr != nil {
			http.Error(w, selectErr.Error(), http.StatusBadRequest)
			return
		}
		call = voice.NewSessionCall(h.Backend, selected.SessionID, selected.ID)
		leaseTarget = selected.ID
	} else {
		voiceSession, ensureErr := h.Backend.EnsureVoiceSession(r.Context(), r.URL.Query().Get("voice_session_id"))
		if ensureErr != nil {
			http.Error(w, ensureErr.Error(), http.StatusBadRequest)
			return
		}
		call = voice.NewCall(h.Backend, voiceSession.ID)
		leaseTarget = voiceSession.ID
	}
	release, replaced, err := h.Lease.AcquireDeviceConnection(voiceDeviceLeaseID(r), callID, leaseTarget)
	if err != nil {
		if err == voice.ErrCallActive {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer release()
	if h.Devices != nil {
		defer h.Devices.DetachCall(callID)
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}, Subprotocols: []string{protocolVersion}})
	if err != nil {
		return
	}
	conn.SetReadLimit(readLimit)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	connectionDone := make(chan struct{})
	defer close(connectionDone)
	go func() {
		select {
		case <-replaced:
			_ = conn.Close(websocket.StatusGoingAway, "reconnected")
		case <-connectionDone:
		}
	}()

	ctx := r.Context()
	var audio *incomingAudio
	responsePacing := voice.ResponsePacingNormal
	baseAudioConfig := h.Backend.VoiceAudioConfig()
	connectionAudioConfig := negotiatedAudioConfig(baseAudioConfig, nil, nil, nil)
	var writeMu sync.Mutex
	if err := writeReady(ctx, conn, &writeMu, call, advertisedAudioConfig(baseAudioConfig), h.Updates); err != nil {
		return
	}
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if messageType == websocket.MessageBinary {
			if err := appendIncomingAudio(connectionAudioConfig, audio, payload); err != nil {
				utteranceID := ""
				if audio != nil {
					utteranceID = audio.utteranceID
				}
				audio = nil
				if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: err.Error()}); writeErr != nil {
					return
				}
				if writeErr := writeReady(ctx, conn, &writeMu, call, connectionAudioConfig, h.Updates); writeErr != nil {
					return
				}
			}
			continue
		}
		if messageType != websocket.MessageText {
			if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: "unsupported WebSocket message type"}); writeErr != nil {
				return
			}
			continue
		}
		var frame clientFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: "invalid JSON: " + err.Error()}); writeErr != nil {
				return
			}
			continue
		}
		if frame.Protocol != "" && frame.Protocol != protocolVersion {
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: fmt.Sprintf("unsupported protocol %q", frame.Protocol)}); err != nil {
				return
			}
			continue
		}
		switch strings.TrimSpace(frame.Type) {
		case "hello":
			parsedPacing, parseErr := voice.ParseResponsePacing(frame.ResponsePacing)
			if parseErr != nil {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: parseErr.Error()}); err != nil {
					return
				}
				continue
			}
			responsePacing = parsedPacing
			connectionAudioConfig = negotiatedAudioConfig(baseAudioConfig, frame.AudioEncodings, frame.InputTransport, frame.OutputTransport)
			if err := writeReady(ctx, conn, &writeMu, call, connectionAudioConfig, h.Updates); err != nil {
				return
			}
		case "ping":
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "pong", ServerTime: time.Now().UTC()}); err != nil {
				return
			}
		case "history":
			page, err := call.History(ctx, frame.BeforeID, frame.Limit)
			if err != nil {
				if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "history", Error: err.Error()}); writeErr != nil {
					return
				}
				continue
			}
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "history", History: page.Entries, HasMore: page.HasMore}); err != nil {
				return
			}
		case "search_history":
			state, stateErr := call.State(ctx)
			if stateErr != nil {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "history_search", Error: stateErr.Error()}); err != nil {
					return
				}
				continue
			}
			var results []voice.TranscriptSearchResult
			var searchErr error
			if state.SessionID != "" && state.ChatID != "" {
				search, ok := h.Backend.(voice.SessionChatHistoryBackend)
				if !ok {
					searchErr = errors.New("transcript search is unavailable")
				} else {
					results, searchErr = search.SearchVoiceChatHistory(ctx, state.SessionID, state.ChatID, frame.Query, frame.Limit)
				}
			} else {
				search, ok := h.Backend.(voice.VoiceHistorySearchBackend)
				if !ok {
					searchErr = errors.New("transcript search is unavailable")
				} else {
					results, searchErr = search.SearchVoiceSessionHistory(ctx, state.VoiceSessionID, frame.Query, frame.Limit)
				}
			}
			if searchErr != nil {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "history_search", Error: searchErr.Error()}); err != nil {
					return
				}
				continue
			}
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "history_search", SearchResults: results}); err != nil {
				return
			}
		case "select_session":
			audio = nil
			message, err := call.SelectSession(ctx, frame.SessionID)
			if err := writeResult(ctx, conn, &writeMu, call, h.Backend, connectionAudioConfig, h.Updates, frame.UtteranceID, "", message, err); err != nil {
				return
			}
		case "select_voice_session":
			audio = nil
			message, err := call.SelectVoiceSession(ctx, frame.VoiceSessionID)
			if err := writeResult(ctx, conn, &writeMu, call, h.Backend, connectionAudioConfig, h.Updates, frame.UtteranceID, "", message, err); err != nil {
				return
			}
		case "create_voice_session":
			audio = nil
			if _, err := call.CreateVoiceSession(ctx, frame.Title); err != nil {
				if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: err.Error()}); writeErr != nil {
					return
				}
				continue
			}
			if err := writeReady(ctx, conn, &writeMu, call, connectionAudioConfig, h.Updates); err != nil {
				return
			}
		case "select_voice_chat":
			audio = nil
			message, err := call.SelectVoiceChat(ctx, frame.SessionID, frame.ChatID)
			if err := writeResult(ctx, conn, &writeMu, call, h.Backend, connectionAudioConfig, h.Updates, frame.UtteranceID, "", message, err); err != nil {
				return
			}
		case "create_voice_chat":
			audio = nil
			if _, err := call.CreateVoiceChat(ctx, frame.SessionID, voiceChatCreateSpec(frame.Title, frame.Backend, frame.WorkflowRole, frame.ProviderID, frame.ModelID, frame.PermissionProfile, frame.MilestoneKey, frame.TaskRef, frame.ToolStates)); err != nil {
				if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: err.Error()}); writeErr != nil {
					return
				}
				continue
			}
			if err := writeReady(ctx, conn, &writeMu, call, connectionAudioConfig, h.Updates); err != nil {
				return
			}
		case "create_temporary_voice_chat":
			audio = nil
			if _, _, err := call.CreateTemporaryVoiceChat(ctx, voiceChatCreateSpec(frame.Title, frame.Backend, frame.WorkflowRole, frame.ProviderID, frame.ModelID, frame.PermissionProfile, frame.MilestoneKey, frame.TaskRef, frame.ToolStates)); err != nil {
				if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: err.Error()}); writeErr != nil {
					return
				}
				continue
			}
			if err := writeReady(ctx, conn, &writeMu, call, connectionAudioConfig, h.Updates); err != nil {
				return
			}
		case "audio_start":
			if audio != nil {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: frame.UtteranceID, Error: "an audio utterance is already recording"}); err != nil {
					return
				}
				continue
			}
			utteranceID := strings.TrimSpace(frame.UtteranceID)
			transportFormat := selectedInputFormat(connectionAudioConfig)
			if utteranceID == "" || frame.AudioFormat == nil || *frame.AudioFormat != transportFormat {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: "audio_start requires an utterance id and the advertised input format"}); err != nil {
					return
				}
				continue
			}
			languages, err := normalizeLanguages(frame.Languages)
			if err != nil {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: err.Error()}); err != nil {
					return
				}
				continue
			}
			audio, err = newIncomingAudio(utteranceID, baseAudioConfig.Input, transportFormat, languages)
			if err != nil {
				if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: "initialize audio decoder: " + err.Error()}); writeErr != nil {
					return
				}
				continue
			}
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "state", UtteranceID: utteranceID, State: "recording"}); err != nil {
				return
			}
		case "audio_cancel":
			audio = nil
			if err := writeReady(ctx, conn, &writeMu, call, connectionAudioConfig, h.Updates); err != nil {
				return
			}
		case "audio_commit":
			if audio == nil || (strings.TrimSpace(frame.UtteranceID) != "" && strings.TrimSpace(frame.UtteranceID) != audio.utteranceID) {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: frame.UtteranceID, Error: "audio_commit does not match an active utterance"}); err != nil {
					return
				}
				continue
			}
			completed := audio
			audio = nil
			if len(completed.pcm) == 0 {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: completed.utteranceID, Error: "audio utterance was empty"}); err != nil {
					return
				}
				if err := writeReady(ctx, conn, &writeMu, call, connectionAudioConfig, h.Updates); err != nil {
					return
				}
				continue
			}
			state, stateErr := call.State(ctx)
			if stateErr != nil {
				if err := writeResult(ctx, conn, &writeMu, call, h.Backend, connectionAudioConfig, h.Updates, completed.utteranceID, "", voice.Message{}, stateErr); err != nil {
					return
				}
				continue
			}
			turn, _, startErr := h.startTurn(callID, completed.utteranceID, audioTurnFingerprint(completed), state, func(turn *cachedTurn) {
				h.runAudioTurn(turn, completed, responsePacing, selectedOutputFormat(connectionAudioConfig))
			})
			if startErr != nil {
				if err := writeResult(ctx, conn, &writeMu, call, h.Backend, connectionAudioConfig, h.Updates, completed.utteranceID, "", voice.Message{}, startErr); err != nil {
					return
				}
				continue
			}
			if err := streamCachedTurn(ctx, conn, &writeMu, turn, resume); err != nil {
				return
			}
			if err := writeReady(ctx, conn, &writeMu, call, connectionAudioConfig, h.Updates); err != nil {
				return
			}
		case "utterance":
			utteranceID := strings.TrimSpace(frame.UtteranceID)
			if utteranceID == "" || strings.TrimSpace(frame.Text) == "" {
				if err := writeResult(ctx, conn, &writeMu, call, h.Backend, connectionAudioConfig, h.Updates, utteranceID, "", voice.Message{}, errors.New("utterance requires an id and text")); err != nil {
					return
				}
				continue
			}
			state, stateErr := call.State(ctx)
			if stateErr != nil {
				if writeErr := writeResult(ctx, conn, &writeMu, call, h.Backend, connectionAudioConfig, h.Updates, utteranceID, frame.Text, voice.Message{}, stateErr); writeErr != nil {
					return
				}
				continue
			}
			turn, _, startErr := h.startTurn(callID, utteranceID, textTurnFingerprint(frame.Text), state, func(turn *cachedTurn) {
				h.runTextTurn(turn, frame.Text, responsePacing, selectedOutputFormat(connectionAudioConfig))
			})
			if startErr != nil {
				if err := writeResult(ctx, conn, &writeMu, call, h.Backend, connectionAudioConfig, h.Updates, utteranceID, frame.Text, voice.Message{}, startErr); err != nil {
					return
				}
				continue
			}
			if err := streamCachedTurn(ctx, conn, &writeMu, turn, resume); err != nil {
				return
			}
			if err := writeReady(ctx, conn, &writeMu, call, connectionAudioConfig, h.Updates); err != nil {
				return
			}
		default:
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: frame.UtteranceID, Error: fmt.Sprintf("unknown frame type %q", frame.Type)}); err != nil {
				return
			}
		}
	}
}

func (h *Handler) serveServerInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessions, err := h.Backend.ListVoiceSessions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	voiceSessions, err := h.Backend.ListVoiceChats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	build := version.Current()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	sessions = withoutDeletedSessions(sessions)
	voiceSessions = withoutDeletedSessions(voiceSessions)
	response := serverInfoResponse{
		Protocol:             protocolVersion,
		ServerTime:           now,
		Version:              build.Version,
		Commit:               build.Commit,
		Dirty:                build.Dirty,
		BuildTime:            build.BuildTime,
		StartedAt:            build.StartedAt,
		UptimeSeconds:        max(0, int64(now.Sub(build.StartedAt)/time.Second)),
		Platform:             runtime.GOOS + "/" + runtime.GOARCH,
		GoVersion:            build.GoVersion,
		LogicalCPUs:          runtime.NumCPU(),
		MaxProcs:             runtime.GOMAXPROCS(0),
		Goroutines:           runtime.NumGoroutine(),
		HeapAllocBytes:       memory.Alloc,
		HeapSysBytes:         memory.HeapSys,
		HeapObjects:          memory.HeapObjects,
		GCCycles:             memory.NumGC,
		SessionCount:         len(sessions),
		VoiceSessionCount:    len(voiceSessions),
		VoiceConnectionCount: h.Lease.ActiveCount(),
		TokenRequired:        strings.TrimSpace(h.Token) != "" || h.Auth != nil,
	}
	if active, ok := h.Lease.Snapshot(); ok {
		response.VoiceConnectionActive = true
		response.VoiceConnectionSince = &active.StartedAt
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		return
	}
}

func voiceDeviceLeaseID(r *http.Request) string {
	if r != nil {
		if browserID, ok := r.Context().Value(browserDeviceContextKey{}).(string); ok && strings.TrimSpace(browserID) != "" {
			return browserID
		}
		if installationID := strings.TrimSpace(r.Header.Get("X-Koder-Device-ID")); installationID != "" {
			return "installation:" + installationID
		}
	}
	return "legacy"
}

func (h *Handler) serveSessions(w http.ResponseWriter, r *http.Request) {
	var created *voice.Session
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, sessionRequestLimit)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request createSessionRequest
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid session request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid session request: expected one JSON object", http.StatusBadRequest)
			return
		}
		session, err := h.Backend.CreateVoiceSession(r.Context(), strings.TrimSpace(request.Title))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		created = &session
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessions, err := h.Backend.ListVoiceChats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sessions = withoutDeletedSessions(sessions)
	sortVoiceSessions(sessions)
	workSessions, err := h.Backend.ListVoiceSessions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	workSessions = withoutDeletedSessions(workSessions)
	slices.SortStableFunc(workSessions, func(a, b voice.Session) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	response := sessionsResponse{
		Protocol: protocolVersion, Sessions: workSessions, VoiceSession: created, VoiceSessions: sessions,
		ChatBackends: h.chatBackendOptions(r.Context()),
	}
	if h.Updates != nil {
		meta, available, err := h.Updates.Manifest()
		if err != nil {
			http.Error(w, "embedded Android update is invalid", http.StatusInternalServerError)
			return
		}
		if available {
			meta.DownloadURI = "/voice/v1/android/koder.apk"
			response.AppUpdate = &meta
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if created != nil {
		w.WriteHeader(http.StatusCreated)
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		return
	}
}

func (h *Handler) chatBackendOptions(ctx context.Context) []voice.ChatBackendOption {
	provider, ok := h.Backend.(interface {
		VoiceChatBackends(context.Context) []voice.ChatBackendOption
	})
	if !ok {
		return []voice.ChatBackendOption{{ID: "koder", Label: "Koder", Available: true}}
	}
	return provider.VoiceChatBackends(ctx)
}

func (h *Handler) serveTemporaryVoiceSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	backend, ok := h.Backend.(voice.SessionChatBackend)
	if !ok {
		http.Error(w, "session chat backend is unavailable", http.StatusServiceUnavailable)
		return
	}
	request, ok := decodeCreateSessionRequest(w, r)
	if !ok {
		return
	}
	session, chat, err := backend.CreateTemporaryVoiceChat(r.Context(), normalizeVoiceChatSpec(request.ChatCreateSpec))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeSessionsResponse(w, http.StatusCreated, sessionsResponse{Protocol: protocolVersion, Session: &session, Chat: &chat, Chats: []voice.Chat{chat}})
}

func (h *Handler) serveVoiceSession(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/voice/v1/sessions/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[1] == "chats" {
		h.serveSessionChat(w, r, parts[0], parts[2])
		return
	}
	if len(parts) == 2 && parts[1] == "chats" {
		h.serveSessionChats(w, r, parts[0])
		return
	}
	sessionID := strings.TrimSpace(path)
	if sessionID == "" || len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodPatch+", "+http.MethodDelete)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if backend, ok := h.Backend.(voice.SessionManagementBackend); ok {
		h.serveManagedSession(w, r, backend, sessionID)
		return
	}
	if r.Method == http.MethodDelete {
		if err := h.Backend.DeleteVoiceSession(r.Context(), sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sessions, err := h.Backend.ListVoiceChats(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sortVoiceSessions(sessions)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(sessionsResponse{Protocol: protocolVersion, VoiceSessions: sessions})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, sessionRequestLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request updateSessionRequest
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid session request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid session request: expected one JSON object", http.StatusBadRequest)
		return
	}
	if request.Title == nil && request.Archived == nil && request.Pinned == nil && request.Favorite == nil && request.Deleted == nil {
		http.Error(w, "voice session update is empty", http.StatusBadRequest)
		return
	}
	updated, err := h.Backend.UpdateVoiceSession(r.Context(), sessionID, voice.SessionUpdate{
		Title: request.Title, Archived: request.Archived, Pinned: request.Pinned, Favorite: request.Favorite, Deleted: request.Deleted,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessions, err := h.Backend.ListVoiceChats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sortVoiceSessions(sessions)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(sessionsResponse{Protocol: protocolVersion, VoiceSession: &updated, VoiceSessions: sessions})
}

func (h *Handler) serveManagedSession(w http.ResponseWriter, r *http.Request, backend voice.SessionManagementBackend, sessionID string) {
	var updated *voice.Session
	if r.Method == http.MethodDelete {
		if err := backend.DeleteClientSession(r.Context(), sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		request, ok := decodeUpdateSessionRequest(w, r)
		if !ok {
			return
		}
		item, err := backend.UpdateClientSession(r.Context(), sessionID, voice.SessionUpdate{
			Title: request.Title, Archived: request.Archived, Pinned: request.Pinned, Favorite: request.Favorite, Deleted: request.Deleted,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		updated = &item
	}
	sessions, err := h.Backend.ListVoiceSessions(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sessions = withoutDeletedSessions(sessions)
	slices.SortStableFunc(sessions, func(a, b voice.Session) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	writeSessionsResponse(w, http.StatusOK, sessionsResponse{Protocol: protocolVersion, Session: updated, Sessions: sessions})
}

func decodeUpdateSessionRequest(w http.ResponseWriter, r *http.Request) (updateSessionRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, sessionRequestLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request updateSessionRequest
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid session request: "+err.Error(), http.StatusBadRequest)
		return updateSessionRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid session request: expected one JSON object", http.StatusBadRequest)
		return updateSessionRequest{}, false
	}
	if request.Title == nil && request.Archived == nil && request.Pinned == nil && request.Favorite == nil && request.Deleted == nil {
		http.Error(w, "session update is empty", http.StatusBadRequest)
		return updateSessionRequest{}, false
	}
	return request, true
}

func (h *Handler) serveSessionChats(w http.ResponseWriter, r *http.Request, sessionID string) {
	backend, ok := h.Backend.(voice.SessionChatBackend)
	if !ok {
		http.Error(w, "session chat backend is unavailable", http.StatusServiceUnavailable)
		return
	}
	var created *voice.Chat
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		request, valid := decodeCreateSessionRequest(w, r)
		if !valid {
			return
		}
		chat, err := backend.CreateVoiceChatInSession(r.Context(), sessionID, normalizeVoiceChatSpec(request.ChatCreateSpec))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		created = &chat
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	chats, err := backend.ListSessionChats(r.Context(), sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	status := http.StatusOK
	if created != nil {
		status = http.StatusCreated
	}
	writeSessionsResponse(w, status, sessionsResponse{Protocol: protocolVersion, Chat: created, Chats: chats})
}

func (h *Handler) serveSessionChat(w http.ResponseWriter, r *http.Request, sessionID, chatID string) {
	backend, ok := h.Backend.(voice.SessionManagementBackend)
	if !ok {
		http.Error(w, "session management backend is unavailable", http.StatusServiceUnavailable)
		return
	}
	chatBackend, ok := h.Backend.(voice.SessionChatBackend)
	if !ok {
		http.Error(w, "session chat backend is unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		r.Body = http.MaxBytesReader(w, r.Body, sessionRequestLimit)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request updateChatRequest
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid chat request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "invalid chat request: expected one JSON object", http.StatusBadRequest)
			return
		}
		if request.Title == nil && request.Archived == nil && request.ProviderID == nil && request.ModelID == nil {
			http.Error(w, "chat update is empty", http.StatusBadRequest)
			return
		}
		if (request.ProviderID == nil) != (request.ModelID == nil) {
			http.Error(w, "provider_id and model_id must be changed together", http.StatusBadRequest)
			return
		}
		if _, err := backend.UpdateClientChat(r.Context(), sessionID, chatID, voice.ChatUpdate{Title: request.Title, Archived: request.Archived, ProviderID: request.ProviderID, ModelID: request.ModelID}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	case http.MethodDelete:
		if err := backend.DeleteClientChat(r.Context(), sessionID, chatID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	default:
		w.Header().Set("Allow", http.MethodPatch+", "+http.MethodDelete)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	chats, err := chatBackend.ListSessionChats(r.Context(), sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeSessionsResponse(w, http.StatusOK, sessionsResponse{Protocol: protocolVersion, Chats: chats})
}

func decodeCreateSessionRequest(w http.ResponseWriter, r *http.Request) (createSessionRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, sessionRequestLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request createSessionRequest
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid session request: "+err.Error(), http.StatusBadRequest)
		return createSessionRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid session request: expected one JSON object", http.StatusBadRequest)
		return createSessionRequest{}, false
	}
	return request, true
}

func voiceChatCreateSpec(title string, backend domain.ChatBackend, role domain.WorkflowRole, providerID, modelID, permissionProfile, milestoneKey, taskRef string, toolStates domain.ToolStates) domain.ChatCreateSpec {
	return normalizeVoiceChatSpec(domain.ChatCreateSpec{
		Title: title, Backend: backend, WorkflowRole: role, ProviderID: providerID, ModelID: modelID,
		PermissionProfile: permissionProfile, MilestoneKey: milestoneKey, TaskRef: taskRef, ToolStates: toolStates,
	})
}

func normalizeVoiceChatSpec(spec domain.ChatCreateSpec) domain.ChatCreateSpec {
	spec.InteractionMode = domain.InteractionModeVoice
	return spec.Normalized()
}

func writeSessionsResponse(w http.ResponseWriter, status int, response sessionsResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func sortVoiceSessions(sessions []voice.Session) {
	slices.SortStableFunc(sessions, func(a, b voice.Session) int {
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
}

func withoutDeletedSessions(sessions []voice.Session) []voice.Session {
	return slices.DeleteFunc(sessions, func(item voice.Session) bool { return item.Deleted })
}

func (h *Handler) serveArtifact(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/voice/v1/artifacts/")
	parts := strings.Split(path, "/")
	var (
		file voice.ArtifactFile
		err  error
	)
	switch {
	case len(parts) == 3 && parts[0] == "session":
		file, err = h.Backend.VoiceSessionArtifact(parts[1], parts[2])
	case len(parts) == 2 && parts[0] == "offered":
		file, err = h.Backend.VoiceOfferedArtifact(r.Context(), parts[1])
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil || strings.TrimSpace(file.Path) == "" {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(file.Path)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSpace(filepath.Base(file.Name))
	if name == "" || name == "." {
		name = filepath.Base(file.Path)
	}
	if contentType := strings.TrimSpace(file.MIMEType); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, file.Path)
}

func (h *Handler) serveAndroidAPK(w http.ResponseWriter, r *http.Request) {
	if h.Updates == nil {
		http.NotFound(w, r)
		return
	}
	meta, available, err := h.Updates.Manifest()
	if err != nil {
		http.Error(w, "embedded Android update is invalid", http.StatusInternalServerError)
		return
	}
	if !available {
		http.NotFound(w, r)
		return
	}
	apk, err := h.Updates.OpenAPK()
	if err != nil {
		http.Error(w, "embedded Android update is unavailable", http.StatusInternalServerError)
		return
	}
	defer func() { _ = apk.Close() }()
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="koder.apk"`)
	w.Header().Set("Content-Length", fmt.Sprint(meta.APKSize))
	w.Header().Set("ETag", `"`+meta.APKSHA256+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-cache")
	if _, err := io.Copy(w, apk); err != nil {
		return
	}
}

func (h *Handler) serveBindDevice(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil {
		http.Error(w, "device binding is unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, sessionRequestLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request bindDeviceRequest
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid device binding request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid device binding request: expected one JSON object", http.StatusBadRequest)
		return
	}
	binding, err := h.Auth.Bind(request.Code, request.Device)
	if err != nil {
		if errors.Is(err, deviceauth.ErrInvitationInvalid) {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		http.Error(w, "bind device: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(bindDeviceResponse{Protocol: protocolVersion, Binding: binding})
}

func appendIncomingAudio(cfg voice.AudioConfig, incoming *incomingAudio, payload []byte) error {
	if incoming == nil {
		return fmt.Errorf("binary audio arrived without audio_start")
	}
	frame, err := voice.DecodeAudioFrame(payload)
	if err != nil {
		return err
	}
	expectedKind := voice.AudioFrameInputPCM
	if incoming.transport.Encoding == voice.Opus {
		expectedKind = voice.AudioFrameInputOpus
	}
	if frame.Kind != expectedKind {
		return fmt.Errorf("client sent audio frame kind %d; expected %d for %s", frame.Kind, expectedKind, incoming.transport.Encoding)
	}
	if frame.Sequence != incoming.nextSequence {
		return fmt.Errorf("voice audio sequence %d arrived; expected %d", frame.Sequence, incoming.nextSequence)
	}
	decoded := frame.Payload
	if incoming.decoder != nil {
		decoded, err = incoming.decoder.Decode(frame.Payload)
		if err != nil {
			return err
		}
	}
	maxBytes := int64(cfg.MaxUtteranceSeconds) * int64(incoming.format.SampleRate) * int64(incoming.format.Channels) * 2
	if int64(len(incoming.pcm)+len(decoded)) > maxBytes {
		return fmt.Errorf("voice utterance exceeds %d seconds", cfg.MaxUtteranceSeconds)
	}
	incoming.pcm = append(incoming.pcm, decoded...)
	incoming.nextSequence++
	return nil
}

func writeResult(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call, backend backend, audioConfig voice.AudioConfig, updates androidUpdateSource, utteranceID, _ string, message voice.Message, callErr error) error {
	if callErr != nil {
		if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: callErr.Error(), ErrorCode: clientErrorCode(callErr)}); err != nil {
			return err
		}
		return writeReady(ctx, conn, writeMu, call, audioConfig, updates)
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "message", UtteranceID: utteranceID, Message: &message}); err != nil {
		return err
	}
	if strings.TrimSpace(message.SpokenText) != "" {
		if err := writeSpeech(ctx, conn, writeMu, backend, selectedOutputFormat(audioConfig), utteranceID, message.SpokenText); err != nil {
			serviceFormat := backend.VoiceAudioConfig().Output
			transportFormat := selectedOutputFormat(audioConfig)
			slog.Warn("voice speech synthesis failed",
				"utterance_id", utteranceID,
				"service_encoding", serviceFormat.Encoding,
				"service_sample_rate", serviceFormat.SampleRate,
				"service_channels", serviceFormat.Channels,
				"transport_encoding", transportFormat.Encoding,
				"transport_sample_rate", transportFormat.SampleRate,
				"transport_channels", transportFormat.Channels,
				"error", err,
			)
			if writeErr := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: "speech synthesis: " + err.Error()}); writeErr != nil {
				return writeErr
			}
		}
	}
	return writeReady(ctx, conn, writeMu, call, audioConfig, updates)
}

func clientErrorCode(err error) string {
	var coded interface{ ClientErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ClientErrorCode()
	}
	return ""
}

func writeSpeech(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, speech voice.SpeechBackend, transport voice.AudioFormat, utteranceID, text string) error {
	serviceFormat := speech.VoiceAudioConfig().Output
	format := playbackFormat(transport)
	packetEncoder, err := newAudioPacketEncoder(serviceFormat, transport)
	if err != nil {
		return err
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "state", UtteranceID: utteranceID, State: "speaking"}); err != nil {
		return err
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "tts_start", UtteranceID: utteranceID, AudioFormat: &format}); err != nil {
		return err
	}
	var sequence uint32
	emit := func(kind voice.AudioFrameKind, payload []byte) error {
		encoded, err := voice.EncodeAudioFrame(voice.AudioFrame{Kind: kind, Sequence: sequence, Payload: payload})
		if err != nil {
			return err
		}
		writeMu.Lock()
		err = conn.Write(ctx, websocket.MessageBinary, encoded)
		writeMu.Unlock()
		if err == nil {
			sequence++
		}
		return err
	}
	err = speech.StreamVoiceSpeech(ctx, text, func(chunk []byte) error {
		return packetEncoder.appendPCM(chunk, emit)
	})
	if err != nil {
		return err
	}
	if err := packetEncoder.finish(emit); err != nil {
		return err
	}
	return writeFrame(ctx, conn, writeMu, serverFrame{Type: "tts_end", UtteranceID: utteranceID})
}

func writeReady(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call, audioConfig voice.AudioConfig, updates androidUpdateSource) error {
	state, err := call.State(ctx)
	if err != nil {
		return writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", Error: err.Error()})
	}
	frame := serverFrame{Type: "ready", CallState: &state, AudioConfig: &audioConfig, State: "listening"}
	if updates != nil {
		meta, available, err := updates.Manifest()
		if err != nil {
			return writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", Error: "embedded Android update is invalid"})
		}
		if available {
			meta.DownloadURI = "/voice/v1/android/koder.apk"
			frame.AppUpdate = &meta
		}
	}
	return writeFrame(ctx, conn, writeMu, frame)
}

func writeFrame(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, frame serverFrame) error {
	frame.Protocol = protocolVersion
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	return conn.Write(ctx, websocket.MessageText, payload)
}

func authorized(r *http.Request, configuredToken string, registry *deviceauth.Registry) bool {
	configuredToken = strings.TrimSpace(configuredToken)
	if configuredToken == "" && registry == nil {
		return true
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if registry != nil {
		return registry.Authorize(provided, deviceInfoFromHeaders(r.Header))
	}
	if configuredToken != "" && len(provided) == len(configuredToken) && subtle.ConstantTimeCompare([]byte(provided), []byte(configuredToken)) == 1 {
		return true
	}
	return false
}

func deviceInfoFromHeaders(header http.Header) deviceauth.DeviceInfo {
	return deviceauth.DeviceInfo{
		InstallationID: header.Get("X-Koder-Device-ID"),
		Name:           header.Get("X-Koder-Device-Name"),
		Manufacturer:   header.Get("X-Koder-Device-Manufacturer"),
		Model:          header.Get("X-Koder-Device-Model"),
		AndroidVersion: header.Get("X-Koder-Android-Version"),
		AppVersion:     header.Get("X-Koder-App-Version"),
		AppID:          header.Get("X-Koder-App-ID"),
	}
}
