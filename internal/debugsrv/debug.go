package debugsrv

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/version"
)

const (
	defaultMaxLogs = 256
	defaultMaxHTTP = 96
)

type debugApproval struct {
	ID         id.ID                 `json:"ID"`
	SessionID  id.ID                 `json:"SessionID"`
	ChatID     id.ID                 `json:"ChatID"`
	Tool       domain.ToolKind       `json:"Tool"`
	ToolCallID string                `json:"ToolCallID"`
	Command    string                `json:"Command"`
	Status     domain.ApprovalStatus `json:"Status"`
	CreatedAt  time.Time             `json:"CreatedAt"`
}

type RecordedEvent struct {
	Timestamp time.Time         `json:"timestamp"`
	SessionID id.ID             `json:"session_id"`
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
	SessionID    id.ID             `json:"session_id,omitempty"`
	ChatID       id.ID             `json:"chat_id,omitempty"`
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

type RuntimeDebug struct {
	Process   ProcessDebug  `json:"process"`
	Clients   []ClientDebug `json:"clients"`
	Chats     []ChatDebug   `json:"chats"`
	DeepDebug bool          `json:"deep_debug"`
}

type ProcessDebug struct {
	Timestamp            time.Time    `json:"timestamp"`
	DebugAPI             string       `json:"debug_api"`
	Build                version.Info `json:"build"`
	Status               string       `json:"status"`
	LastError            string       `json:"last_error,omitempty"`
	WebsocketClientCount int          `json:"websocket_client_count"`
}

type ClientDebug struct {
	ID                     string    `json:"id"`
	Connected              bool      `json:"connected"`
	ConnectedAt            time.Time `json:"connected_at"`
	LastSeen               time.Time `json:"last_seen"`
	RemoteAddr             string    `json:"remote_addr,omitempty"`
	UserAgent              string    `json:"user_agent,omitempty"`
	SelectedSession        id.ID     `json:"selected_session,omitempty"`
	SelectedChat           id.ID     `json:"selected_chat,omitempty"`
	DocumentVisible        bool      `json:"document_visible"`
	WindowFocused          bool      `json:"window_focused"`
	ComposerFocused        bool      `json:"composer_focused"`
	ViewportWidth          int       `json:"viewport_width,omitempty"`
	ViewportHeight         int       `json:"viewport_height,omitempty"`
	TranscriptScrollTop    int       `json:"transcript_scroll_top,omitempty"`
	TranscriptScrollHeight int       `json:"transcript_scroll_height,omitempty"`
	TranscriptClientHeight int       `json:"transcript_client_height,omitempty"`
	StickToBottom          bool      `json:"stick_to_bottom"`
	OpenDialog             string    `json:"open_dialog,omitempty"`
	InterruptVisible       bool      `json:"interrupt_visible"`
	InterruptArmed         bool      `json:"interrupt_armed"`
}

type ChatDebug struct {
	ID                        id.ID  `json:"id"`
	SessionID                 id.ID  `json:"session_id"`
	Title                     string `json:"title,omitempty"`
	Status                    string `json:"status"`
	StatusText                string `json:"status_text,omitempty"`
	Active                    bool   `json:"active"`
	Busy                      bool   `json:"busy"`
	QueueLen                  int    `json:"queue_len"`
	PendingAssistantText      int    `json:"pending_assistant_text_len"`
	PendingAssistantReasoning int    `json:"pending_assistant_reasoning_len"`
	PendingApprovals          int    `json:"pending_approvals"`
	RunningToolCalls          int    `json:"running_tool_calls"`
}

type ArchitectureDebug struct {
	Summary        string   `json:"summary"`
	Organization   []string `json:"organization"`
	DataSources    []string `json:"data_sources"`
	MoreDataNeeded []string `json:"more_data_needed,omitempty"`
}

type SessionDebug struct {
	ID                  id.ID              `json:"id"`
	Title               string             `json:"title,omitempty"`
	ProjectRoot         string             `json:"project_root,omitempty"`
	Hydration           string             `json:"hydration"`
	Hydrated            bool               `json:"hydrated"`
	StoredChatCount     int                `json:"stored_chat_count"`
	HydratedChatCount   int                `json:"hydrated_chat_count"`
	VisibleChatCount    int                `json:"visible_chat_count"`
	ArchivedChatCount   int                `json:"archived_chat_count"`
	SelectedClientCount int                `json:"selected_client_count"`
	Record              domain.Session     `json:"record"`
	Chats               []SessionChatDebug `json:"chats"`
	DataNotes           []string           `json:"data_notes,omitempty"`
}

type SessionChatDebug struct {
	ID                         id.ID      `json:"id"`
	SessionID                  id.ID      `json:"session_id"`
	Title                      string     `json:"title,omitempty"`
	WorkflowRole               string     `json:"workflow_role,omitempty"`
	Archived                   bool       `json:"archived"`
	Hydration                  string     `json:"hydration"`
	Hydrated                   bool       `json:"hydrated"`
	QueueLen                   int        `json:"queue_len"`
	TimelineCount              int        `json:"timeline_count"`
	PendingApprovals           int        `json:"pending_approvals"`
	PendingExecutableToolCalls int        `json:"pending_executable_tool_calls"`
	SelectedClientCount        int        `json:"selected_client_count"`
	LastKnownContextTokens     int        `json:"last_known_context_tokens,omitempty"`
	ContextTokensKnown         bool       `json:"context_tokens_known"`
	LastMessage                string     `json:"last_message,omitempty"`
	Runtime                    *ChatDebug `json:"runtime,omitempty"`
	Diagnostics                []string   `json:"diagnostics,omitempty"`
}

type SessionAnalysis struct {
	SessionID       id.ID                   `json:"session_id"`
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
	ChatID           id.ID     `json:"chat_id"`
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
	process       ProcessDebug
	clients       map[string]ClientDebug
	chats         map[id.ID]ChatDebug
	events        []RecordedEvent
	sessionEvents map[id.ID][]RecordedEvent
	httpTraces    []HTTPTrace
	lastHTTPBody  map[string]string
}

func NewRecorder() *Recorder {
	return &Recorder{
		maxEvents:     defaultMaxLogs,
		maxHTTP:       defaultMaxHTTP,
		clients:       map[string]ClientDebug{},
		chats:         map[id.ID]ChatDebug{},
		sessionEvents: map[id.ID][]RecordedEvent{},
		lastHTTPBody:  map[string]string{},
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
	r.process.DebugAPI = r.debugAPI
}

func (r *Recorder) SetDeepDebug(enabled bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deepDebug = enabled
}

func (r *Recorder) DeepDebug() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.deepDebug
}

func (r *Recorder) RecordEvent(sessionID id.ID, evt domain.Event) {
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
		r.process.LastError = entry.Error
	}
}

