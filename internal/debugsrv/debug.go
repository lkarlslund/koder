package debugsrv

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/pprof"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/version"
)

const (
	defaultMaxLogs = 256
	defaultMaxHTTP = 20
)

type DebugApproval struct {
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
	Timestamp     time.Time         `json:"timestamp"`
	ProviderID    string            `json:"provider_id"`
	SessionID     id.ID             `json:"session_id,omitempty"`
	ChatID        id.ID             `json:"chat_id,omitempty"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Status        int               `json:"status"`
	DurationMS    int64             `json:"duration_ms"`
	RequestBytes  int               `json:"request_bytes,omitempty"`
	ResponseBytes int               `json:"response_bytes,omitempty"`
	RequestBody   string            `json:"request_body,omitempty"`
	ResponseBody  string            `json:"response_body,omitempty"`
	RequestHdrs   map[string]string `json:"request_headers,omitempty"`
	ResponseHdrs  map[string]string `json:"response_headers,omitempty"`
	Meta          map[string]string `json:"meta,omitempty"`
	Error         string            `json:"error,omitempty"`
}

type ActiveHTTPTrace struct {
	ID            id.ID             `json:"id"`
	Timestamp     time.Time         `json:"timestamp"`
	ProviderID    string            `json:"provider_id"`
	SessionID     id.ID             `json:"session_id,omitempty"`
	ChatID        id.ID             `json:"chat_id,omitempty"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Status        int               `json:"status,omitempty"`
	DurationMS    int64             `json:"duration_ms"`
	RequestBytes  int               `json:"request_bytes,omitempty"`
	ResponseBytes int               `json:"response_bytes,omitempty"`
	RequestBody   string            `json:"request_body,omitempty"`
	ResponseBody  string            `json:"response_body,omitempty"`
	RequestHdrs   map[string]string `json:"request_headers,omitempty"`
	ResponseHdrs  map[string]string `json:"response_headers,omitempty"`
	Meta          map[string]string `json:"meta,omitempty"`
	Error         string            `json:"error,omitempty"`
	Canceling     bool              `json:"canceling,omitempty"`
}

