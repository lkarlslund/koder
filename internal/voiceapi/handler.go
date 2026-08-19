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
	Type           string             `json:"type"`
	Protocol       string             `json:"protocol,omitempty"`
	UtteranceID    string             `json:"utterance_id,omitempty"`
	Text           string             `json:"text,omitempty"`
	SessionID      string             `json:"session_id,omitempty"`
	VoiceSessionID string             `json:"voice_session_id,omitempty"`
	Title          string             `json:"title,omitempty"`
	AudioFormat    *voice.AudioFormat `json:"audio_format,omitempty"`
	Languages      []string           `json:"languages,omitempty"`
	BeforeID       string             `json:"before_id,omitempty"`
	Limit          int                `json:"limit,omitempty"`
	Query          string             `json:"query,omitempty"`
	ResponsePacing string             `json:"response_pacing,omitempty"`
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
	Error         string                         `json:"error,omitempty"`
	ServerTime    time.Time                      `json:"server_time,omitempty"`
	AppUpdate     *androidupdate.Manifest        `json:"app_update,omitempty"`
	History       []voice.TranscriptEntry        `json:"history,omitempty"`
	SearchResults []voice.TranscriptSearchResult `json:"search_results,omitempty"`
	HasMore       bool                           `json:"has_more,omitempty"`
}

type sessionsResponse struct {
	Protocol      string                  `json:"protocol"`
	VoiceSession  *voice.Session          `json:"voice_session,omitempty"`
	VoiceSessions []voice.Session         `json:"voice_sessions"`
	AppUpdate     *androidupdate.Manifest `json:"app_update,omitempty"`
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
	VoiceConnectionActive bool       `json:"voice_connection_active"`
	VoiceConnectionSince  *time.Time `json:"voice_connection_since,omitempty"`
	TokenRequired         bool       `json:"token_required"`
}

