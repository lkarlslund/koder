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

type fakeBackend struct{ delegated bool }

func (f *fakeBackend) ListVoiceSessions(context.Context) ([]voice.Session, error) {
	return []voice.Session{{ID: "session-1", Title: "Laptop repair"}}, nil
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
	readType(t, ctx, conn, "ready")
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