type HTTPTraceFilter struct {
	SessionID  id.ID
	ChatID     id.ID
	ProviderID string
	Limit      int
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

type SessionDetail struct {
	Debug     SessionDebug
	Session   domain.Session
	Chats     []domain.Chat
	Timeline  []domain.TimelineItem
	Approvals []DebugApproval
	Plan      planning.Plan
	Todos     []planning.TodoItem
	Tasks     []planning.Task
}

type ChatDetail struct {
	Chat             SessionChatDebug
	Timeline         []domain.TimelineItem
	LatestCompaction map[string]any
	LatestUsage      map[string]any
}

type TranscriptOptions struct {
	Before id.ID
	Limit  int
	Tail   bool
	All    bool
}

type Source interface {
	DebugSessions(ctx context.Context, runtime RuntimeDebug) ([]SessionDebug, error)
	DebugSession(ctx context.Context, sessionID id.ID, runtime RuntimeDebug) (SessionDetail, error)
	DebugChat(ctx context.Context, sessionID, chatID id.ID, runtime RuntimeDebug) (ChatDetail, error)
	ChatTranscript(ctx context.Context, sessionID, chatID id.ID, opts TranscriptOptions) ([]domain.TimelineItem, error)
	DefaultTranscript(ctx context.Context, sessionID id.ID, opts TranscriptOptions) ([]domain.TimelineItem, error)
	SessionApprovals(ctx context.Context, sessionID id.ID) ([]DebugApproval, error)
	Milestones(ctx context.Context, sessionID id.ID) (planning.Plan, error)
	Todos(ctx context.Context, sessionID id.ID) ([]planning.TodoItem, error)
	Tasks(ctx context.Context, sessionID id.ID) ([]planning.Task, error)
	ResolveRewindAnchor(ctx context.Context, sessionID, chatID id.ID, selector string) (id.ID, error)
	RewindLiveChat(ctx context.Context, sessionID, chatID, anchorItemID id.ID) (any, error)
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
	activeHTTP    map[id.ID]activeHTTPTrace
}

type activeHTTPTrace struct {
	trace  ActiveHTTPTrace
	cancel context.CancelFunc
	start  time.Time
}

func NewRecorder() *Recorder {
	return &Recorder{
		maxEvents:     defaultMaxLogs,
		maxHTTP:       defaultMaxHTTP,
		clients:       map[string]ClientDebug{},
		chats:         map[id.ID]ChatDebug{},
		sessionEvents: map[id.ID][]RecordedEvent{},
		lastHTTPBody:  map[string]string{},
		activeHTTP:    map[id.ID]activeHTTPTrace{},
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
	trace.RequestBytes = len(trace.RequestBody)
	trace.ResponseBytes = len(trace.ResponseBody)
	trace.Meta = requestDiagnostics(trace, trace.Meta)
	trace.Meta = responseDiagnostics(trace, trace.Meta)
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
	trace.ResponseBody = truncate(trace.ResponseBody, 8192)
	trace.RequestHdrs = cloneMeta(trace.RequestHdrs)
	trace.ResponseHdrs = cloneMeta(trace.ResponseHdrs)
	r.httpTraces = appendHTTPTrace(r.httpTraces, trace, r.maxHTTP)
}

func (r *Recorder) StartHTTP(ctx context.Context, trace HTTPTrace) (context.Context, id.ID, func()) {
	if r == nil {
		return ctx, "", func() {}
	}
	child, cancel := context.WithCancel(ctx)
	requestID := id.New()
	now := time.Now().UTC()
	trace.Meta = requestDiagnostics(trace, trace.Meta)
	active := activeHTTPTrace{
		trace: ActiveHTTPTrace{
			ID:           requestID,
			Timestamp:    now,
			ProviderID:   trace.ProviderID,
			SessionID:    trace.SessionID,
			ChatID:       trace.ChatID,
			Method:       trace.Method,
			Path:         trace.Path,
			RequestBytes: len(trace.RequestBody),
			RequestBody:  trace.RequestBody,
			RequestHdrs:  cloneMeta(trace.RequestHdrs),
			Meta:         cloneMeta(trace.Meta),
		},
		cancel: cancel,
		start:  now,
	}
	r.mu.Lock()
	r.activeHTTP[requestID] = active
	r.mu.Unlock()
	return child, requestID, func() {
		r.mu.Lock()
		delete(r.activeHTTP, requestID)
		r.mu.Unlock()
		cancel()
	}
}

func (r *Recorder) UpdateActiveHTTP(requestID id.ID, trace HTTPTrace) {
	if r == nil || requestID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	active, ok := r.activeHTTP[requestID]
	if !ok {
		return
	}
	if trace.Status != 0 {
		active.trace.Status = trace.Status
	}
	if trace.ResponseBody != "" {
		active.trace.ResponseBody = truncate(trace.ResponseBody, 8192)
		active.trace.ResponseBytes = len(trace.ResponseBody)
	}
	if trace.ResponseHdrs != nil {
		active.trace.ResponseHdrs = cloneMeta(trace.ResponseHdrs)
	}
	if trace.Meta != nil {
		active.trace.Meta = mergeMeta(active.trace.Meta, trace.Meta)
	}
	if trace.Error != "" {
		active.trace.Error = trace.Error
	}
	active.trace.DurationMS = time.Since(active.start).Milliseconds()
	r.activeHTTP[requestID] = active
}

func (r *Recorder) ActiveHTTPTraces(filters ...HTTPTraceFilter) []ActiveHTTPTrace {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	filter := HTTPTraceFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	out := make([]ActiveHTTPTrace, 0, len(r.activeHTTP))
	for _, active := range r.activeHTTP {
		trace := active.trace
		trace.DurationMS = time.Since(active.start).Milliseconds()
		if !activeHTTPTraceMatches(trace, filter) {
			continue
		}
		out = append(out, cloneActiveHTTPTrace(trace))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[len(out)-filter.Limit:]
	}
	return out
}

func (r *Recorder) CancelActiveHTTP(requestID id.ID) bool {
	if r == nil || requestID == "" {
		return false
	}
	r.mu.Lock()
	active, ok := r.activeHTTP[requestID]
	if ok {
		active.trace.Canceling = true
		active.trace.DurationMS = time.Since(active.start).Milliseconds()
		r.activeHTTP[requestID] = active
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	active.cancel()
	return true
}

func (r *Recorder) CancelActiveHTTPTraces(filter HTTPTraceFilter) int {
	if r == nil {
		return 0
	}
	active := r.ActiveHTTPTraces(filter)
	count := 0
	for _, trace := range active {
		if r.CancelActiveHTTP(trace.ID) {
			count++
		}
	}
	return count
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

func responseDiagnostics(trace HTTPTrace, meta map[string]string) map[string]string {
	body := strings.TrimSpace(trace.ResponseBody)
	if body == "" {
		return cloneMeta(meta)
	}
	out := cloneMeta(meta)
	if out == nil {
		out = map[string]string{}
	}
	out["response_bytes"] = strconv.Itoa(len(body))
	if total, cache, processed, ok := parsePromptProgress(body); ok {
		out["prompt_progress_total"] = strconv.Itoa(total)
		out["prompt_progress_cache"] = strconv.Itoa(cache)
		out["prompt_progress_processed"] = strconv.Itoa(processed)
	}
	return out
}

func parsePromptProgress(body string) (int, int, int, bool) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var decoded struct {
			PromptProgress *struct {
				Total     int `json:"total"`
				Cache     int `json:"cache"`
				Processed int `json:"processed"`
			} `json:"prompt_progress"`
		}
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil || decoded.PromptProgress == nil {
			continue
		}
		return decoded.PromptProgress.Total, decoded.PromptProgress.Cache, decoded.PromptProgress.Processed, true
	}
	return 0, 0, 0, false
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

func (r *Recorder) HTTPTraces(filters ...HTTPTraceFilter) []HTTPTrace {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	filter := HTTPTraceFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	return cloneHTTPTraces(filterHTTPTraces(r.httpTraces, filter))
}

func (r *Recorder) SessionHTTPTraces(sessionID id.ID, limit int) []HTTPTrace {
	return r.HTTPTraces(HTTPTraceFilter{SessionID: sessionID, Limit: limit})
}

func (r *Recorder) ChatHTTPTraces(sessionID, chatID id.ID, limit int) []HTTPTrace {
	return r.HTTPTraces(HTTPTraceFilter{SessionID: sessionID, ChatID: chatID, Limit: limit})
}

type Server struct {
	source   Source
	recorder *Recorder
}

func NewServer(source Source, recorder *Recorder) *Server {
	if recorder == nil {
		recorder = NewRecorder()
	}
	return &Server{
		source:   source,
		recorder: recorder,
	}
}

func Handler(source Source, recorder *Recorder) http.Handler {
	mux := http.NewServeMux()
	NewServer(source, recorder).Register(mux)
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
		Summary: "Engine owns live sessions, sessions own live chats, and debug state is read through live snapshots plus recorder data.",
		Organization: []string{
			"debugsrv does not read persistence directly",
			"session and chat data comes from live owners",
			"client selection is grouped by selected session and selected chat",
		},
		DataSources: []string{
			"live snapshots: sessions, chats, timelines, milestones, tasks, queue, approvals, and context",
			"recorder: connected clients, HTTP traces, lifecycle events, and active provider requests",
		},
		MoreDataNeeded: []string{
			"offline persisted data inspection should use a separate repair/debug tool, not debugsrv",
		},
	}
}

func (s *Server) debugSessions(ctx context.Context) ([]SessionDebug, error) {
	source, err := s.sourceRequired()
	if err != nil {
		return nil, err
	}
	return source.DebugSessions(ctx, s.recorder.Runtime())
}

func (s *Server) sourceRequired() (Source, error) {
	if s == nil || s.source == nil {
		return nil, fmt.Errorf("debug source is unavailable")
	}
	return s.source, nil
}

func LatestCompactionDebug(timeline []domain.TimelineItem) map[string]any {
	for idx := len(timeline) - 1; idx >= 0; idx-- {
		compaction, ok := timeline[idx].Content.(domain.Compaction)
		if !ok {
			continue
		}
		return map[string]any{
			"item_id":               timeline[idx].ID,
			"seq":                   timeline[idx].Seq,
			"status":                compaction.Status,
			"trigger":               compaction.Trigger,
			"first_kept_item_id":    compaction.FirstKeptItemID,
			"before_context_tokens": compaction.BeforeContextTokens,
			"after_context_tokens":  compaction.AfterContextTokens,
			"summary_bytes":         len(compaction.Summary),
			"updated_at":            timeline[idx].UpdatedAt,
		}
	}
	return nil
}

func LatestUsageDebug(timeline []domain.TimelineItem) map[string]any {
	for idx := len(timeline) - 1; idx >= 0; idx-- {
		assistant, ok := timeline[idx].Content.(domain.AssistantMessage)
		if !ok || assistant.Usage == nil {
			continue
		}
		return map[string]any{
			"item_id":    timeline[idx].ID,
			"seq":        timeline[idx].Seq,
			"usage":      assistant.Usage.Normalized(),
			"updated_at": timeline[idx].UpdatedAt,
		}
	}
	return nil
}

func PendingApprovalsFromTimeline(chatRecord domain.Chat, items []domain.TimelineItem) []DebugApproval {
	var approvals []DebugApproval
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range assistant.Tools {
			if call.Status != domain.ToolStatusAwaitingApproval {
				continue
			}
			approvals = append(approvals, DebugApproval{
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
	return approvals
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

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	filter := httpTraceFilterFromQuery(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"debug_api": s.recorder.Runtime().Process.DebugAPI,
		"active":    s.recorder.ActiveHTTPTraces(filter),
		"traces":    s.recorder.HTTPTraces(filter),
	})
}

func httpTraceFilterFromQuery(r *http.Request) HTTPTraceFilter {
	if r == nil {
		return HTTPTraceFilter{}
	}
	query := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(query.Get("limit")))
	return HTTPTraceFilter{
		SessionID:  id.ID(strings.TrimSpace(query.Get("session_id"))),
		ChatID:     id.ID(strings.TrimSpace(query.Get("chat_id"))),
		ProviderID: strings.TrimSpace(query.Get("provider_id")),
		Limit:      limit,
	}
}

func transcriptOptionsFromRequest(r *http.Request) TranscriptOptions {
	if r == nil {
		return TranscriptOptions{}
	}
	query := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(query.Get("limit")))
	return TranscriptOptions{
		Before: id.ID(strings.TrimSpace(query.Get("before"))),
		Limit:  limit,
		Tail:   strings.EqualFold(strings.TrimSpace(query.Get("tail")), "true"),
		All:    strings.EqualFold(strings.TrimSpace(query.Get("all")), "true"),
	}
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
	case "chats":
		s.handleSessionChatRoutes(w, r, id.ID(sessionID), parts[2:])
	case "http":
		s.handleSessionHTTP(w, r, id.ID(sessionID))
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

func (s *Server) handleSessionChatRoutes(w http.ResponseWriter, r *http.Request, sessionID id.ID, parts []string) {
	if len(parts) == 1 {
		s.handleSessionChat(w, r, sessionID, id.ID(strings.TrimSpace(parts[0])))
		return
	}
	if len(parts) != 2 && len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	chatID := id.ID(strings.TrimSpace(parts[0]))
	if chatID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid chat id"))
		return
	}
	switch parts[1] {
	case "http":
		if len(parts) == 3 && parts[2] == "active" {
			s.handleSessionChatActiveHTTP(w, r, sessionID, chatID)
			return
		}
		if len(parts) == 2 {
			s.handleSessionChatHTTP(w, r, sessionID, chatID)
			return
		}
		http.NotFound(w, r)
	case "transcript":
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		s.handleChatTranscript(w, r, sessionID, chatID)
	case "rewind":
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		s.handleSessionChatRewind(w, r, sessionID, chatID)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSessionChatRewind(w http.ResponseWriter, r *http.Request, sessionID, chatID id.ID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	source, err := s.sourceRequired()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	var req struct {
		AnchorItemID id.ID  `json:"anchor_item_id"`
		Selector     string `json:"selector"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	anchorID := req.AnchorItemID
	if anchorID == "" {
		var err error
		anchorID, err = source.ResolveRewindAnchor(r.Context(), sessionID, chatID, req.Selector)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	result, err := source.RewindLiveChat(r.Context(), sessionID, chatID, anchorID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":     sessionID,
		"chat_id":        chatID,
		"anchor_item_id": anchorID,
		"result":         result,
	})
}

func (s *Server) handleSessionHTTP(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	filter := HTTPTraceFilter{SessionID: sessionID, Limit: limit}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"active":     s.recorder.ActiveHTTPTraces(filter),
		"traces":     s.recorder.SessionHTTPTraces(sessionID, limit),
	})
}

func (s *Server) handleSessionChatHTTP(w http.ResponseWriter, r *http.Request, sessionID, chatID id.ID) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	if _, err := source.DebugChat(r.Context(), sessionID, chatID, s.recorder.Runtime()); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	filter := HTTPTraceFilter{SessionID: sessionID, ChatID: chatID, Limit: limit}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"chat_id":    chatID,
		"active":     s.recorder.ActiveHTTPTraces(filter),
		"traces":     s.recorder.ChatHTTPTraces(sessionID, chatID, limit),
	})
}

func (s *Server) handleSessionChatActiveHTTP(w http.ResponseWriter, r *http.Request, sessionID, chatID id.ID) {
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	if _, err := source.DebugChat(r.Context(), sessionID, chatID, s.recorder.Runtime()); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	filter := HTTPTraceFilter{SessionID: sessionID, ChatID: chatID}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sessionID,
			"chat_id":    chatID,
			"active":     s.recorder.ActiveHTTPTraces(filter),
		})
	case http.MethodPost:
		var req struct {
			RequestID id.ID  `json:"request_id"`
			Action    string `json:"action"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		if action := strings.TrimSpace(req.Action); action != "" && action != "cancel" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported action %q", action))
			return
		}
		canceled := 0
		if req.RequestID != "" {
			active := s.recorder.ActiveHTTPTraces(filter)
			if !slices.ContainsFunc(active, func(trace ActiveHTTPTrace) bool { return trace.ID == req.RequestID }) {
				writeError(w, http.StatusNotFound, fmt.Errorf("active request %s not found for chat %s", req.RequestID, chatID))
				return
			}
			if s.recorder.CancelActiveHTTP(req.RequestID) {
				canceled = 1
			}
		} else {
			canceled = s.recorder.CancelActiveHTTPTraces(filter)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"session_id": sessionID,
			"chat_id":    chatID,
			"canceled":   canceled,
			"active":     s.recorder.ActiveHTTPTraces(filter),
		})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
	}
}

func (s *Server) handleSessionChat(w http.ResponseWriter, r *http.Request, sessionID, chatID id.ID) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	detail, err := source.DebugChat(r.Context(), sessionID, chatID, s.recorder.Runtime())
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":        sessionID,
		"chat_id":           chatID,
		"chat":              detail.Chat,
		"latest_compaction": detail.LatestCompaction,
		"latest_usage":      detail.LatestUsage,
		"http_traces":       s.recorder.ChatHTTPTraces(sessionID, chatID, 5),
	})
}

