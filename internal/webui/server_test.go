package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/app"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestServerDoesNotOpenBrowserWhenWebSocketConnects(t *testing.T) {
	ctrl := newTestController(t)
	opened := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{
		Bind:      "127.0.0.1:0",
		OpenDelay: 30 * time.Millisecond,
		OpenBrowser: func(url string) error {
			opened <- url
			return nil
		},
	})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(ctrl.State().Session.ID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	select {
	case url := <-opened:
		t.Fatalf("expected no browser open after websocket connect, got %s", url)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestServerServesSessionAndWelcomeRoutes(t *testing.T) {
	ctrl := newTestController(t)
	second, err := ctrl.CreateSession(context.Background(), "Second", t.TempDir(), false)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	secondID := second.ID
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	resp, err := http.Get(srv.URL() + "/")
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected root welcome app ok, got %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL() + "/s/" + string(secondID))
	if err != nil {
		t.Fatalf("get inactive session url: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected session app ok, got %d", resp.StatusCode)
	}

	resp, err = http.Get(srv.URL() + "/s/019e72fa-1cb8-73ef-a5ca-247275f3f62f")
	if err != nil {
		t.Fatalf("get missing session url: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected missing session to serve welcome app shell, got %d", resp.StatusCode)
	}
}

func TestWebSocketHelloUsesURLSessionSelection(t *testing.T) {
	ctrl := newTestController(t)
	firstID := ctrl.State().Session.ID
	second, err := ctrl.CreateSession(context.Background(), "Second", t.TempDir(), false)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	secondID := second.ID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(secondID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"hello","params":{}}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			State struct {
				Session struct {
					ID id.ID
				}
			}
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected hello ok, got %s", resp.Error)
	}
	if resp.Result.State.Session.ID != secondID {
		t.Fatalf("expected hello for second session %s, got %s; first was %s", secondID, resp.Result.State.Session.ID, firstID)
	}
}

func TestWebSocketClientsKeepIndependentSessionSelections(t *testing.T) {
	ctrl := newTestController(t)
	firstID := ctrl.State().Session.ID
	second, err := ctrl.CreateSession(context.Background(), "Second", t.TempDir(), false)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	secondID := second.ID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	firstConn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(firstID), nil)
	if err != nil {
		t.Fatalf("dial first websocket: %v", err)
	}
	defer firstConn.Close(websocket.StatusNormalClosure, "")
	secondConn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(secondID), nil)
	if err != nil {
		t.Fatalf("dial second websocket: %v", err)
	}
	defer secondConn.Close(websocket.StatusNormalClosure, "")

	writeRPC(t, ctx, firstConn, 1, "hello", `{}`)
	if got := readRPCStateSession(t, ctx, firstConn, 1); got != firstID {
		t.Fatalf("expected first hello session %s, got %s", firstID, got)
	}
	writeRPC(t, ctx, secondConn, 1, "hello", `{}`)
	if got := readRPCStateSession(t, ctx, secondConn, 1); got != secondID {
		t.Fatalf("expected second hello session %s, got %s", secondID, got)
	}
	writeRPC(t, ctx, secondConn, 2, "get_state", `{}`)
	if got := readRPCStateSession(t, ctx, secondConn, 2); got != secondID {
		t.Fatalf("expected second get_state session %s, got %s", secondID, got)
	}
	writeRPC(t, ctx, firstConn, 2, "get_state", `{}`)
	if got := readRPCStateSession(t, ctx, firstConn, 2); got != firstID {
		t.Fatalf("expected first get_state to remain %s, got %s", firstID, got)
	}
	writeRPC(t, ctx, secondConn, 3, "switch_session", fmt.Sprintf(`{"session_id":"%s"}`, firstID))
	if got := readRPCStateSession(t, ctx, secondConn, 3); got != firstID {
		t.Fatalf("expected second client switch to %s, got %s", firstID, got)
	}
	writeRPC(t, ctx, firstConn, 3, "get_state", `{}`)
	if got := readRPCStateSession(t, ctx, firstConn, 3); got != firstID {
		t.Fatalf("expected first client to remain %s after second switch, got %s", firstID, got)
	}
}

func TestHTTPRPCEnvelopeDispatchesWebSocketMethods(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	resp, err := http.Post(srv.URL()+"/api/rpc", "application/json", strings.NewReader(`{"id":7,"method":"list_sessions","params":{}}`))
	if err != nil {
		t.Fatalf("post rpc: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected rpc ok status, got %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		ID     int  `json:"id"`
		OK     bool `json:"ok"`
		Result struct {
			ActiveID id.ID `json:"active_id"`
			Sessions []struct {
				ID id.ID `json:"id"`
			} `json:"sessions"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if !payload.OK {
		t.Fatalf("expected rpc ok, got %s", payload.Error)
	}
	if payload.ID != 7 {
		t.Fatalf("expected response id 7, got %d", payload.ID)
	}
	if payload.Result.ActiveID != ctrl.State().Session.ID || len(payload.Result.Sessions) != 1 {
		t.Fatalf("unexpected sessions response: %#v", payload.Result)
	}
}

func TestHTTPRPCMethodPathUsesExplicitSelection(t *testing.T) {
	ctrl := newTestController(t)
	second, err := ctrl.CreateSession(context.Background(), "Second", t.TempDir(), false)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	secondID := second.ID
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	resp, err := http.Post(srv.URL()+"/api/rpc/get_state?selected_session="+string(secondID), "application/json", nil)
	if err != nil {
		t.Fatalf("post rpc method: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected rpc ok status, got %d: %s", resp.StatusCode, body)
	}
	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				ID id.ID `json:"id"`
			} `json:"session"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if !payload.OK {
		t.Fatalf("expected rpc ok, got %s", payload.Error)
	}
	if payload.Result.Session.ID != secondID {
		t.Fatalf("expected selected session %s, got %s", secondID, payload.Result.Session.ID)
	}
}

func TestTrimStateTimelinesKeepsOnlyTail(t *testing.T) {
	chatID := id.ID("chat-1")
	items := make([]domain.TimelineItem, 0, 5)
	for i := 0; i < 5; i++ {
		items = append(items, domain.TimelineItem{
			ID:      id.ID(fmt.Sprintf("item-%d", i+1)),
			ChatID:  chatID,
			Seq:     int64(i + 1),
			Content: domain.UserMessage{Text: fmt.Sprintf("message %d", i+1)},
		})
	}
	state := app.State{
		ActiveChatID: chatID,
		Snapshot: chat.Snapshot{
			Chat:     domain.Chat{ID: chatID},
			Timeline: items,
		},
		Snapshots: map[id.ID]chat.Snapshot{
			chatID: {
				Chat:     domain.Chat{ID: chatID},
				Timeline: items,
			},
		},
	}

	trimmed := trimStateTimelines(state, 2)
	snapshot := trimmed.Snapshots[chatID]
	if got, want := len(snapshot.Timeline), 2; got != want {
		t.Fatalf("timeline length = %d, want %d", got, want)
	}
	if snapshot.Timeline[0].ID != "item-4" || !snapshot.TimelineHasMore || snapshot.TimelineLoadedAll || snapshot.TimelineBefore != "item-4" {
		t.Fatalf("unexpected snapshot metadata: %#v", snapshot)
	}
	if trimmed.Snapshot.Timeline[0].ID != "item-4" {
		t.Fatalf("active snapshot was not trimmed: %#v", trimmed.Snapshot.Timeline)
	}
	if len(state.Snapshots[chatID].Timeline) != 5 {
		t.Fatalf("trim mutated source state")
	}
}

func TestWebSocketHelloReturnsWelcomeForStaleURLSessionSelection(t *testing.T) {
	ctrl := newTestController(t)
	activeID := ctrl.State().Session.ID
	staleID := id.ID("019e72fa-1cb8-73ef-a5ca-247275f3f62f")
	if staleID == activeID {
		t.Fatal("test stale id unexpectedly matches active session")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(staleID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"hello","params":{}}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			State struct {
				Session struct {
					ID id.ID
				}
				Sessions []domain.Session `json:"sessions"`
				Error    string           `json:"error"`
			} `json:"state"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected stale session hello to return welcome state, got %s", resp.Error)
	}
	if resp.Result.State.Session.ID != "" {
		t.Fatalf("expected welcome state without active session, got %s", resp.Result.State.Session.ID)
	}
	if len(resp.Result.State.Sessions) == 0 || !strings.Contains(resp.Result.State.Error, string(staleID)) {
		t.Fatalf("expected welcome state with sessions and stale session message, got %#v", resp.Result.State)
	}
	if got := ctrl.State().Session.ID; got != activeID {
		t.Fatalf("expected stale session hello not to switch active session from %s, got %s", activeID, got)
	}
}

func TestRestartProcessRPCRequestsSupervisorRestart(t *testing.T) {
	ctrl := newTestController(t)
	restarted := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{
		Bind:          "127.0.0.1:0",
		NoOpenBrowser: true,
		RequestProcessRestart: func() error {
			restarted <- struct{}{}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"restart_process","params":{}}`)); err != nil {
		t.Fatalf("write restart_process: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Result struct {
			Restarting   bool `json:"restarting"`
			Acknowledged bool `json:"acknowledged"`
			Hard         bool `json:"hard"`
		} `json:"result"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode restart response: %v", err)
	}
	if !resp.OK || !resp.Result.Restarting || !resp.Result.Acknowledged || resp.Result.Hard {
		t.Fatalf("expected restart_process ok, got %#v", resp)
	}
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("expected restart_process to call restart hook")
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":2,"method":"restart_process","params":{"hard":true}}`)); err != nil {
		t.Fatalf("write hard restart_process: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 2)
	resp = struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Result struct {
			Restarting   bool `json:"restarting"`
			Acknowledged bool `json:"acknowledged"`
			Hard         bool `json:"hard"`
		} `json:"result"`
	}{}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode hard restart response: %v", err)
	}
	if !resp.OK || !resp.Result.Restarting || !resp.Result.Acknowledged || !resp.Result.Hard {
		t.Fatalf("expected hard restart_process ok, got %#v", resp)
	}
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("expected hard restart_process to call restart hook")
	}
}

