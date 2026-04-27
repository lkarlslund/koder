package debugsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/version"
)

const (
	EnvDebugAPI    = "KODER_DEBUG_API"
	defaultMaxLogs = 256
	defaultMaxHTTP = 96
)

type RecordedEvent struct {
	Timestamp time.Time         `json:"timestamp"`
	SessionID int64             `json:"session_id"`
	Source    string            `json:"source"`
	Kind      string            `json:"kind"`
	Text      string            `json:"text,omitempty"`
	Tool      domain.ToolKind   `json:"tool,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
	Error     string            `json:"error,omitempty"`
	RawJSON   string            `json:"raw_json,omitempty"`
	Usage     *domain.Usage     `json:"usage,omitempty"`
}

type HTTPTrace struct {
	Timestamp    time.Time         `json:"timestamp"`
	ProviderID   string            `json:"provider_id"`
	Method       string            `json:"method"`
	Path         string            `json:"path"`
	Status       int               `json:"status"`
	DurationMS   int64             `json:"duration_ms"`
	RequestBody  string            `json:"request_body,omitempty"`
	ResponseBody string            `json:"response_body,omitempty"`
	RequestHdrs  map[string]string `json:"request_headers,omitempty"`
	ResponseHdrs map[string]string `json:"response_headers,omitempty"`
	Meta         map[string]string `json:"meta,omitempty"`
	Error        string            `json:"error,omitempty"`
}

type RuntimeSnapshot struct {
	Timestamp          time.Time           `json:"timestamp"`
	DebugAPI           string              `json:"debug_api"`
	DeepDebug          bool                `json:"deep_debug"`
	Build              version.Info        `json:"build"`
	CurrentSession     int64               `json:"current_session"`
	SessionTitle       string              `json:"session_title"`
	ProviderID         string              `json:"provider_id"`
	ModelID            string              `json:"model_id"`
	Status             string              `json:"status"`
	Busy               bool                `json:"busy"`
	BusyStatus         string              `json:"busy_status,omitempty"`
	OpenDialog         string              `json:"open_dialog,omitempty"`
	ShowSidebar        bool                `json:"show_sidebar"`
	ShowReasoning      bool                `json:"show_reasoning"`
	ShowSystem         bool                `json:"show_system"`
	LastError          string              `json:"last_error,omitempty"`
	ViewportWidth      int                 `json:"viewport_width"`
	ViewportHeight     int                 `json:"viewport_height"`
	ViewportYOffset    int                 `json:"viewport_y_offset"`
	MessageCount       int                 `json:"message_count"`
	RenderBlockCount   int                 `json:"render_block_count"`
	ViewportPreview    string              `json:"viewport_preview,omitempty"`
	ViewportContentLen int                 `json:"viewport_content_len"`
	FrameLines         []string            `json:"frame_lines,omitempty"`
	TranscriptControls []ControlRef        `json:"transcript_controls,omitempty"`
	TranscriptItems    []TranscriptItemRef `json:"transcript_items,omitempty"`
}

type ControlRef struct {
	ID      string `json:"id"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Enabled bool   `json:"enabled"`
}