func (s *Server) handleChatTranscript(w http.ResponseWriter, r *http.Request, sessionID, chatID id.ID) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	timeline, err := source.ChatTranscript(r.Context(), sessionID, chatID, transcriptOptionsFromRequest(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"chat_id":    chatID,
		"timeline":   timeline,
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	detail, err := source.DebugSession(r.Context(), sessionID, s.recorder.Runtime())
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"architecture":   debugArchitecture(),
		"debug":          detail.Debug,
		"session":        detail.Session,
		"chats":          detail.Chats,
		"timeline":       detail.Timeline,
		"approvals":      detail.Approvals,
		"milestone_plan": detail.Plan,
		"todos":          detail.Todos,
		"events":         s.recorder.Events(sessionID),
	})
}

func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	timeline, err := source.DefaultTranscript(r.Context(), sessionID, transcriptOptionsFromRequest(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
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
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	timeline, err := source.DefaultTranscript(r.Context(), sessionID, transcriptOptionsFromRequest(r))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, analyzeSession(sessionID, timeline, s.recorder.Events(sessionID)))
}

func (s *Server) handleGlobalEvents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": "",
		"events":     s.recorder.Events(""),
	})
}

func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	approvals, err := source.SessionApprovals(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"approvals":  approvals,
	})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	tasks, err := source.Tasks(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"tasks":      tasks,
	})
}

