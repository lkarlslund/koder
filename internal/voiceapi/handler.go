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

type backend interface {
	voice.Backend
	voice.SpeechBackend
	voice.VoiceSessionBackend
}

// Handler serves voice calls for one Koder process.
type Handler struct {
	Backend backend
	Token   string
	Lease   *voice.CallLease
}

// NewHandler creates a process-scoped voice protocol handler.
func NewHandler(backend backend, token string) *Handler {
	return &Handler{Backend: backend, Token: token, Lease: voice.NewCallLease()}
}

type clientFrame struct {
	Type        string             `json:"type"`
	Protocol    string             `json:"protocol,omitempty"`
	UtteranceID string             `json:"utterance_id,omitempty"`
	Text        string             `json:"text,omitempty"`
	SessionID   string             `json:"session_id,omitempty"`
	AudioFormat *voice.AudioFormat `json:"audio_format,omitempty"`
}

type serverFrame struct {
	Type        string             `json:"type"`
	Protocol    string             `json:"protocol"`
	UtteranceID string             `json:"utterance_id,omitempty"`
	CallState   *voice.CallState   `json:"call_state,omitempty"`
	AudioConfig *voice.AudioConfig `json:"audio_config,omitempty"`
	AudioFormat *voice.AudioFormat `json:"audio_format,omitempty"`
	Transcript  string             `json:"transcript,omitempty"`
	State       string             `json:"state,omitempty"`
	Message     *voice.Message     `json:"message,omitempty"`
	Error       string             `json:"error,omitempty"`
	ServerTime  time.Time          `json:"server_time,omitempty"`
}

type incomingAudio struct {
	utteranceID  string
	format       voice.AudioFormat
	nextSequence uint32
	pcm          []byte
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
	voiceSession, err := h.Backend.EnsureVoiceSession(r.Context(), r.URL.Query().Get("voice_session_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	release, err := h.Lease.Acquire(callID, voiceSession.ID)
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
	call := voice.NewCall(h.Backend, voiceSession.ID)
	var audio *incomingAudio
	var writeMu sync.Mutex
	if err := writeReady(ctx, conn, &writeMu, call, h.Backend); err != nil {
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
				if writeErr := writeReady(ctx, conn, &writeMu, call, h.Backend); writeErr != nil {
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
			if err := writeReady(ctx, conn, &writeMu, call, h.Backend); err != nil {
				return
			}
		case "ping":
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "pong", ServerTime: time.Now().UTC()}); err != nil {
				return
			}
		case "select_session":
			message, err := call.SelectSession(ctx, frame.SessionID)
			if err := writeResult(ctx, conn, &writeMu, call, h.Backend, frame.UtteranceID, "", message, err); err != nil {
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
			audio = &incomingAudio{utteranceID: utteranceID, format: *frame.AudioFormat}
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "state", UtteranceID: utteranceID, State: "recording"}); err != nil {
				return
			}
		case "audio_cancel":
			audio = nil
			if err := writeReady(ctx, conn, &writeMu, call, h.Backend); err != nil {
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
			message, err := call.HandleText(delegationCtx, frame.Text, frame.SessionID)
			cancel()
			if err := writeResult(ctx, conn, &writeMu, call, h.Backend, frame.UtteranceID, frame.Text, message, err); err != nil {
				return
			}
		default:
			if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: frame.UtteranceID, Error: fmt.Sprintf("unknown frame type %q", frame.Type)}); err != nil {
				return
			}
		}
	}
}

func (h *Handler) handleAudio(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call, audio *incomingAudio, sessionID string) error {
	if len(audio.pcm) == 0 {
		if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: audio.utteranceID, Error: "audio utterance was empty"}); err != nil {
			return err
		}
		return writeReady(ctx, conn, writeMu, call, h.Backend)
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "state", UtteranceID: audio.utteranceID, State: "transcribing"}); err != nil {
		return err
	}
	transcript, err := h.Backend.TranscribeVoice(ctx, audio.format, audio.pcm)
	if err != nil {
		if writeErr := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: audio.utteranceID, Error: err.Error()}); writeErr != nil {
			return writeErr
		}
		return writeReady(ctx, conn, writeMu, call, h.Backend)
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "transcript", UtteranceID: audio.utteranceID, Transcript: transcript}); err != nil {
		return err
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "state", UtteranceID: audio.utteranceID, State: "processing"}); err != nil {
		return err
	}
	delegationCtx, cancel := context.WithTimeout(ctx, delegationTimeout)
	message, callErr := call.HandleText(delegationCtx, transcript, sessionID)
	cancel()
	return writeResult(ctx, conn, writeMu, call, h.Backend, audio.utteranceID, transcript, message, callErr)
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

func writeResult(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call, backend backend, utteranceID, transcript string, message voice.Message, callErr error) error {
	if callErr != nil {
		if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: callErr.Error()}); err != nil {
			return err
		}
		return writeReady(ctx, conn, writeMu, call, backend)
	}
	state, err := call.State(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(transcript) != "" {
		if err := backend.RecordVoiceExchange(ctx, state.VoiceSessionID, transcript, message); err != nil {
			if writeErr := writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: "record voice exchange: " + err.Error()}); writeErr != nil {
				return writeErr
			}
			return writeReady(ctx, conn, writeMu, call, backend)
		}
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
	return writeReady(ctx, conn, writeMu, call, backend)
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

func writeReady(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, call *voice.Call, speech voice.SpeechBackend) error {
	state, err := call.State(ctx)
	if err != nil {
		return writeFrame(ctx, conn, writeMu, serverFrame{Type: "error", Error: err.Error()})
	}
	audioConfig := speech.VoiceAudioConfig()
	return writeFrame(ctx, conn, writeMu, serverFrame{Type: "ready", CallState: &state, AudioConfig: &audioConfig, State: "listening"})
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
