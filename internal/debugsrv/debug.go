package debugsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/version"
)

const (
	EnvDebugAPI    = "KODER_DEBUG_API"
	defaultMaxLogs = 256
	defaultMaxHTTP = 96
)

type RecordedEvent struct {
	Timestamp time.Time         `json:"timestamp"`
	SessionID domain.ID         `json:"session_id"`
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
	Timestamp               time.Time           `json:"timestamp"`
	DebugAPI                string              `json:"debug_api"`
	DeepDebug               bool                `json:"deep_debug"`
	Build                   version.Info        `json:"build"`
	CurrentSession          domain.ID           `json:"current_session"`
	CurrentChat             domain.ID           `json:"current_chat"`
	SessionTitle            string              `json:"session_title"`
	ProviderID              string              `json:"provider_id"`
	ModelID                 string              `json:"model_id"`
	Status                  string              `json:"status"`
	Busy                    bool                `json:"busy"`
	BusyStatus              string              `json:"busy_status,omitempty"`
	Loading                 bool                `json:"loading"`
	ActiveEventStream       bool                `json:"active_event_stream"`
	RuntimeAttached         bool                `json:"runtime_attached"`
	RuntimeSubscribed       bool                `json:"runtime_subscribed"`
	RuntimeStatus           string              `json:"runtime_status,omitempty"`
	RuntimeStatusText       string              `json:"runtime_status_text,omitempty"`
	RuntimeActive           bool                `json:"runtime_active"`
	RuntimeQueueLen         int                 `json:"runtime_queue_len"`
	RuntimePendingText      int                 `json:"runtime_pending_text_len"`
	RuntimePendingReasoning int                 `json:"runtime_pending_reasoning_len"`
	TranscriptBusy          bool                `json:"transcript_busy"`
	SidebarBusy             bool                `json:"sidebar_busy"`
	BusyScope               string              `json:"busy_scope,omitempty"`
	CanInterrupt            bool                `json:"can_interrupt"`
	HasActiveCancel         bool                `json:"has_active_cancel"`
	HasChatCancel           bool                `json:"has_chat_cancel"`
	QueueEditMode           bool                `json:"queue_edit_mode"`
	FocusedWindow           string              `json:"focused_window,omitempty"`
	ComposerFocused         bool                `json:"composer_focused"`
	InterruptKeyTarget      bool                `json:"interrupt_key_target"`
	OpenDialog              string              `json:"open_dialog,omitempty"`
	ShowSidebar             bool                `json:"show_sidebar"`
	ShowReasoning           bool                `json:"show_reasoning"`
	ShowSystem              bool                `json:"show_system"`
	LastError               string              `json:"last_error,omitempty"`
	ViewportWidth           int                 `json:"viewport_width"`
	ViewportHeight          int                 `json:"viewport_height"`
	ViewportYOffset         int                 `json:"viewport_y_offset"`
	MessageCount            int                 `json:"message_count"`
	RenderBlockCount        int                 `json:"render_block_count"`
	ViewportPreview         string              `json:"viewport_preview,omitempty"`
	ViewportContentLen      int                 `json:"viewport_content_len"`
	FrameLines              []string            `json:"frame_lines,omitempty"`
	TranscriptControls      []ControlRef        `json:"transcript_controls,omitempty"`
	TranscriptItems         []TranscriptItemRef `json:"transcript_items,omitempty"`
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
	MessageID string          `json:"message_id,omitempty"`
	Role      string          `json:"role,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Tool      domain.ToolKind `json:"tool,omitempty"`
	ToolRunID string          `json:"tool_run_id,omitempty"`
	Title     string          `json:"title,omitempty"`
	ControlID string          `json:"control_id,omitempty"`
}

type SessionAnalysis struct {
	SessionID       domain.ID               `json:"session_id"`
	ContinueCount   int                     `json:"continue_count"`
	Continues       []SessionContinueRecord `json:"continues,omitempty"`
	BadStopCount    int                     `json:"bad_stop_count"`
	BadStops        []SessionBadStopRecord  `json:"bad_stops,omitempty"`
	TranscriptCount int                     `json:"transcript_count"`
}

type SessionContinueRecord struct {
	Timestamp time.Time         `json:"timestamp"`
	Kind      string            `json:"kind"`
	Text      string            `json:"text,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

type SessionBadStopRecord struct {
	MessageID        string    `json:"message_id"`
	ChatID           domain.ID `json:"chat_id"`
	CreatedAt        time.Time `json:"created_at"`
	Summary          string    `json:"summary,omitempty"`
	Text             string    `json:"text,omitempty"`
	NextMessageID    string    `json:"next_message_id,omitempty"`
	NextRole         string    `json:"next_role,omitempty"`
	NextKind         string    `json:"next_kind,omitempty"`
	NextTool         string    `json:"next_tool,omitempty"`
	NextSummary      string    `json:"next_summary,omitempty"`
	NextText         string    `json:"next_text,omitempty"`
	SameTurnToolCall bool      `json:"same_turn_tool_call,omitempty"`
}

type Recorder struct {
	mu            sync.RWMutex
	debugAPI      string
	deepDebug     bool
	maxEvents     int
	maxHTTP       int
	runtime       RuntimeSnapshot
	events        []RecordedEvent
	sessionEvents map[domain.ID][]RecordedEvent
	httpTraces    []HTTPTrace
}

func NewRecorder() *Recorder {
	return &Recorder{
		maxEvents:     defaultMaxLogs,
		maxHTTP:       defaultMaxHTTP,
		sessionEvents: map[domain.ID][]RecordedEvent{},
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

func (r *Recorder) RecordEvent(sessionID domain.ID, evt domain.Event) {
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
	if evt.Usage.HasAnyTokens() {
		usage := evt.Usage.Normalized()
		entry.Usage = &usage
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = appendRecordedEvent(r.events, entry, r.maxEvents)
	if sessionID != "" {
		r.sessionEvents[sessionID] = appendRecordedEvent(r.sessionEvents[sessionID], entry, r.maxEvents)
	}
	if entry.Error != "" {
		r.runtime.LastError = entry.Error
	}
}

func (r *Recorder) RecordLifecycle(sessionID domain.ID, kind, text string, meta map[string]string) {
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
	if sessionID != "" {
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

func (r *Recorder) Events(sessionID domain.ID) []RecordedEvent {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sessionID != "" {
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
	sessionID := strings.TrimSpace(parts[0])
	if sessionID == "" {
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
	case "analysis":
		s.handleAnalysis(w, r, sessionID)
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

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, sessionID domain.ID) {
	session, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	timeline, err := s.sessionTimeline(r.Context(), sessionID)
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
		"timeline":       timeline,
		"approvals":      approvals,
		"milestone_plan": plan,
		"todos":          todos,
		"events":         s.recorder.Events(sessionID),
	})
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request, sessionID domain.ID) {
	timeline, err := s.sessionTimeline(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"timeline":   timeline,
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, _ *http.Request, sessionID domain.ID) {
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"events":     s.recorder.Events(sessionID),
	})
}

func (s *Server) handleAnalysis(w http.ResponseWriter, r *http.Request, sessionID domain.ID) {
	timeline, err := s.sessionTimeline(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, analyzeSession(sessionID, timeline, s.recorder.Events(sessionID)))
}

func (s *Server) sessionTimeline(ctx context.Context, sessionID domain.ID) ([]domain.TimelineItem, error) {
	chat, err := s.store.DefaultChat(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return s.store.TimelineForChat(ctx, chat.ID)
}

func (s *Server) handleGlobalEvents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": "",
		"events":     s.recorder.Events(""),
	})
}

func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request, sessionID domain.ID) {
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

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request, sessionID domain.ID) {
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

func (s *Server) handleMilestones(w http.ResponseWriter, r *http.Request, sessionID domain.ID) {
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

func (s *Server) handleTodos(w http.ResponseWriter, r *http.Request, sessionID domain.ID) {
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

type analyzedTranscriptMessage struct {
	item        domain.TimelineItem
	role        domain.MessageRole
	text        string
	summary     string
	kind        string
	toolNames   []string
	hasToolCall bool
}

func analyzeSession(sessionID domain.ID, timeline []domain.TimelineItem, events []RecordedEvent) SessionAnalysis {
	transcript := make([]analyzedTranscriptMessage, 0, len(timeline))
	for _, item := range timeline {
		transcript = append(transcript, analyzeTranscriptItem(item))
	}
	out := SessionAnalysis{
		SessionID:       sessionID,
		TranscriptCount: len(transcript),
	}
	for _, evt := range events {
		if evt.Source != "lifecycle" {
			continue
		}
		if evt.Kind != "continue" && evt.Kind != "continue_with_note" {
			continue
		}
		out.Continues = append(out.Continues, SessionContinueRecord{
			Timestamp: evt.Timestamp,
			Kind:      evt.Kind,
			Text:      evt.Text,
			Meta:      cloneMeta(evt.Meta),
		})
	}
	out.ContinueCount = len(out.Continues)
	for i, msg := range transcript {
		if !looksLikeBadStop(msg) {
			continue
		}
		record := SessionBadStopRecord{
			MessageID:        msg.item.ID,
			ChatID:           msg.item.ChatID,
			CreatedAt:        msg.item.CreatedAt,
			Summary:          msg.summary,
			Text:             msg.text,
			SameTurnToolCall: msg.hasToolCall,
		}
		if i+1 < len(transcript) {
			next := transcript[i+1]
			record.NextMessageID = next.item.ID
			record.NextRole = string(next.role)
			record.NextKind = next.kind
			if len(next.toolNames) > 0 {
				record.NextTool = next.toolNames[0]
			}
			record.NextSummary = next.summary
			record.NextText = next.text
		}
		out.BadStops = append(out.BadStops, record)
	}
	out.BadStopCount = len(out.BadStops)
	return out
}

func analyzeTranscriptItem(item domain.TimelineItem) analyzedTranscriptMessage {
	out := analyzedTranscriptMessage{item: item}
	switch content := item.Content.(type) {
	case domain.UserMessage:
		out.role = domain.MessageRoleUser
		out.kind = string(domain.TimelineKindUser)
		out.text = strings.TrimSpace(content.Text)
	case domain.AssistantMessage:
		out.role = domain.MessageRoleAssistant
		out.kind = string(domain.TimelineKindAssistant)
		out.text = strings.TrimSpace(content.Text)
		if out.text == "" {
			out.text = strings.TrimSpace(content.Reasoning.Text)
		}
		for _, tool := range content.Tools {
			out.hasToolCall = true
			if tool.Tool != "" {
				out.toolNames = append(out.toolNames, string(tool.Tool))
			}
		}
	case domain.ToolExecution:
		out.role = domain.MessageRoleTool
		out.kind = string(domain.TimelineKindTool)
		out.toolNames = append(out.toolNames, string(content.Tool))
		if content.Result != nil {
			out.text = strings.TrimSpace(content.Result.Text)
		}
		if content.Error != nil {
			out.text = strings.TrimSpace(content.Error.Message)
		}
	case domain.Notice:
		out.role = domain.MessageRoleTool
		out.kind = string(domain.TimelineKindNotice)
		out.text = strings.TrimSpace(content.Text)
	case domain.Compaction:
		out.role = domain.MessageRoleTool
		out.kind = string(domain.TimelineKindCompaction)
		out.text = strings.TrimSpace(content.Summary)
	default:
		out.kind = "item"
	}
	out.summary = truncate(out.text, 120)
	return out
}

func looksLikeBadStop(msg analyzedTranscriptMessage) bool {
	if msg.role != domain.MessageRoleAssistant {
		return false
	}
	if msg.hasToolCall {
		return false
	}
	text := compactText(msg.text)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	prefixes := []string{"now ", "now let me ", "let me ", "next ", "next, ", "i'll ", "i will "}
	prefixMatch := false
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			prefixMatch = true
			break
		}
	}
	if !prefixMatch {
		return false
	}
	if strings.Contains(text, "```") {
		return false
	}
	verbs := []string{" update ", " update`", " check ", " fix ", " change ", " edit ", " adjust ", " inspect "}
	verbMatch := strings.Contains(lower, ":")
	if !verbMatch {
		for _, verb := range verbs {
			if strings.Contains(lower, verb) {
				verbMatch = true
				break
			}
		}
	}
	return verbMatch
}

func compactText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func firstLine(value string) string {
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return strings.TrimSpace(value)
}
