package webui

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/uicore"
)

const defaultOpenDelay = 5 * time.Second
const assetHashPlaceholder = "__KODER_ASSET_HASH__"
const indexAssetPath = "assets/index.html"

//go:embed assets
var webAssets embed.FS

var (
	indexHTML        = mustReadAsset(indexAssetPath)
	currentAssetHash = computeAssetHash(webAssets)
)

// Options configures the web UI server.
type Options struct {
	Bind        string
	NoBrowser   bool
	OpenDelay   time.Duration
	OpenBrowser func(string) error
}

// Server serves the browser UI and bridges websocket RPC to the controller.
type Server struct {
	controller *uicore.Controller
	options    Options
	server     *http.Server
	listener   net.Listener
	connected  chan struct{}
	once       sync.Once
}

// Start starts the web UI server.
func Start(ctx context.Context, controller *uicore.Controller, options Options) (*Server, error) {
	if controller == nil {
		return nil, fmt.Errorf("controller is nil")
	}
	bind := strings.TrimSpace(options.Bind)
	if bind == "" {
		bind = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, fmt.Errorf("listen web ui: %w", err)
	}
	s := &Server{
		controller: controller,
		options:    options,
		listener:   listener,
		connected:  make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/favicon.ico", handleFavicon)
	mux.Handle("/assets/", assetHandler())
	mux.HandleFunc("/api/show-image", handleShowImage)
	mux.HandleFunc("/ws", s.handleWebSocket)
	s.server = &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("web ui server stopped", "error", err)
		}
	}()
	go s.openBrowserIfNeeded(ctx)
	return s, nil
}

