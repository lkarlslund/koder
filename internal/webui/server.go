package webui

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
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
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/app"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

const defaultOpenDelay = 5 * time.Second
const assetHashPlaceholder = "__KODER_ASSET_HASH__"
const indexAssetPath = "assets/index.html"
const defaultTimelinePageSize = 80

//go:embed assets
var webAssets embed.FS

var (
	indexHTML        = mustReadAsset(indexAssetPath)
	currentAssetHash = computeAssetHash(webAssets)
)

// Options configures the web UI server.
type Options struct {
	Bind                  string
	NoOpenBrowser         bool
	OpenDelay             time.Duration
	OpenBrowser           func(string) error
	Debug                 *debugsrv.Recorder
	RequestProcessRestart func() error
}

// Server serves the browser UI and bridges websocket RPC to the controller.
type Server struct {
	controller        *app.Controller
	options           Options
	server            *http.Server
	listener          net.Listener
	connected         chan struct{}
	once              sync.Once
	debug             *debugsrv.Recorder
	clientSelectionMu sync.Mutex
	clientSelections  map[string]clientSelection
}

type clientSelection struct {
	SessionID id.ID
	ChatID    id.ID
}

type timelinePageResponse struct {
	ChatID    id.ID                 `json:"chat_id"`
	Items     []domain.TimelineItem `json:"items"`
	HasMore   bool                  `json:"has_more"`
	Before    id.ID                 `json:"before"`
	LoadedAll bool                  `json:"loaded_all"`
	Total     int                   `json:"total"`
}

// Start starts the web UI server.
func Start(ctx context.Context, controller *app.Controller, options Options) (*Server, error) {
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
		controller:       controller,
		options:          options,
		listener:         listener,
		connected:        make(chan struct{}),
		debug:            options.Debug,
		clientSelections: map[string]clientSelection{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/favicon.ico", handleFavicon)
	mux.Handle("/assets/", assetHandler())
	mux.HandleFunc("/api/health", handleHealth)
	mux.HandleFunc("/api/restart-needed", s.handleRestartNeeded)
	mux.HandleFunc("/api/rpc", s.handleHTTPRPC)
	mux.HandleFunc("/api/rpc/", s.handleHTTPRPC)
	mux.HandleFunc("/api/sessions/", s.handleSessionAPI)
	mux.HandleFunc("/api/show-image", handleShowImage)
	mux.HandleFunc("/api/attachments/clipboard-image", s.handleClipboardImage)
	mux.HandleFunc("/ws", s.handleWebSocket)
	if s.debug != nil {
		s.debug.SetDebugAPI(s.URL())
		debugServer := debugsrv.NewServer(controller, s.debug)
		debugServer.Register(mux)
	}
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

// AppURL returns the canonical browser app URL for the active session.
func (s *Server) AppURL() string {
	if s == nil {
		return ""
	}
	return s.URL()
}

func (s *Server) openBrowserIfNeeded(ctx context.Context) {
	if s.options.NoOpenBrowser {
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
	if err := open(s.AppURL()); err != nil {
		slog.Warn("open browser failed", "url", s.AppURL(), "error", err)
	}
}

func (s *Server) markConnected() {
	s.once.Do(func() {
		close(s.connected)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.markConnected()
	if _, ok := fileBrowserSessionFromPath(r.URL.Path); ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(renderFileBrowserHTML()))
		return
	}
	if _, ok := planningBoardSessionFromPath(r.URL.Path); ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(renderPlanningBoardHTML()))
		return
	}
	if r.URL.Path != "/" {
		selection, ok := routeSelectionFromPath(r.URL.Path)
		if !ok || selection.SessionID == "" {
			http.NotFound(w, r)
			return
		}
		if !s.routeSelectionExists(r.Context(), selection) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(renderIndexHTML()))
}

func (s *Server) routeSelectionExists(ctx context.Context, selection clientSelection) bool {
	if selection.SessionID == "" {
		return false
	}
	if selection.ChatID == "" {
		_, err := s.controller.SessionByID(ctx, selection.SessionID)
		return err == nil
	}
	_, err := s.controller.StateForSelection(ctx, app.Selection{SessionID: selection.SessionID, ChatID: selection.ChatID})
	return err == nil
}

func routeSelectionFromPath(path string) (clientSelection, bool) {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return clientSelection{}, true
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 && len(parts) != 4 {
		return clientSelection{}, false
	}
	if parts[0] != "s" || strings.TrimSpace(parts[1]) == "" {
		return clientSelection{}, false
	}
	selection := clientSelection{SessionID: id.ID(strings.TrimSpace(parts[1]))}
	if len(parts) == 2 {
		return selection, true
	}
	if parts[2] != "c" || strings.TrimSpace(parts[3]) == "" {
		return clientSelection{}, false
	}
	selection.ChatID = id.ID(strings.TrimSpace(parts[3]))
	return selection, true
}

func handleFavicon(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/favicon.ico" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(webAssets, "assets/favicon.ico")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"asset_hash": currentAssetHash,
	})
}

func (s *Server) handleRestartNeeded(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var build app.RestartBuildInfo
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&build); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, fmt.Sprintf("decode restart build info: %v", err), http.StatusBadRequest)
			return
		}
	}
	if build.BuildID == "" {
		build.BuildID = restartBuildID(build)
	}
	slog.Info("restart-needed api received",
		"remote", r.RemoteAddr,
		"build_id", build.BuildID,
		"commit", build.Commit,
		"dirty", build.Dirty,
		"build_time", build.BuildTime,
	)
	s.controller.MarkRestartNeeded(build)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":             true,
		"restart_needed": true,
		"restart_build":  build,
	})
}

