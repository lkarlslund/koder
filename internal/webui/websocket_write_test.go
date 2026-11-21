package webui

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A large message to a client that reads slowly arrives: the write limit
// grows with the message. With a fixed limit the client was dropped before
// its state arrived, reconnected, and asked for it again, forever.
func TestWebSocketWriteWaitsForLargeMessages(t *testing.T) {
	savedTimeout, savedRate := websocketWriteTimeout, websocketMinWriteRate
	websocketWriteTimeout, websocketMinWriteRate = 200*time.Millisecond, 1<<20
	t.Cleanup(func() { websocketWriteTimeout, websocketMinWriteRate = savedTimeout, savedRate })

	// Random text does not compress, and 16 MB is more than the loopback
	// socket buffers hold, so the write waits for the reader.
	raw := make([]byte, 12<<20)
	_, _ = rand.Read(raw)
	payload := base64.StdEncoding.EncodeToString(raw)

	written := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			written <- err
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
		_, err = writeJSONLocked(r.Context(), conn, map[string]string{"state": payload})
		written <- err
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	conn.SetReadLimit(-1)
	time.Sleep(time.Second) // a slow link: far past the 200 ms base limit
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read the large message: %v", err)
	}
	if err := <-written; err != nil {
		t.Fatalf("write the large message: %v", err)
	}
	if len(data) < len(payload) {
		t.Fatalf("read %d bytes, want at least %d", len(data), len(payload))
	}
}

// The web UI's WebSocket compresses messages when the client offers it, as
// browsers do.
func TestWebSocketNegotiatesCompression(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.server.Close() }()
	conn, resp, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", &websocket.DialOptions{CompressionMode: websocket.CompressionNoContextTakeover})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	if got := resp.Header.Get("Sec-WebSocket-Extensions"); !strings.Contains(got, "permessage-deflate") {
		t.Fatalf("Sec-WebSocket-Extensions = %q, want permessage-deflate", got)
	}
}