type createSessionRequest struct {
	Title string `json:"title"`
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
	nextSequence uint32
	pcm          []byte
	languages    []string
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
	if !authorized(r, h.Token, h.Auth) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="koder-voice"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/voice/v1/sessions" {
		h.serveSessions(w, r)
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
	voiceSession, err := h.Backend.EnsureVoiceSession(r.Context(), r.URL.Query().Get("voice_session_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	release, replaced, err := h.Lease.AcquireConnection(callID, voiceSession.ID)
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
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
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
	call := voice.NewCall(h.Backend, voiceSession.ID)
	var audio *incomingAudio
	responsePacing := voice.ResponsePacingNormal
	var writeMu sync.Mutex
	if err := writeReady(ctx, conn, &writeMu, call, h.Backend, h.Updates); err != nil {
		return
	}
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if messageType == websocket.MessageBinary {
			if err := appendIncomingAudio(h.Backend.VoiceAudioConfig(), audio, payload); err != nil {
				utteranceID := ""
				if audio != nil {
					utteranceID = audio.utteranceID
				}
				audio = nil
				if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: err.Error()}); writeErr != nil {
					return
				}
				if writeErr := writeReady(ctx, conn, &writeMu, call, h.Backend, h.Updates); writeErr != nil {
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
			if err := writeReady(ctx, conn, &writeMu, call, h.Backend, h.Updates); err != nil {
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
			search, ok := h.Backend.(voice.VoiceHistorySearchBackend)
			if !ok {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "history_search", Error: "transcript search is unavailable"}); err != nil {
					return
				}
				continue
			}
			state, stateErr := call.State(ctx)
			if stateErr != nil {
				if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "history_search", Error: stateErr.Error()}); err != nil {
					return
				}
				continue
			}
			results, searchErr := search.SearchVoiceSessionHistory(ctx, state.VoiceSessionID, frame.Query, frame.Limit)
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
			if err := writeResult(ctx, conn, &writeMu, call, h.Backend, h.Updates, frame.UtteranceID, "", message, err); err != nil {
				return
			}
		case "select_voice_session":
			audio = nil
			message, err := call.SelectVoiceSession(ctx, frame.VoiceSessionID)
			if err := writeResult(ctx, conn, &writeMu, call, h.Backend, h.Updates, frame.UtteranceID, "", message, err); err != nil {
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
			if err := writeReady(ctx, conn, &writeMu, call, h.Backend, h.Updates); err != nil {
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
			if utteranceID == "" || frame.AudioFormat == nil || *frame.AudioFormat != h.Backend.VoiceAudioConfig().Input {
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
			audio = &incomingAudio{utteranceID: utteranceID, format: *frame.AudioFormat, languages: languages}
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "state", UtteranceID: utteranceID, State: "recording"}); err != nil {
				return
			}
		case "audio_cancel":
			audio = nil
			if err := writeReady(ctx, conn, &writeMu, call, h.Backend, h.Updates); err != nil {
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
				if err := writeReady(ctx, conn, &writeMu, call, h.Backend, h.Updates); err != nil {
					return
				}
				continue
			}
			state, stateErr := call.State(ctx)
			if stateErr != nil {
				if err := writeResult(ctx, conn, &writeMu, call, h.Backend, h.Updates, completed.utteranceID, "", voice.Message{}, stateErr); err != nil {
					return
				}
				continue
			}
			turn, _, startErr := h.turns.start(callID, completed.utteranceID, audioTurnFingerprint(completed), state.VoiceSessionID, func(turn *cachedTurn) {
				h.runAudioTurn(turn, completed, responsePacing)
			})
			if startErr != nil {
				if err := writeResult(ctx, conn, &writeMu, call, h.Backend, h.Updates, completed.utteranceID, "", voice.Message{}, startErr); err != nil {
					return
				}
				continue
			}
			if err := streamCachedTurn(ctx, conn, &writeMu, turn, resume); err != nil {
				return
			}
			if err := writeReady(ctx, conn, &writeMu, call, h.Backend, h.Updates); err != nil {
				return
			}
		case "utterance":
			utteranceID := strings.TrimSpace(frame.UtteranceID)
			if utteranceID == "" || strings.TrimSpace(frame.Text) == "" {
				if err := writeResult(ctx, conn, &writeMu, call, h.Backend, h.Updates, utteranceID, "", voice.Message{}, errors.New("utterance requires an id and text")); err != nil {
					return
				}
				continue
			}
			state, stateErr := call.State(ctx)
			if stateErr != nil {
				if writeErr := writeResult(ctx, conn, &writeMu, call, h.Backend, h.Updates, utteranceID, frame.Text, voice.Message{}, stateErr); writeErr != nil {
					return
				}
				continue
			}
			turn, _, startErr := h.turns.start(callID, utteranceID, textTurnFingerprint(frame.Text), state.VoiceSessionID, func(turn *cachedTurn) {
				h.runTextTurn(turn, frame.Text, responsePacing)
			})
			if startErr != nil {
				if err := writeResult(ctx, conn, &writeMu, call, h.Backend, h.Updates, utteranceID, frame.Text, voice.Message{}, startErr); err != nil {
					return
				}
				continue
			}
			if err := streamCachedTurn(ctx, conn, &writeMu, turn, resume); err != nil {
				return
			}
			if err := writeReady(ctx, conn, &writeMu, call, h.Backend, h.Updates); err != nil {
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
	response := serverInfoResponse{
		Protocol:          protocolVersion,
		ServerTime:        now,
		Version:           build.Version,
		Commit:            build.Commit,
		Dirty:             build.Dirty,
		BuildTime:         build.BuildTime,
		StartedAt:         build.StartedAt,
		UptimeSeconds:     max(0, int64(now.Sub(build.StartedAt)/time.Second)),
		Platform:          runtime.GOOS + "/" + runtime.GOARCH,
		GoVersion:         build.GoVersion,
		LogicalCPUs:       runtime.NumCPU(),
		MaxProcs:          runtime.GOMAXPROCS(0),
		Goroutines:        runtime.NumGoroutine(),
		HeapAllocBytes:    memory.Alloc,
		HeapSysBytes:      memory.HeapSys,
		HeapObjects:       memory.HeapObjects,
		GCCycles:          memory.NumGC,
		SessionCount:      len(sessions),
		VoiceSessionCount: len(voiceSessions),
		TokenRequired:     strings.TrimSpace(h.Token) != "" || h.Auth != nil,
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
	slices.SortStableFunc(sessions, func(a, b voice.Session) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	response := sessionsResponse{
		Protocol: protocolVersion, VoiceSession: created, VoiceSessions: sessions,
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

func (h *Handler) serveVoiceSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/voice/v1/sessions/"))
	if sessionID == "" || strings.Contains(sessionID, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodPatch+", "+http.MethodDelete)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
		slices.SortStableFunc(sessions, func(a, b voice.Session) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(sessionsResponse{Protocol: protocolVersion, VoiceSessions: sessions})
		return
	}
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
	renamed, err := h.Backend.RenameVoiceSession(r.Context(), sessionID, request.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sessions, err := h.Backend.ListVoiceChats(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slices.SortStableFunc(sessions, func(a, b voice.Session) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(sessionsResponse{Protocol: protocolVersion, VoiceSession: &renamed, VoiceSessions: sessions})
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
	if frame.Kind != voice.AudioFrameInputPCM {
		return fmt.Errorf("client may only send input PCM frames")
	}
	if frame.Sequence != incoming.nextSequence {
		return fmt.Errorf("voice audio sequence %d arrived; expected %d", frame.Sequence, incoming.nextSequence)
	}
	maxBytes := int64(cfg.MaxUtteranceSeconds) * int64(incoming.format.SampleRate) * int64(incoming.format.Channels) * 2
	if int64(len(incoming.pcm)+len(frame.PCM)) > maxBytes {
		return fmt.Errorf("voice utterance exceeds %d seconds", cfg.MaxUtteranceSeconds)
	}
	incoming.pcm = append(incoming.pcm, frame.PCM...)
	incoming.nextSequence++
	return nil
}

func writeResult(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call, backend backend, updates androidUpdateSource, utteranceID, _ string, message voice.Message, callErr error) error {
	if callErr != nil {
		if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: callErr.Error()}); err != nil {
			return err
		}
		return writeReady(ctx, conn, writeMu, call, backend, updates)
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "message", UtteranceID: utteranceID, Message: &message}); err != nil {
		return err
	}
	if strings.TrimSpace(message.SpokenText) != "" {
		if err := writeSpeech(ctx, conn, writeMu, backend, utteranceID, message.SpokenText); err != nil {
			if writeErr := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: "speech synthesis: " + err.Error()}); writeErr != nil {
				return writeErr
			}
		}
	}
	return writeReady(ctx, conn, writeMu, call, backend, updates)
}

func writeSpeech(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, speech voice.SpeechBackend, utteranceID, text string) error {
	format := speech.VoiceAudioConfig().Output
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "state", UtteranceID: utteranceID, State: "speaking"}); err != nil {
		return err
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "tts_start", UtteranceID: utteranceID, AudioFormat: &format}); err != nil {
		return err
	}
	var sequence uint32
	var pending []byte
	err := speech.StreamVoiceSpeech(ctx, text, func(chunk []byte) error {
		pending = append(pending, chunk...)
		for len(pending) >= 2 {
			size := min(len(pending), voice.MaxAudioPayloadSize)
			size -= size % 2
			encoded, err := voice.EncodeAudioFrame(voice.AudioFrame{Kind: voice.AudioFrameOutputPCM, Sequence: sequence, PCM: pending[:size]})
			if err != nil {
				return err
			}
			writeMu.Lock()
			err = conn.Write(ctx, websocket.MessageBinary, encoded)
			writeMu.Unlock()
			if err != nil {
				return err
			}
			sequence++
			pending = append(pending[:0], pending[size:]...)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return fmt.Errorf("TTS returned an odd PCM16 byte count")
	}
	return writeFrame(ctx, conn, writeMu, serverFrame{Type: "tts_end", UtteranceID: utteranceID})
}

func writeReady(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call, speech voice.SpeechBackend, updates androidUpdateSource) error {
	state, err := call.State(ctx)
	if err != nil {
		return writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", Error: err.Error()})
	}
	audioConfig := speech.VoiceAudioConfig()
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
