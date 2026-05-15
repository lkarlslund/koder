package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
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

func TestServerHealthEndpoint(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
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

func TestWebSocketHelloReturnsState(t *testing.T) {
	ctrl := newTestController(t)
	recorder := debugsrv.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true, Debug: recorder})
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
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true, Debug: recorder})
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
	event, ok := webEventFromControllerEvent(uicore.Event{Seq: 9, Type: "chat_update", Payload: update})
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

func TestWebSocketStreamingDeltaUsesMutatedSnapshotItem(t *testing.T) {
	itemID := domain.ID("019aa000-0000-7000-8000-000000000043")
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

func TestWebSocketSnapshotEventIsCompactedToStateDelta(t *testing.T) {
	state := uicore.State{
		Session:      domain.Session{ID: "session-1", Title: "Session"},
		Chats:        []domain.Chat{{ID: "chat-7", SessionID: "session-1", Title: "Chat"}},
		ActiveChatID: "chat-7",
		Snapshots: map[domain.ID]chat.Snapshot{
			"chat-7": {
				Chat:     domain.Chat{ID: "chat-7", SessionID: "session-1", Title: "Chat"},
				Timeline: []domain.TimelineItem{{ID: "019aa000-0000-7000-8000-000000000001", ChatID: "chat-7", Seq: 1, Content: domain.UserMessage{Text: "old transcript"}}},
			},
		},
	}
	event, ok := webEventFromControllerEvent(uicore.Event{Seq: 2, Type: "snapshot", Payload: state})
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
	if !strings.Contains(fullPage, `class="btn btn-danger interrupt-button"`) || !strings.Contains(fullPage, `x-show="chatInterruptible()"`) || !strings.Contains(fullPage, `rpc('stop_after_turn', {})`) || !strings.Contains(fullPage, `rpc('stop', {})`) {
		t.Fatalf("expected composer interrupt button with staged then immediate stop behavior")
	}
	if !strings.Contains(fullPage, `hello.client_id`) || !strings.Contains(fullPage, `rpcOn(this.ws, 'client_state'`) || !strings.Contains(fullPage, `selected_chat: String(this.activeChatID() || '')`) {
		t.Fatalf("expected browser to report per-client debug state")
	}
	if strings.Contains(fullPage, `btn btn-sm btn-outline-danger" @click="rpc('stop', {})"`) {
		t.Fatalf("expected interrupt control to be removed from the topbar")
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
	if !strings.Contains(fullPage, `/assets/vendor/alpine/cdn.min.js`) {
		t.Fatalf("expected Alpine to be loaded from vendored assets")
	}
	if strings.Contains(fullPage, `cdn.jsdelivr.net`) {
		t.Fatalf("expected index to avoid CDN dependencies")
	}
	if !strings.Contains(fullPage, `x-html="markdownHTML(item.content?.text || '')"`) {
		t.Fatalf("expected assistant text to render as markdown HTML")
	}
	if !strings.Contains(fullPage, `x-html="markdownHTML(pendingText())"`) {
		t.Fatalf("expected streaming assistant text to render as markdown HTML")
	}
	if !strings.Contains(fullPage, `class="turn user-turn"`) || !strings.Contains(fullPage, `.transcript-turn { width: 100%; max-width: none; }`) {
		t.Fatalf("expected user turns to use the full transcript width")
	}
	if !strings.Contains(fullPage, `class="turn assistant-turn"`) || strings.Contains(fullPage, `bi bi-stars`) || strings.Contains(fullPage, `> Assistant</div>`) || strings.Contains(fullPage, `message assistant p-3`) || strings.Contains(fullPage, `message user p-3 ms-auto`) {
		t.Fatalf("expected assistant turns without repeated assistant title or chat-bubble boxes")
	}
	if !strings.Contains(fullPage, `.user-turn, .assistant-turn { border-left: 0; padding-left: 0; }`) || strings.Contains(fullPage, `.tool { border-left`) || strings.Contains(fullPage, `class="tool mt-3 ps-3"`) {
		t.Fatalf("expected transcript turns and tools to avoid redundant border rails")
	}
	if !strings.Contains(fullPage, `marked.parse(source)`) || !strings.Contains(fullPage, `DOMPurify.sanitize`) || !strings.Contains(fullPage, `hljs.highlight`) {
		t.Fatalf("expected browser markdown renderer to parse, sanitize, and syntax-highlight")
	}
	if strings.Contains(fullPage, `formatArgs(tool.args)`) || strings.Contains(fullPage, `JSON.stringify(tool.args`) {
		t.Fatalf("expected tool calls to avoid raw JSON argument rendering")
	}
	if !strings.Contains(fullPage, `toolResultHTML(tool)`) || !strings.Contains(fullPage, `function renderToolResult(tool)`) {
		t.Fatalf("expected tool results to render through the per-tool formatter")
	}
	if !strings.Contains(fullPage, `toolErrorHTML(tool)`) || !strings.Contains(fullPage, `function renderToolError(tool)`) || !strings.Contains(fullPage, `toolStatusBadge(tool)`) {
		t.Fatalf("expected tool errors to render through the compact per-tool formatter")
	}
	if !strings.Contains(fullPage, `item.kind === 'notice'`) || !strings.Contains(fullPage, `noticeText(item.content || {})`) || !strings.Contains(fullPage, `.notice-warning`) || strings.Contains(fullPage, `JSON.stringify(item.content`) {
		t.Fatalf("expected notices to render as compact UI instead of raw JSON")
	}
	if !strings.Contains(fullPage, `function renderDiffBlock(title, diff)`) || !strings.Contains(fullPage, `tool-diff-add`) || !strings.Contains(fullPage, `tool-diff-del`) {
		t.Fatalf("expected edit and patch results to use colored diff rendering")
	}
	if !strings.Contains(fullPage, `compactLines(lines, head = 2, tail = 2)`) {
		t.Fatalf("expected file write results to use compact head/tail rendering")
	}
	if strings.Contains(fullPage, `case 'bash': return command ? 'Run ' + command`) || strings.Contains(fullPage, `const title = firstValue(data, ['command', 'Command'`) {
		t.Fatalf("expected command tools to render the command once, not in title and result header")
	}
	if !strings.Contains(fullPage, `toolStatus(tool) === 'done' || toolStatus(tool) === 'errored'`) || !strings.Contains(fullPage, `return 'exit ' + exitCode`) || !strings.Contains(fullPage, `isBareExitStatus(error)`) || !strings.Contains(fullPage, `return renderCompactBlock('Output'`) {
		t.Fatalf("expected bash tool rendering to show Ran command, exit code, and compact output")
	}
	if !strings.Contains(fullPage, `String((tool && tool.tool) || '') === 'bash' && (toolStatus(tool) === 'done' || toolStatus(tool) === 'errored')`) {
		t.Fatalf("expected completed and errored bash tool rendering to avoid repeating the command preview")
	}
	if !strings.Contains(fullPage, `if (args.command) values.push(args.command)`) || !strings.Contains(fullPage, `function execResultLines(data, fallback)`) {
		t.Fatalf("expected command preview and exec result helpers")
	}
	if !strings.Contains(fullPage, `tool-result-body-mono`) || !strings.Contains(fullPage, `renderCompactBlock('Result', execResultLines(data, toolResultText(tool)), 'tool-result-body-mono')`) {
		t.Fatalf("expected exec output to render with monospace result styling")
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
	if !strings.Contains(fullPage, `const id = String(delta.chat_id || delta.ChatID || delta.chat?.id || delta.chat?.ID || '').trim()`) || strings.Contains(fullPage, `const id = Number(delta.chat_id`) {
		t.Fatalf("expected browser chat deltas to keep UUID chat ids as strings")
	}
	if !strings.Contains(fullPage, `if (!id) throw new Error('timeline delta missing item id')`) || !strings.Contains(fullPage, `return existingID === id`) {
		t.Fatalf("expected browser timeline patching to require stable item ids")
	}
	if !strings.Contains(fullPage, `:key="item.id || item.ID"`) || strings.Contains(fullPage, `timelineKey(item)`) {
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
	if !strings.Contains(fullPage, `const id = String(raw || '').trim()`) || strings.Contains(fullPage, `const id = Number(raw)`) {
		t.Fatalf("expected selected chat restore to keep UUID chat ids as strings")
	}
	if !strings.Contains(fullPage, `connectWatchdog`) || !strings.Contains(fullPage, `WebSocket.CONNECTING`) || !strings.Contains(fullPage, `ws.close()`) {
		t.Fatalf("expected stuck websocket handshakes to be closed and retried")
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
	if !strings.Contains(fullPage, `scroll.stickToBottom`) || !strings.Contains(fullPage, `el.scrollTop = scroll.top`) {
		t.Fatalf("expected transcript to follow only when near bottom and preserve scroll otherwise")
	}
	if !strings.Contains(fullPage, `scrollRestoreSeq`) || !strings.Contains(fullPage, `seq === this.scrollRestoreSeq`) {
		t.Fatalf("expected stale deferred transcript scroll restorations to be ignored")
	}
	if !strings.Contains(fullPage, `afterTranscriptDOMUpdate`) || !strings.Contains(fullPage, `requestAnimationFrame`) || !strings.Contains(fullPage, `setTimeout(fn, 0)`) {
		t.Fatalf("expected transcript scroll restoration to run after deferred DOM height updates")
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
	if !strings.Contains(fullPage, `text === '/permissions'`) {
		t.Fatalf("expected /permissions to be handled locally")
	}
	if !strings.Contains(fullPage, `set_permission_profile`) {
		t.Fatalf("expected permissions UI to call set_permission_profile")
	}
	if !strings.Contains(fullPage, `openModelDialog()`) {
		t.Fatalf("expected model text to open model dialog")
	}
	if !strings.Contains(fullPage, `list_models`) {
		t.Fatalf("expected model dialog to list models")
	}
	if !strings.Contains(fullPage, `set_model`) {
		t.Fatalf("expected model dialog to set model")
	}
	if !strings.Contains(fullPage, `class="sidebar-info-row"`) || !strings.Contains(fullPage, `class="sidebar-label">Model`) || !strings.Contains(fullPage, `class="sidebar-label">Permissions`) {
		t.Fatalf("expected sidebar facts to render as compact single-line label/value rows")
	}
	if !strings.Contains(fullPage, `topbar-workspace`) || strings.Contains(fullPage, `class="sidebar-label">Workspace`) {
		t.Fatalf("expected workspace to render in the top status bar instead of the sidebar")
	}
	if !strings.Contains(fullPage, `milestoneItems()`) {
		t.Fatalf("expected sidebar to render milestones")
	}
	if !strings.Contains(fullPage, `todoItemsForMilestone(milestone)`) {
		t.Fatalf("expected sidebar to render todos as milestone children")
	}
	if !strings.Contains(fullPage, `milestoneTodoSummary(milestone)`) {
		t.Fatalf("expected collapsed milestones to show todo counts")
	}
	if !strings.Contains(fullPage, `.planning-tree { display: grid; gap: .05rem;`) || !strings.Contains(fullPage, `.planning-row { width: 100%; display: grid;`) || !strings.Contains(fullPage, `padding: .12rem 0`) {
		t.Fatalf("expected compact milestone spacing in sidebar")
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
	if !strings.Contains(fullPage, `@pointerdown="startSidebarResize($event)"`) {
		t.Fatalf("expected draggable sidebar divider")
	}
	if !strings.Contains(fullPage, `readPreference('theme'`) {
		t.Fatalf("expected theme to use shared browser preference storage")
	}
	if !strings.Contains(fullPage, `writePreference('sidebarRatio'`) {
		t.Fatalf("expected sidebar split ratio to use shared browser preference storage")
	}
	if !strings.Contains(fullPage, `selectedChatPreferenceName()`) {
		t.Fatalf("expected selected chat to use browser preference storage")
	}
	if !strings.Contains(fullPage, `restoreSelectedChat()`) {
		t.Fatalf("expected selected chat to be restored after reload")
	}
	if !strings.Contains(fullPage, `delete_chat`) {
		t.Fatalf("expected chat deletion RPC")
	}
	if !strings.Contains(fullPage, `deleteChat(chatID(chat))`) {
		t.Fatalf("expected chat list trash action")
	}
	if !strings.Contains(fullPage, `chatStatusLabel(chat)`) || !strings.Contains(fullPage, `chat-status-icon`) {
		t.Fatalf("expected chat sidebar to render per-chat animated status icons")
	}
	if !strings.Contains(fullPage, `chatContextLabel(chat)`) || !strings.Contains(fullPage, `context_window`) || !strings.Contains(fullPage, `'% ctx)'`) {
		t.Fatalf("expected chat sidebar to render context percentage")
	}
	if !strings.Contains(fullPage, `toolApprovalPending(tool)`) || !strings.Contains(fullPage, `rpc('approve', {tool_call_id: toolCallID(tool)})`) || !strings.Contains(fullPage, `rpc('deny', {tool_call_id: toolCallID(tool)})`) {
		t.Fatalf("expected pending tool approval cards to expose approve and deny actions inline")
	}
	if !strings.Contains(fullPage, `toolStatus(tool) === 'awaiting_approval'`) {
		t.Fatalf("expected approval actions to hide once the pushed tool turn is no longer pending")
	}
	if !strings.Contains(fullPage, `this.state.chat_statuses`) || !strings.Contains(fullPage, `waiting_llm: 'Waiting for LLM'`) {
		t.Fatalf("expected chat sidebar status helpers for all chats")
	}
	if strings.Contains(fullPage, `x-show="!chatStatusIdle(chat)"`) || !strings.Contains(fullPage, `:title="chatStatusLabel(chat)"`) {
		t.Fatalf("expected all chat status icons to render with hover tooltips")
	}
	if !strings.Contains(fullPage, `.chat-status-icon.status-idle`) || strings.Contains(fullPage, `.chat-status-icon.status-idle { color: var(--bs-secondary-color); opacity: .65; animation`) {
		t.Fatalf("expected idle chat status icon to be static")
	}
	if !strings.Contains(fullPage, `@keyframes chat-status-spin`) || !strings.Contains(fullPage, `chatStatusIcon(chat)`) {
		t.Fatalf("expected chat status icons to animate per state")
	}
	if !strings.Contains(fullPage, `openProviderDialog()`) {
		t.Fatalf("expected top status bar provider dialog button")
	}
	if !strings.Contains(fullPage, `openSessionDialog()`) {
		t.Fatalf("expected top status bar session dialog button")
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
	if !strings.Contains(fullPage, `provider_state`) {
		t.Fatalf("expected provider dialog to load provider state")
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
	if !strings.Contains(fullPage, `showToast`) {
		t.Fatalf("expected toast error path")
	}
}

func TestFaviconDoesNot404(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
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
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
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

func TestWebSocketSwitchChatReturnsUpdatedState(t *testing.T) {
	ctrl := newTestController(t)
	firstID := ctrl.State().ActiveChatID
	if err := ctrl.NewChat(context.Background(), "side chat"); err != nil {
		t.Fatalf("new chat: %v", err)
	}
	secondID := ctrl.State().ActiveChatID
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("expected two distinct chats, first=%s second=%s", firstID, secondID)
	}

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
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":1,"method":"switch_chat","params":{"chat_id":"%s"}}`, firstID))); err != nil {
		t.Fatalf("write switch_chat: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			ActiveChatID domain.ID `json:"active_chat_id"`
			Snapshot     struct {
				Chat struct {
					ID domain.ID
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
	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":1,"method":"delete_chat","params":{"chat_id":"%s"}}`, deletedID))); err != nil {
		t.Fatalf("write delete_chat: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			ActiveChatID domain.ID `json:"active_chat_id"`
			Chats        []struct {
				ID domain.ID
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
		t.Fatalf("expected active chat to switch away from %s", deletedID)
	}
	for _, chat := range resp.Result.Chats {
		if chat.ID == deletedID {
			t.Fatalf("deleted chat still listed: %#v", resp.Result.Chats)
		}
	}
}

func TestWebSocketSessionManagementCreatesAndSwitchesWorkspaceSessions(t *testing.T) {
	ctrl := newTestController(t)
	initialID := ctrl.State().Session.ID
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
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"list_sessions","params":{}}`)); err != nil {
		t.Fatalf("write list_sessions: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
	var listResp struct {
		OK     bool `json:"ok"`
		Result struct {
			ActiveID domain.ID `json:"active_id"`
			Sessions []struct {
				ID domain.ID
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

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":2,"method":"new_session","params":{"title":"Side Session"}}`)); err != nil {
		t.Fatalf("write new_session: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 2)
	var newResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				ID    domain.ID
				Title string
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
	if newID == "" || newID == initialID || newResp.Result.Session.Title != "Side Session" {
		t.Fatalf("unexpected new session response: %#v", newResp.Result.Session)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`{"id":3,"method":"switch_session","params":{"session_id":"%s"}}`, initialID))); err != nil {
		t.Fatalf("write switch_session: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 3)
	var switchResp struct {
		OK     bool `json:"ok"`
		Result struct {
			Session struct {
				ID domain.ID
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

func TestWebSocketComposerCompletionsReturnsSkillsAndReferences(t *testing.T) {
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
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"method":"composer_completions","params":{"text":"Use $rev","cursor":8}}`)); err != nil {
		t.Fatalf("write skill completion request: %v", err)
	}
	msg := readRPCResponse(t, ctx, conn, 1)
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

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"id":2,"method":"composer_completions","params":{"text":"Read @REA","cursor":9}}`)); err != nil {
		t.Fatalf("write reference completion request: %v", err)
	}
	msg = readRPCResponse(t, ctx, conn, 2)
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

func newTestController(t *testing.T) *uicore.Controller {
	t.Helper()
	return newTestControllerWithWorkdir(t, t.TempDir())
}

func newTestControllerWithWorkdir(t *testing.T, workdir string) *uicore.Controller {
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
	engine := agent.New(cfg, st, nil, nil, workdir)
	ctrl := uicore.New(cfg, st, engine, workdir)
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