// Addr returns the resolved server bind address.
func (s *Server) Addr() string {
	if s == nil || s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// URL returns the local browser URL.
func (s *Server) URL() string {
	if s == nil {
		return ""
	}
	return "http://" + s.Addr()
}

func (s *Server) openBrowserIfNeeded(ctx context.Context) {
	if s.options.NoBrowser {
		return
	}
	delay := s.options.OpenDelay
	if delay <= 0 {
		delay = defaultOpenDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-s.connected:
		return
	case <-timer.C:
	}
	open := s.options.OpenBrowser
	if open == nil {
		open = OpenBrowser
	}
	if err := open(s.URL()); err != nil {
		slog.Warn("open browser failed", "url", s.URL(), "error", err)
	}
}

func (s *Server) markConnected() {
	s.once.Do(func() {
		close(s.connected)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.markConnected()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(renderIndexHTML()))
}

func handleFavicon(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/favicon.ico" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusNoContent)
}

func assetHandler() http.Handler {
	files := http.FileServer(http.FS(webAssets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

func handleShowImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	path := filepath.Clean(rawPath)
	info, err := os.Stat(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer file.Close()
	var sniff [512]byte
	n, err := file.Read(sniff[:])
	if err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	mimeType := http.DetectContentType(sniff[:n])
	if attachment.ClassifyMIME(mimeType) != attachment.KindImage {
		http.Error(w, "path is not an image", http.StatusUnsupportedMediaType)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	s.markConnected()

	ctx := r.Context()
	events, unsub := s.controller.Subscribe()
	defer unsub()
	done := make(chan struct{})
	var writeMu sync.Mutex
	var baselineMu sync.RWMutex
	baselineEstablished := false
	go func() {
		defer close(done)
		for event := range events {
			baselineMu.RLock()
			ready := baselineEstablished
			baselineMu.RUnlock()
			if !ready {
				continue
			}
			webEvent, ok := webEventFromControllerEvent(event)
			if !ok {
				continue
			}
			if err := writeJSON(ctx, conn, &writeMu, webEvent); err != nil {
				return
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(data, &req); err != nil {
			_ = writeJSON(ctx, conn, &writeMu, rpcResponse{ID: nil, OK: false, Error: err.Error()})
			continue
		}
		result, err := s.handleRPC(ctx, req.Method, req.Params)
		resp := rpcResponse{ID: req.ID, OK: err == nil, Result: result}
		if err != nil {
			resp.Error = err.Error()
		}
		if err := writeJSON(ctx, conn, &writeMu, resp); err != nil {
			return
		}
		if err == nil && rpcEstablishesSnapshotBaseline(req.Method, result) {
			baselineMu.Lock()
			baselineEstablished = true
			baselineMu.Unlock()
		}
		select {
		case <-done:
			return
		default:
		}
	}
}

func (s *Server) handleRPC(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch strings.TrimSpace(method) {
	case "hello":
		return rpcHello{
			AssetHash: currentAssetHash,
			State:     s.controller.State(),
		}, nil
	case "get_state":
		return s.controller.State(), nil
	case "send_prompt":
		var in struct {
			Text string `json:"text"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"queued": true}, s.controller.SendPrompt(in.Text)
	case "continue":
		var in struct {
			Note string `json:"note"`
		}
		_ = decodeParams(params, &in)
		return map[string]bool{"queued": true}, s.controller.Continue(in.Note)
	case "stop":
		return map[string]bool{"stopped": true}, s.controller.Stop()
	case "compact":
		return map[string]bool{"started": true}, s.controller.Compact()
	case "refresh_workspace":
		if err := s.controller.RefreshWorkspace(ctx); err != nil {
			return nil, err
		}
		return s.controller.State(), nil
	case "switch_chat":
		var in struct {
			ChatID int64 `json:"chat_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if err := s.controller.SwitchChat(ctx, in.ChatID); err != nil {
			return nil, err
		}
		return s.controller.State(), nil
	case "new_chat":
		var in struct {
			Title string `json:"title"`
		}
		_ = decodeParams(params, &in)
		if err := s.controller.NewChat(ctx, in.Title); err != nil {
			return nil, err
		}
		return s.controller.State(), nil
	case "list_sessions":
		return s.controller.Sessions(ctx)
	case "switch_session":
		var in struct {
			SessionID int64 `json:"session_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if err := s.controller.SwitchSession(ctx, in.SessionID); err != nil {
			return nil, err
		}
		return s.controller.State(), nil
	case "new_session":
		var in struct {
			Title string `json:"title"`
		}
		_ = decodeParams(params, &in)
		if err := s.controller.NewSession(ctx, in.Title); err != nil {
			return nil, err
		}
		return s.controller.State(), nil
	case "delete_chat":
		var in struct {
			ChatID int64 `json:"chat_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if err := s.controller.DeleteChat(ctx, in.ChatID); err != nil {
			return nil, err
		}
		return s.controller.State(), nil
	case "approve":
		var in struct {
			ToolCallID string `json:"tool_call_id"`
			ID         int64  `json:"id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.ToolCallID) == "" && in.ID != 0 {
			in.ToolCallID = fmt.Sprint(in.ID)
		}
		return map[string]bool{"accepted": true}, s.controller.Approve(in.ToolCallID)
	case "deny":
		var in struct {
			ToolCallID string `json:"tool_call_id"`
			ID         int64  `json:"id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.ToolCallID) == "" && in.ID != 0 {
			in.ToolCallID = fmt.Sprint(in.ID)
		}
		return map[string]bool{"accepted": true}, s.controller.Deny(in.ToolCallID)
	case "composer_completions":
		var in struct {
			Text   string `json:"text"`
			Cursor int    `json:"cursor"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.CompleteComposer(in.Text, in.Cursor)
	case "set_theme":
		var in struct {
			Theme string `json:"theme"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		s.controller.SetTheme(in.Theme)
		return map[string]string{"theme": in.Theme}, nil
	case "list_models":
		options, err := s.controller.ModelOptions(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"models": options}, nil
	case "set_model":
		var in struct {
			ProviderID string `json:"provider_id"`
			ModelID    string `json:"model_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if err := s.controller.SetModel(ctx, in.ProviderID, in.ModelID); err != nil {
			return nil, err
		}
		return s.controller.State(), nil
	case "provider_state":
		return s.controller.Providers(), nil
	case "new_provider_draft":
		var in struct {
			TemplateID string `json:"template_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.NewProviderDraft(in.TemplateID)
	case "test_provider":
		var in uicore.ProviderDraft
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.TestProvider(ctx, in)
	case "save_provider":
		var in uicore.ProviderDraft
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		providers, err := s.controller.SaveProvider(ctx, in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"providers": providers, "state": s.controller.State()}, nil
	case "delete_provider":
		var in struct {
			ProviderID string `json:"provider_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		providers, err := s.controller.DeleteProvider(ctx, in.ProviderID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"providers": providers, "state": s.controller.State()}, nil
	case "set_permission_profile":
		var in struct {
			Profile string `json:"profile"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if err := s.controller.SetPermissionProfile(ctx, in.Profile); err != nil {
			return nil, err
		}
		return s.controller.State(), nil
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

type stateDelta struct {
	Session      any    `json:"session,omitempty"`
	Sessions     any    `json:"sessions,omitempty"`
	Chats        any    `json:"chats,omitempty"`
	ChatStatuses any    `json:"chat_statuses,omitempty"`
	ActiveChatID int64  `json:"active_chat_id,omitempty"`
	Permissions  any    `json:"permissions,omitempty"`
	Milestones   any    `json:"milestones,omitempty"`
	Todos        any    `json:"todos,omitempty"`
	TodosByRef   any    `json:"todos_by_milestone,omitempty"`
	Workspace    any    `json:"workspace_status,omitempty"`
	Theme        string `json:"theme,omitempty"`
	Workdir      string `json:"workdir,omitempty"`
	Error        string `json:"error,omitempty"`
}

type chatDelta struct {
	ChatID            int64                `json:"chat_id"`
	Chat              any                  `json:"chat,omitempty"`
	Item              *domain.TimelineItem `json:"item,omitempty"`
	Approvals         any                  `json:"approvals,omitempty"`
	Queue             any                  `json:"queue,omitempty"`
	Context           any                  `json:"context,omitempty"`
	Status            string               `json:"status,omitempty"`
	StatusText        string               `json:"status_text,omitempty"`
	Active            bool                 `json:"active"`
	TranscriptChanged bool                 `json:"transcript_changed,omitempty"`
	QueueChanged      bool                 `json:"queue_changed,omitempty"`
	StatusChanged     bool                 `json:"status_changed,omitempty"`
	ContextChanged    bool                 `json:"context_changed,omitempty"`
	ApprovalsChanged  bool                 `json:"approvals_changed,omitempty"`
	Error             string               `json:"error,omitempty"`
}

func webEventFromControllerEvent(event uicore.Event) (uicore.Event, bool) {
	switch event.Type {
	case "chat_update":
		update, ok := event.Payload.(chat.Update)
		if !ok {
			return uicore.Event{}, false
		}
		return uicore.Event{Seq: event.Seq, Type: "chat_delta", Payload: chatDeltaFromUpdate(update)}, true
	case "snapshot":
		state, ok := event.Payload.(uicore.State)
		if !ok {
			return uicore.Event{}, false
		}
		return uicore.Event{Seq: event.Seq, Type: "state_delta", Payload: stateDeltaFromState(state)}, true
	default:
		return event, true
	}
}

func chatDeltaFromUpdate(update chat.Update) chatDelta {
	snapshot := update.Snapshot
	delta := chatDelta{
		ChatID:            snapshot.Chat.ID,
		Chat:              snapshot.Chat,
		Approvals:         snapshot.Approvals,
		Queue:             snapshot.QueuedInputs,
		Context:           snapshot.Context,
		Status:            string(snapshot.Status),
		StatusText:        snapshot.StatusText,
		Active:            snapshot.Active,
		TranscriptChanged: update.TranscriptChanged,
		QueueChanged:      update.QueueChanged,
		StatusChanged:     update.StatusChanged,
		ContextChanged:    update.ContextChanged,
		ApprovalsChanged:  update.ApprovalsChanged,
	}
	if delta.Status == "" && update.Status != "" {
		delta.Status = string(update.Status)
	}
	if delta.StatusText == "" {
		delta.StatusText = update.StatusText
	}
	if update.Event != nil && update.Event.Err != nil {
		delta.Error = update.Event.Err.Error()
	}
	if item, ok := changedTimelineItem(update); ok {
		delta.Item = &item
	}
	return delta
}

func changedTimelineItem(update chat.Update) (domain.TimelineItem, bool) {
	if update.Event != nil && update.Event.Item.ID != "" {
		return update.Event.Item, true
	}
	if !update.TranscriptChanged {
		return domain.TimelineItem{}, false
	}
	timeline := update.Snapshot.Timeline
	if len(timeline) == 0 {
		return domain.TimelineItem{}, false
	}
	return timeline[len(timeline)-1], true
}

func stateDeltaFromState(state uicore.State) stateDelta {
	return stateDelta{
		Session:      state.Session,
		Sessions:     state.Sessions,
		Chats:        state.Chats,
		ChatStatuses: state.ChatStatuses,
		ActiveChatID: state.ActiveChatID,
		Permissions:  state.Permissions,
		Milestones:   state.Milestones,
		Todos:        state.Todos,
		TodosByRef:   state.TodosByRef,
		Workspace:    state.Workspace,
		Theme:        state.Theme,
		Workdir:      state.Workdir,
		Error:        state.Error,
	}
}

func rpcEstablishesSnapshotBaseline(method string, result any) bool {
	method = strings.TrimSpace(method)
	if method == "hello" || method == "get_state" {
		return true
	}
	if _, ok := result.(uicore.State); ok {
		return true
	}
	if hello, ok := result.(rpcHello); ok && hello.State != nil {
		return true
	}
	if value, ok := result.(map[string]any); ok {
		_, ok = value["state"]
		return ok
	}
	return false
}

type rpcHello struct {
	AssetHash string `json:"asset_hash"`
	State     any    `json:"state"`
}

type rpcRequest struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcResponse struct {
	ID     any    `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func writeJSON(ctx context.Context, conn *websocket.Conn, mu *sync.Mutex, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	return conn.Write(ctx, websocket.MessageText, data)
}

func renderIndexHTML() string {
	return strings.ReplaceAll(indexHTML, assetHashPlaceholder, currentAssetHash)
}

func mustReadAsset(path string) string {
	data, err := fs.ReadFile(webAssets, path)
	if err != nil {
		panic(fmt.Sprintf("read embedded web asset %s: %v", path, err))
	}
	return string(data)
}

func computeAssetHash(assets fs.FS) string {
	h := sha256.New()
	_ = fs.WalkDir(assets, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".html") {
			data = []byte(strings.ReplaceAll(string(data), assetHashPlaceholder, ""))
		}
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(path))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		return nil
	})
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

// OpenBrowser opens url with the platform's default browser.
func OpenBrowser(url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return fmt.Errorf("url is empty")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
