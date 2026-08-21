package voiceapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/voice"
)

const readinessReply = "Readiness check complete. I can hear you and speak back."

// serveReadiness exercises the configured speech services without acquiring a
// call lease, creating a session, or invoking an LLM.
func (h *Handler) serveReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	conn.SetReadLimit(readLimit)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	ctx := r.Context()
	var writeMu sync.Mutex
	baseConfig := h.Backend.VoiceAudioConfig()
	config := negotiatedAudioConfig(baseConfig, nil, nil, nil)
	advertised := advertisedAudioConfig(baseConfig)
	if err := writeFrame(ctx, conn, &writeMu, serverFrame{Type: "readiness_ready", AudioConfig: &advertised, State: "listening"}); err != nil {
		return
	}
	var audio *incomingAudio
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if messageType == websocket.MessageBinary {
			if err := appendIncomingAudio(config, audio, payload); err != nil {
				audio = nil
				if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: err.Error()}) != nil {
					return
				}
			}
			continue
		}
		if messageType != websocket.MessageText {
			continue
		}
		var frame clientFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: "invalid JSON: " + err.Error()}) != nil {
				return
			}
			continue
		}
		switch strings.TrimSpace(frame.Type) {
		case "hello":
			config = negotiatedAudioConfig(baseConfig, frame.AudioEncodings, frame.InputTransport, frame.OutputTransport)
			if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "readiness_ready", AudioConfig: &config, State: "listening"}) != nil {
				return
			}
		case "ping":
			if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "pong"}) != nil {
				return
			}
		case "audio_start":
			utteranceID := strings.TrimSpace(frame.UtteranceID)
			transportFormat := selectedInputFormat(config)
			if audio != nil || utteranceID == "" || frame.AudioFormat == nil || *frame.AudioFormat != transportFormat {
				if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: "readiness audio_start requires a unique id and the advertised input format"}) != nil {
					return
				}
				continue
			}
			languages, err := normalizeLanguages(frame.Languages)
			if err != nil {
				if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: err.Error()}) != nil {
					return
				}
				continue
			}
			audio, err = newIncomingAudio(utteranceID, baseConfig.Input, transportFormat, languages)
			if err != nil {
				if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: utteranceID, Error: "initialize audio decoder: " + err.Error()}) != nil {
					return
				}
				continue
			}
			if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "state", UtteranceID: utteranceID, State: "recording"}) != nil {
				return
			}
		case "audio_commit":
			if audio == nil || strings.TrimSpace(frame.UtteranceID) != audio.utteranceID {
				if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: frame.UtteranceID, Error: "readiness audio_commit does not match an active utterance"}) != nil {
					return
				}
				continue
			}
			completed := audio
			audio = nil
			if len(completed.pcm) == 0 {
				if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: completed.utteranceID, Error: "readiness audio was empty"}) != nil {
					return
				}
				continue
			}
			if err := h.finishReadiness(ctx, conn, &writeMu, completed, selectedOutputFormat(config)); err != nil {
				_ = writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", UtteranceID: completed.utteranceID, Error: err.Error()})
				continue
			}
		default:
			if writeFrame(ctx, conn, &writeMu, serverFrame{Type: "error", Error: fmt.Sprintf("unknown readiness frame type %q", frame.Type)}) != nil {
				return
			}
		}
	}
}

func (h *Handler) finishReadiness(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, audio *incomingAudio, outputTransport voice.AudioFormat) error {
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "state", UtteranceID: audio.utteranceID, State: "transcribing"}); err != nil {
		return err
	}
	transcript, err := h.Backend.TranscribeVoice(ctx, audio.format, audio.pcm, voice.TranscriptionHints{Languages: audio.languages})
	if err != nil {
		return fmt.Errorf("speech transcription: %w", err)
	}
	if strings.TrimSpace(transcript) == "" {
		return fmt.Errorf("speech transcription returned no words")
	}
	if err := writeFrame(ctx, conn, writeMu, serverFrame{Type: "transcript", UtteranceID: audio.utteranceID, Transcript: transcript}); err != nil {
		return err
	}
	if err := writeSpeech(ctx, conn, writeMu, h.Backend, outputTransport, audio.utteranceID, readinessReply); err != nil {
		return fmt.Errorf("speech synthesis: %w", err)
	}
	return writeFrame(ctx, conn, writeMu, serverFrame{Type: "readiness_complete", UtteranceID: audio.utteranceID, Transcript: transcript, State: "complete"})
}
