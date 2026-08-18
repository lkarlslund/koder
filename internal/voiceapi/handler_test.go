package voiceapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/voice"
)

type fakeBackend struct {
	delegated              bool
	transcript             string
	spoken                 string
	recordedVoiceSessionID string
	recordedTranscript     string
}

func (f *fakeBackend) ListVoiceSessions(context.Context) ([]voice.Session, error) {
	return []voice.Session{{ID: "session-1", Title: "Laptop repair"}}, nil
}

func (f *fakeBackend) VoiceAudioConfig() voice.AudioConfig {
	return voice.AudioConfig{
		Input:               voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 16000, Channels: 1},
		Output:              voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 24000, Channels: 1},
		MaxUtteranceSeconds: 60,
	}
}

func (f *fakeBackend) EnsureVoiceSession(_ context.Context, requestedID string) (voice.Session, error) {
	if requestedID == "" {
		requestedID = "voice-1"
	}
	return voice.Session{ID: requestedID, Title: "Voice Chat"}, nil
}

func (f *fakeBackend) RecordVoiceExchange(_ context.Context, voiceSessionID, transcript string, _ voice.Message) error {
	f.recordedVoiceSessionID = voiceSessionID
	f.recordedTranscript = transcript
	return nil
}

func (f *fakeBackend) TranscribeVoice(_ context.Context, format voice.AudioFormat, pcm []byte) (string, error) {
	if format != f.VoiceAudioConfig().Input || len(pcm) == 0 {
		return "", context.Canceled
	}
	f.transcript = "check the laptop"
	return f.transcript, nil
}

func (f *fakeBackend) StreamVoiceSpeech(_ context.Context, text string, consume func([]byte) error) error {
	f.spoken = text
	if err := consume([]byte{1}); err != nil {
		return err
	}
	return consume([]byte{0, 2, 0})
}

func TestSharedProtocolFixturesDecode(t *testing.T) {
	utteranceData, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", "utterance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var utterance clientFrame
	if err := json.Unmarshal(utteranceData, &utterance); err != nil {
		t.Fatal(err)
	}
	if utterance.Protocol != protocolVersion || utterance.Type != "utterance" || utterance.UtteranceID == "" {
		t.Fatalf("utterance fixture = %#v", utterance)
	}
	messageData, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", "message.json"))
	if err != nil {
		t.Fatal(err)
	}
	var message serverFrame
	if err := json.Unmarshal(messageData, &message); err != nil {
		t.Fatal(err)
	}
	if message.Protocol != protocolVersion || message.Type != "message" || message.Message == nil || len(message.Message.Parts) == 0 {
		t.Fatalf("message fixture = %#v", message)
	}
}

func (f *fakeBackend) DelegateVoice(_ context.Context, sessionID, text string) (voice.DelegationResult, error) {
	f.delegated = true
	return voice.DelegationResult{SessionID: sessionID, SessionTitle: "Laptop repair", ChatID: "chat-1", Text: "Done: " + text}, nil
}

func TestHandlerAuthenticatesAndDelegates(t *testing.T) {
	backend := &fakeBackend{}
	server := httptest.NewServer(NewHandler(backend, "secret"))
	defer server.Close()
	ctx := context.Background()
	url := "ws" + server.URL[len("http"):]

	_, response, err := websocket.Dial(ctx, url, nil)
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dial response=%v err=%v", response, err)
	}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	readType(t, ctx, conn, "ready")
	if err := writeClientFrame(ctx, conn, clientFrame{Type: "utterance", Protocol: protocolVersion, UtteranceID: "utt-1", Text: "check it", SessionID: "session-1"}); err != nil {
		t.Fatal(err)
	}
	readType(t, ctx, conn, "state")
	message := readType(t, ctx, conn, "message")
	if message.Message == nil || message.Message.SpokenText != "Done: check it" || !backend.delegated {
		t.Fatalf("message=%#v delegated=%v", message, backend.delegated)
	}
	readType(t, ctx, conn, "state")
	ttsStart := readType(t, ctx, conn, "tts_start")
	if ttsStart.AudioFormat == nil || *ttsStart.AudioFormat != backend.VoiceAudioConfig().Output {
		t.Fatalf("tts_start = %#v", ttsStart)
	}
	frame := readAudioFrame(t, ctx, conn)
	if frame.Kind != voice.AudioFrameOutputPCM || frame.Sequence != 0 || len(frame.PCM) != 4 {
		t.Fatalf("output audio = %#v", frame)
	}
	readType(t, ctx, conn, "tts_end")
	readType(t, ctx, conn, "ready")
	if backend.recordedVoiceSessionID != "voice-1" || backend.recordedTranscript != "check it" {
		t.Fatalf("recorded voice exchange = session %q transcript %q", backend.recordedVoiceSessionID, backend.recordedTranscript)
	}
}