func (r *Recorder) RecordLifecycle(sessionID id.ID, kind, text string, meta map[string]string) {
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
	fullRequestBody := trace.RequestBody
	trace.Meta = requestDiagnostics(trace, trace.Meta)
	r.mu.Lock()
	defer r.mu.Unlock()
	if key := previousRequestKey(trace); key != "" {
		if previous := r.lastHTTPBody[key]; previous != "" {
			if trace.Meta == nil {
				trace.Meta = map[string]string{}
			}
			trace.Meta["previous_lcp_bytes"] = strconv.Itoa(commonPrefixLen(previous, fullRequestBody))
		}
		r.lastHTTPBody[key] = fullRequestBody
	}
	trace.RequestBody = truncate(trace.RequestBody, 8192)
	trace.ResponseBody = truncate(trace.ResponseBody, 8192)
	trace.RequestHdrs = cloneMeta(trace.RequestHdrs)
	trace.ResponseHdrs = cloneMeta(trace.ResponseHdrs)
	r.httpTraces = appendHTTPTrace(r.httpTraces, trace, r.maxHTTP)
}

func requestDiagnostics(trace HTTPTrace, meta map[string]string) map[string]string {
	body := strings.TrimSpace(trace.RequestBody)
	if body == "" {
		return cloneMeta(meta)
	}
	out := cloneMeta(meta)
	if out == nil {
		out = map[string]string{}
	}
	out["request_bytes"] = strconv.Itoa(len(body))
	out["request_sha256"] = shortSHA256(body)
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return out
	}
	if raw := decoded["messages"]; len(raw) > 0 {
		out["messages_sha256"] = shortSHA256(string(raw))
		out["system_sha256"] = systemMessageHash(raw)
	}
	if raw := decoded["tools"]; len(raw) > 0 {
		out["tools_sha256"] = shortSHA256(string(raw))
	}
	return out
}

