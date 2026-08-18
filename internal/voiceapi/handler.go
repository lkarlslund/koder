// Package voiceapi adapts the versioned voice WebSocket protocol to a coordinator.
package voiceapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/voice"
)

const protocolVersion = "voice.v1"
const readLimit = 256 * 1024
const delegationTimeout = 30 * time.Minute

// Handler serves voice calls for one Koder process.
type Handler struct {
	Backend voice.Backend
	Token   string
	Lease   *voice.CallLease
}

// NewHandler creates a process-scoped voice protocol handler.
func NewHandler(backend voice.Backend, token string) *Handler {
	return &Handler{Backend: backend, Token: token, Lease: voice.NewCallLease()}
}

type clientFrame struct {
	Type        string `json:"type"`
	Protocol    string `json:"protocol,omitempty"`
	UtteranceID string `json:"utterance_id,omitempty"`
	Text        string `json:"text,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}

type serverFrame struct {
	Type        string           `json:"type"`
	Protocol    string           `json:"protocol"`
	UtteranceID string           `json:"utterance_id,omitempty"`
	CallState   *voice.CallState `json:"call_state,omitempty"`
	State       string           `json:"state,omitempty"`
	Message     *voice.Message   `json:"message,omitempty"`
	Error       string           `json:"error,omitempty"`
	ServerTime  time.Time        `json:"server_time,omitempty"`
}

// ServeHTTP accepts one authenticated voice call.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Backend == nil {
		http.Error(w, "voice backend unavailable", http.StatusServiceUnavailable)
		return
	}
	if h.Lease == nil {
		http.Error(w, "voice call lease unavailable", http.StatusServiceUnavailable)
		return
	}
	if !authorized(r, h.Token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="koder-voice"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	callID := strings.TrimSpace(r.URL.Query().Get("call_id"))
	if callID == "" {
		callID = string(id.New())
	}
	release, err := h.Lease.Acquire(callID, r.URL.Query().Get("voice_session_id"))
	if err != nil {
		if err == voice.ErrCallActive {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer release()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	conn.SetReadLimit(readLimit)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	ctx := r.Context()
	call := voice.NewCall(h.Backend)
	var writeMu sync.Mutex
	if err := writeReady(ctx, conn, &writeMu, call); err != nil {
		return
	}
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if messageType != websocket.MessageText {
			if writeErr := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: "voice.v1 accepts JSON text control frames"}); writeErr != nil {
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
			if err := writeReady(ctx, conn, &writeMu, call); err != nil {
				return
			}
		case "ping":
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "pong", ServerTime: time.Now().UTC()}); err != nil {
				return
			}
		case "select_session":
			message, err := call.SelectSession(ctx, frame.SessionID)
			if err := writeResult(ctx, conn, &writeMu, call, frame.UtteranceID, message, err); err != nil {
				return
			}
		case "utterance":
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "state", UtteranceID: frame.UtteranceID, State: "processing"}); err != nil {
				return
			}
			delegationCtx, cancel := context.WithTimeout(ctx, delegationTimeout)
			message, err := call.HandleText(delegationCtx, frame.Text, frame.SessionID)
			cancel()
			if err := writeResult(ctx, conn, &writeMu, call, frame.UtteranceID, message, err); err != nil {
				return
			}
		default:
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: frame.UtteranceID, Error: fmt.Sprintf("unknown frame type %q", frame.Type)}); err != nil {
				return
			}
		}
	}
}

func writeResult(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call, utteranceID string, message voice.Message, callErr error) error {
	if callErr != nil {
		return writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: callErr.Error()})
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "message", UtteranceID: utteranceID, Message: &message}); err != nil {
		return err
	}
	return writeReady(ctx, conn, writeMu, call)
}

func writeReady(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call) error {
	state, err := call.State(ctx)
	if err != nil {
		return writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", Error: err.Error()})
	}
	return writeFrame(ctx, conn, writeMu, serverFrame{Type: "ready", CallState: &state, State: "listening"})
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