func TestHandlerTranscribesStreamsAndDelegatesAudio(t *testing.T) {
	backend := &fakeBackend{}
	server := httptest.NewServer(NewHandler(backend, ""))
	defer server.Close()
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, "ws"+server.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	ready := readType(t, ctx, conn, "ready")
	if ready.AudioConfig == nil {
		t.Fatal("ready did not advertise audio config")
	}
	format := ready.AudioConfig.Input
	if err := writeClientFrame(ctx, conn, clientFrame{Type: "audio_start", UtteranceID: "audio-1", AudioFormat: &format}); err != nil {
		t.Fatal(err)
	}
	readType(t, ctx, conn, "state")
	encoded, err := voice.EncodeAudioFrame(voice.AudioFrame{Kind: voice.AudioFrameInputPCM, Sequence: 0, PCM: []byte{7, 0, 8, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		t.Fatal(err)
	}
	if err := writeClientFrame(ctx, conn, clientFrame{Type: "audio_commit", UtteranceID: "audio-1", SessionID: "session-1"}); err != nil {
		t.Fatal(err)
	}
	readType(t, ctx, conn, "state") // transcribing
	transcript := readType(t, ctx, conn, "transcript")
	if transcript.Transcript != "check the laptop" {
		t.Fatalf("transcript = %#v", transcript)
	}
	readType(t, ctx, conn, "state") // processing
	message := readType(t, ctx, conn, "message")
	if message.Message == nil || message.Message.SpokenText != "Done: check the laptop" || !backend.delegated {
		t.Fatalf("message=%#v backend=%#v", message, backend)
	}
	readType(t, ctx, conn, "state") // speaking
	readType(t, ctx, conn, "tts_start")
	output := readAudioFrame(t, ctx, conn)
	if output.Kind != voice.AudioFrameOutputPCM || output.Sequence != 0 || len(output.PCM) != 4 {
		t.Fatalf("output = %#v", output)
	}
	readType(t, ctx, conn, "tts_end")
	readType(t, ctx, conn, "ready")
	if backend.spoken != "Done: check the laptop" {
		t.Fatalf("synthesized text = %q", backend.spoken)
	}
	if backend.recordedVoiceSessionID != "voice-1" || backend.recordedTranscript != "check the laptop" {
		t.Fatalf("recorded voice exchange = session %q transcript %q", backend.recordedVoiceSessionID, backend.recordedTranscript)
	}
}

func TestHandlerRejectsConcurrentCalls(t *testing.T) {
	server := httptest.NewServer(NewHandler(&fakeBackend{}, ""))
	defer server.Close()
	ctx := context.Background()
	url := "ws" + server.URL[len("http"):]
	first, _, err := websocket.Dial(ctx, url+"?call_id=first", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close(websocket.StatusNormalClosure, "") }()
	readType(t, ctx, first, "ready")
	_, response, err := websocket.Dial(ctx, url+"?call_id=second", nil)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("concurrent dial response=%v err=%v", response, err)
	}
}

func writeClientFrame(ctx context.Context, conn *websocket.Conn, frame clientFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}

func readType(t *testing.T, ctx context.Context, conn *websocket.Conn, want string) serverFrame {
	t.Helper()
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var frame serverFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != want {
		t.Fatalf("frame type = %q, want %q; payload=%s", frame.Type, want, payload)
	}
	return frame
}

func readAudioFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) voice.AudioFrame {
	t.Helper()
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageBinary {
		t.Fatalf("message type = %v, want binary; payload=%s", messageType, payload)
	}
	frame, err := voice.DecodeAudioFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}