func systemMessageHash(raw json.RawMessage) string {
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		return ""
	}
	for _, msg := range messages {
		if strings.TrimSpace(strings.ToLower(msg.Role)) == "system" {
			return shortSHA256(string(msg.Content))
		}
	}
	return ""
}

func shortSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:8])
}

func previousRequestKey(trace HTTPTrace) string {
	if trace.ChatID != "" {
		return string(trace.ChatID)
	}
	if trace.SessionID != "" {
		return string(trace.SessionID)
	}
	return ""
}

func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	for idx := 0; idx < n; idx++ {
		if a[idx] != b[idx] {
			return idx
		}
	}
	return n
}

func (r *Recorder) UpdateProcess(process ProcessDebug) {
	if r == nil {
		return
	}
	process.Timestamp = time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	if process.DebugAPI == "" {
		process.DebugAPI = r.debugAPI
	}
	if process.LastError == "" {
		process.LastError = r.process.LastError
	}
	if process.Build.Name == "" {
		process.Build = version.Current()
	}
	r.process = process
}

func (r *Recorder) RegisterClient(client ClientDebug) ClientDebug {
	if r == nil {
		return ClientDebug{}
	}
	now := time.Now().UTC()
	client.ID = strings.TrimSpace(client.ID)
	if client.ID == "" {
		client.ID = string(id.New())
	}
	client.Connected = true
	client.ConnectedAt = now
	client.LastSeen = now
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clients == nil {
		r.clients = map[string]ClientDebug{}
	}
	r.clients[client.ID] = client
	return client
}