type TranscriptItemRef struct {
	Index     int             `json:"index"`
	Key       string          `json:"key"`
	Kind      string          `json:"kind"`
	GapBefore int             `json:"gap_before"`
	Height    int             `json:"height"`
	BlankRows int             `json:"blank_rows"`
	MessageID int64           `json:"message_id,omitempty"`
	Role      string          `json:"role,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Tool      domain.ToolKind `json:"tool,omitempty"`
	ToolRunID string          `json:"tool_run_id,omitempty"`
	Title     string          `json:"title,omitempty"`
	ControlID string          `json:"control_id,omitempty"`
}

type InputRequest struct {
	Mouse *MouseInput `json:"mouse,omitempty"`
	Key   *KeyInput   `json:"key,omitempty"`
}

type MouseInput struct {
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Button string `json:"button"`
	Action string `json:"action"`
	Alt    bool   `json:"alt,omitempty"`
}

type KeyInput struct {
	Type  string `json:"type"`
	Runes string `json:"runes,omitempty"`
	Alt   bool   `json:"alt,omitempty"`
}

type Recorder struct {
	mu            sync.RWMutex
	debugAPI      string
	deepDebug     bool
	maxEvents     int
	maxHTTP       int
	runtime       RuntimeSnapshot
	events        []RecordedEvent
	sessionEvents map[int64][]RecordedEvent
	httpTraces    []HTTPTrace
}

func NewRecorder() *Recorder {
	return &Recorder{
		maxEvents:     defaultMaxLogs,
		maxHTTP:       defaultMaxHTTP,
		sessionEvents: map[int64][]RecordedEvent{},
	}
}

func (r *Recorder) Enabled() bool {
	return r != nil
}

func (r *Recorder) SetDebugAPI(addr string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.debugAPI = strings.TrimSpace(addr)
	r.runtime.DebugAPI = r.debugAPI
}

func (r *Recorder) SetDeepDebug(enabled bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deepDebug = enabled
	r.runtime.DeepDebug = enabled
}

func (r *Recorder) DeepDebug() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.deepDebug
}

func (r *Recorder) RecordEvent(sessionID int64, evt domain.Event) {
	if r == nil {
		return
	}
	entry := RecordedEvent{
		Timestamp: time.Now().UTC(),
		SessionID: sessionID,
		Source:    "event",
		Kind:      string(evt.Kind),
		Text:      truncate(evt.Text, 4096),
		Tool:      evt.Tool,
		Meta:      cloneMeta(evt.Meta),
		RawJSON:   truncate(evt.RawJSON, 4096),
	}
	if evt.Err != nil {
		entry.Error = evt.Err.Error()
	}
	if evt.Usage.TotalTokens > 0 {
		usage := evt.Usage
		entry.Usage = &usage
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = appendRecordedEvent(r.events, entry, r.maxEvents)
	if sessionID > 0 {
		r.sessionEvents[sessionID] = appendRecordedEvent(r.sessionEvents[sessionID], entry, r.maxEvents)
	}
	if entry.Error != "" {
		r.runtime.LastError = entry.Error
	}
}

func (r *Recorder) RecordLifecycle(sessionID int64, kind, text string, meta map[string]string) {
	if r == nil {
		return
	}
	entry := RecordedEvent{
		Timestamp: time.Now().UTC(),
		SessionID: sessionID,
		Source:    "lifecycle",
		Kind:      strings.TrimSpace(kind),
		Text:      truncate(strings.TrimSpace(text), 4096),
		Meta:      cloneMeta(meta),
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = appendRecordedEvent(r.events, entry, r.maxEvents)
	if sessionID > 0 {
		r.sessionEvents[sessionID] = appendRecordedEvent(r.sessionEvents[sessionID], entry, r.maxEvents)
	}
}

func (r *Recorder) RecordHTTP(trace HTTPTrace) {
	if r == nil {
		return
	}
	trace.Timestamp = time.Now().UTC()
	trace.RequestBody = truncate(trace.RequestBody, 8192)
	trace.ResponseBody = truncate(trace.ResponseBody, 8192)
	trace.RequestHdrs = cloneMeta(trace.RequestHdrs)
	trace.ResponseHdrs = cloneMeta(trace.ResponseHdrs)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.httpTraces = appendHTTPTrace(r.httpTraces, trace, r.maxHTTP)
}

func (r *Recorder) UpdateRuntime(snapshot RuntimeSnapshot) {
	if r == nil {
		return
	}
	snapshot.Timestamp = time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	if snapshot.DebugAPI == "" {
		snapshot.DebugAPI = r.debugAPI
	}
	snapshot.DeepDebug = r.deepDebug
	if snapshot.LastError == "" {
		snapshot.LastError = r.runtime.LastError
	}
	if snapshot.Build.Name == "" {
		snapshot.Build = version.Current()
	}
	r.runtime = snapshot
}

func (r *Recorder) Runtime() RuntimeSnapshot {
	if r == nil {
		return RuntimeSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runtime
}

func (r *Recorder) Events(sessionID int64) []RecordedEvent {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sessionID > 0 {
		return cloneEvents(r.sessionEvents[sessionID])
	}
	return cloneEvents(r.events)
}

func (r *Recorder) HTTPTraces() []HTTPTrace {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneHTTPTraces(r.httpTraces)
}

type Server struct {
	store    *store.Store
	recorder *Recorder
	server   *http.Server
	listener net.Listener
	mu       sync.RWMutex
	input    func(ui.Msg)
}

func Start(bind string, st *store.Store, recorder *Recorder) (*Server, error) {
	if strings.TrimSpace(bind) == "" {
		return nil, fmt.Errorf("%s is empty", EnvDebugAPI)
	}
	if recorder == nil {
		recorder = NewRecorder()
	}
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, fmt.Errorf("listen debug api: %w", err)
	}
	s := &Server{
		store:    st,
		recorder: recorder,
		listener: ln,
	}
	s.recorder.SetDebugAPI(ln.Addr().String())
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/health", s.handleHealth)
	mux.HandleFunc("/debug/runtime", s.handleRuntime)
	mux.HandleFunc("/debug/http", s.handleHTTP)
	mux.HandleFunc("/debug/events", s.handleGlobalEvents)
	mux.HandleFunc("/debug/sessions", s.handleSessions)
	mux.HandleFunc("/debug/sessions/", s.handleSessionRoutes)
	mux.HandleFunc("/debug/input", s.handleInput)
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = s.server.Serve(ln)
	}()
	return s, nil
}

func (s *Server) Addr() string {
	if s == nil || s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) Recorder() *Recorder {
	if s == nil {
		return nil
	}
	return s.recorder
}

func (s *Server) SetInputSink(fn func(ui.Msg)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.input = fn
}

func (s *Server) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"debug": s.Addr(),
	})
}

func (s *Server) handleRuntime(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.recorder.Runtime())
	case http.MethodPost:
		var req struct {
			DeepDebug bool `json:"deep_debug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		s.recorder.SetDeepDebug(req.DeepDebug)
		writeJSON(w, http.StatusOK, s.recorder.Runtime())
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
	}
}

