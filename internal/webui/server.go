package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/uicore"
)

const defaultOpenDelay = 5 * time.Second

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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
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
	go func() {
		defer close(done)
		for event := range events {
			if err := writeJSON(ctx, conn, &writeMu, event); err != nil {
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
		select {
		case <-done:
			return
		default:
		}
	}
}

func (s *Server) handleRPC(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch strings.TrimSpace(method) {
	case "hello", "get_state":
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
	case "switch_chat":
		var in struct {
			ChatID int64 `json:"chat_id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return s.controller.State(), s.controller.SwitchChat(ctx, in.ChatID)
	case "new_chat":
		var in struct {
			Title string `json:"title"`
		}
		_ = decodeParams(params, &in)
		return s.controller.State(), s.controller.NewChat(ctx, in.Title)
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
			ID int64 `json:"id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"accepted": true}, s.controller.Approve(in.ID)
	case "deny":
		var in struct {
			ID int64 `json:"id"`
		}
		if err := decodeParams(params, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"accepted": true}, s.controller.Deny(in.ID)
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
		return s.controller.State(), s.controller.SetPermissionProfile(ctx, in.Profile)
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
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