func (s *Server) handleMilestones(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	plan, err := source.Milestones(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"plan":       plan,
	})
}

func (s *Server) handleTodos(w http.ResponseWriter, r *http.Request, sessionID id.ID) {
	source, sourceErr := s.sourceRequired()
	if sourceErr != nil {
		writeError(w, http.StatusServiceUnavailable, sourceErr)
		return
	}
	todos, err := source.Todos(r.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
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

func filterHTTPTraces(items []HTTPTrace, filter HTTPTraceFilter) []HTTPTrace {
	limit := normalizeHTTPTraceLimit(filter.Limit)
	out := make([]HTTPTrace, 0, min(len(items), limit))
	for idx := len(items) - 1; idx >= 0 && len(out) < limit; idx-- {
		item := items[idx]
		if filter.SessionID != "" && item.SessionID != filter.SessionID {
			continue
		}
		if filter.ChatID != "" && item.ChatID != filter.ChatID {
			continue
		}
		if strings.TrimSpace(filter.ProviderID) != "" && item.ProviderID != strings.TrimSpace(filter.ProviderID) {
			continue
		}
		out = append(out, item)
	}
	slices.Reverse(out)
	return out
}

func activeHTTPTraceMatches(item ActiveHTTPTrace, filter HTTPTraceFilter) bool {
	if filter.SessionID != "" && item.SessionID != filter.SessionID {
		return false
	}
	if filter.ChatID != "" && item.ChatID != filter.ChatID {
		return false
	}
	if strings.TrimSpace(filter.ProviderID) != "" && item.ProviderID != strings.TrimSpace(filter.ProviderID) {
		return false
	}
	return true
}

func normalizeHTTPTraceLimit(limit int) int {
	if limit <= 0 || limit > defaultMaxHTTP {
		return defaultMaxHTTP
	}
	return limit
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

func mergeMeta(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return cloneMeta(dst)
	}
	out := cloneMeta(dst)
	if out == nil {
		out = map[string]string{}
	}
	for key, value := range src {
		out[key] = value
	}
	return out
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

func cloneActiveHTTPTrace(item ActiveHTTPTrace) ActiveHTTPTrace {
	item.RequestHdrs = cloneMeta(item.RequestHdrs)
	item.ResponseHdrs = cloneMeta(item.ResponseHdrs)
	item.Meta = cloneMeta(item.Meta)
	return item
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
			if tool.Tool != "" {
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