func (s *Server) handleHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"debug_api": s.Addr(),
		"traces":    s.recorder.HTTPTraces(),
	})
}

func (s *Server) handleInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	s.mu.RLock()
	input := s.input
	s.mu.RUnlock()
	if input == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("live input is unavailable"))
		return
	}
	var req InputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	msg, err := decodeInputRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	input(msg)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"runtime": s.recorder.Runtime(),
	})
}

func decodeInputRequest(req InputRequest) (ui.Msg, error) {
	if req.Mouse != nil {
		return ui.MouseMsg{
			X:      req.Mouse.X,
			Y:      req.Mouse.Y,
			Button: parseMouseButton(req.Mouse.Button),
			Action: parseMouseAction(req.Mouse.Action),
			Alt:    req.Mouse.Alt,
		}, nil
	}
	if req.Key != nil {
		return ui.KeyMsg{
			Type:  parseKeyType(req.Key.Type),
			Runes: []rune(req.Key.Runes),
			Alt:   req.Key.Alt,
		}, nil
	}
	return nil, fmt.Errorf("missing input payload")
}

func parseMouseButton(value string) ui.MouseButton {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "left":
		return ui.MouseButtonLeft
	case "middle":
		return ui.MouseButtonMiddle
	case "right":
		return ui.MouseButtonRight
	case "wheel_up":
		return ui.MouseButtonWheelUp
	case "wheel_down":
		return ui.MouseButtonWheelDown
	default:
		return ui.MouseButtonNone
	}
}

func parseMouseAction(value string) ui.MouseAction {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "release":
		return ui.MouseActionRelease
	case "motion":
		return ui.MouseActionMotion
	default:
		return ui.MouseActionPress
	}
}

func parseKeyType(value string) ui.KeyType {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "enter":
		return ui.KeyEnter
	case "tab":
		return ui.KeyTab
	case "shift+tab":
		return ui.KeyShiftTab
	case "esc":
		return ui.KeyEsc
	case "up":
		return ui.KeyUp
	case "down":
		return ui.KeyDown
	case "left":
		return ui.KeyLeft
	case "right":
		return ui.KeyRight
	case "ctrl+c":
		return ui.KeyCtrlC
	case "ctrl+v":
		return ui.KeyCtrlV
	case "ctrl+y":
		return ui.KeyCtrlY
	case "space":
		return ui.KeySpace
	case "runes":
		return ui.KeyRunes
	default:
		return ui.KeyUnknown
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/debug/sessions" {
		http.NotFound(w, r)
		return
	}
	sessions, err := s.store.ListSessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime":  s.recorder.Runtime(),
		"sessions": sessions,
	})
}

