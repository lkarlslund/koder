package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/uicore"
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
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
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
		if url != srv.URL() {
			t.Fatalf("expected opened URL %q, got %q", srv.URL(), url)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected browser open after timeout")
	}
}

func TestServerNoBrowserSuppressesBrowserOpen(t *testing.T) {
	ctrl := newTestController(t)
	opened := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := Start(ctx, ctrl, Options{
		Bind:      "127.0.0.1:0",
		NoBrowser: true,
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
		t.Fatalf("expected no browser open with --nobrowser, got %s", url)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWebSocketHelloReturnsState(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	_ = readMessage(t, ctx, conn)
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
}

func TestIndexServesHTML(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(body), `@keydown.enter.exact.prevent="send()"`) {
		t.Fatalf("expected plain enter to submit composer")
	}
	if !strings.Contains(string(body), `text === '/permissions'`) {
		t.Fatalf("expected /permissions to be handled locally")
	}
	if !strings.Contains(string(body), `set_permission_profile`) {
		t.Fatalf("expected permissions UI to call set_permission_profile")
	}
	if !strings.Contains(string(body), `openModelDialog()`) {
		t.Fatalf("expected model text to open model dialog")
	}
	if !strings.Contains(string(body), `list_models`) {
		t.Fatalf("expected model dialog to list models")
	}
	if !strings.Contains(string(body), `set_model`) {
		t.Fatalf("expected model dialog to set model")
	}
	if !strings.Contains(string(body), `milestoneItems()`) {
		t.Fatalf("expected sidebar to render milestones")
	}
	if !strings.Contains(string(body), `todoItems()`) {
		t.Fatalf("expected sidebar to render todos")
	}
	if !strings.Contains(string(body), `@pointerdown="startSidebarResize($event)"`) {
		t.Fatalf("expected draggable sidebar divider")
	}
	if !strings.Contains(string(body), `readPreference('theme'`) {
		t.Fatalf("expected theme to use shared browser preference storage")
	}
	if !strings.Contains(string(body), `writePreference('sidebarRatio'`) {
		t.Fatalf("expected sidebar split ratio to use shared browser preference storage")
	}
	if !strings.Contains(string(body), `delete_chat`) {
		t.Fatalf("expected chat deletion RPC")
	}
	if !strings.Contains(string(body), `deleteChat(chatID(chat))`) {
		t.Fatalf("expected chat list trash action")
	}
	if !strings.Contains(string(body), `openProviderDialog()`) {
		t.Fatalf("expected top status bar provider dialog button")
	}
	if !strings.Contains(string(body), `provider_state`) {
		t.Fatalf("expected provider dialog to load provider state")
	}
	if !strings.Contains(string(body), `test_provider`) {
		t.Fatalf("expected provider dialog test action")
	}
	if !strings.Contains(string(body), `save_provider`) {
		t.Fatalf("expected provider dialog save action")
	}
	if !strings.Contains(string(body), `delete_provider`) {
		t.Fatalf("expected provider dialog delete action")
	}
	if !strings.Contains(string(body), `showToast`) {
		t.Fatalf("expected toast error path")
	}
}

func TestWebSocketSetModelReturnsUpdatedState(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	_ = readMessage(t, ctx, conn)
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"set_model","params":{"provider_id":"test","model_id":"next-model"}}`)); err != nil {
		t.Fatalf("write set_model: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				ModelID string
			}
			Snapshot struct {
				Session struct {
					ModelID string
				}
			}
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected set_model ok, got %s", resp.Error)
	}
	if resp.Result.Session.ModelID != "next-model" {
		t.Fatalf("expected response session next-model, got %q", resp.Result.Session.ModelID)
	}
	if resp.Result.Snapshot.Session.ModelID != "next-model" {
		t.Fatalf("expected runtime snapshot next-model, got %q", resp.Result.Snapshot.Session.ModelID)
	}
}

func TestWebSocketDeleteChatReturnsUpdatedState(t *testing.T) {
	ctrl := newTestController(t)
	if err := ctrl.NewChat(context.Background(), "side chat"); err != nil {
		t.Fatalf("new chat: %v", err)
	}
	deletedID := ctrl.State().ActiveChatID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	_ = readMessage(t, ctx, conn)
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":1,"method":"delete_chat","params":{"chat_id":%d}}`, deletedID))); err != nil {
		t.Fatalf("write delete_chat: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			ActiveChatID int64 `json:"active_chat_id"`
			Chats        []struct {
				ID int64
			}
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(msg, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected delete_chat ok, got %s", resp.Error)
	}
	if resp.Result.ActiveChatID == deletedID {
		t.Fatalf("expected active chat to switch away from %d", deletedID)
	}
	for _, chat := range resp.Result.Chats {
		if chat.ID == deletedID {
			t.Fatalf("deleted chat still listed: %#v", resp.Result.Chats)
		}
	}
}

func TestWebSocketProviderCRUDReturnsProviderState(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	_ = readMessage(t, ctx, conn)
	save := `{"id":1,"method":"save_provider","params":{"original_provider_id":"","provider_id":"local","template_id":"openai-compatible","kind":"openai-compatible","name":"Local","base_url":"https://example.invalid/v1","api_key":"secret","model":"local-model","headers":{"X-Test":"yes"}}}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(save)); err != nil {
		t.Fatalf("write save_provider: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var saveResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Providers struct {
				DefaultProvider string `json:"default_provider"`
				Providers       []struct {
					ID           string `json:"id"`
					DefaultModel string `json:"default_model"`
				} `json:"providers"`
				Drafts map[string]struct {
					Headers map[string]string `json:"headers"`
				} `json:"drafts"`
			} `json:"providers"`
			State struct {
				Session struct {
					ProviderID string
					ModelID    string
				}
			} `json:"state"`
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
		if item.ID == "local" && item.DefaultModel == "local-model" {
			foundLocal = true
		}
	}
	if !foundLocal {
		t.Fatalf("expected saved local provider, got %#v", saveResp.Result.Providers.Providers)
	}
	if saveResp.Result.State.Session.ProviderID != "test" || saveResp.Result.State.Session.ModelID != "model" {
		t.Fatalf("expected active session to remain on current usable provider/model, got %#v", saveResp.Result.State.Session)
	}
	if got := saveResp.Result.Providers.Drafts["local"].Headers["X-Test"]; got != "yes" {
		t.Fatalf("expected saved header in draft, got %q", got)
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
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"alpha"},{"id":"beta"}]}`))
	}))
	defer providerServer.Close()

	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	_ = readMessage(t, ctx, conn)
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
}

func newTestController(t *testing.T) *uicore.Controller {
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
		"test": {BaseURL: "https://example.invalid/v1", DefaultModel: "model"},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := agent.New(cfg, st, nil, nil, t.TempDir())
	ctrl := uicore.New(cfg, st, engine, t.TempDir())
	if err := ctrl.Start(context.Background(), uicore.StartupModeNew); err != nil {
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