func (r *Recorder) UpdateClient(clientID string, update ClientDebug) {
	if r == nil {
		return
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clients == nil {
		r.clients = map[string]ClientDebug{}
	}
	existing := r.clients[clientID]
	if existing.ID == "" {
		existing.ID = clientID
		existing.ConnectedAt = time.Now().UTC()
	}
	update.ID = clientID
	update.Connected = existing.Connected
	if !update.Connected {
		update.Connected = true
	}
	update.ConnectedAt = existing.ConnectedAt
	update.LastSeen = time.Now().UTC()
	update.RemoteAddr = firstNonEmpty(update.RemoteAddr, existing.RemoteAddr)
	update.UserAgent = firstNonEmpty(update.UserAgent, existing.UserAgent)
	r.clients[clientID] = update
}

func (r *Recorder) UnregisterClient(clientID string) {
	if r == nil {
		return
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	client, ok := r.clients[clientID]
	if !ok {
		return
	}
	client.Connected = false
	client.LastSeen = time.Now().UTC()
	r.clients[clientID] = client
}

func (r *Recorder) UpdateChats(chats []ChatDebug) {
	if r == nil {
		return
	}
	next := make(map[id.ID]ChatDebug, len(chats))
	for _, chat := range chats {
		if chat.ID == "" {
			continue
		}
		next[chat.ID] = chat
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chats = next
}

func (r *Recorder) Runtime() RuntimeDebug {
	if r == nil {
		return RuntimeDebug{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	process := r.process
	process.Timestamp = time.Now().UTC()
	process.DebugAPI = firstNonEmpty(process.DebugAPI, r.debugAPI)
	if process.Build.Name == "" {
		process.Build = version.Current()
	}
	clients := cloneClients(r.clients)
	chats := cloneChats(r.chats)
	process.WebsocketClientCount = connectedClientCount(clients)
	return RuntimeDebug{Process: process, Clients: clients, Chats: chats, DeepDebug: r.deepDebug}
}

func (r *Recorder) Clients() []ClientDebug {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneClients(r.clients)
}

func (r *Recorder) Client(clientID string) (ClientDebug, bool) {
	if r == nil {
		return ClientDebug{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	client, ok := r.clients[strings.TrimSpace(clientID)]
	return client, ok
}

func (r *Recorder) Chats() []ChatDebug {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneChats(r.chats)
}

func (r *Recorder) Chat(chatID id.ID) (ChatDebug, bool) {
	if r == nil {
		return ChatDebug{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	chat, ok := r.chats[chatID]
	return chat, ok
}

func (r *Recorder) Events(sessionID id.ID) []RecordedEvent {
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
}

func NewServer(st *store.Store, recorder *Recorder) *Server {
	if recorder == nil {
		recorder = NewRecorder()
	}
	return &Server{
		store:    st,
		recorder: recorder,
	}
}

func Handler(st *store.Store, recorder *Recorder) http.Handler {
	mux := http.NewServeMux()
	NewServer(st, recorder).Register(mux)
	return mux
}

func (s *Server) Register(mux *http.ServeMux) {
	if s == nil || mux == nil {
		return
	}
	mux.HandleFunc("/debug/health", s.handleHealth)
	mux.HandleFunc("/debug/runtime", s.handleRuntime)
	mux.HandleFunc("/debug/clients", s.handleClients)
	mux.HandleFunc("/debug/clients/", s.handleClient)
	mux.HandleFunc("/debug/chats", s.handleChats)
	mux.HandleFunc("/debug/chats/", s.handleChat)
	mux.HandleFunc("/debug/http", s.handleHTTP)
	mux.HandleFunc("/debug/events", s.handleGlobalEvents)
	mux.HandleFunc("/debug/sessions", s.handleSessions)
	mux.HandleFunc("/debug/sessions/", s.handleSessionRoutes)
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

func (s *Server) Recorder() *Recorder {
	if s == nil {
		return nil
	}
	return s.recorder
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"debug": s.recorder.Runtime().Process.DebugAPI,
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

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/debug/clients" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": s.recorder.Clients()})
}

func (s *Server) handleClient(w http.ResponseWriter, r *http.Request) {
	clientID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/debug/clients/"), "/")
	if clientID == "" {
		http.NotFound(w, r)
		return
	}
	client, ok := s.recorder.Client(clientID)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("client not found"))
		return
	}
	writeJSON(w, http.StatusOK, client)
}

func (s *Server) handleChats(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/debug/chats" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": s.recorder.Chats()})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	chatID := id.ID(strings.Trim(strings.TrimPrefix(r.URL.Path, "/debug/chats/"), "/"))
	if chatID == "" {
		http.NotFound(w, r)
		return
	}
	chat, ok := s.recorder.Chat(chatID)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("chat not found"))
		return
	}
	writeJSON(w, http.StatusOK, chat)
}

func debugArchitecture() ArchitectureDebug {
	return ArchitectureDebug{
		Summary: "Engine owns sessions, sessions own persisted chats, and hydrated chat runtimes report live state through the debug recorder.",
		Organization: []string{
			"session records and chat records are loaded from the store",
			"chat hydration is detected from live recorder chat state",
			"session hydration is inferred when at least one stored chat has live recorder state",
			"client selection is grouped by selected session and selected chat",
		},
		DataSources: []string{
			"store: sessions, chats, timelines, and approvals",
			"recorder: connected clients, live chat runtime status, queue length, approvals, and running tool counts",
		},
		MoreDataNeeded: []string{
			"exact engine-owned session hydration is inferred here; expose agent session snapshots if the debug API must distinguish a loaded session with no active chat runtime",
			"stored-only chats have persisted queue, timeline, approval, and context counts but no live goroutine status until hydrated",
		},
	}
}

func (s *Server) debugSessions(ctx context.Context) ([]SessionDebug, error) {
	sessions, err := debugListSessions(ctx, s.store)
	if err != nil {
		return nil, err
	}
	out := make([]SessionDebug, 0, len(sessions))
	for _, session := range sessions {
		item, _, err := s.debugSession(ctx, session)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Server) debugSession(ctx context.Context, session domain.Session) (SessionDebug, []domain.Chat, error) {
	chats, err := debugListChats(ctx, s.store, session.ID)
	if err != nil {
		return SessionDebug{}, nil, err
	}
	runtime := s.recorder.Runtime()
	runtimeByChat := make(map[id.ID]ChatDebug, len(runtime.Chats))
	for _, chat := range runtime.Chats {
		runtimeByChat[chat.ID] = chat
	}
	selectedSessions, selectedChats := selectedClientCounts(runtime.Clients)

	out := SessionDebug{
		ID:                  session.ID,
		Title:               session.Title,
		ProjectRoot:         session.ProjectRoot,
		Hydration:           "stored",
		StoredChatCount:     len(chats),
		SelectedClientCount: selectedSessions[session.ID],
		Record:              session,
		Chats:               make([]SessionChatDebug, 0, len(chats)),
	}
	for _, chatRecord := range chats {
		chatDebug, err := s.debugChat(ctx, chatRecord, runtimeByChat, selectedChats)
		if err != nil {
			return SessionDebug{}, nil, err
		}
		out.Chats = append(out.Chats, chatDebug)
		if chatRecord.Archived {
			out.ArchivedChatCount++
		} else {
			out.VisibleChatCount++
		}
		if chatDebug.Hydrated {
			out.HydratedChatCount++
		}
	}
	if out.HydratedChatCount > 0 {
		out.Hydrated = true
		out.Hydration = "hydrated"
	}
	if out.HydratedChatCount == 0 && len(chats) > 0 {
		out.DataNotes = append(out.DataNotes, "no stored chat in this session has live recorder state")
	}
	if out.HydratedChatCount > 0 && out.HydratedChatCount < out.StoredChatCount {
		out.DataNotes = append(out.DataNotes, "some stored chats are not currently hydrated")
	}
	return out, chats, nil
}

func (s *Server) debugChat(ctx context.Context, chatRecord domain.Chat, runtimeByChat map[id.ID]ChatDebug, selectedChats map[id.ID]int) (SessionChatDebug, error) {
	timeline, err := debugTimelineForChat(ctx, s.store, chatRecord.ID)
	if err != nil {
		return SessionChatDebug{}, err
	}
	approvals, err := debugPendingApprovalsForChat(ctx, s.store, chatRecord)
	if err != nil {
		return SessionChatDebug{}, err
	}
	out := SessionChatDebug{
		ID:                         chatRecord.ID,
		SessionID:                  chatRecord.SessionID,
		Title:                      chatRecord.Title,
		WorkflowRole:               string(chatRecord.WorkflowRole),
		Archived:                   chatRecord.Archived,
		Hydration:                  "stored",
		QueueLen:                   len(chatRecord.QueuedInputs),
		TimelineCount:              len(timeline),
		PendingApprovals:           len(approvals),
		PendingExecutableToolCalls: pendingExecutableToolCalls(timeline),
		SelectedClientCount:        selectedChats[chatRecord.ID],
		LastKnownContextTokens:     chatRecord.LastKnownContextTokens,
		ContextTokensKnown:         chatRecord.ContextTokensKnown,
		LastMessage:                chatRecord.LastMessage,
	}
	if runtime, ok := runtimeByChat[chatRecord.ID]; ok {
		out.Hydrated = true
		out.Hydration = "hydrated"
		out.Runtime = &runtime
		out.QueueLen = runtime.QueueLen
		out.PendingApprovals = runtime.PendingApprovals
		if runtime.Busy && runtime.Status == "running_tools" && runtime.RunningToolCalls == 0 {
			out.Diagnostics = append(out.Diagnostics, "live runtime reports running_tools with no running tool calls")
		}
		return out, nil
	}
	out.Diagnostics = append(out.Diagnostics, "no live recorder state; runtime goroutine status is unavailable")
	return out, nil
}

func selectedClientCounts(clients []ClientDebug) (map[id.ID]int, map[id.ID]int) {
	sessions := map[id.ID]int{}
	chats := map[id.ID]int{}
	for _, client := range clients {
		if !client.Connected {
			continue
		}
		if client.SelectedSession != "" {
			sessions[client.SelectedSession]++
		}
		if client.SelectedChat != "" {
			chats[client.SelectedChat]++
		}
	}
	return sessions, chats
}

func pendingExecutableToolCalls(timeline []domain.TimelineItem) int {
	for i := len(timeline) - 1; i >= 0; i-- {
		message, ok := timeline[i].Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		var count int
		for _, call := range message.Tools {
			if call.Status == domain.ToolStatusPending && call.Result == nil && call.Error == nil && call.Approval == nil && call.ApprovalID == "" {
				count++
			}
		}
		return count
	}
	return 0
}

func debugTimelineCollection(st *store.Store) store.Collection[domain.TimelineItem] {
	return store.NewCollection(st, store.CollectionSpec[domain.TimelineItem]{
		Namespace: "timeline",
		GetID:     func(v domain.TimelineItem) string { return v.ID },
		SetID:     func(v *domain.TimelineItem, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.TimelineItem]{
			{Name: "chat", Value: func(v domain.TimelineItem) string { return v.ChatID }},
		},
	})
}

func debugSessionCollection(st *store.Store) store.Collection[domain.Session] {
	return store.NewCollection(st, store.CollectionSpec[domain.Session]{
		Namespace: "sessions",
		GetID:     func(v domain.Session) string { return v.ID },
		SetID:     func(v *domain.Session, id string) { v.ID = id },
	})
}

func debugChatCollection(st *store.Store) store.Collection[domain.Chat] {
	return store.NewCollection(st, store.CollectionSpec[domain.Chat]{
		Namespace: "chats",
		GetID:     func(v domain.Chat) string { return v.ID },
		SetID:     func(v *domain.Chat, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.Chat]{
			{Name: "session", Value: func(v domain.Chat) string { return v.SessionID }},
		},
	})
}

func debugPlanCollection(st *store.Store) store.Collection[planning.Plan] {
	return store.NewCollection(st, store.CollectionSpec[planning.Plan]{
		Namespace: "milestone-plans",
		GetID:     func(v planning.Plan) string { return v.SessionID },
		SetID:     func(v *planning.Plan, id string) { v.SessionID = id },
	})
}

func debugTodoCollection(st *store.Store) store.Collection[planning.TodoItem] {
	return store.NewCollection(st, store.CollectionSpec[planning.TodoItem]{
		Namespace: "todos",
		GetID:     func(v planning.TodoItem) string { return v.ID },
		SetID:     func(v *planning.TodoItem, id string) { v.ID = id },
		Indexes: []store.IndexSpec[planning.TodoItem]{
			{Name: "session", Value: func(v planning.TodoItem) string { return v.SessionID }},
			{Name: "milestone", Value: func(v planning.TodoItem) string { return v.SessionID + "/" + v.MilestoneRef }},
		},
	})
}

func debugTaskCollection(st *store.Store) store.Collection[planning.Task] {
	return store.NewCollection(st, store.CollectionSpec[planning.Task]{
		Namespace: "tasks",
		GetID:     func(v planning.Task) string { return v.ID },
		SetID:     func(v *planning.Task, id string) { v.ID = id },
		Indexes: []store.IndexSpec[planning.Task]{
			{Name: "session", Value: func(v planning.Task) string { return v.SessionID }},
		},
	})
}

func debugListSessions(ctx context.Context, st *store.Store) ([]domain.Session, error) {
	sessions, err := debugSessionCollection(st).List(ctx, store.All[domain.Session]())
	if err != nil {
		return nil, err
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		return sessions[i].ID < sessions[j].ID
	})
	return sessions, nil
}

func debugGetSession(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Session, error) {
	return debugSessionCollection(st).Get(ctx, sessionID)
}

func debugListChats(ctx context.Context, st *store.Store, sessionID id.ID) ([]domain.Chat, error) {
	chats, err := debugChatCollection(st).List(ctx, store.ByIndex[domain.Chat]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(chats, func(i, j int) bool {
		if chats[i].Position != chats[j].Position {
			return chats[i].Position < chats[j].Position
		}
		if !chats[i].CreatedAt.Equal(chats[j].CreatedAt) {
			return chats[i].CreatedAt.Before(chats[j].CreatedAt)
		}
		return chats[i].ID < chats[j].ID
	})
	return chats, nil
}

func debugDefaultChat(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Chat, error) {
	chats, err := debugListChats(ctx, st, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	for _, chatRecord := range chats {
		if chatRecord.ParentChatID == nil {
			return chatRecord, nil
		}
	}
	if len(chats) > 0 {
		return chats[0], nil
	}
	return domain.Chat{}, fmt.Errorf("session %s has no chats", sessionID)
}

func debugGetPlan(ctx context.Context, st *store.Store, sessionID id.ID) (planning.Plan, error) {
	plan, err := debugPlanCollection(st).Get(ctx, sessionID)
	if err != nil {
		return planning.Plan{SessionID: sessionID}, nil
	}
	return plan, nil
}

func debugListTodos(ctx context.Context, st *store.Store, sessionID id.ID, milestoneRef string) ([]planning.TodoItem, error) {
	query := store.ByIndex[planning.TodoItem]("session", string(sessionID))
	milestoneRef = strings.TrimSpace(milestoneRef)
	if milestoneRef != "" {
		query = store.ByIndex[planning.TodoItem]("milestone", string(sessionID)+"/"+milestoneRef)
	}
	items, err := debugTodoCollection(st).List(ctx, query)
	if err != nil {
		return nil, err
	}
	planning.SortTodos(items)
	return items, nil
}

func debugListTasks(ctx context.Context, st *store.Store, sessionID id.ID) ([]planning.Task, error) {
	items, err := debugTaskCollection(st).List(ctx, store.ByIndex[planning.Task]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func debugTimelineForChat(ctx context.Context, st *store.Store, chatID id.ID) ([]domain.TimelineItem, error) {
	items, err := debugTimelineCollection(st).List(ctx, store.ByIndex[domain.TimelineItem]("chat", string(chatID)))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Seq != items[j].Seq {
			return items[i].Seq < items[j].Seq
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func debugPendingApprovalsForChat(ctx context.Context, st *store.Store, chatRecord domain.Chat) ([]debugApproval, error) {
	items, err := debugTimelineForChat(ctx, st, chatRecord.ID)
	if err != nil {
		return nil, err
	}
	var approvals []debugApproval
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range assistant.Tools {
			if call.Status != domain.ToolStatusAwaitingApproval {
				continue
			}
			approvals = append(approvals, debugApproval{
				ID:         id.ID(strings.TrimSpace(string(call.ToolCallID))),
				SessionID:  chatRecord.SessionID,
				ChatID:     chatRecord.ID,
				Tool:       call.Tool,
				ToolCallID: string(call.ToolCallID),
				Command:    debugToolCallPreview(call),
				Status:     domain.ApprovalStatusPending,
				CreatedAt:  item.UpdatedAt,
			})
		}
	}
	return approvals, nil
}

func debugToolCallPreview(call domain.ToolCall) string {
	if command := strings.TrimSpace(call.Args["command"]); command != "" {
		return command
	}
	if path := strings.TrimSpace(call.Args["path"]); path != "" {
		return path
	}
	if pattern := strings.TrimSpace(call.Args["pattern"]); pattern != "" {
		return pattern
	}
	return strings.TrimSpace(call.Tool.String())
}

func (s *Server) handleHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"debug_api": s.recorder.Runtime().Process.DebugAPI,
		"traces":    s.recorder.HTTPTraces(),
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/debug/sessions" {
		http.NotFound(w, r)
		return
	}
	sessions, err := s.debugSessions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"architecture": debugArchitecture(),
		"runtime":      s.recorder.Runtime(),
		"sessions":     sessions,
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

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	session, err := debugGetSession(r.Context(), s.store, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	sessionDebug, chats, err := s.debugSession(r.Context(), session)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	timeline, err := s.sessionTimeline(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	approvals, err := s.sessionApprovals(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	plan, err := debugGetPlan(r.Context(), s.store, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var todos []planning.TodoItem
	for _, milestone := range plan.Milestones {
		items, err := debugListTodos(r.Context(), s.store, sessionID, milestone.Ref)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		todos = append(todos, items...)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"architecture":   debugArchitecture(),
		"debug":          sessionDebug,
		"session":        session,
		"chats":          chats,
		"timeline":       timeline,
		"approvals":      approvals,
		"milestone_plan": plan,
		"todos":          todos,
		"events":         s.recorder.Events(sessionID),
	})
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
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

func (s *Server) handleEvents(w http.ResponseWriter, _ *http.Request, sessionID id.ID) {
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"events":     s.recorder.Events(sessionID),
	})
}

func (s *Server) handleAnalysis(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	timeline, err := s.sessionTimeline(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, analyzeSession(sessionID, timeline, s.recorder.Events(sessionID)))
}

func (s *Server) sessionTimeline(ctx context.Context, sessionID id.ID) ([]domain.TimelineItem, error) {
	chat, err := debugDefaultChat(ctx, s.store, sessionID)
	if err != nil {
		return nil, err
	}
	return debugTimelineForChat(ctx, s.store, chat.ID)
}

func (s *Server) handleGlobalEvents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": "",
		"events":     s.recorder.Events(""),
	})
}

func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	approvals, err := s.sessionApprovals(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"approvals":  approvals,
	})
}

func (s *Server) sessionApprovals(ctx context.Context, sessionID id.ID) ([]debugApproval, error) {
	chats, err := debugListChats(ctx, s.store, sessionID)
	if err != nil {
		return nil, err
	}
	var approvals []debugApproval
	for _, chatRecord := range chats {
		next, err := debugPendingApprovalsForChat(ctx, s.store, chatRecord)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, next...)
	}
	return approvals, nil
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	tasks, err := debugListTasks(r.Context(), s.store, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"tasks":      tasks,
	})
}