func (s *Server) handleSessionRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/debug/sessions/")
	path = strings.Trim(path, "/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(path, "/")
	sessionID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || sessionID <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid session id"))
		return
	}
	if len(parts) == 1 {
		s.handleSession(w, r, sessionID)
		return
	}
	switch parts[1] {
	case "transcript":
		s.handleTranscript(w, r, sessionID)
	case "events":
		s.handleEvents(w, r, sessionID)
	case "approvals":
		s.handleApprovals(w, r, sessionID)
	case "milestones":
		s.handleMilestones(w, r, sessionID)
	case "tasks":
		s.handleTasks(w, r, sessionID)
	case "todos":
		s.handleTodos(w, r, sessionID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, sessionID int64) {
	session, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	messages, parts, err := s.store.PartsForSession(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	approvals, err := s.store.PendingApprovals(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	plan, err := s.store.GetMilestonePlan(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var todos []store.TodoItem
	for _, milestone := range plan.Milestones {
		items, err := s.store.ListTodos(r.Context(), sessionID, milestone.Ref)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		todos = append(todos, items...)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session":        session,
		"messages":       messages,
		"parts":          parts,
		"approvals":      approvals,
		"milestone_plan": plan,
		"todos":          todos,
		"events":         s.recorder.Events(sessionID),
	})
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request, sessionID int64) {
	messages, parts, err := s.store.PartsForSession(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	type transcriptMessage struct {
		Message domain.Message `json:"message"`
		Parts   []domain.Part  `json:"parts"`
	}
	out := make([]transcriptMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, transcriptMessage{Message: msg, Parts: parts[msg.ID]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"messages":   out,
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, _ *http.Request, sessionID int64) {
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"events":     s.recorder.Events(sessionID),
	})
}

func (s *Server) handleGlobalEvents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": 0,
		"events":     s.recorder.Events(0),
	})
}

func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request, sessionID int64) {
	approvals, err := s.store.PendingApprovals(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"approvals":  approvals,
	})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request, sessionID int64) {
	tasks, err := s.store.ListTasks(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"tasks":      tasks,
	})
}

func (s *Server) handleMilestones(w http.ResponseWriter, r *http.Request, sessionID int64) {
	plan, err := s.store.GetMilestonePlan(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"plan":       plan,
	})
}

func (s *Server) handleTodos(w http.ResponseWriter, r *http.Request, sessionID int64) {
	plan, err := s.store.GetMilestonePlan(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var todos []store.TodoItem
	for _, milestone := range plan.Milestones {
		items, err := s.store.ListTodos(r.Context(), sessionID, milestone.Ref)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		todos = append(todos, items...)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"todos":      todos,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func appendRecordedEvent(items []RecordedEvent, item RecordedEvent, limit int) []RecordedEvent {
	items = append(items, item)
	if len(items) <= limit {
		return items
	}
	trim := len(items) - limit
	out := make([]RecordedEvent, limit)
	copy(out, items[trim:])
	return out
}

func appendHTTPTrace(items []HTTPTrace, item HTTPTrace, limit int) []HTTPTrace {
	items = append(items, item)
	if len(items) <= limit {
		return items
	}
	trim := len(items) - limit
	out := make([]HTTPTrace, limit)
	copy(out, items[trim:])
	return out
}

func cloneMeta(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneEvents(src []RecordedEvent) []RecordedEvent {
	if len(src) == 0 {
		return nil
	}
	dst := make([]RecordedEvent, len(src))
	copy(dst, src)
	return dst
}

func cloneHTTPTraces(src []HTTPTrace) []HTTPTrace {
	if len(src) == 0 {
		return nil
	}
	dst := make([]HTTPTrace, len(src))
	for i, item := range src {
		dst[i] = item
		dst[i].RequestHdrs = cloneMeta(item.RequestHdrs)
		dst[i].ResponseHdrs = cloneMeta(item.ResponseHdrs)
		dst[i].Meta = cloneMeta(item.Meta)
	}
	return dst
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max-1] + "…"
}