func TestRestartNeededEndpointBroadcastsRestartDelta(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"hello","params":{}}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	_ = readRPCResponse(t, ctx, conn, 1)

	body := strings.NewReader(`{"version":"0.1.0","commit":"abc123","dirty":"true","build_time":"2026-06-02T12:00:00Z"}`)
	resp, err := http.Post(srv.URL()+"/api/restart-needed", "application/json", body)
	if err != nil {
		t.Fatalf("post restart-needed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected restart-needed status 200, got %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !ctrl.State().RestartNeeded {
		t.Fatal("expected controller state to mark restart needed")
	}
	if got := ctrl.State().RestartBuild.BuildID; got != "abc123-dirty @ 2026-06-02T12:00:00Z" {
		t.Fatalf("expected restart build id, got %q", got)
	}

	msg := readMessage(t, ctx, conn)
	var event struct {
		Type    string `json:"type"`
		Payload struct {
			RestartNeeded bool `json:"restart_needed"`
			RestartBuild  struct {
				Commit  string `json:"commit"`
				BuildID string `json:"build_id"`
			} `json:"restart_build"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(msg, &event); err != nil {
		t.Fatalf("decode restart event: %v", err)
	}
	if event.Type != "restart_delta" || !event.Payload.RestartNeeded {
		t.Fatalf("expected restart_delta with restart_needed=true, got %s", string(msg))
	}
	if event.Payload.RestartBuild.Commit != "abc123" || event.Payload.RestartBuild.BuildID == "" {
		t.Fatalf("expected restart build metadata, got %s", string(msg))
	}
}

func TestServerDoesNotOpenBrowserWhenExistingTabRefreshes(t *testing.T) {
	ctrl := newTestController(t)
	opened := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{
		Bind:      "127.0.0.1:0",
		OpenDelay: 30 * time.Millisecond,
		OpenBrowser: func(url string) error {
			opened <- url
			return nil
		},
	})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	resp, err := http.Get(srv.URL())
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected index status 200, got %d", resp.StatusCode)
	}

	select {
	case url := <-opened:
		t.Fatalf("expected no browser open after existing tab refresh, got %s", url)
	case <-time.After(80 * time.Millisecond):
	}
}

func TestServerOpensBrowserWhenNoWebSocketConnects(t *testing.T) {
	ctrl := newTestController(t)
	opened := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{
		Bind:      "127.0.0.1:0",
		OpenDelay: 10 * time.Millisecond,
		OpenBrowser: func(url string) error {
			opened <- url
			return nil
		},
	})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	select {
	case url := <-opened:
		if url != srv.AppURL() {
			t.Fatalf("expected opened URL %q, got %q", srv.AppURL(), url)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected browser open after timeout")
	}
}

func TestServerNoOpenBrowserSuppressesBrowserOpen(t *testing.T) {
	ctrl := newTestController(t)
	opened := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := Start(ctx, ctrl, Options{
		Bind:          "127.0.0.1:0",
		NoOpenBrowser: true,
		OpenDelay:     10 * time.Millisecond,
		OpenBrowser: func(url string) error {
			opened <- url
			return nil
		},
	})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	select {
	case url := <-opened:
		t.Fatalf("expected no browser open with --nobrowser, got %s", url)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestServerHealthEndpoint(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	resp, err := http.Get(srv.URL() + "/api/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected health status 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if body["ok"] != true || body["asset_hash"] != currentAssetHash {
		t.Fatalf("unexpected health body: %#v", body)
	}
}

func TestServerExposesDebugEndpointsOnWebPort(t *testing.T) {
	ctrl := newTestController(t)
	recorder := debugsrv.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true, Debug: recorder})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	resp, err := http.Get(srv.URL() + "/debug/runtime")
	if err != nil {
		t.Fatalf("get debug runtime: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected debug runtime status 200, got %d", resp.StatusCode)
	}
	var body debugsrv.RuntimeDebug
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode debug runtime: %v", err)
	}
	if body.Process.DebugAPI != srv.URL() {
		t.Fatalf("expected debug API %q, got %q", srv.URL(), body.Process.DebugAPI)
	}
}

func TestWebSocketHelloReturnsState(t *testing.T) {
	ctrl := newTestController(t)
	recorder := debugsrv.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true, Debug: recorder})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"hello","params":{}}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	msg := readMessage(t, ctx, conn)
	var resp rpcResponse
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected hello ok, got %#v", resp)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected hello result object, got %#v", resp.Result)
	}
	if result["asset_hash"] != currentAssetHash {
		t.Fatalf("expected asset hash %q, got %#v", currentAssetHash, result["asset_hash"])
	}
	clientID, _ := result["client_id"].(string)
	if clientID == "" {
		t.Fatalf("expected hello client id, got %#v", result)
	}
	if _, ok := result["state"].(map[string]any); !ok {
		t.Fatalf("expected hello state object, got %#v", result["state"])
	}
	clients := recorder.Clients()
	if len(clients) != 1 || clients[0].ID != clientID || !clients[0].Connected {
		t.Fatalf("expected registered debug client %q, got %#v", clientID, clients)
	}
	chats := recorder.Chats()
	if len(chats) == 0 || chats[0].ID == "" {
		t.Fatalf("expected debug chat records after hello, got %#v", chats)
	}
}

func TestWebSocketClientStateUpdatesDebugClient(t *testing.T) {
	ctrl := newTestController(t)
	recorder := debugsrv.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true, Debug: recorder})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"hello","params":{}}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var hello rpcResponse
	if err := json.Unmarshal(msg, &hello); err != nil {
		t.Fatalf("decode hello: %v", err)
	}
	result := hello.Result.(map[string]any)
	clientID := result["client_id"].(string)
	activeChatID := ctrl.State().ActiveChatID
	update := fmt.Sprintf(`{"id":2,"method":"client_state","params":{"selected_session":"%s","selected_chat":"%s","document_visible":true,"window_focused":true,"composer_focused":true,"viewport_width":120,"viewport_height":40,"stick_to_bottom":true,"open_dialog":"models","interrupt_visible":true,"interrupt_armed":true}}`, ctrl.State().Session.ID, activeChatID)
	if err := conn.Write(ctx, websocket.MessageText, []byte(update)); err != nil {
		t.Fatalf("write client_state: %v", err)
	}
	_ = readRPCResponse(t, ctx, conn, 2)
	client, ok := recorder.Client(clientID)
	if !ok {
		t.Fatalf("expected debug client %q", clientID)
	}
	if client.SelectedChat != activeChatID || !client.ComposerFocused || client.OpenDialog != "models" || !client.InterruptVisible || !client.InterruptArmed {
		t.Fatalf("unexpected client debug state: %#v", client)
	}
}

func TestApprovalRPCRequiresToolCallID(t *testing.T) {
	ctrl := newTestController(t)
	srv := &Server{controller: ctrl}
	for _, method := range []string{"approve", "deny"} {
		t.Run(method, func(t *testing.T) {
			_, err := srv.handleRPC(context.Background(), "client", method, json.RawMessage(`{"id":"legacy-approval"}`))
			if err == nil || !strings.Contains(err.Error(), "tool_call_id is required") {
				t.Fatalf("expected missing tool_call_id error, got %v", err)
			}
		})
	}
}

func TestWebSocketChatUpdateIsCompactedToSingleItemDelta(t *testing.T) {
	item := domain.TimelineItem{
		ID:     "019aa000-0000-7000-8000-000000000042",
		ChatID: "chat-7",
		Seq:    3,
		Content: domain.AssistantMessage{
			Text: "streamed",
		},
	}
	update := chat.Update{
		Snapshot: chat.Snapshot{
			Chat:     domain.Chat{ID: "chat-7", SessionID: "session-1", Title: "Chat"},
			Timeline: []domain.TimelineItem{{ID: "019aa000-0000-7000-8000-000000000001", ChatID: "chat-7", Seq: 1, Content: domain.UserMessage{Text: "old"}}, item},
			Status:   chat.StatusStreamingResponse,
			Active:   true,
		},
		TranscriptChanged: true,
		StatusChanged:     true,
	}
	event, ok := webEventFromControllerEvent(app.Event{Seq: 9, Type: "chat_delta", Payload: update})
	if !ok {
		t.Fatal("expected compact web event")
	}
	if event.Type != "chat_delta" {
		t.Fatalf("expected chat_delta, got %q", event.Type)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	payload := string(data)
	if strings.Contains(payload, `"Timeline"`) || strings.Contains(payload, `"timeline"`) || strings.Contains(payload, `"Snapshots"`) || strings.Contains(payload, `"snapshots"`) {
		t.Fatalf("expected compact chat delta without full timelines/snapshots, got %s", payload)
	}
	if !strings.Contains(payload, `"item"`) || !strings.Contains(payload, `"streamed"`) {
		t.Fatalf("expected changed item in chat delta, got %s", payload)
	}
	if strings.Contains(payload, `"old"`) {
		t.Fatalf("expected only the changed timeline item, got %s", payload)
	}
}

func TestWebSocketChatUpdateCanReplaceTimeline(t *testing.T) {
	item := domain.TimelineItem{
		ID:      "019aa000-0000-7000-8000-000000000042",
		ChatID:  "chat-7",
		Seq:     1,
		Content: domain.UserMessage{Text: "kept"},
	}
	update := chat.Update{
		Snapshot: chat.Snapshot{
			Chat:     domain.Chat{ID: "chat-7", SessionID: "session-1", Title: "Chat"},
			Timeline: []domain.TimelineItem{item},
			Status:   chat.StatusIdle,
			Active:   true,
		},
		ReplaceTimeline: true,
	}
	delta := chatDeltaFromUpdate(update)
	if !delta.ReplaceTimeline {
		t.Fatal("expected replace timeline flag")
	}
	if delta.Item != nil {
		t.Fatalf("expected no single item patch for replace, got %#v", delta.Item)
	}
	if len(delta.Timeline) != 1 || delta.Timeline[0].ID != item.ID {
		t.Fatalf("expected replacement timeline, got %#v", delta.Timeline)
	}
}

func TestWebSocketStreamingDeltaUsesMutatedSnapshotItem(t *testing.T) {
	itemID := id.ID("019aa000-0000-7000-8000-000000000043")
	emptyEventItem := domain.TimelineItem{
		ID:      itemID,
		ChatID:  "chat-7",
		Seq:     2,
		Content: domain.AssistantMessage{},
	}
	streamedSnapshotItem := emptyEventItem
	streamedSnapshotItem.Content = domain.AssistantMessage{Text: "partial stream"}
	update := chat.Update{
		Event: &domain.Event{
			Kind: domain.EventKindMessageDelta,
			Text: "partial stream",
			Item: emptyEventItem,
		},
		Snapshot: chat.Snapshot{
			Chat:     domain.Chat{ID: "chat-7", SessionID: "session-1", Title: "Chat"},
			Timeline: []domain.TimelineItem{streamedSnapshotItem},
			Status:   chat.StatusStreamingResponse,
			Active:   true,
		},
		TranscriptChanged: true,
		StatusChanged:     true,
	}
	delta := chatDeltaFromUpdate(update)
	if delta.Item == nil {
		t.Fatal("expected streaming chat delta item")
	}
	assistant, ok := delta.Item.Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant item, got %T", delta.Item.Content)
	}
	if assistant.Text != "partial stream" {
		t.Fatalf("expected mutated snapshot text in streaming delta, got %q", assistant.Text)
	}
}

func TestChatDeltaOmitsExecProcessesWhenSnapshotIsNotAuthoritative(t *testing.T) {
	update := chat.Update{
		Snapshot: chat.Snapshot{
			Chat:   domain.Chat{ID: "chat-7", SessionID: "session-1", Title: "Chat"},
			Status: chat.StatusWaitingLLM,
			Active: true,
		},
		StatusChanged: true,
	}
	event, ok := webEventFromControllerEvent(app.Event{Seq: 10, Type: "chat_delta", Payload: update})
	if !ok {
		t.Fatal("expected chat delta event")
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if strings.Contains(string(data), "exec_processes") {
		t.Fatalf("non-authoritative chat delta should not clear exec process list, got %s", string(data))
	}
}

func TestChatDeltaIncludesEmptyExecProcessesWhenAuthoritative(t *testing.T) {
	update := chat.Update{
		Snapshot: chat.Snapshot{
			Chat:          domain.Chat{ID: "chat-7", SessionID: "session-1", Title: "Chat"},
			ExecProcesses: []domain.ExecProcess{},
			Status:        chat.StatusIdle,
		},
		StatusChanged: true,
	}
	event, ok := webEventFromControllerEvent(app.Event{Seq: 11, Type: "chat_delta", Payload: update})
	if !ok {
		t.Fatal("expected chat delta event")
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if !strings.Contains(string(data), `"exec_processes":[]`) {
		t.Fatalf("authoritative empty exec process list should clear UI state, got %s", string(data))
	}
}

func TestWebSocketSnapshotEventIsCompactedToStateDelta(t *testing.T) {
	state := app.State{
		Session:       domain.Session{ID: "session-1", Title: "Session"},
		Chats:         []domain.Chat{{ID: "chat-7", SessionID: "session-1", Title: "Chat"}},
		ActiveChatID:  "chat-7",
		RestartNeeded: true,
		Milestones: planning.Plan{
			Summary:    "Live plan",
			Milestones: []planning.Milestone{{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusExecuting}},
		},
		Todos:      []planning.TodoItem{{ID: "todo-1", MilestoneRef: "alpha", Content: "First", Status: planning.TodoStatusInProgress}},
		TodosByRef: map[string][]planning.TodoItem{"alpha": {{ID: "todo-1", MilestoneRef: "alpha", Content: "First", Status: planning.TodoStatusInProgress}}},
		Snapshots: map[id.ID]chat.Snapshot{
			"chat-7": {
				Chat:     domain.Chat{ID: "chat-7", SessionID: "session-1", Title: "Chat"},
				Timeline: []domain.TimelineItem{{ID: "019aa000-0000-7000-8000-000000000001", ChatID: "chat-7", Seq: 1, Content: domain.UserMessage{Text: "old transcript"}}},
			},
		},
	}
	event, ok := webEventFromControllerEvent(app.Event{Seq: 2, Type: "snapshot", Payload: state})
	if !ok {
		t.Fatal("expected compact state event")
	}
	if event.Type != "state_delta" {
		t.Fatalf("expected state_delta, got %q", event.Type)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	payload := string(data)
	if strings.Contains(payload, `"snapshot"`) || strings.Contains(payload, `"snapshots"`) || strings.Contains(payload, `"timeline"`) || strings.Contains(payload, "old transcript") {
		t.Fatalf("expected state delta without transcript snapshots, got %s", payload)
	}
	if !strings.Contains(payload, `"chats"`) || !strings.Contains(payload, `"Chat"`) {
		t.Fatalf("expected state delta to include sidebar chat state, got %s", payload)
	}
	if !strings.Contains(payload, `"milestones"`) || !strings.Contains(payload, `"todos_by_milestone"`) || !strings.Contains(payload, `"Alpha"`) {
		t.Fatalf("expected state delta to include planning state, got %s", payload)
	}
	if !strings.Contains(payload, `"restart_needed":true`) {
		t.Fatalf("expected state delta to include restart-needed state, got %s", payload)
	}
}

func TestWebSocketSelectionDeltaIsNotBroadcast(t *testing.T) {
	event, ok := webEventFromControllerEvent(app.Event{Seq: 3, Type: "selection_delta", Payload: map[string]id.ID{"active_chat_id": "chat-2"}})
	if ok {
		t.Fatalf("expected selection delta to be private to the initiating RPC, got %#v", event)
	}
}

func TestIndexServesHTML(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	resp, err := http.Get(srv.URL())
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected index status 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("expected index to disable stale cache, got %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	document := string(body)
	appJS := getAssetBody(t, srv, "/assets/app.js")
	appCSS := getAssetBody(t, srv, "/assets/app.css")
	fullPage := document + "\n" + appJS + "\n" + appCSS
	if !strings.Contains(document, `/assets/app.css`) {
		t.Fatalf("expected app CSS to be loaded from embedded assets")
	}
	if !strings.Contains(document, `/assets/app.js`) {
		t.Fatalf("expected app JS to be loaded from embedded assets")
	}
	if !strings.Contains(fullPage, `welcomeMode()`) ||
		!strings.Contains(fullPage, `welcome-view`) ||
		!strings.Contains(fullPage, `beginCreateSessionFromWelcome()`) ||
		!strings.Contains(fullPage, `allowSessionURLSync`) {
		t.Fatalf("expected app to include welcome screen and opt-in session URL sync")
	}
	if strings.Contains(document, `<style>`) || strings.Contains(document, `function koderApp()`) {
		t.Fatalf("expected first-party CSS and JS to live in embedded asset files, not inline index content")
	}
	appScript := strings.Index(document, `/assets/app.js`)
	alpineScript := strings.Index(document, `/assets/vendor/alpine/cdn.min.js`)
	if appScript < 0 || alpineScript < 0 || appScript > alpineScript {
		t.Fatalf("expected app JS to load before Alpine so x-data scope is registered before Alpine initializes")
	}
	if !strings.Contains(fullPage, `@keydown="onComposerKeydown($event)"`) || !strings.Contains(fullPage, `if (ev.key === 'Enter' && !ev.shiftKey)`) {
		t.Fatalf("expected plain enter to submit composer")
	}
	if !strings.Contains(fullPage, `composerSendMenuOpen`) ||
		!strings.Contains(fullPage, `Send as steer`) ||
		!strings.Contains(fullPage, `send({steer: true})`) ||
		!strings.Contains(fullPage, `ev.altKey`) ||
		!strings.Contains(fullPage, `steer: !!options.steer`) {
		t.Fatalf("expected composer to expose explicit steer submission")
	}
	if !strings.Contains(fullPage, `activeTokenUsageLabel()`) ||
		!strings.Contains(fullPage, `activeCachedTokenLabel()`) ||
		!strings.Contains(fullPage, `Token burn since compact`) {
		t.Fatalf("expected sidebar to render chat token usage counters")
	}
	if !strings.Contains(fullPage, `class="btn btn-danger interrupt-button"`) ||
		!strings.Contains(fullPage, `:disabled="!chatInterruptible()"`) ||
		!strings.Contains(fullPage, `rpc('stop_after_turn', {})`) ||
		!strings.Contains(fullPage, `rpc('stop', {})`) ||
		!strings.Contains(fullPage, `event.key === 'Escape'`) {
		t.Fatalf("expected composer interrupt button with staged then immediate stop behavior and Escape shortcut")
	}
	if !strings.Contains(fullPage, `@click="requestRestart()"`) ||
		!strings.Contains(fullPage, `rpc('restart_process', {hard})`) ||
		!strings.Contains(fullPage, `:disabled="restartRequestPending"`) ||
		!strings.Contains(fullPage, `Restart acknowledged; press again for hard restart`) ||
		!strings.Contains(fullPage, `restartHardRequested`) ||
		!strings.Contains(fullPage, `restartBuildAgeLabel()`) ||
		!strings.Contains(fullPage, `commit + ' (' + age + ')'`) {
		t.Fatalf("expected restart-needed control to acknowledge restart and allow hard restart escalation")
	}
	if !strings.Contains(fullPage, `hello.client_id`) || !strings.Contains(fullPage, `rpcOn(this.ws, 'client_state'`) || !strings.Contains(fullPage, `selected_chat: String(this.activeChatID() || '')`) {
		t.Fatalf("expected browser to report per-client debug state")
	}
	if strings.Contains(fullPage, assetHashPlaceholder) {
		t.Fatalf("expected served index to contain the rendered asset hash")
	}
	if !strings.Contains(fullPage, `window.KODER_ASSET_HASH = "`+currentAssetHash+`"`) {
		t.Fatalf("expected served index to publish the current asset hash")
	}
	if !strings.Contains(fullPage, `/assets/vendor/bootstrap/bootstrap.min.css`) {
		t.Fatalf("expected Bootstrap CSS to be loaded from vendored assets")
	}
	if !strings.Contains(fullPage, `/assets/vendor/bootstrap-icons/font/bootstrap-icons.css`) {
		t.Fatalf("expected Bootstrap Icons to be loaded from vendored assets")
	}
	if !strings.Contains(fullPage, `/assets/vendor/marked/marked.umd.js`) {
		t.Fatalf("expected marked to be loaded from vendored assets")
	}
	if !strings.Contains(fullPage, `/assets/vendor/dompurify/purify.min.js`) {
		t.Fatalf("expected DOMPurify to be loaded from vendored assets")
	}
	if !strings.Contains(fullPage, `/assets/vendor/highlight/highlight.min.js`) {
		t.Fatalf("expected highlight.js to be loaded from vendored assets")
	}
	if !strings.Contains(fullPage, `/assets/vendor/mermaid/mermaid.min.js`) {
		t.Fatalf("expected Mermaid to be loaded from vendored assets")
	}
	if !strings.Contains(fullPage, `/assets/vendor/alpine/cdn.min.js`) {
		t.Fatalf("expected Alpine to be loaded from vendored assets")
	}
	if !strings.Contains(fullPage, `.settings-tabs .list-group-item.active { background: var(--bs-primary);`) ||
		!strings.Contains(fullPage, `.settings-tabs { min-height: 0; overflow: auto; display: flex; flex-direction: column; gap:`) {
		t.Fatalf("expected settings tabs to render as block buttons with primary active background")
	}
	if !strings.Contains(fullPage, `x-effect="renderMarkdownElement($el, item.content?.text || '', itemMarkdownOptions(item))"`) {
		t.Fatalf("expected assistant text to render through status-aware markdown element renderer")
	}
	if !strings.Contains(fullPage, `x-effect="renderMarkdownElement($el, pendingText(), {deferDiagrams: true, incremental: true})"`) {
		t.Fatalf("expected streaming assistant text to render markdown incrementally with deferred diagrams")
	}
	if !strings.Contains(fullPage, `class="turn user-turn"`) || !strings.Contains(fullPage, `.transcript-turn { width: 100%; max-width: none; }`) {
		t.Fatalf("expected user turns to use the full transcript width")
	}
	if !strings.Contains(fullPage, `class="markdown-body user-markdown-body"`) ||
		!strings.Contains(fullPage, `:class="userMessageIcon(item)"`) ||
		!strings.Contains(fullPage, `<span class="turn-source-label">user</span>`) ||
		!strings.Contains(fullPage, `class="turn-source-qualifier" x-show="userMessageSourceQualifier(item)"`) ||
		!strings.Contains(fullPage, `class="user-message-text" x-html="markdownHTML(item.content?.text || '')"`) ||
		!strings.Contains(fullPage, `case 'auto_generated': return 'auto-generated'`) {
		t.Fatalf("expected user turns to render icon/source headers and message text")
	}
	if !strings.Contains(fullPage, `class="turn-header"`) ||
		!strings.Contains(fullPage, `class="turn-timestamp" :datetime="itemTimestamp(item)" x-text="formatItemTime(item)"`) ||
		!strings.Contains(fullPage, `return pad(date.getHours()) + ':' + pad(date.getMinutes()) + ':' + pad(date.getSeconds())`) ||
		!strings.Contains(fullPage, `.turn-header { display: flex; align-items: center; justify-content: space-between;`) {
		t.Fatalf("expected transcript entries to render right-aligned HH:MM:SS timestamps in their header")
	}
	if !strings.Contains(fullPage, `A tool call was interrupted by the process restart and has been marked failed.`) ||
		!strings.Contains(fullPage, `return 'auto_resume'`) {
		t.Fatalf("expected stored auto-resume messages without source metadata to render as auto-resume")
	}
	if !strings.Contains(fullPage, `class="turn assistant-turn"`) {
		t.Fatalf("expected assistant turns to render")
	}
	if !strings.Contains(fullPage, `<i class="bi bi-robot"></i><span class="turn-source-label">agent</span>`) ||
		!strings.Contains(fullPage, `class="turn-header tool-header"`) ||
		!strings.Contains(fullPage, `<i class="bi bi-wrench-adjustable"></i>`) ||
		!strings.Contains(fullPage, `class="turn-source-qualifier" x-show="lintFiles(item.content || {})"`) {
		t.Fatalf("expected user, assistant, and tool sections to share icon-title-timestamp headers")
	}
	if !strings.Contains(fullPage, `marked.parse(source)`) || !strings.Contains(fullPage, `DOMPurify.sanitize`) || !strings.Contains(fullPage, `hljs.highlight`) {
		t.Fatalf("expected browser markdown renderer to parse, sanitize, and syntax-highlight")
	}
	if !strings.Contains(fullPage, `language-mermaid`) || !strings.Contains(fullPage, `mermaid.render`) || !strings.Contains(fullPage, `sanitizeDiagramSVG`) {
		t.Fatalf("expected browser markdown renderer to render Mermaid diagrams and sanitize SVG output")
	}
	if !strings.Contains(fullPage, `deferStreamingDiagrams`) || !strings.Contains(fullPage, `diagram-stream-placeholder`) || !strings.Contains(fullPage, `Mermaid diagram`) || !strings.Contains(fullPage, `SVG`) ||
		!strings.Contains(fullPage, `stableMarkdownPrefixLength`) || !strings.Contains(fullPage, `data-markdown-tail`) || !strings.Contains(fullPage, `itemMarkdownOptions(item)`) {
		t.Fatalf("expected streaming markdown renderer to defer Mermaid and SVG rendering")
	}
	if !strings.Contains(fullPage, `.markdown-body svg { max-width: 100%; height: auto; }`) || !strings.Contains(fullPage, `foreignObject`) {
		t.Fatalf("expected inline SVG output to be constrained and sanitized")
	}
	if !strings.Contains(fullPage, `toolResultHTML(tool)`) || !strings.Contains(fullPage, `function renderToolResult(tool)`) {
		t.Fatalf("expected tool results to render through the per-tool formatter")
	}
	if !strings.Contains(fullPage, `toolErrorHTML(tool)`) || !strings.Contains(fullPage, `function renderToolError(tool)`) || !strings.Contains(fullPage, `toolStatusBadge(tool)`) || !strings.Contains(fullPage, `toolStatusBadgeClass(tool)`) {
		t.Fatalf("expected tool errors to render through the compact per-tool formatter")
	}
	if !strings.Contains(fullPage, `item.kind === 'notice'`) || !strings.Contains(fullPage, `noticeText(item.content || {})`) || !strings.Contains(fullPage, `.notice-warning`) {
		t.Fatalf("expected notices to render as compact UI instead of raw JSON")
	}
	if !strings.Contains(fullPage, `noticeReasonText`) ||
		!strings.Contains(fullPage, `case 'user_interrupted': return 'user interrupted'`) ||
		!strings.Contains(fullPage, `case 'process_terminating': return 'process terminating'`) ||
		!strings.Contains(fullPage, `case 'process_restart': return 'process restarting'`) {
		t.Fatalf("expected interruption notices to render readable reason text")
	}
	if !strings.Contains(fullPage, `function renderDiffBlock(title, diff)`) || !strings.Contains(fullPage, `tool-diff-add`) || !strings.Contains(fullPage, `tool-diff-del`) {
		t.Fatalf("expected edit and patch results to use colored diff rendering")
	}
	if !strings.Contains(fullPage, `compactLines(lines, head = 2, tail = 2)`) {
		t.Fatalf("expected file write results to use compact head/tail rendering")
	}
	if !strings.Contains(fullPage, `toolStatus(tool) === 'done' || toolStatus(tool) === 'errored'`) || !strings.Contains(fullPage, `return 'exit ' + exitCode`) || !strings.Contains(fullPage, `isBareExitStatus(error)`) || !strings.Contains(fullPage, `return renderCompactBlock('Output'`) {
		t.Fatalf("expected bash tool rendering to show Ran command, exit code, and compact output")
	}
	if !strings.Contains(fullPage, `return status === 'completed' ? 'done' : status`) ||
		!strings.Contains(fullPage, `tool-status-badge-done`) ||
		!strings.Contains(fullPage, `tool-status-badge-running`) ||
		!strings.Contains(fullPage, `tool-status-badge-error`) {
		t.Fatalf("expected colorful tool status badges with done wording")
	}
	if !strings.Contains(fullPage, `if (args.command) values.push(args.command)`) ||
		!strings.Contains(fullPage, `if (args.cmd) values.push(args.cmd)`) ||
		!strings.Contains(fullPage, `if (args.process_id) values.push('process_id=' + args.process_id)`) ||
		!strings.Contains(fullPage, `(args.yield_time_ms ? 'wait=' : 'timeout=') + timeout`) ||
		!strings.Contains(fullPage, `case 'exec_command': return command ? 'Start exec ' + command : 'Start exec'`) ||
		!strings.Contains(fullPage, `if (command) lines.push('command: ' + command)`) ||
		!strings.Contains(fullPage, `if (timeout) lines.push('timeout: ' + timeout)`) ||
		!strings.Contains(fullPage, `function execResultLines(data, fallback)`) {
		t.Fatalf("expected command preview and exec result helpers")
	}
	if !strings.Contains(fullPage, `tool-result-body-mono`) || !strings.Contains(fullPage, `renderCompactBlock('Result', execResultLines(data, toolResultText(tool)), 'tool-result-body-mono')`) {
		t.Fatalf("expected exec output to render with monospace result styling")
	}
	if !strings.Contains(fullPage, `function renderImagePreviewBlock`) ||
		!strings.Contains(fullPage, `if (kind === 'view_image')`) ||
		!strings.Contains(fullPage, `data-lightbox-src`) ||
		!strings.Contains(fullPage, `handleMediaPreviewClick`) ||
		!strings.Contains(fullPage, `image-lightbox`) ||
		!strings.Contains(fullPage, `.tool-image-thumb`) {
		t.Fatalf("expected view_image results to render clickable image thumbnails with a lightbox")
	}
	if !strings.Contains(fullPage, `execProcesses()`) ||
		!strings.Contains(fullPage, `allExecProcesses()`) ||
		!strings.Contains(fullPage, `showAllExecProcesses`) ||
		!strings.Contains(fullPage, `execProcessState(process) === 'running'`) ||
		!strings.Contains(fullPage, `exec-process-tooltip`) ||
		!strings.Contains(fullPage, `execHoverStyle()`) ||
		!strings.Contains(fullPage, `terminate_exec_process`) ||
		!strings.Contains(fullPage, `terminateExecProcess(process)`) ||
		!strings.Contains(fullPage, `execProcessTimeout(process)`) ||
		!strings.Contains(fullPage, `execProcessOutput(process)`) {
		t.Fatalf("expected current chat exec processes to render with filtering, overlay tooltips, and termination controls")
	}
	if !strings.Contains(fullPage, `hello.asset_hash !== window.KODER_ASSET_HASH`) || !strings.Contains(fullPage, `location.reload()`) {
		t.Fatalf("expected websocket reconnect to reload on asset mismatch")
	}
	if !strings.Contains(fullPage, `rpcOn(ws, 'hello', {})`) {
		t.Fatalf("expected hello RPC to be bound to the socket that opened")
	}
	if !strings.Contains(fullPage, `applyChatDelta(delta)`) || !strings.Contains(fullPage, `patchTimelineItem`) || !strings.Contains(fullPage, `msg.type === 'chat_delta'`) {
		t.Fatalf("expected browser to patch compact chat deltas")
	}
	if !strings.Contains(fullPage, `rollback_chat`) ||
		!strings.Contains(fullPage, `fork_chat`) ||
		!strings.Contains(fullPage, `timelineAction.open`) ||
		!strings.Contains(fullPage, `timelineItemActionAvailable(item)`) ||
		!strings.Contains(fullPage, `openTimelineRollback(item)`) ||
		!strings.Contains(fullPage, `openTimelineFork(item)`) ||
		!strings.Contains(fullPage, `replace_timeline`) {
		t.Fatalf("expected transcript rollback/fork controls with html modals and timeline replacement")
	}
	if !strings.Contains(fullPage, `const id = String(delta.chat_id || delta.ChatID || delta.chat?.id || delta.chat?.ID || '').trim()`) {
		t.Fatalf("expected browser chat deltas to keep UUID chat ids as strings")
	}
	if !strings.Contains(fullPage, `if (!id) throw new Error('timeline delta missing item id')`) || !strings.Contains(fullPage, `return existingID === id`) {
		t.Fatalf("expected browser timeline patching to require stable item ids")
	}
	if !strings.Contains(fullPage, `function readRangeLabel(args, data)`) ||
		!strings.Contains(fullPage, `if (!requestedStart && !requestedEnd) return ''`) ||
		!strings.Contains(fullPage, `return renderCompactBlock(readTitle(path, args, data), lines)`) {
		t.Fatalf("expected read tool rendering to include requested line ranges")
	}
	if !strings.Contains(fullPage, `:key="item.id || item.ID"`) {
		t.Fatalf("expected transcript rows to use item ids directly")
	}
	if !strings.Contains(fullPage, `applyStateDelta(delta)`) || !strings.Contains(fullPage, `msg.type === 'state_delta'`) {
		t.Fatalf("expected browser to patch compact state deltas")
	}
	if !strings.Contains(fullPage, `handleSocketOpen(ws)`) || !strings.Contains(fullPage, `ws.readyState === WebSocket.OPEN && !this.connected`) || !strings.Contains(fullPage, `queueMicrotask`) {
		t.Fatalf("expected open-but-not-connected websocket state to be recovered for Firefox")
	}
	if !strings.Contains(fullPage, `ws.readyState !== WebSocket.OPEN`) || !strings.Contains(fullPage, `try {`) {
		t.Fatalf("expected websocket sends to be guarded against closed sockets")
	}
	if !strings.Contains(fullPage, `rejectPending('connection closed')`) {
		t.Fatalf("expected websocket close to reject pending RPCs")
	}
	if !strings.Contains(fullPage, `connectionLabel()`) || !strings.Contains(fullPage, `return 'connecting'`) {
		t.Fatalf("expected connection badge to show connecting instead of offline during reconnect")
	}
	if !strings.Contains(fullPage, `const id = String(raw || '').trim()`) {
		t.Fatalf("expected selected chat restore to keep UUID chat ids as strings")
	}
	if !strings.Contains(fullPage, `connectWatchdog`) || !strings.Contains(fullPage, `WebSocket.CONNECTING`) || !strings.Contains(fullPage, `ws.close()`) {
		t.Fatalf("expected stuck websocket handshakes to be closed and retried")
	}
	if !strings.Contains(fullPage, `msg.type === 'heartbeat'`) ||
		!strings.Contains(fullPage, `checkWebsocketHealth()`) ||
		!strings.Contains(fullPage, `lastWSMessageAt`) ||
		!strings.Contains(fullPage, `reconnectStaleSocket`) ||
		!strings.Contains(fullPage, `websocket message failed`) {
		t.Fatalf("expected websocket heartbeat/watchdog handling for stale live-update sockets")
	}
	if !strings.Contains(fullPage, `}, 500);`) || !strings.Contains(fullPage, `Math.min(2000`) || !strings.Contains(fullPage, `reconnectDelay: 150`) {
		t.Fatalf("expected reconnect timing to back off without spamming")
	}
	if !strings.Contains(fullPage, `connectWhenReady()`) || !strings.Contains(fullPage, `fetch('/api/health'`) || !strings.Contains(fullPage, `server not ready`) {
		t.Fatalf("expected reconnect to probe HTTP readiness before opening websocket")
	}
	if !strings.Contains(fullPage, `performance.mark('koder-ready')`) {
		t.Fatalf("expected browser readiness to be marked after hello hydration")
	}
	if !strings.Contains(fullPage, `window.addEventListener('online'`) || !strings.Contains(fullPage, `window.addEventListener('focus'`) || !strings.Contains(fullPage, `visibilitychange`) {
		t.Fatalf("expected browser to reconnect immediately when page becomes active or network returns")
	}
	if !strings.Contains(fullPage, `transcriptScrollState()`) || !strings.Contains(fullPage, `distance <= 48`) {
		t.Fatalf("expected transcript scroll anchoring when new output arrives")
	}
	if !strings.Contains(fullPage, `transcriptStickToBottom`) || !strings.Contains(fullPage, `updateTranscriptStickiness()`) {
		t.Fatalf("expected transcript sticky-bottom intent to be tracked from scroll events")
	}
	if !strings.Contains(fullPage, `@scroll.passive="onTranscriptScroll()"`) ||
		!strings.Contains(fullPage, `@keydown.home.prevent="loadAllTimeline()"`) ||
		!strings.Contains(fullPage, `load_timeline`) ||
		!strings.Contains(fullPage, `mergeTimelinePage(page`) ||
		!strings.Contains(fullPage, `timelineLoadingActive()`) {
		t.Fatalf("expected transcript timeline to lazy-load older chunks and load all on Home")
	}
	if !strings.Contains(fullPage, `scroll.stickToBottom`) || !strings.Contains(fullPage, `el.scrollTop = scroll.top`) {
		t.Fatalf("expected transcript to follow only when near bottom and preserve scroll otherwise")
	}
	if !strings.Contains(fullPage, `scrollRestoreSeq`) || !strings.Contains(fullPage, `seq === this.scrollRestoreSeq`) {
		t.Fatalf("expected stale deferred transcript scroll restorations to be ignored")
	}
	if !strings.Contains(fullPage, `afterTranscriptDOMUpdate`) || !strings.Contains(fullPage, `requestAnimationFrame`) ||
		!strings.Contains(fullPage, `Promise.resolve(rendered).then`) ||
		!strings.Contains(fullPage, `return renderMermaidIn(root).then`) {
		t.Fatalf("expected transcript scroll restoration to run after deferred DOM height updates with optional diagram rendering")
	}
	if !strings.Contains(fullPage, `applyState(s, {scrollToBottom: true})`) {
		t.Fatalf("expected explicit chat switches to scroll to the bottom")
	}
	if !strings.Contains(fullPage, `this.applyState((hello && hello.state) || hello || {}, {scrollToBottom: true})`) {
		t.Fatalf("expected fresh page loads to start at the bottom of the transcript")
	}
	if !strings.Contains(fullPage, `class="form-control composer-input" rows="1"`) {
		t.Fatalf("expected composer to start as a single line")
	}
	if !strings.Contains(fullPage, `@input="onComposerInput()"`) {
		t.Fatalf("expected composer input to resize itself as text changes")
	}
	if !strings.Contains(fullPage, `composer_completions`) {
		t.Fatalf("expected composer to request web completions")
	}
	if !strings.Contains(fullPage, `acceptCompletion(this.completion.selected)`) {
		t.Fatalf("expected composer completion keyboard acceptance")
	}
	if !strings.Contains(fullPage, `completion.items.length > 0`) {
		t.Fatalf("expected composer completion menu")
	}
	if !strings.Contains(fullPage, `* 0.2`) {
		t.Fatalf("expected composer height to cap at 20 percent of the viewport")
	}
	if !strings.Contains(fullPage, `el.style.overflowY = el.scrollHeight > maxHeight ? 'auto' : 'hidden'`) {
		t.Fatalf("expected composer to scroll after reaching the height cap")
	}
	if !strings.Contains(fullPage, `composerDraftPreferenceName()`) ||
		!strings.Contains(fullPage, `restoreComposerDraftForActiveChat()`) ||
		!strings.Contains(fullPage, `focusComposerAfterInitialLoad()`) ||
		!strings.Contains(fullPage, `focusComposerAndInsert(event.key)`) ||
		!strings.Contains(fullPage, `textEntryActive()`) ||
		!strings.Contains(fullPage, `this.$refs.composerInput`) ||
		!strings.Contains(fullPage, `this.$watch('draft', () => this.writeComposerDraft())`) ||
		!strings.Contains(fullPage, `preserveComposerDraftDuringSend`) {
		t.Fatalf("expected composer drafts to survive browser reloads and focus the composer")
	}
	if !strings.Contains(fullPage, `@paste.prevent="onComposerPaste($event)"`) ||
		!strings.Contains(fullPage, `/api/attachments/clipboard-image`) ||
		!strings.Contains(fullPage, `composerAttachments`) ||
		!strings.Contains(fullPage, `this.rpc('send_prompt', {text, attachments, steer: !!options.steer})`) ||
		!strings.Contains(fullPage, `this.send({steer: true})`) {
		t.Fatalf("expected composer to upload pasted images and send them as attachments")
	}
	if !strings.Contains(fullPage, `activeQueue().length > 0`) ||
		!strings.Contains(fullPage, `reorder_queue`) ||
		!strings.Contains(fullPage, `delete_queue_item`) ||
		!strings.Contains(fullPage, `send_queue_item_now`) {
		t.Fatalf("expected composer queue controls and RPCs")
	}
	if !strings.Contains(fullPage, `text === '/permissions'`) {
		t.Fatalf("expected /permissions to be handled locally")
	}
	if !strings.Contains(fullPage, `text === '/settings'`) || !strings.Contains(fullPage, `text === '/model'`) {
		t.Fatalf("expected settings and model slash commands to be handled locally")
	}
	if !strings.Contains(fullPage, `text.startsWith('/compact ')`) ||
		!strings.Contains(fullPage, `instructions: text.slice('/compact'.length).trim()`) {
		t.Fatalf("expected /compact to accept optional instructions")
	}
	if !strings.Contains(fullPage, `set_access_settings`) {
		t.Fatalf("expected access UI to call set_access_settings")
	}
	if !strings.Contains(fullPage, `openModelDialog()`) {
		t.Fatalf("expected model text to open model dialog")
	}
	if !strings.Contains(fullPage, `:title="activeModelTooltip()"`) ||
		!strings.Contains(fullPage, `Context: `) ||
		!strings.Contains(fullPage, `Images: `) ||
		!strings.Contains(fullPage, `PDFs: `) {
		t.Fatalf("expected model sidebar hover tooltip to show context and capabilities")
	}
	if !strings.Contains(fullPage, `list_models`) {
		t.Fatalf("expected model dialog to list models")
	}
	if !strings.Contains(fullPage, `refreshModelOptions()`) || !strings.Contains(fullPage, `Refresh models`) {
		t.Fatalf("expected model dialog to refresh live model options")
	}
	if !strings.Contains(fullPage, `set_model`) {
		t.Fatalf("expected model dialog to set model")
	}
	if !strings.Contains(fullPage, `save_model_config`) ||
		!strings.Contains(fullPage, `modelSettingsDraft.thinking_mode`) ||
		!strings.Contains(fullPage, `modelSettingsDraft.temperature`) ||
		!strings.Contains(fullPage, `customizeModelSettings()`) ||
		!strings.Contains(fullPage, `Auto-detected models are read-only`) ||
		!strings.Contains(fullPage, `modelSettingsEditable()`) {
		t.Fatalf("expected model dialog to edit chat model settings")
	}
	if !strings.Contains(fullPage, `class="sidebar-info-row"`) || !strings.Contains(fullPage, `class="sidebar-label">Chat`) || !strings.Contains(fullPage, `activeChatRoleLabel()`) || !strings.Contains(fullPage, `class="sidebar-label">Model`) || !strings.Contains(fullPage, `class="sidebar-label">Access`) {
		t.Fatalf("expected sidebar facts to render as compact single-line label/value rows")
	}
	if !strings.Contains(fullPage, `mobile-sidebar-toggle`) ||
		!strings.Contains(fullPage, `mobileSidebarOpen`) ||
		!strings.Contains(fullPage, `mobile-sidebar-backdrop`) ||
		!strings.Contains(fullPage, `.sidebar.mobile-open`) {
		t.Fatalf("expected mobile sidebar to open as an overlay")
	}
	if !strings.Contains(fullPage, `topbar-workspace`) {
		t.Fatalf("expected workspace to render in the top status bar instead of the sidebar")
	}
	if !strings.Contains(fullPage, `sessionTitle(currentSession())`) ||
		!strings.Contains(fullPage, `workspaceTitleSuffix()`) ||
		!strings.Contains(fullPage, "return root ? `(${root})` : '';") {
		t.Fatalf("expected top status bar to render session title followed by parenthesized workspace")
	}
	if !strings.Contains(fullPage, `milestoneItems()`) {
		t.Fatalf("expected sidebar to render milestones")
	}
	if !strings.Contains(fullPage, `theme: 'base'`) ||
		!strings.Contains(fullPage, `themeVariables: koderMermaidThemeVariables(dark)`) ||
		!strings.Contains(fullPage, `fontSize: '16px'`) ||
		!strings.Contains(fullPage, `.mermaid-diagram svg text { font-size: 16px; }`) ||
		!strings.Contains(fullPage, `markMermaidThemeDirty`) {
		t.Fatalf("expected mermaid rendering to use owned readable theme variables and rerender on theme changes")
	}
	if !strings.Contains(fullPage, `media-expand-button`) ||
		!strings.Contains(fullPage, `openSVGLightbox`) ||
		!strings.Contains(fullPage, `onLightboxWheel($event)`) ||
		!strings.Contains(fullPage, `lightboxTransform()`) {
		t.Fatalf("expected images and diagrams to support expandable pan/zoom lightbox")
	}
	if !strings.Contains(fullPage, `visibleMilestones()`) ||
		!strings.Contains(fullPage, `flattenedMilestones()`) ||
		!strings.Contains(fullPage, `milestoneDependsOnRef`) ||
		!strings.Contains(fullPage, `milestoneStatusFilterOptions()`) ||
		!strings.Contains(fullPage, `milestoneFilterStatuses()`) ||
		!strings.Contains(fullPage, `milestoneStatusFilterClass(filter.status)`) ||
		!strings.Contains(fullPage, `toggleMilestoneStatusFilter(filter.status)`) ||
		!strings.Contains(fullPage, `hiddenMilestoneStatuses`) {
		t.Fatalf("expected sidebar to filter milestones by status")
	}
	if !strings.Contains(fullPage, `todoItemsForMilestone(node.milestone)`) {
		t.Fatalf("expected sidebar to render tasks as milestone children")
	}
	if !strings.Contains(fullPage, `milestoneTodoSummary(node.milestone)`) {
		t.Fatalf("expected collapsed milestones to show task counts")
	}
	if !strings.Contains(fullPage, `milestone-progress`) ||
		!strings.Contains(fullPage, `milestoneProgressStyle(node.milestone, 'failed')`) ||
		!strings.Contains(fullPage, `milestoneProgressStyle(node.milestone, 'cancelled')`) ||
		!strings.Contains(fullPage, `.milestone-progress-failed`) {
		t.Fatalf("expected milestone progress bars with failed/cancelled segments")
	}
	if !strings.Contains(fullPage, `const sameSession =`) || !strings.Contains(fullPage, `if (!sameSession)`) {
		t.Fatalf("expected pushed planning state to update dynamically for the current session")
	}
	if !strings.Contains(fullPage, `milestoneExpansionPreferenceName()`) ||
		!strings.Contains(fullPage, `readJSONPreference(this.milestoneExpansionPreferenceName(), {})`) ||
		!strings.Contains(fullPage, `writeJSONPreference(this.milestoneExpansionPreferenceName(), this.expandedMilestones || {})`) {
		t.Fatalf("expected milestone expansion state to persist in browser storage")
	}
	if !strings.Contains(fullPage, `.planning-tree { display: grid; gap: .05rem;`) || !strings.Contains(fullPage, `.planning-row { width: 100%; display: grid;`) || !strings.Contains(fullPage, `--milestone-depth`) || !strings.Contains(fullPage, `padding: .12rem 0`) {
		t.Fatalf("expected compact milestone spacing in sidebar")
	}
	if !strings.Contains(fullPage, `x-show="milestoneItems().length > 0"`) || strings.Contains(fullPage, `milestoneItems().length === 0`) {
		t.Fatalf("expected milestones sidebar section to hide when there are no milestones")
	}
	if !strings.Contains(fullPage, `planning-badge-executing`) || !strings.Contains(fullPage, `planning-badge-completed`) || !strings.Contains(fullPage, `planning-badge-blocked`) {
		t.Fatalf("expected colorful milestone status badge classes")
	}
	if !strings.Contains(fullPage, `todoBadge(todoStatus(todo))`) || !strings.Contains(fullPage, `todoBadge(status)`) {
		t.Fatalf("expected colorful task status badge classes")
	}
	if !strings.Contains(fullPage, `gitStatus()`) {
		t.Fatalf("expected sidebar to render git status")
	}
	if !strings.Contains(fullPage, `gitFiles()`) {
		t.Fatalf("expected sidebar to render git diff files")
	}
	if !strings.Contains(fullPage, `refresh_workspace`) {
		t.Fatalf("expected git sidebar refresh RPC")
	}
	if !strings.Contains(fullPage, `git-summary-row`) ||
		!strings.Contains(fullPage, `x-text="gitStatus().branch || '-'"`) ||
		!strings.Contains(fullPage, `x-text="'+' + (gitStatus().added || 0)"`) ||
		!strings.Contains(fullPage, `title="Refresh git status"`) {
		t.Fatalf("expected git branch, change summary, and refresh button on one compact row")
	}
	if !strings.Contains(fullPage, `@pointerdown="startSidebarResize($event)"`) {
		t.Fatalf("expected draggable sidebar divider")
	}
	if !strings.Contains(fullPage, `readPreference('theme'`) {
		t.Fatalf("expected theme to use shared browser preference storage")
	}
	if !strings.Contains(fullPage, `writePreference('sidebarRatio'`) {
		t.Fatalf("expected sidebar split ratio to use shared browser preference storage")
	}
	if !strings.Contains(fullPage, `selectedChatPreferenceName()`) ||
		!strings.Contains(fullPage, `writeTabPreference(this.selectedChatPreferenceName()`) ||
		!strings.Contains(fullPage, `readTabPreference(this.selectedChatPreferenceName()`) {
		t.Fatalf("expected selected chat to use tab-local browser preference storage")
	}
	if !strings.Contains(fullPage, `restoreSelectedChat()`) {
		t.Fatalf("expected selected chat to be restored after reload")
	}
	if !strings.Contains(fullPage, `delete_chat`) {
		t.Fatalf("expected chat deletion RPC")
	}
	if !strings.Contains(fullPage, `visibleChats()`) ||
		!strings.Contains(fullPage, `chatStatusFilterOptions()`) ||
		!strings.Contains(fullPage, `chatFilterStatuses()`) ||
		!strings.Contains(fullPage, `toggleChatStatusFilter(filter.status)`) ||
		!strings.Contains(fullPage, `hiddenChatStatuses`) ||
		!strings.Contains(fullPage, `Archive this chat?`) ||
		!strings.Contains(fullPage, `bi-archive`) {
		t.Fatalf("expected chat sidebar to archive chats and filter by status")
	}
	if !strings.Contains(fullPage, `draggable="true"`) ||
		!strings.Contains(fullPage, `@drop.stop.prevent="dropChat($event, chatID(chat))"`) ||
		!strings.Contains(fullPage, `reorder_chats`) ||
		!strings.Contains(fullPage, `chat_ids: orderedIDs`) {
		t.Fatalf("expected drag/drop chat reordering")
	}
	if !strings.Contains(fullPage, `deleteChat(chatID(chat))`) {
		t.Fatalf("expected chat list trash action")
	}
	if !strings.Contains(fullPage, `chatStatusLabel(chat)`) || !strings.Contains(fullPage, `chat-status-icon`) {
		t.Fatalf("expected chat sidebar to render per-chat animated status icons")
	}
	statusIdx := strings.Index(fullPage, `chat-status-icon bi`)
	chatIconIdx := strings.Index(fullPage, `bi-chat-left-text`)
	if statusIdx < 0 || chatIconIdx < 0 || statusIdx > chatIconIdx {
		t.Fatalf("expected chat busy indicator to render before the chat icon")
	}
	if !strings.Contains(fullPage, `chat-list-item`) || !strings.Contains(fullPage, `.sidebar-list .chat-list-item { padding: .16rem .25rem; min-height: 1.65rem; }`) {
		t.Fatalf("expected compact chat list row spacing")
	}
	if !strings.Contains(fullPage, `chat-list-main`) ||
		!strings.Contains(fullPage, `chat-list-content`) ||
		!strings.Contains(fullPage, `chat-title`) ||
		!strings.Contains(fullPage, `.sidebar-list .chat-list-main, .sidebar-list .chat-list-content, .sidebar-list .chat-title { min-width: 0; }`) ||
		!strings.Contains(fullPage, `.sidebar-list .chat-list-item > .btn:last-child { flex: 0 0 auto; }`) {
		t.Fatalf("expected chat rows to constrain title overflow inside sidebar")
	}
	if !strings.Contains(fullPage, `chatContextLabel(chat)`) ||
		!strings.Contains(fullPage, `:title="chatContextTooltip(chat)"`) ||
		!strings.Contains(fullPage, `chatContextWindow()`) ||
		!strings.Contains(fullPage, `formatContextTokens`) ||
		!strings.Contains(fullPage, `Remaining: `) ||
		!strings.Contains(fullPage, `'% ctx)'`) {
		t.Fatalf("expected chat sidebar to render context percentage with hover details")
	}
	if !strings.Contains(fullPage, `class="context-meter"`) ||
		!strings.Contains(fullPage, `activeContextStyle()`) ||
		!strings.Contains(fullPage, `activeContextClass()`) ||
		!strings.Contains(fullPage, `.context-meter-fill.context-danger`) {
		t.Fatalf("expected active chat context to render as a progress meter")
	}
	if !strings.Contains(fullPage, `thinkingLabel(item.content.reasoning)`) ||
		!strings.Contains(fullPage, `estimateTextTokens(text)`) ||
		!strings.Contains(fullPage, `'thinking (' + tokens + ' tokens)'`) ||
		!strings.Contains(fullPage, `cavemanThinkingSuffix(reasoning)`) ||
		!strings.Contains(fullPage, `caveman available (' + tokens + ' tokens)'`) ||
		!strings.Contains(fullPage, `hasCavemanReasoning(item.content.reasoning)`) ||
		!strings.Contains(fullPage, `reasoningDisplayText(item)`) {
		t.Fatalf("expected reasoning summary to render live token count and optional caveman view")
	}
	if !strings.Contains(fullPage, `toolApprovalPending(tool)`) || !strings.Contains(fullPage, `rpc('approve', {tool_call_id: toolCallID(tool)})`) || !strings.Contains(fullPage, `rpc('deny', {tool_call_id: toolCallID(tool)})`) {
		t.Fatalf("expected pending tool approval cards to expose approve and deny actions inline")
	}
	if !strings.Contains(fullPage, `x-show="approvals().length > 0"`) {
		t.Fatalf("expected approvals sidebar section to hide when there are no approvals")
	}
	if !strings.Contains(fullPage, `toolStatus(tool) === 'awaiting_approval'`) {
		t.Fatalf("expected approval actions to hide once the pushed tool turn is no longer pending")
	}
	if !strings.Contains(fullPage, `this.state.chat_statuses`) || !strings.Contains(fullPage, `waiting_llm: 'Waiting for LLM'`) {
		t.Fatalf("expected chat sidebar status helpers for all chats")
	}
	if !strings.Contains(fullPage, `:title="chatStatusLabel(chat)"`) {
		t.Fatalf("expected all chat status icons to render with hover tooltips")
	}
	if !strings.Contains(fullPage, `chatPendingApprovals(chat)`) || !strings.Contains(fullPage, `bi-exclamation-triangle-fill`) {
		t.Fatalf("expected chats with pending approvals to render a warning triangle status")
	}
	if !strings.Contains(fullPage, `.chat-status-icon.status-idle`) {
		t.Fatalf("expected idle chat status icon to be static")
	}
	if !strings.Contains(fullPage, `.chat-list-item.active .chat-status-icon.status-running`) || !strings.Contains(fullPage, `drop-shadow(0 0 2px rgba(0, 0, 0, .55))`) {
		t.Fatalf("expected selected busy chat status icons to stay visible on active row background")
	}
	if !strings.Contains(fullPage, `@keyframes chat-status-spin`) || !strings.Contains(fullPage, `chatStatusIcon(chat)`) {
		t.Fatalf("expected chat status icons to animate per state")
	}
	if !strings.Contains(fullPage, `openSessionDialog()`) {
		t.Fatalf("expected top status bar session dialog button")
	}
	if !strings.Contains(fullPage, `title="Edit session title"`) ||
		!strings.Contains(fullPage, `@click="beginEditSession(currentSession())"`) ||
		!strings.Contains(fullPage, `currentSession()`) {
		t.Fatalf("expected top status bar session title edit action")
	}
	if !strings.Contains(fullPage, `openSettingsDialog()`) {
		t.Fatalf("expected top status bar settings dialog button")
	}
	if !strings.Contains(fullPage, `list_sessions`) {
		t.Fatalf("expected session dialog to list sessions")
	}
	if !strings.Contains(fullPage, `switch_session`) {
		t.Fatalf("expected session dialog to switch sessions")
	}
	if !strings.Contains(fullPage, `new_session`) {
		t.Fatalf("expected session dialog to create sessions")
	}
	if !strings.Contains(fullPage, `showSessionEditor`) ||
		!strings.Contains(fullPage, `browse_project_folder`) ||
		!strings.Contains(fullPage, `Create folder and save`) ||
		!strings.Contains(fullPage, `create_project_root`) ||
		!strings.Contains(fullPage, `sessionProjectRoot(session)`) ||
		!strings.Contains(fullPage, `:readonly="sessionEditorMode === 'edit'"`) {
		t.Fatalf("expected session create/edit dialog with locked edit project folder, browse action, and create-folder offer")
	}
	if !strings.Contains(fullPage, `rename_session`) || !strings.Contains(fullPage, `delete_session`) {
		t.Fatalf("expected session dialog to rename and delete sessions")
	}
	if !strings.Contains(fullPage, `test_provider`) {
		t.Fatalf("expected provider dialog test action")
	}
	if !strings.Contains(fullPage, `save_provider`) {
		t.Fatalf("expected provider dialog save action")
	}
	if !strings.Contains(fullPage, `delete_provider`) {
		t.Fatalf("expected provider dialog delete action")
	}
	if !strings.Contains(fullPage, `preferences_state`) || !strings.Contains(fullPage, `save_preferences`) || !strings.Contains(fullPage, `reset_prompt`) {
		t.Fatalf("expected preferences dialog RPC actions")
	}
	if !strings.Contains(fullPage, `settingsListRows('providers')`) ||
		!strings.Contains(fullPage, `settingsListRows('mcp')`) ||
		!strings.Contains(fullPage, `showProviderEditor`) ||
		!strings.Contains(fullPage, `showMCPEditor`) {
		t.Fatalf("expected providers and MCP to use shared preferences list editors")
	}
	if !strings.Contains(fullPage, `settingsListRows('models')`) ||
		!strings.Contains(fullPage, `showModelConfigEditor`) ||
		!strings.Contains(fullPage, `list="model-config-options"`) ||
		!strings.Contains(fullPage, `providerModelOptions`) ||
		!strings.Contains(fullPage, `defaultModelValue()`) ||
		!strings.Contains(fullPage, `modelConfigDraft.thinking_mode`) ||
		!strings.Contains(fullPage, `source_model_id`) ||
		!strings.Contains(fullPage, `modelOptionLabel(model)`) {
		t.Fatalf("expected model settings editor to offer custom model aliases, detected model choices, defaults, and model request settings")
	}
	if !strings.Contains(fullPage, `Chat model`) ||
		!strings.Contains(fullPage, `settings-prompt`) ||
		!strings.Contains(fullPage, `resetPrompt(prompt.target)`) ||
		!strings.Contains(fullPage, `modelOptionValue(model)`) ||
		!strings.Contains(fullPage, `$el.value = compactionModelValue()`) {
		t.Fatalf("expected compaction model and prompt settings UI")
	}
	if !strings.Contains(fullPage, `settingsTab === 'thinking'`) ||
		!strings.Contains(fullPage, `settings.thinking.caveman_enabled`) ||
		!strings.Contains(fullPage, `thinkingModelValue()`) ||
		!strings.Contains(fullPage, `settings.thinking.caveman_prompt`) {
		t.Fatalf("expected thinking preferences tab with caveman controls")
	}
	if !strings.Contains(fullPage, `settingsTab === 'access'`) ||
		!strings.Contains(fullPage, `addAccessMount(settings.access.settings)`) ||
		!strings.Contains(fullPage, `cloneAccessSettings(preset.settings)`) {
		t.Fatalf("expected access settings to use structured controls")
	}
	if !strings.Contains(fullPage, `settingsTab === 'tools'`) ||
		!strings.Contains(fullPage, `toolDefaultGroups()`) ||
		!strings.Contains(fullPage, `setToolGroupEnabled(group, $event.target.checked)`) ||
		!strings.Contains(fullPage, `toolGroupPartial(group)`) ||
		!strings.Contains(fullPage, `setToolDefaultEnabled(item, $event.target.checked)`) ||
		!strings.Contains(document, `settings-tool-group`) ||
		!strings.Contains(document, `toggle-switch-input`) ||
		!strings.Contains(document, `toggle-switch-track`) {
		t.Fatalf("expected tools settings and boolean preferences to use shared toggle sliders")
	}
	if !strings.Contains(fullPage, `showToast`) {
		t.Fatalf("expected toast error path")
	}
	if !strings.Contains(document, `title="Save settings"`) ||
		!strings.Contains(document, `title="Save provider"`) ||
		!strings.Contains(document, `title="Save session"`) {
		t.Fatalf("expected modals to use header icon actions instead of footer Save/Cancel buttons")
	}
}

func TestFaviconDoesNot404(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	resp, err := http.Get(srv.URL() + "/favicon.ico")
	if err != nil {
		t.Fatalf("get favicon: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected favicon status 204, got %d", resp.StatusCode)
	}
}

func TestVendoredAssetsServe(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/assets/vendor/bootstrap/bootstrap.min.css", want: "Bootstrap"},
		{path: "/assets/vendor/bootstrap-icons/font/bootstrap-icons.css", want: "bootstrap-icons"},
		{path: "/assets/vendor/bootstrap-icons/font/fonts/bootstrap-icons.woff2", want: ""},
		{path: "/assets/vendor/alpine/cdn.min.js", want: "Alpine"},
		{path: "/assets/vendor/marked/marked.umd.js", want: "marked"},
		{path: "/assets/vendor/mermaid/mermaid.min.js", want: "mermaid"},
	} {
		resp, err := http.Get(srv.URL() + tc.path)
		if err != nil {
			t.Fatalf("get asset %s: %v", tc.path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected asset %s status 200, got %d", tc.path, resp.StatusCode)
		}
		if readErr != nil {
			t.Fatalf("read asset %s: %v", tc.path, readErr)
		}
		if tc.want != "" && !strings.Contains(string(body), tc.want) {
			t.Fatalf("expected asset %s body to contain %q", tc.path, tc.want)
		}
	}
}

func getAssetBody(t *testing.T, srv *Server, path string) string {
	t.Helper()
	resp, err := http.Get(srv.URL() + path)
	if err != nil {
		t.Fatalf("get asset %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected asset %s status 200, got %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read asset %s: %v", path, err)
	}
	return string(body)
}

func TestAssetHashIncludesVendoredAssets(t *testing.T) {
	first := computeAssetHash(fstest.MapFS{
		"assets/index.html":  {Data: []byte("hello " + assetHashPlaceholder)},
		"assets/vendor/a.js": {Data: []byte("one")},
	})
	second := computeAssetHash(fstest.MapFS{
		"assets/index.html":  {Data: []byte("hello " + assetHashPlaceholder)},
		"assets/vendor/a.js": {Data: []byte("two")},
	})
	if first == second {
		t.Fatalf("expected asset hash to change when vendored asset changes")
	}
}

func TestWebSocketSetModelAcknowledgesAndUpdatesChat(t *testing.T) {
	ctrl := newTestController(t)
	sessionID := ctrl.State().Session.ID
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(sessionID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"set_model","params":{"provider_id":"test","model_id":"next-model"}}`)); err != nil {
		t.Fatalf("write set_model: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Updated bool `json:"updated"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected set_model ok, got %s", resp.Error)
	}
	if !resp.Result.Updated {
		t.Fatal("expected set_model acknowledgement")
	}
	state, err := ctrl.StateForSelection(ctx, app.Selection{SessionID: sessionID})
	if err != nil {
		t.Fatalf("state for session: %v", err)
	}
	if state.Snapshot.Chat.ModelID != "next-model" {
		t.Fatalf("expected controller chat next-model, got %q", state.Snapshot.Chat.ModelID)
	}
}

func TestWebSocketSwitchChatReturnsUpdatedState(t *testing.T) {
	ctrl := newTestController(t)
	sessionID := ctrl.State().Session.ID
	firstID := ctrl.State().ActiveChatID
	second, err := ctrl.NewChatForSelection(context.Background(), app.Selection{SessionID: sessionID, ChatID: firstID}, "side chat")
	if err != nil {
		t.Fatalf("new chat: %v", err)
	}
	secondID := second.ID
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("expected two distinct chats, first=%s second=%s", firstID, secondID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(sessionID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":1,"method":"switch_chat","params":{"chat_id":"%s"}}`, firstID))); err != nil {
		t.Fatalf("write switch_chat: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			ActiveChatID id.ID `json:"active_chat_id"`
			Snapshot     struct {
				Chat struct {
					ID id.ID
				}
			}
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected switch_chat ok, got %s", resp.Error)
	}
	if resp.Result.ActiveChatID != firstID {
		t.Fatalf("expected response active chat %s, got %s", firstID, resp.Result.ActiveChatID)
	}
	if resp.Result.Snapshot.Chat.ID != firstID {
		t.Fatalf("expected response snapshot chat %s, got %s", firstID, resp.Result.Snapshot.Chat.ID)
	}
}

func TestWebSocketReceivesSelectedSessionUpdates(t *testing.T) {
	ctrl := newTestController(t)
	ctx := context.Background()
	second, err := ctrl.CreateSession(ctx, "Second", t.TempDir(), false)
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	secondState, err := ctrl.StateForSelection(ctx, app.Selection{SessionID: second.ID})
	if err != nil {
		t.Fatalf("state for second session: %v", err)
	}
	secondChatID := secondState.ActiveChatID
	if secondChatID == "" {
		t.Fatal("expected second session active chat")
	}

	wsCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(wsCtx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(wsCtx, "ws://"+srv.Addr()+"/ws?session="+string(second.ID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	writeRPC(t, wsCtx, conn, 1, "hello", `{}`)
	_ = readRPCResponse(t, wsCtx, conn, 1)

	if _, err := ctrl.UpdateChat(ctx, second.ID, secondChatID, secondChatID, tools.ChatUpdateRequest{Title: "Renamed second chat"}); err != nil {
		t.Fatalf("update second session chat: %v", err)
	}

	deadline, stop := context.WithTimeout(wsCtx, time.Second)
	defer stop()
	for {
		_, data, err := conn.Read(deadline)
		if err != nil {
			t.Fatalf("expected selected session chat_delta: %v", err)
		}
		var msg struct {
			Type    string `json:"type"`
			Payload struct {
				ChatID id.ID `json:"chat_id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("decode websocket push: %v", err)
		}
		if msg.Type == "chat_delta" && msg.Payload.ChatID == secondChatID {
			return
		}
	}
}

func TestWebSocketReorderChatsAcknowledgesAndUpdatesOrder(t *testing.T) {
	ctrl := newTestController(t)
	sessionID := ctrl.State().Session.ID
	firstID := ctrl.State().ActiveChatID
	second, err := ctrl.NewChatForSelection(context.Background(), app.Selection{SessionID: sessionID, ChatID: firstID}, "second")
	if err != nil {
		t.Fatalf("new second chat: %v", err)
	}
	secondID := second.ID
	third, err := ctrl.NewChatForSelection(context.Background(), app.Selection{SessionID: sessionID, ChatID: secondID}, "third")
	if err != nil {
		t.Fatalf("new third chat: %v", err)
	}
	thirdID := third.ID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(sessionID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	payload := fmt.Sprintf(`{"id":1,"method":"reorder_chats","params":{"chat_ids":["%s","%s","%s"]}}`, thirdID, firstID, secondID)
	if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
		t.Fatalf("write reorder_chats: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Reordered bool `json:"reordered"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected reorder_chats ok, got %s", resp.Error)
	}
	if !resp.Result.Reordered {
		t.Fatal("expected reorder_chats acknowledgement")
	}
	state, err := ctrl.StateForSelection(ctx, app.Selection{SessionID: sessionID})
	if err != nil {
		t.Fatalf("state for session: %v", err)
	}
	got := make([]id.ID, 0, len(state.Chats))
	for _, chat := range state.Chats {
		got = append(got, chat.ID)
	}
	if !slices.Equal(got, []id.ID{thirdID, firstID, secondID}) {
		t.Fatalf("unexpected chat order: %#v", got)
	}
	for idx, chat := range state.Chats {
		if chat.Position != idx {
			t.Fatalf("expected position %d for %s, got %d", idx, chat.ID, chat.Position)
		}
	}
}

func TestWebSocketDeleteChatAcknowledgesAndArchivesChat(t *testing.T) {
	ctrl := newTestController(t)
	sessionID := ctrl.State().Session.ID
	firstID := ctrl.State().ActiveChatID
	deleted, err := ctrl.NewChatForSelection(context.Background(), app.Selection{SessionID: sessionID, ChatID: firstID}, "side chat")
	if err != nil {
		t.Fatalf("new chat: %v", err)
	}
	deletedID := deleted.ID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(sessionID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":1,"method":"delete_chat","params":{"chat_id":"%s"}}`, deletedID))); err != nil {
		t.Fatalf("write delete_chat: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			ActiveChatID id.ID `json:"active_chat_id"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected delete_chat ok, got %s", resp.Error)
	}
	if resp.Result.ActiveChatID == "" || resp.Result.ActiveChatID == deletedID {
		t.Fatalf("expected delete_chat response to select a different chat, got %s", resp.Result.ActiveChatID)
	}
	state, err := ctrl.StateForSelection(ctx, app.Selection{SessionID: sessionID})
	if err != nil {
		t.Fatalf("state for session: %v", err)
	}
	if state.ActiveChatID == deletedID {
		t.Fatalf("expected active chat to switch away from %s", deletedID)
	}
	foundArchived := false
	for _, chat := range state.Chats {
		if chat.ID == deletedID {
			foundArchived = chat.Archived
		}
	}
	if !foundArchived {
		t.Fatalf("expected archived chat to remain listed as archived: %#v", state.Chats)
	}
}

func TestWebSocketSessionManagementCreatesAndSwitchesWorkspaceSessions(t *testing.T) {
	ctrl := newTestController(t)
	initialID := ctrl.State().Session.ID
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"list_sessions","params":{}}`)); err != nil {
		t.Fatalf("write list_sessions: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var listResp struct {
		OK     bool `json:"ok"`
		Result struct {
			ActiveID id.ID `json:"active_id"`
			Sessions []struct {
				ID id.ID
			} `json:"sessions"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if !listResp.OK {
		t.Fatalf("expected list_sessions ok, got %s", listResp.Error)
	}
	if listResp.Result.ActiveID != initialID || len(listResp.Result.Sessions) != 1 || listResp.Result.Sessions[0].ID != initialID {
		t.Fatalf("unexpected initial session list: %#v", listResp.Result)
	}

	projectRoot := t.TempDir()
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":2,"method":"new_session","params":{"title":"Side Session","project_root":"%s"}}`, projectRoot))); err != nil {
		t.Fatalf("write new_session: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 2)
	var newResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				ID          id.ID
				Title       string
				ProjectRoot string
			}
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &newResp); err != nil {
		t.Fatalf("decode new response: %v", err)
	}
	if !newResp.OK {
		t.Fatalf("expected new_session ok, got %s", newResp.Error)
	}
	newID := newResp.Result.Session.ID
	if newID == "" || newID == initialID || newResp.Result.Session.Title != "Side Session" || newResp.Result.Session.ProjectRoot != projectRoot {
		t.Fatalf("unexpected new session response: %#v", newResp.Result.Session)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":3,"method":"rename_session","params":{"session_id":"%s","title":"Renamed Session"}}`, newID))); err != nil {
		t.Fatalf("write rename_session: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 3)
	var renameResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				ID id.ID
			}
			Sessions []struct {
				ID    id.ID
				Title string
			}
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &renameResp); err != nil {
		t.Fatalf("decode rename response: %v", err)
	}
	if !renameResp.OK {
		t.Fatalf("expected rename_session ok, got %s", renameResp.Error)
	}
	foundRenamed := false
	for _, session := range renameResp.Result.Sessions {
		if session.ID == newID && session.Title == "Renamed Session" {
			foundRenamed = true
		}
	}
	if !foundRenamed {
		t.Fatalf("expected renamed session in response: %#v", renameResp.Result.Sessions)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":4,"method":"switch_session","params":{"session_id":"%s"}}`, initialID))); err != nil {
		t.Fatalf("write switch_session: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 4)
	var switchResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				ID id.ID
			}
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &switchResp); err != nil {
		t.Fatalf("decode switch response: %v", err)
	}
	if !switchResp.OK {
		t.Fatalf("expected switch_session ok, got %s", switchResp.Error)
	}
	if switchResp.Result.Session.ID != initialID {
		t.Fatalf("expected switched back to %s, got %#v", initialID, switchResp.Result.Session)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":5,"method":"delete_session","params":{"session_id":"%s"}}`, newID))); err != nil {
		t.Fatalf("write delete_session: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 5)
	var deleteResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				ID id.ID
			}
			Sessions []struct {
				ID id.ID
			}
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &deleteResp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if !deleteResp.OK {
		t.Fatalf("expected delete_session ok, got %s", deleteResp.Error)
	}
	if deleteResp.Result.Session.ID != initialID {
		t.Fatalf("expected active session to remain %s, got %s", initialID, deleteResp.Result.Session.ID)
	}
	for _, session := range deleteResp.Result.Sessions {
		if session.ID == newID {
			t.Fatalf("deleted session still listed: %#v", deleteResp.Result.Sessions)
		}
	}
}

func TestWebSocketNewSessionCreatesMissingProjectRootOnlyWhenRequested(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	missingRoot := filepath.Join(t.TempDir(), "missing", "project")
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":1,"method":"new_session","params":{"title":"Missing","project_root":%q}}`, missingRoot))); err != nil {
		t.Fatalf("write missing new_session: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var missingResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &missingResp); err != nil {
		t.Fatalf("decode missing response: %v", err)
	}
	if missingResp.OK || !strings.Contains(missingResp.Error, "project root does not exist") {
		t.Fatalf("expected missing project root error, got %#v", missingResp)
	}
	if _, err := os.Stat(missingRoot); !os.IsNotExist(err) {
		t.Fatalf("expected missing project root to remain absent, got %v", err)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":2,"method":"new_session","params":{"title":"Created","project_root":%q,"create_project_root":true}}`, missingRoot))); err != nil {
		t.Fatalf("write create new_session: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 2)
	var createdResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				Title       string
				ProjectRoot string
			}
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &createdResp); err != nil {
		t.Fatalf("decode created response: %v", err)
	}
	if !createdResp.OK {
		t.Fatalf("expected create new_session ok, got %s", createdResp.Error)
	}
	if createdResp.Result.Session.Title != "Created" || createdResp.Result.Session.ProjectRoot != missingRoot {
		t.Fatalf("unexpected created session: %#v", createdResp.Result.Session)
	}
	if info, err := os.Stat(missingRoot); err != nil || !info.IsDir() {
		t.Fatalf("expected project root directory to be created, info=%#v err=%v", info, err)
	}
}

func TestWebSocketProviderCRUDReturnsProviderState(t *testing.T) {
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"detected-model"}]}`))
	}))
	defer providerServer.Close()

	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws?session="+string(ctrl.State().Session.ID), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	save := fmt.Sprintf(`{"id":1,"method":"save_provider","params":{"original_provider_id":"","provider_id":"local","template_id":"openai-compatible","kind":"openai-compatible","name":"Local","base_url":%q,"api_key":"secret","model":"stale-model","headers":{"X-Test":"yes"}}}`, providerServer.URL+"/v1")
	if err := conn.Write(ctx, websocket.MessageText, []byte(save)); err != nil {
		t.Fatalf("write save_provider: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var saveResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Providers struct {
				DefaultProvider string `json:"default_provider"`
				DefaultModel    string `json:"default_model"`
				Providers       []struct {
					ID                      string `json:"id"`
					PromptProgressProbed    bool   `json:"prompt_progress_probed"`
					PromptProgressSupported bool   `json:"prompt_progress_supported"`
				} `json:"providers"`
				Drafts map[string]struct {
					Headers map[string]string `json:"headers"`
				} `json:"drafts"`
			} `json:"providers"`
			State struct {
				Snapshot struct {
					Chat struct {
						ProviderID string `json:"ProviderID"`
						ModelID    string `json:"ModelID"`
					} `json:"Chat"`
				} `json:"Snapshot"`
			} `json:"state"`
			Preferences struct {
				Models []struct {
					ProviderID string `json:"provider_id"`
					ModelID    string `json:"model_id"`
				} `json:"models"`
			} `json:"preferences"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &saveResp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if !saveResp.OK {
		t.Fatalf("expected save_provider ok, got %s", saveResp.Error)
	}
	var foundLocal bool
	for _, item := range saveResp.Result.Providers.Providers {
		if item.ID == "local" {
			foundLocal = true
			if !item.PromptProgressProbed || item.PromptProgressSupported {
				t.Fatalf("expected rejected prompt progress status on local provider, got %#v", item)
			}
		}
	}
	if !foundLocal {
		t.Fatalf("expected saved local provider, got %#v", saveResp.Result.Providers.Providers)
	}
	if saveResp.Result.Providers.DefaultProvider != "test" || saveResp.Result.Providers.DefaultModel != "model" {
		t.Fatalf("expected existing default provider/model to remain, got %#v", saveResp.Result.Providers)
	}
	if saveResp.Result.State.Snapshot.Chat.ProviderID != "test" || saveResp.Result.State.Snapshot.Chat.ModelID != "model" {
		t.Fatalf("expected active chat to remain on current usable provider/model, got %#v", saveResp.Result.State.Snapshot.Chat)
	}
	if got := saveResp.Result.Providers.Drafts["local"].Headers["X-Test"]; got != "yes" {
		t.Fatalf("expected saved header in draft, got %q", got)
	}
	foundDetectedModel := false
	for _, model := range saveResp.Result.Preferences.Models {
		if model.ProviderID == "local" && model.ModelID == "detected-model" {
			foundDetectedModel = true
			break
		}
	}
	if !foundDetectedModel {
		t.Fatalf("expected saved provider model in returned preferences, got %#v", saveResp.Result.Preferences.Models)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":2,"method":"delete_provider","params":{"provider_id":"local"}}`)); err != nil {
		t.Fatalf("write delete_provider: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 2)
	var deleteResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Providers struct {
				DefaultProvider string `json:"default_provider"`
				Providers       []struct {
					ID string `json:"id"`
				} `json:"providers"`
			} `json:"providers"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &deleteResp); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if !deleteResp.OK {
		t.Fatalf("expected delete_provider ok, got %s", deleteResp.Error)
	}
	for _, item := range deleteResp.Result.Providers.Providers {
		if item.ID == "local" {
			t.Fatalf("deleted provider still listed: %#v", deleteResp.Result.Providers.Providers)
		}
	}
}

func TestWebSocketTestProviderReturnsProbeResult(t *testing.T) {
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"alpha"},{"id":"beta"}]}`))
	}))
	defer providerServer.Close()

	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	request := fmt.Sprintf(`{"id":1,"method":"test_provider","params":{"provider_id":"probe","template_id":"openai-compatible","kind":"openai-compatible","name":"Probe","base_url":%q,"api_key":"secret","model":"alpha","headers":{}}}`, providerServer.URL+"/v1")
	if err := conn.Write(ctx, websocket.MessageText, []byte(request)); err != nil {
		t.Fatalf("write test_provider: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			ModelCount int      `json:"model_count"`
			Models     []string `json:"models"`
			Selected   string   `json:"selected_model"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode test provider response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected test_provider ok, got %s", resp.Error)
	}
	if resp.Result.ModelCount != 2 || strings.Join(resp.Result.Models, ",") != "alpha,beta" {
		t.Fatalf("unexpected probe result: %#v", resp.Result)
	}
	if resp.Result.Selected != "alpha" {
		t.Fatalf("expected selected model alpha, got %q", resp.Result.Selected)
	}
}

func TestWebSocketComposerCompletionsReturnsCommandsSkillsAndReferences(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".agents", "skills", "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, ".agents", "skills", "review", "SKILL.md"), []byte("---\nname: review\ndescription: Review code carefully\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctrl := newTestControllerWithWorkdir(t, workdir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"composer_completions","params":{"text":"/pro","cursor":4}}`)); err != nil {
		t.Fatalf("write command completion request: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var commandResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Kind  string `json:"kind"`
			Start int    `json:"start"`
			End   int    `json:"end"`
			Items []struct {
				Label      string `json:"label"`
				InsertText string `json:"insert_text"`
			} `json:"items"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &commandResp); err != nil {
		t.Fatalf("decode command completions: %v", err)
	}
	if !commandResp.OK {
		t.Fatalf("expected command completions ok, got %s", commandResp.Error)
	}
	if commandResp.Result.Kind != "command" || commandResp.Result.Start != 0 || commandResp.Result.End != 4 || len(commandResp.Result.Items) != 1 || commandResp.Result.Items[0].InsertText != "/providers" {
		t.Fatalf("unexpected command completions: %#v", commandResp.Result)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":2,"method":"composer_completions","params":{"text":"Use $rev","cursor":8}}`)); err != nil {
		t.Fatalf("write skill completion request: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 2)
	var skillResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Kind  string `json:"kind"`
			Start int    `json:"start"`
			End   int    `json:"end"`
			Items []struct {
				Label      string `json:"label"`
				InsertText string `json:"insert_text"`
			} `json:"items"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &skillResp); err != nil {
		t.Fatalf("decode skill completions: %v", err)
	}
	if !skillResp.OK {
		t.Fatalf("expected skill completions ok, got %s", skillResp.Error)
	}
	if skillResp.Result.Kind != "skill" || skillResp.Result.Start != 4 || skillResp.Result.End != 8 || len(skillResp.Result.Items) != 1 || skillResp.Result.Items[0].InsertText != "$review" {
		t.Fatalf("unexpected skill completions: %#v", skillResp.Result)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":3,"method":"composer_completions","params":{"text":"Read @REA","cursor":9}}`)); err != nil {
		t.Fatalf("write reference completion request: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 3)
	var refResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Kind  string `json:"kind"`
			Start int    `json:"start"`
			End   int    `json:"end"`
			Items []struct {
				Label      string `json:"label"`
				InsertText string `json:"insert_text"`
				Kind       string `json:"kind"`
				Path       string `json:"path"`
			} `json:"items"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &refResp); err != nil {
		t.Fatalf("decode reference completions: %v", err)
	}
	if !refResp.OK {
		t.Fatalf("expected reference completions ok, got %s", refResp.Error)
	}
	if refResp.Result.Kind != "reference" || refResp.Result.Start != 5 || refResp.Result.End != 9 || len(refResp.Result.Items) == 0 || refResp.Result.Items[0].InsertText != "@README.md" || refResp.Result.Items[0].Path != "README.md" {
		t.Fatalf("unexpected reference completions: %#v", refResp.Result)
	}
}

func TestClipboardImageUploadEndpointReturnsDraftAttachment(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("image", "paste.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("\x89PNG\r\n\x1a\nfake")); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL()+"/api/attachments/clipboard-image", &body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %s: %s", resp.Status, body)
	}
	var draft struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		MIME   string `json:"mime"`
		Path   string `json:"path"`
		Source string `json:"source"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&draft); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if draft.ID == "" || draft.Name != "paste.png" || draft.MIME != "image/png" || draft.Source != "clipboard_image" {
		t.Fatalf("unexpected draft: %#v", draft)
	}
	if _, err := os.Stat(draft.Path); err != nil {
		t.Fatalf("expected uploaded draft file: %v", err)
	}
}

func newTestController(t *testing.T) *app.Controller {
	t.Helper()
	return newTestControllerWithWorkdir(t, t.TempDir())
}

func newTestControllerWithWorkdir(t *testing.T, workdir string) *app.Controller {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	cfg.Providers = map[string]config.Provider{
		"test": {BaseURL: "https://example.invalid/v1"},
	}
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "test", ModelID: "model", ContextWindow: 32768})
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := agent.New(cfg, st, nil, nil)
	ctrl := app.New(cfg, st, engine)
	if err := ctrl.Start(context.Background(), app.StartupModeNew, workdir); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	return ctrl
}

func readMessage(t *testing.T, ctx context.Context, conn *websocket.Conn) []byte {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("read websocket: %v", err)
	}
	return data
}

func readRPCResponse(t *testing.T, ctx context.Context, conn *websocket.Conn, id float64) []byte {
	t.Helper()
	for {
		msg := readMessage(t, ctx, conn)
		var header struct {
			ID any `json:"id"`
		}
		if err := json.Unmarshal(msg, &header); err != nil {
			t.Fatalf("decode response header: %v", err)
		}
		if got, ok := header.ID.(float64); ok && got == id {
			return msg
		}
	}
}

func writeRPC(t *testing.T, ctx context.Context, conn *websocket.Conn, rpcID int, method, params string) {
	t.Helper()
	payload := fmt.Sprintf(`{"id":%d,"method":"%s","params":%s}`, rpcID, method, params)
	if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
}

func readRPCStateSession(t *testing.T, ctx context.Context, conn *websocket.Conn, rpcID float64) id.ID {
	t.Helper()
	msg := readRPCResponse(t, ctx, conn, rpcID)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				ID id.ID `json:"id"`
			} `json:"session"`
			State struct {
				Session struct {
					ID id.ID `json:"id"`
				} `json:"session"`
			} `json:"state"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected rpc ok, got %s", resp.Error)
	}
	if resp.Result.State.Session.ID != "" {
		return resp.Result.State.Session.ID
	}
	return resp.Result.Session.ID
}