func (s *Server) handleMilestones(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	plan, err := debugGetPlan(r.Context(), s.store, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"plan":       plan,
	})
}

func (s *Server) handleTodos(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	plan, err := debugGetPlan(r.Context(), s.store, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var todos []planning.TodoItem
	for _, milestone := range plan.Milestones {
		items, err := debugListTodos(r.Context(), s.store, sessionID, milestone.Ref)
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

func cloneClients(src map[string]ClientDebug) []ClientDebug {
	if len(src) == 0 {
		return nil
	}
	out := make([]ClientDebug, 0, len(src))
	for _, item := range src {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func cloneChats(src map[id.ID]ChatDebug) []ChatDebug {
	if len(src) == 0 {
		return nil
	}
	out := make([]ChatDebug, 0, len(src))
	for _, item := range src {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func connectedClientCount(clients []ClientDebug) int {
	var count int
	for _, client := range clients {
		if client.Connected {
			count++
		}
	}
	return count
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

func analyzeSession(sessionID id.ID, timeline []domain.TimelineItem, events []RecordedEvent) SessionAnalysis {
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
			record.NextRole = next.role.String()
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
			if tool.Tool != 0 {
				out.toolNames = append(out.toolNames, tool.Tool.DisplayName())
			}
		}
	case domain.ToolExecution:
		out.role = domain.MessageRoleTool
		out.kind = string(domain.TimelineKindTool)
		out.toolNames = append(out.toolNames, content.Tool.DisplayName())
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
