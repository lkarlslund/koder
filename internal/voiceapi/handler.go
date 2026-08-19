// Package voiceapi adapts the versioned voice WebSocket protocol to a coordinator.
package voiceapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/androidupdate"
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
}

// NewHandler creates a process-scoped voice protocol handler.
func NewHandler(backend backend, token string) *Handler {
	handler := &Handler{Backend: backend, Token: token, Lease: voice.NewCallLease(), Updates: androidupdate.Embedded()}
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
}

type serverFrame struct {
	Type        string                  `json:"type"`
	Protocol    string                  `json:"protocol"`
	UtteranceID string                  `json:"utterance_id,omitempty"`
	CallState   *voice.CallState        `json:"call_state,omitempty"`
	AudioConfig *voice.AudioConfig      `json:"audio_config,omitempty"`
	AudioFormat *voice.AudioFormat      `json:"audio_format,omitempty"`
	Transcript  string                  `json:"transcript,omitempty"`
	State       string                  `json:"state,omitempty"`
	WorkingOn   *voice.Session          `json:"working_on,omitempty"`
	Message     *voice.Message          `json:"message,omitempty"`
	Error       string                  `json:"error,omitempty"`
	ServerTime  time.Time               `json:"server_time,omitempty"`
	AppUpdate   *androidupdate.Manifest `json:"app_update,omitempty"`
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

// ServeHTTP accepts one authenticated voice call.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Backend == nil {
		http.Error(w, "voice backend unavailable", http.StatusServiceUnavailable)
		return
	}
	if !authorized(r, h.Token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="koder-voice"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/voice/v1/sessions" {
		h.serveSessions(w, r)
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
			if err := writeReady(ctx, conn, &writeMu, call, h.Backend, h.Updates); err != nil {
				return
			}
		case "ping":
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "pong", ServerTime: time.Now().UTC()}); err != nil {
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
			if err := h.handleAudio(ctx, conn, &writeMu, call, completed, frame.SessionID); err != nil {
				return
			}
		case "utterance":
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "state", UtteranceID: frame.UtteranceID, State: "processing"}); err != nil {
				return
			}
			delegationCtx, cancel := context.WithTimeout(ctx, delegationTimeout)
			state, stateErr := call.State(delegationCtx)
			if stateErr != nil {
				cancel()
				if writeErr := writeResult(ctx, conn, &writeMu, call, h.Backend, h.Updates, frame.UtteranceID, frame.Text, voice.Message{}, stateErr); writeErr != nil {
					return
				}
				continue
			}
			message, err := h.Backend.RunVoiceTurn(delegationCtx, state.VoiceSessionID, frame.Text, func(session voice.Session) error {
				return writeWorking(ctx, conn, &writeMu, frame.UtteranceID, session)
			})
			cancel()
			if err := writeResult(ctx, conn, &writeMu, call, h.Backend, h.Updates, frame.UtteranceID, frame.Text, message, err); err != nil {
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
		TokenRequired:     strings.TrimSpace(h.Token) != "",
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
	defer apk.Close()
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

func (h *Handler) handleAudio(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call, audio *incomingAudio, _ string) error {
	if len(audio.pcm) == 0 {
		if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: audio.utteranceID, Error: "audio utterance was empty"}); err != nil {
			return err
		}
		return writeReady(ctx, conn, writeMu, call, h.Backend, h.Updates)
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "state", UtteranceID: audio.utteranceID, State: "transcribing"}); err != nil {
		return err
	}
	transcript, err := h.Backend.TranscribeVoice(ctx, audio.format, audio.pcm, voice.TranscriptionHints{Languages: audio.languages})
	if err != nil {
		if writeErr := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: audio.utteranceID, Error: err.Error()}); writeErr != nil {
			return writeErr
		}
		return writeReady(ctx, conn, writeMu, call, h.Backend, h.Updates)
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "transcript", UtteranceID: audio.utteranceID, Transcript: transcript}); err != nil {
		return err
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "state", UtteranceID: audio.utteranceID, State: "processing"}); err != nil {
		return err
	}
	delegationCtx, cancel := context.WithTimeout(ctx, delegationTimeout)
	state, stateErr := call.State(delegationCtx)
	if stateErr != nil {
		cancel()
		return writeResult(ctx, conn, writeMu, call, h.Backend, h.Updates, audio.utteranceID, transcript, voice.Message{}, stateErr)
	}
	message, callErr := h.Backend.RunVoiceTurn(delegationCtx, state.VoiceSessionID, transcript, func(session voice.Session) error {
		return writeWorking(ctx, conn, writeMu, audio.utteranceID, session)
	})
	cancel()
	return writeResult(ctx, conn, writeMu, call, h.Backend, h.Updates, audio.utteranceID, transcript, message, callErr)
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

func writeWorking(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, utteranceID string, session voice.Session) error {
	return writeFrame(ctx, conn, writeMu, serverFrame{
		Type:        "state",
		UtteranceID: utteranceID,
		State:       "working",
		WorkingOn:   &session,
	})
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

func authorized(r *http.Request, configuredToken string) bool {
	configuredToken = strings.TrimSpace(configuredToken)
	if configuredToken == "" {
		return true
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	if len(provided) != len(configuredToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(configuredToken)) == 1
}