func restartBuildID(build app.RestartBuildInfo) string {
	commit := strings.TrimSpace(build.Commit)
	if commit == "" {
		return ""
	}
	if strings.TrimSpace(build.Dirty) == "true" {
		commit += "-dirty"
	}
	if built := strings.TrimSpace(build.BuildTime); built != "" {
		return commit + " @ " + built
	}
	return commit
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

func (s *Server) handleClipboardImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 25<<20)
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		http.Error(w, "parse image upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "image is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read image upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	mimeType := strings.TrimSpace(header.Header.Get("Content-Type"))
	draft, err := s.controller.ImportClipboardImage(data, header.Filename, mimeType)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(draft)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	s.markConnected()

	ctx := r.Context()
	clientID := string(id.New())
	initialSelection := clientSelection{
		SessionID: id.ID(strings.TrimSpace(r.URL.Query().Get("session"))),
		ChatID:    id.ID(strings.TrimSpace(r.URL.Query().Get("chat"))),
	}
	slog.Info("websocket connected", "client", clientID, "remote", r.RemoteAddr, "session", initialSelection.SessionID, "chat", initialSelection.ChatID)
	if initialSelection.SessionID != "" || initialSelection.ChatID != "" {
		if initialSelection.SessionID != "" {
			if err := s.controller.EnsureSessionWorkspace(ctx, initialSelection.SessionID); err != nil {
				slog.Debug("ensure websocket session workspace", "client", clientID, "session", initialSelection.SessionID, "error", err)
			}
		}
		s.setClientSelection(clientID, initialSelection)
	}
	if s.debug != nil {
		s.debug.RegisterClient(debugsrv.ClientDebug{
			ID:         clientID,
			RemoteAddr: r.RemoteAddr,
			UserAgent:  r.UserAgent(),
		})
		s.updateDebugChats()
		defer s.debug.UnregisterClient(clientID)
	}
	defer s.deleteClientSelection(clientID)
	pushEvents := make(chan app.Event, 128)
	controllerEvents, controllerUnsub := s.controller.Subscribe()
	defer controllerUnsub()
	go func() {
		for event := range controllerEvents {
			if !websocketUsesGlobalControllerEvent(event.Type) {
				continue
			}
			select {
			case pushEvents <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	var selectedUnsub func()
	var selectedSubscription clientSelection
	syncSelectedSubscription := func() {
		selection := s.clientSelection(clientID)
		if selection.SessionID == selectedSubscription.SessionID && selection.ChatID == selectedSubscription.ChatID {
			return
		}
		if selectedUnsub != nil {
			selectedUnsub()
			selectedUnsub = nil
		}
		selectedSubscription = selection
		if selection.SessionID == "" {
			return
		}
		events, unsub, err := s.controller.SubscribeSelection(ctx, app.Selection{SessionID: selection.SessionID, ChatID: selection.ChatID})
		if err != nil {
			slog.Error("subscribe websocket to selected session", "client", clientID, "session", selection.SessionID, "chat", selection.ChatID, "error", err)
			return
		}
		selectedUnsub = unsub
		slog.Info("websocket subscribed to selected session", "client", clientID, "session", selection.SessionID, "chat", selection.ChatID)
		go func(sessionID, chatID id.ID, events <-chan app.Event) {
			for event := range events {
				select {
				case pushEvents <- event:
				case <-ctx.Done():
					return
				}
			}
			slog.Info("websocket selected session subscription closed", "client", clientID, "session", sessionID, "chat", chatID)
		}(selection.SessionID, selection.ChatID, events)
	}
	defer func() {
		if selectedUnsub != nil {
			selectedUnsub()
		}
	}()
	done := make(chan struct{})
	var writeMu sync.Mutex
	var baselineMu sync.RWMutex
	baselineEstablished := false
	go func() {
		defer close(done)
		heartbeat := time.NewTicker(websocketHeartbeatInterval)
		defer heartbeat.Stop()
		for {
			select {
			case event := <-pushEvents:
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
				s.updateDebugChats()
				size, err := writeJSON(ctx, conn, &writeMu, webEvent)
				if err != nil {
					slog.Info("websocket closed while writing event", "client", clientID, "type", webEvent.Type, "error", err)
					return
				}
				s.recordWebSocketWrite(clientID, webEvent.Type, size)
			case <-heartbeat.C:
				size, err := writeJSON(ctx, conn, &writeMu, map[string]any{
					"type": "heartbeat",
					"payload": map[string]string{
						"server_time": time.Now().UTC().Format(time.RFC3339Nano),
					},
				})
				if err != nil {
					slog.Info("websocket closed while writing heartbeat", "client", clientID, "error", err)
					return
				}
				s.recordWebSocketWrite(clientID, "heartbeat", size)
			case <-ctx.Done():
				slog.Info("websocket context closed", "client", clientID, "error", ctx.Err())
				return
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			slog.Info("websocket closed while reading", "client", clientID, "error", err)
			return
		}
		var req rpcRequest
		if err := json.Unmarshal(data, &req); err != nil {
			if size, writeErr := writeJSON(ctx, conn, &writeMu, rpcResponse{ID: nil, OK: false, Error: err.Error()}); writeErr == nil {
				s.recordWebSocketWrite(clientID, "rpc_response", size)
			}
			continue
		}
		result, err := s.handleRPC(ctx, clientID, req.Method, req.Params)
		if err == nil {
			s.updateClientSelectionFromResult(clientID, result)
		}
		resp := rpcResponse{ID: req.ID, OK: err == nil, Result: result}
		if err != nil {
			resp.Error = err.Error()
		}
		size, writeErr := writeJSON(ctx, conn, &writeMu, resp)
		if writeErr != nil {
			slog.Info("websocket closed while writing rpc response", "client", clientID, "method", req.Method, "error", writeErr)
			return
		}
		s.recordWebSocketWrite(clientID, "rpc_response", size)
		if err == nil {
			s.updateDebugChats()
		}
		if err == nil && rpcEstablishesSnapshotBaseline(req.Method, result) {
			baselineMu.Lock()
			baselineEstablished = true
			baselineMu.Unlock()
			syncSelectedSubscription()
		}
		select {
		case <-done:
			return
		default:
		}
	}
}

func (s *Server) recordWebSocketWrite(clientID string, eventType string, size int) {
	if s.debug == nil || size <= 0 {
		return
	}
	patch := debugsrv.ClientDebug{LastOutboundWSBytes: size}
	switch eventType {
	case "chat_delta":
		patch.LastChatDeltaBytes = size
	case "state_delta":
		patch.LastStateDeltaBytes = size
	}
	s.debug.PatchClient(clientID, patch)
}

func (s *Server) handleHTTPRPC(w http.ResponseWriter, r *http.Request) {
	s.markConnected()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req, err := decodeHTTPRPCRequest(r)
	if err != nil {
		writeHTTPRPCResponse(w, rpcResponse{OK: false, Error: err.Error()})
		return
	}
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		clientID = "http-" + string(id.New())
	}
	if req.SelectedSession != "" || req.SelectedChat != "" {
		if req.SelectedSession != "" {
			if current := s.clientSelection(clientID); current.SessionID != req.SelectedSession {
				if err := s.controller.EnsureSessionWorkspace(r.Context(), req.SelectedSession); err != nil {
					slog.Debug("ensure http rpc session workspace", "client", clientID, "session", req.SelectedSession, "error", err)
				}
			}
		}
		s.setClientSelection(clientID, clientSelection{SessionID: req.SelectedSession, ChatID: req.SelectedChat})
	}
	result, err := s.handleRPC(r.Context(), clientID, req.Method, req.Params)
	if err == nil {
		s.updateClientSelectionFromResult(clientID, result)
		s.updateDebugChats()
	}
	resp := rpcResponse{ID: req.ID, OK: err == nil, Result: result}
	if err != nil {
		resp.Error = err.Error()
	}
	writeHTTPRPCResponse(w, resp)
}

func (s *Server) handleRPC(ctx context.Context, clientID string, method string, params json.RawMessage) (any, error) {
	switch strings.TrimSpace(method) {
	case "hello":
		state, err := s.stateForClient(ctx, clientID)
		if err != nil {
			return nil, err
		}
		return rpcHello{
			AssetHash: currentAssetHash,
			ClientID:  clientID,
			State:     state,
		}, nil
	case "get_state":
		return s.stateForClient(ctx, clientID)
	case "client_state":
		var in debugsrv.ClientDebug
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		s.setClientSelection(clientID, clientSelection{SessionID: in.SelectedSession, ChatID: in.SelectedChat})
		if s.debug != nil {
			s.debug.UpdateClient(clientID, in)
		}
		return map[string]bool{"accepted": true}, nil
	case "send_prompt":
		var in struct {
			Text        string             `json:"text"`
			Attachments []attachment.Draft `json:"attachments"`
			Steer       bool               `json:"steer"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		selection := s.appSelection(clientID)
		if in.Steer {
			return map[string]bool{"queued": true}, s.controller.SendPromptWithKindSelection(ctx, selection, chat.QueueKindSteer, in.Text, in.Attachments)
		}
		return map[string]bool{"queued": true}, s.controller.SendPromptWithKindSelection(ctx, selection, chat.QueueKindUser, in.Text, in.Attachments)
	case "continue":
		var in struct {
			Note string `json:"note"`
		}
		_ = decodeParams(params, &in)
		return map[string]bool{"queued": true}, s.controller.ContinueForSelection(ctx, s.appSelection(clientID), in.Note)
	case "reorder_queue":
		var in struct {
			IDs []id.ID `json:"ids"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"reordered": true}, s.controller.ReorderQueueForSelection(ctx, s.appSelection(clientID), in.IDs)
	case "delete_queue_item":
		var in struct {
			ID id.ID `json:"id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": true}, s.controller.DeleteQueueItemForSelection(ctx, s.appSelection(clientID), in.ID)
	case "toggle_queue_item_kind":
		var in struct {
			ID id.ID `json:"id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"updated": true}, s.controller.ToggleQueueItemKindForSelection(ctx, s.appSelection(clientID), in.ID)
	case "send_queue_item_now":
		var in struct {
			ID id.ID `json:"id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"queued": true}, s.controller.SendQueueItemNowForSelection(ctx, s.appSelection(clientID), in.ID)
	case "abort_and_send_queue_item_now":
		var in struct {
			ID id.ID `json:"id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"queued": true}, s.controller.AbortAndSendQueueItemNowForSelection(ctx, s.appSelection(clientID), in.ID)
	case "stop":
		return map[string]bool{"stopped": true}, s.controller.StopForSelection(ctx, s.appSelection(clientID))
	case "stop_after_turn":
		return map[string]bool{"stopping": true}, s.controller.StopAfterCurrentTurnForSelection(ctx, s.appSelection(clientID))
	case "shutdown":
		var in struct {
			Reason string `json:"reason"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		reason, ok := chat.ParseCancelReason(in.Reason)
		if !ok {
			return nil, fmt.Errorf("unknown shutdown reason %q", in.Reason)
		}
		slog.Info("shutdown rpc received", "client", clientID, "reason", reason)
		return map[string]bool{"stopping": true}, s.controller.ShutdownWithCancelReason(ctx, reason)
	case "restart_process":
		var in struct {
			Hard bool `json:"hard"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		reason := chat.CancelReasonRestartInterrupt
		if in.Hard {
			reason = chat.CancelReasonRestartInterruptHard
		}
		slog.Info("restart rpc received", "client", clientID, "hard", in.Hard, "reason", reason)
		go func() {
			s.restartAfterShutdown(reason, in.Hard)
		}()
		return map[string]bool{"restarting": true, "acknowledged": true, "hard": in.Hard}, nil
	case "compact":
		var in struct {
			Instructions string `json:"instructions"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"started": true}, s.controller.CompactForSelection(ctx, s.appSelection(clientID), in.Instructions)
	case "refresh_workspace":
		status, err := s.controller.RefreshWorkspaceForSelection(ctx, s.appSelection(clientID))
		if err != nil {
			return nil, err
		}
		return map[string]any{"workspace_status": status}, nil
	case "load_timeline":
		var in struct {
			ChatID id.ID `json:"chat_id"`
			Before id.ID `json:"before"`
			Limit  int   `json:"limit"`
			All    bool  `json:"all"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit <= 0 || limit > defaultTimelinePageSize*4 {
			limit = defaultTimelinePageSize
		}
		page, err := s.controller.TimelinePage(ctx, s.clientSelection(clientID).SessionID, in.ChatID, in.Before, limit, in.All)
		if err != nil {
			return nil, err
		}
		return timelinePageResponse{
			ChatID:    in.ChatID,
			Items:     page.Items,
			HasMore:   page.HasMore,
			Before:    page.Before,
			LoadedAll: page.LoadedAll,
			Total:     page.Total,
		}, nil
	case "switch_chat":
		var in struct {
			ChatID id.ID `json:"chat_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		selection := s.clientSelection(clientID)
		selection.ChatID = in.ChatID
		s.setClientSelection(clientID, selection)
		return s.stateForClient(ctx, clientID)
	case "new_chat":
		var in struct {
			Title string `json:"title"`
		}
		_ = decodeParams(params, &in)
		chatRecord, err := s.controller.NewChatForSelection(ctx, s.appSelection(clientID), in.Title)
		if err != nil {
			return nil, err
		}
		selection := s.clientSelection(clientID)
		selection.ChatID = chatRecord.ID
		s.setClientSelection(clientID, selection)
		return s.stateForClient(ctx, clientID)
	case "rename_chat":
		var in struct {
			ChatID id.ID  `json:"chat_id"`
			Title  string `json:"title"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if in.ChatID == "" {
			return nil, fmt.Errorf("chat id is required")
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			return nil, fmt.Errorf("chat title is required")
		}
		selection := s.clientSelection(clientID)
		if selection.SessionID == "" {
			return nil, fmt.Errorf("session is required")
		}
		if _, err := s.controller.UpdateChat(ctx, selection.SessionID, in.ChatID, in.ChatID, chattool.UpdateRequest{Title: title}); err != nil {
			return nil, err
		}
		return s.stateForClient(ctx, clientID)
	case "rollback_chat":
		var in struct {
			ChatID       id.ID `json:"chat_id"`
			AnchorItemID id.ID `json:"anchor_item_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if _, err := s.controller.RollbackChatForSelection(ctx, s.appSelection(clientID), in.ChatID, in.AnchorItemID); err != nil {
			return nil, err
		}
		return s.stateForClient(ctx, clientID)
	case "fork_chat":
		var in struct {
			ChatID       id.ID  `json:"chat_id"`
			AnchorItemID id.ID  `json:"anchor_item_id"`
			Title        string `json:"title"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		chatRecord, err := s.controller.ForkChatForSelection(ctx, s.appSelection(clientID), in.ChatID, in.AnchorItemID, in.Title)
		if err != nil {
			return nil, err
		}
		selection := s.clientSelection(clientID)
		selection.ChatID = chatRecord.ID
		s.setClientSelection(clientID, selection)
		return s.stateForClient(ctx, clientID)
	case "list_sessions":
		return s.controller.Sessions(ctx)
	case "switch_session":
		var in struct {
			SessionID id.ID `json:"session_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		exists, err := s.sessionExists(ctx, in.SessionID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("session not found: %s", in.SessionID)
		}
		if err := s.controller.EnsureSessionWorkspace(ctx, in.SessionID); err != nil {
			return nil, err
		}
		s.setClientSelection(clientID, clientSelection{SessionID: in.SessionID})
		return s.stateForClient(ctx, clientID)
	case "new_session":
		var in struct {
			Title             string `json:"title"`
			ProjectRoot       string `json:"project_root"`
			CreateProjectRoot bool   `json:"create_project_root"`
		}
		_ = decodeParams(params, &in)
		session, err := s.controller.CreateSession(ctx, in.Title, in.ProjectRoot, in.CreateProjectRoot)
		if err != nil {
			return nil, err
		}
		if err := s.controller.EnsureSessionWorkspace(ctx, session.ID); err != nil {
			return nil, err
		}
		s.setClientSelection(clientID, clientSelection{SessionID: session.ID})
		return s.stateForClient(ctx, clientID)
	case "rename_session":
		var in struct {
			SessionID id.ID  `json:"session_id"`
			Title     string `json:"title"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if err := s.controller.RenameSession(ctx, in.SessionID, in.Title); err != nil {
			return nil, err
		}
		return s.stateForClient(ctx, clientID)
	case "delete_session":
		var in struct {
			SessionID id.ID `json:"session_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		selection := s.clientSelection(clientID)
		if err := s.controller.DeleteSession(ctx, in.SessionID); err != nil {
			return nil, err
		}
		if selection.SessionID == in.SessionID {
			s.clearClientSelection(clientID)
			return s.welcomeState(ctx, "Session deleted."), nil
		}
		return s.stateForClient(ctx, clientID)
	case "browse_project_folder":
		path, err := browseProjectFolder()
		if err != nil {
			return nil, err
		}
		return map[string]string{"project_root": path}, nil
	case "delete_chat":
		var in struct {
			ChatID id.ID `json:"chat_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		selection := s.clientSelection(clientID)
		if err := s.controller.DeleteChatForSelection(ctx, s.appSelection(clientID), in.ChatID); err != nil {
			return nil, err
		}
		if selection.ChatID == in.ChatID {
			selection.ChatID = ""
			s.setClientSelection(clientID, selection)
		}
		return s.stateForClient(ctx, clientID)
	case "reorder_chats":
		var in struct {
			ChatIDs []id.ID `json:"chat_ids"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if err := s.controller.ReorderChatsForSelection(ctx, s.appSelection(clientID), in.ChatIDs); err != nil {
			return nil, err
		}
		return map[string]bool{"reordered": true}, nil
	case "terminate_exec_process":
		var in struct {
			ChatID    id.ID  `json:"chat_id"`
			ProcessID string `json:"process_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		selection := s.appSelection(clientID)
		if in.ChatID != "" {
			selection.ChatID = in.ChatID
		}
		process, err := s.controller.TerminateExecProcessForSelection(ctx, selection, in.ProcessID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"process": process}, nil
	case "approve":
		var in struct {
			ToolCallID string `json:"tool_call_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		in.ToolCallID = strings.TrimSpace(in.ToolCallID)
		if in.ToolCallID == "" {
			return nil, fmt.Errorf("tool_call_id is required")
		}
		return map[string]bool{"accepted": true}, s.controller.ApproveForSelection(ctx, s.appSelection(clientID), in.ToolCallID)
	case "deny":
		var in struct {
			ToolCallID string `json:"tool_call_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		in.ToolCallID = strings.TrimSpace(in.ToolCallID)
		if in.ToolCallID == "" {
			return nil, fmt.Errorf("tool_call_id is required")
		}
		return map[string]bool{"accepted": true}, s.controller.DenyForSelection(ctx, s.appSelection(clientID), in.ToolCallID)
	case "composer_completions":
		var in struct {
			Text      string `json:"text"`
			Cursor    int    `json:"cursor"`
			SessionID id.ID  `json:"session_id"`
			ChatID    id.ID  `json:"chat_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		selection := s.appSelection(clientID)
		if in.SessionID != "" {
			selection.SessionID = in.SessionID
			selection.ChatID = in.ChatID
		}
		return s.controller.CompleteComposerForSelection(ctx, selection, in.Text, in.Cursor)
	case "preferences_state":
		return s.controller.Preferences(ctx)
	case "save_preferences":
		var in app.PreferencesState
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.SavePreferences(ctx, in)
	case "reset_prompt":
		var in struct {
			Target string `json:"target"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.ResetPrompt(in.Target)
	case "list_models":
		options, err := s.controller.ModelOptionsForSelection(ctx, s.appSelection(clientID))
		if err != nil {
			return nil, err
		}
		return map[string]any{"models": options}, nil
	case "tts_speech":
		var in struct {
			Text string              `json:"text"`
			TTS  *app.TTSPreferences `json:"tts"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		speech, err := s.controller.SynthesizeSpeechWithTTS(ctx, in.Text, in.TTS)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"provider_id":  speech.ProviderID,
			"model_id":     speech.ModelID,
			"content_type": speech.ContentType,
			"audio_base64": base64.StdEncoding.EncodeToString(speech.Audio),
		}, nil
	case "set_tts_enabled":
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.SetTTSEnabled(in.Enabled)
	case "model_config":
		var in struct {
			ProviderID string `json:"provider_id"`
			ModelID    string `json:"model_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.ModelConfig(in.ProviderID, in.ModelID), nil
	case "save_model_config":
		var in app.ModelConfigPreference
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.SaveModelConfig(ctx, in)
	case "delete_model_config":
		var in struct {
			ProviderID string `json:"provider_id"`
			ModelID    string `json:"model_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.DeleteModelConfig(ctx, in.ProviderID, in.ModelID)
	case "set_default_model":
		var in struct {
			ProviderID string `json:"provider_id"`
			ModelID    string `json:"model_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.SetDefaultModel(ctx, in.ProviderID, in.ModelID)
	case "set_model":
		var in struct {
			ProviderID string `json:"provider_id"`
			ModelID    string `json:"model_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if err := s.controller.SetModelForSelection(ctx, s.appSelection(clientID), in.ProviderID, in.ModelID); err != nil {
			return nil, err
		}
		return map[string]bool{"updated": true}, nil
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
		var in app.ProviderDraft
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.TestProvider(ctx, in)
	case "save_provider":
		var in app.ProviderDraft
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		providers, err := s.controller.SaveProvider(ctx, in)
		if err != nil {
			return nil, err
		}
		preferences, err := s.controller.Preferences(ctx)
		if err != nil {
			return nil, err
		}
		state, err := s.stateForClient(ctx, clientID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"providers": providers, "preferences": preferences, "state": state}, nil
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
		state, err := s.stateForClient(ctx, clientID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"providers": providers, "state": state}, nil
	case "set_access_settings":
		var in accesssettings.Settings
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		if err := s.controller.SetAccessSettingsForSelection(ctx, s.appSelection(clientID), in); err != nil {
			return nil, err
		}
		return map[string]bool{"updated": true}, nil
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (s *Server) restartAfterShutdown(reason chat.CancelReason, hard bool) {
	started := time.Now()
	slog.Info("restart shutdown starting", "hard", hard, "reason", reason)
	done := make(chan error, 1)
	go func() {
		done <- s.controller.ShutdownWithCancelReason(context.Background(), reason)
	}()
	if hard {
		select {
		case err := <-done:
			if err != nil {
				slog.Error("shutdown for process restart", "error", err, "hard", hard)
				return
			}
			slog.Info("shutdown for process restart complete", "hard", hard, "elapsed_ms", time.Since(started).Milliseconds())
		case <-time.After(750 * time.Millisecond):
			slog.Warn("hard restart proceeding before shutdown completed", "elapsed_ms", time.Since(started).Milliseconds())
		}
	} else {
		if err := <-done; err != nil {
			slog.Error("shutdown for process restart", "error", err, "hard", hard)
			return
		}
		slog.Info("shutdown for process restart complete", "hard", hard, "elapsed_ms", time.Since(started).Milliseconds())
	}
	requestStarted := time.Now()
	if err := s.requestProcessRestart(); err != nil {
		slog.Error("request process restart", "error", err, "hard", hard)
		return
	}
	slog.Info("process restart requested", "hard", hard, "shutdown_elapsed_ms", time.Since(started).Milliseconds(), "request_elapsed_ms", time.Since(requestStarted).Milliseconds())
}

func (s *Server) requestProcessRestart() error {
	if s != nil && s.options.RequestProcessRestart != nil {
		return s.options.RequestProcessRestart()
	}
	time.AfterFunc(100*time.Millisecond, func() {
		process, err := os.FindProcess(os.Getpid())
		if err != nil {
			slog.Error("locate koder process for restart", "error", err)
			return
		}
		if err := process.Signal(syscall.SIGUSR1); err != nil {
			slog.Error("signal koder process for restart", "error", err)
		}
	})
	return nil
}

func (s *Server) stateForClient(ctx context.Context, clientID string) (app.State, error) {
	selection := s.clientSelection(clientID)
	if selection.SessionID == "" {
		return s.welcomeState(ctx, ""), nil
	}
	state, err := s.controller.StateForSelection(ctx, app.Selection{SessionID: selection.SessionID, ChatID: selection.ChatID})
	if err != nil {
		if exists, existsErr := s.sessionExists(ctx, selection.SessionID); existsErr == nil && !exists {
			s.clearClientSelection(clientID)
			return s.welcomeState(ctx, fmt.Sprintf("Session not found: %s", selection.SessionID)), nil
		}
		return app.State{}, err
	}
	s.setClientSelection(clientID, clientSelection{SessionID: state.Session.ID, ChatID: state.ActiveChatID})
	state, err = s.fillActiveTimelineForClient(ctx, state)
	if err != nil {
		return app.State{}, err
	}
	return trimStateTimelines(state, defaultTimelinePageSize), nil
}

func (s *Server) welcomeState(ctx context.Context, message string) app.State {
	sessionState, err := s.controller.Sessions(ctx)
	if err != nil {
		message = strings.TrimSpace(strings.Join([]string{message, err.Error()}, "\n"))
	}
	state := s.controller.State()
	return app.State{
		Sessions:      sessionState.Sessions,
		Theme:         state.Theme,
		TTS:           state.TTS,
		ProjectRoot:   firstNonEmpty(sessionState.ProjectRoot, state.ProjectRoot),
		Build:         state.Build,
		RestartNeeded: state.RestartNeeded,
		RestartBuild:  state.RestartBuild,
		Error:         strings.TrimSpace(message),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func trimStateTimelines(state app.State, limit int) app.State {
	if limit <= 0 {
		return state
	}
	if len(state.Snapshots) > 0 {
		snapshots := make(map[id.ID]chat.Snapshot, len(state.Snapshots))
		for chatID, snapshot := range state.Snapshots {
			snapshots[chatID] = trimSnapshotTimeline(snapshot, limit)
		}
		state.Snapshots = snapshots
	}
	if state.Snapshot.Chat.ID != "" || len(state.Snapshot.Timeline) > 0 {
		state.Snapshot = trimSnapshotTimeline(state.Snapshot, limit)
	}
	if state.ActiveChatID != "" {
		if snapshot, ok := state.Snapshots[state.ActiveChatID]; ok {
			state.Snapshot = snapshot
		}
	}
	return state
}

func (s *Server) fillActiveTimelineForClient(ctx context.Context, state app.State) (app.State, error) {
	chatID := state.ActiveChatID
	if chatID == "" {
		return state, nil
	}
	snapshot, ok := state.Snapshots[chatID]
	if !ok {
		return state, nil
	}
	if len(snapshot.Timeline) > 0 || snapshot.TimelineLoadedAll {
		return state, nil
	}
	page, err := s.controller.TimelinePage(ctx, state.Session.ID, chatID, "", defaultTimelinePageSize, false)
	if err != nil {
		return app.State{}, err
	}
	snapshot.Timeline = page.Items
	snapshot.TimelineHasMore = page.HasMore
	snapshot.TimelineLoadedAll = page.LoadedAll
	snapshot.TimelineBefore = page.Before
	state.Snapshots[chatID] = snapshot
	state.Snapshot = snapshot
	return state, nil
}

func trimSnapshotTimeline(snapshot chat.Snapshot, limit int) chat.Snapshot {
	total := len(snapshot.Timeline)
	if total == 0 {
		snapshot.TimelineHasMore = snapshot.Chat.ID != "" && !snapshot.TimelineLoadedAll
		snapshot.TimelineBefore = ""
		return snapshot
	}
	if limit <= 0 || total <= limit {
		snapshot.Timeline = slices.Clone(snapshot.Timeline)
		if snapshot.TimelineHasMore {
			snapshot.TimelineLoadedAll = false
		} else {
			snapshot.TimelineLoadedAll = true
		}
		snapshot.TimelineBefore = snapshot.Timeline[0].ID
		return snapshot
	}
	start := total - limit
	snapshot.Timeline = slices.Clone(snapshot.Timeline[start:])
	snapshot.TimelineHasMore = true
	snapshot.TimelineLoadedAll = false
	snapshot.TimelineBefore = snapshot.Timeline[0].ID
	return snapshot
}

func (s *Server) sessionExists(ctx context.Context, sessionID id.ID) (bool, error) {
	state, err := s.controller.Sessions(ctx)
	if err != nil {
		return false, err
	}
	for _, session := range state.Sessions {
		if session.ID == sessionID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) updateClientSelectionFromResult(clientID string, result any) {
	switch value := result.(type) {
	case app.State:
		s.setClientSelection(clientID, clientSelection{SessionID: value.Session.ID, ChatID: value.ActiveChatID})
	case rpcHello:
		if state, ok := value.State.(app.State); ok {
			s.setClientSelection(clientID, clientSelection{SessionID: state.Session.ID, ChatID: state.ActiveChatID})
		}
	}
}

func (s *Server) setClientSelection(clientID string, selection clientSelection) {
	if clientID == "" {
		return
	}
	if selection.SessionID == "" && selection.ChatID == "" {
		return
	}
	s.clientSelectionMu.Lock()
	s.clientSelections[clientID] = selection
	s.clientSelectionMu.Unlock()
}

func (s *Server) clearClientSelection(clientID string) {
	if clientID == "" {
		return
	}
	s.clientSelectionMu.Lock()
	delete(s.clientSelections, clientID)
	s.clientSelectionMu.Unlock()
}

func (s *Server) clientSelection(clientID string) clientSelection {
	s.clientSelectionMu.Lock()
	defer s.clientSelectionMu.Unlock()
	return s.clientSelections[clientID]
}

func (s *Server) appSelection(clientID string) app.Selection {
	selection := s.clientSelection(clientID)
	return app.Selection{SessionID: selection.SessionID, ChatID: selection.ChatID}
}

func (s *Server) deleteClientSelection(clientID string) {
	s.clientSelectionMu.Lock()
	delete(s.clientSelections, clientID)
	s.clientSelectionMu.Unlock()
}

type stateDelta struct {
	Session       any    `json:"session,omitempty"`
	Sessions      any    `json:"sessions,omitempty"`
	Chats         any    `json:"chats,omitempty"`
	ChatStatuses  any    `json:"chat_statuses,omitempty"`
	ActiveChatID  id.ID  `json:"active_chat_id,omitempty"`
	Access        any    `json:"access,omitempty"`
	Milestones    any    `json:"milestones,omitempty"`
	Tasks         any    `json:"tasks,omitempty"`
	TasksByKey    any    `json:"tasks_by_milestone,omitempty"`
	Workspace     any    `json:"workspace_status,omitempty"`
	ContextWindow int    `json:"context_window,omitempty"`
	ModelInfo     any    `json:"model_info,omitempty"`
	Theme         string `json:"theme,omitempty"`
	ProjectRoot   string `json:"project_root,omitempty"`
	Build         any    `json:"build,omitempty"`
	RestartNeeded bool   `json:"restart_needed,omitempty"`
	RestartBuild  any    `json:"restart_build,omitempty"`
	Error         string `json:"error,omitempty"`
}

type chatDelta struct {
	ChatID            id.ID                 `json:"chat_id"`
	Chat              any                   `json:"chat,omitempty"`
	Item              *domain.TimelineItem  `json:"item,omitempty"`
	Timeline          []domain.TimelineItem `json:"timeline,omitempty"`
	Approvals         any                   `json:"approvals,omitempty"`
	Queue             any                   `json:"queue,omitempty"`
	ExecProcesses     any                   `json:"exec_processes,omitempty"`
	Context           any                   `json:"context,omitempty"`
	TokenUsage        any                   `json:"token_usage,omitempty"`
	Status            string                `json:"status,omitempty"`
	StatusText        string                `json:"status_text,omitempty"`
	Active            bool                  `json:"active"`
	TranscriptChanged bool                  `json:"transcript_changed,omitempty"`
	ReplaceTimeline   bool                  `json:"replace_timeline,omitempty"`
	QueueChanged      bool                  `json:"queue_changed,omitempty"`
	StatusChanged     bool                  `json:"status_changed,omitempty"`
	ContextChanged    bool                  `json:"context_changed,omitempty"`
	ApprovalsChanged  bool                  `json:"approvals_changed,omitempty"`
	Error             string                `json:"error,omitempty"`
}

func webEventFromControllerEvent(event app.Event) (app.Event, bool) {
	switch event.Type {
	case "chat_delta":
		update, ok := event.Payload.(chat.Update)
		if !ok {
			return app.Event{}, false
		}
		return app.Event{Seq: event.Seq, Type: "chat_delta", Payload: chatDeltaFromUpdate(update)}, true
	case "snapshot":
		state, ok := event.Payload.(app.State)
		if !ok {
			return app.Event{}, false
		}
		return app.Event{Seq: event.Seq, Type: "state_delta", Payload: stateDeltaFromState(state)}, true
	case "selection_delta":
		return app.Event{}, false
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
		TokenUsage:        snapshot.TokenUsage,
		Status:            string(snapshot.Status),
		StatusText:        snapshot.StatusText,
		Active:            snapshot.Active,
		TranscriptChanged: update.TranscriptChanged,
		ReplaceTimeline:   update.ReplaceTimeline,
		QueueChanged:      update.QueueChanged,
		StatusChanged:     update.StatusChanged,
		ContextChanged:    update.ContextChanged,
		ApprovalsChanged:  update.ApprovalsChanged,
	}
	if snapshot.ExecProcesses != nil {
		delta.ExecProcesses = snapshot.ExecProcesses
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
	if update.ReplaceTimeline {
		delta.Timeline = snapshot.Timeline
	} else if item, ok := changedTimelineItem(update); ok {
		delta.Item = &item
	}
	return delta
}

func changedTimelineItem(update chat.Update) (domain.TimelineItem, bool) {
	if update.Item.ID != "" {
		return update.Item, true
	}
	if update.Event != nil && update.Event.Item.ID != "" {
		if item, ok := snapshotTimelineItem(update.Snapshot.Timeline, update.Event.Item.ID); ok {
			return item, true
		}
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

func snapshotTimelineItem(timeline []domain.TimelineItem, id id.ID) (domain.TimelineItem, bool) {
	for idx := len(timeline) - 1; idx >= 0; idx-- {
		if timeline[idx].ID == id {
			return timeline[idx], true
		}
	}
	return domain.TimelineItem{}, false
}

func stateDeltaFromState(state app.State) stateDelta {
	return stateDelta{
		Session:       state.Session,
		Sessions:      state.Sessions,
		Chats:         state.Chats,
		ChatStatuses:  state.ChatStatuses,
		ActiveChatID:  state.ActiveChatID,
		Access:        state.Access,
		Milestones:    state.Milestones,
		Tasks:         state.Tasks,
		TasksByKey:    state.TasksByKey,
		Workspace:     state.Workspace,
		ContextWindow: state.ContextWindow,
		ModelInfo:     state.ModelInfo,
		Theme:         state.Theme,
		ProjectRoot:   state.ProjectRoot,
		Build:         state.Build,
		RestartNeeded: state.RestartNeeded,
		RestartBuild:  state.RestartBuild,
		Error:         state.Error,
	}
}

func browseProjectFolder() (string, error) {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, []string{"osascript", "-e", `POSIX path of (choose folder with prompt "Choose project folder")`})
	case "windows":
		candidates = append(candidates, []string{"powershell", "-NoProfile", "-Command", `Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.FolderBrowserDialog; if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { $d.SelectedPath }`})
	default:
		candidates = append(candidates,
			[]string{"zenity", "--file-selection", "--directory", "--title=Choose project folder"},
			[]string{"kdialog", "--getexistingdirectory", ".", "Choose project folder"},
		)
	}
	for _, args := range candidates {
		out, err := exec.Command(args[0], args[1:]...).Output()
		path := strings.TrimSpace(string(out))
		if err == nil && path != "" {
			return path, nil
		}
	}
	return "", fmt.Errorf("no supported folder picker is available")
}

func rpcEstablishesSnapshotBaseline(method string, result any) bool {
	method = strings.TrimSpace(method)
	if method == "hello" || method == "get_state" {
		return true
	}
	if _, ok := result.(app.State); ok {
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

func websocketUsesGlobalControllerEvent(typ string) bool {
	switch typ {
	case "chat_delta", "chats_delta", "planning_delta", "tasks_delta", "session_delta", "selection_delta", "workspace_delta", "snapshot":
		return false
	case "tts":
		return true
	default:
		return true
	}
}

func (s *Server) updateDebugChats() {
	if s == nil || s.debug == nil || s.controller == nil {
		return
	}
	s.debug.UpdateChats(chatDebugFromState(s.controller.State()))
}

func chatDebugFromState(state app.State) []debugsrv.ChatDebug {
	statuses := make(map[id.ID]app.ChatSidebarStatus, len(state.ChatStatuses))
	for _, status := range state.ChatStatuses {
		statuses[status.ChatID] = status
	}
	out := make([]debugsrv.ChatDebug, 0, len(state.Chats))
	for _, item := range state.Chats {
		if item.ID == "" {
			continue
		}
		snapshot := state.Snapshots[item.ID]
		status := statuses[item.ID]
		value := status.Status
		if value == "" {
			value = string(snapshot.Status)
		}
		text := status.StatusText
		if text == "" {
			text = snapshot.StatusText
		}
		queue := snapshot.QueuedInputs
		if queue == nil {
			queue = item.QueuedInputs
		}
		out = append(out, debugsrv.ChatDebug{
			ID:                        item.ID,
			SessionID:                 item.SessionID,
			Title:                     item.Title,
			Status:                    value,
			StatusText:                text,
			Active:                    snapshot.Active,
			Busy:                      status.Busy || snapshot.Active,
			QueueLen:                  len(queue),
			PendingAssistantText:      len(snapshot.PendingAssistant.Text),
			PendingAssistantReasoning: len(snapshot.PendingAssistant.Reasoning),
			PendingApprovals:          len(snapshot.Approvals),
			RunningToolCalls:          runningToolCalls(snapshot.Timeline),
		})
	}
	return out
}

func runningToolCalls(timeline []domain.TimelineItem) int {
	var count int
	for _, item := range timeline {
		message, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, tool := range message.Tools {
			if tool.Status == domain.ToolStatusRunning {
				count++
			}
		}
	}
	return count
}

type rpcHello struct {
	AssetHash string `json:"asset_hash"`
	ClientID  string `json:"client_id"`
	State     any    `json:"state"`
}

type rpcRequest struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type httpRPCRequest struct {
	rpcRequest
	ClientID        string `json:"client_id"`
	SelectedSession id.ID  `json:"selected_session"`
	SelectedChat    id.ID  `json:"selected_chat"`
}

type rpcResponse struct {
	ID     any    `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

const (
	websocketHeartbeatInterval = 15 * time.Second
	websocketWriteTimeout      = 5 * time.Second
)

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func decodeHTTPRPCRequest(r *http.Request) (httpRPCRequest, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return httpRPCRequest{}, fmt.Errorf("read rpc request: %w", err)
	}
	method := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/rpc"), "/")
	if method != "" {
		return httpRPCRequest{
			rpcRequest: rpcRequest{Method: method, Params: paramsBody(body)},
			ClientID:   strings.TrimSpace(r.URL.Query().Get("client_id")),
			SelectedSession: id.ID(strings.TrimSpace(firstNonEmpty(
				r.URL.Query().Get("selected_session"),
				r.URL.Query().Get("session_id"),
				r.URL.Query().Get("session"),
			))),
			SelectedChat: id.ID(strings.TrimSpace(firstNonEmpty(
				r.URL.Query().Get("selected_chat"),
				r.URL.Query().Get("chat_id"),
				r.URL.Query().Get("chat"),
			))),
		}, nil
	}
	var req httpRPCRequest
	if len(bytesTrimSpace(body)) == 0 {
		return httpRPCRequest{}, fmt.Errorf("rpc request body is required")
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return httpRPCRequest{}, fmt.Errorf("decode rpc request: %w", err)
	}
	req.Method = strings.TrimSpace(req.Method)
	if req.Method == "" {
		return httpRPCRequest{}, fmt.Errorf("rpc method is required")
	}
	if len(req.Params) == 0 {
		req.Params = json.RawMessage(`{}`)
	}
	return req, nil
}

func paramsBody(body []byte) json.RawMessage {
	if len(bytesTrimSpace(body)) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(body)
}

func bytesTrimSpace(value []byte) []byte {
	return []byte(strings.TrimSpace(string(value)))
}

func writeHTTPRPCResponse(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	if !resp.OK {
		w.WriteHeader(http.StatusBadRequest)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func writeJSON(ctx context.Context, conn *websocket.Conn, mu *sync.Mutex, value any) (int, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	writeCtx, cancel := context.WithTimeout(ctx, websocketWriteTimeout)
	defer cancel()
	mu.Lock()
	defer mu.Unlock()
	if err := conn.Write(writeCtx, websocket.MessageText, data); err != nil {
		return 0, err
	}
	return len(data), nil
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
