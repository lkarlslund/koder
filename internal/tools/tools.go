package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/toolkind"
)

type chatIDContextKey struct{}

type ChatRunState string

const (
	ChatRunStateIdle            ChatRunState = "idle"
	ChatRunStateRunning         ChatRunState = "running"
	ChatRunStateWaitingApproval ChatRunState = "waiting_approval"
	ChatRunStateCompleted       ChatRunState = "completed"
	ChatRunStateFailed          ChatRunState = "failed"
	ChatRunStateCancelled       ChatRunState = "cancelled"
)

type ChatStatus struct {
	Chat             domain.Chat
	State            ChatRunState
	Status           string
	Busy             bool
	PendingApprovals int
	LastError        string
	StatusText       string
}

type ChatStartRequest struct {
	Profile      domain.WorkflowRole
	Objective    string
	Title        string
	MilestoneRef string
	TodoRef      id.ID
}

type ChatUpdateRequest struct {
	Archived *bool
	Title    string
}

type ChatControl interface {
	ListChats(context.Context, id.ID) ([]ChatStatus, error)
	StartChat(context.Context, id.ID, id.ID, ChatStartRequest) (ChatStatus, error)
	PollChat(context.Context, id.ID, id.ID) (ChatStatus, error)
	UpdateChat(context.Context, id.ID, id.ID, ChatUpdateRequest) (ChatStatus, error)
}

type Request struct {
	Tool       domain.ToolKind   `json:"tool"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Args       map[string]string `json:"-"`
}

type ProviderCallError struct {
	Request Request
	Err     error
}

func (e ProviderCallError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e ProviderCallError) Unwrap() error { return e.Err }

func (r Request) MarshalJSON() ([]byte, error) {
	payload := r.Meta()
	return json.Marshal(payload)
}

func (r *Request) UnmarshalJSON(data []byte) error {
	raw, err := decodeStringMap(data)
	if err != nil {
		return err
	}
	req, err := RequestFromMetaMap(raw)
	if err != nil {
		return err
	}
	*r = req
	return nil
}

func (r Request) Meta() map[string]string {
	payload := make(map[string]string, len(r.Args)+2)
	payload["tool"] = r.Tool.String()
	if strings.TrimSpace(r.ToolCallID) != "" {
		payload["tool_call_id"] = r.ToolCallID
	}
	for key, value := range r.Args {
		if strings.TrimSpace(value) == "" {
			continue
		}
		payload[key] = value
	}
	return payload
}

func (r Request) ArgumentsJSON() string {
	if r.Args == nil {
		return "{}"
	}
	data, err := json.Marshal(r.Args)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (r Request) ContextString() string {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf(`{"tool":"%s"}`, r.Tool.String())
	}
	return string(data)
}

type Result struct {
	Output   string
	DiffText string
	Meta     map[string]string
	Stored   any
}

type Presentation struct {
	Title    string
	Subtitle string
	Preview  string
}

type Runtime struct {
	Workdir               string
	HTTPClient            *http.Client
	Store                 *store.Store
	SessionID             id.ID
	ChatID                id.ID
	ChatRole              domain.WorkflowRole
	ActiveMilestoneRef    string
	AssignedTodoBucketRef string
	AssignedTodoRef       id.ID
	ChatControl           ChatControl
	SessionControl        SessionControl
	TaskControl           TaskControl
	AllowedTools          map[domain.ToolKind]bool
	Exec                  execruntime.Control
	MCP                   MCPExecutor
	FileTracker           FileTracker
	AccessSettings        accesssettings.Settings
}

type MCPExecutor interface {
	ExecuteTool(context.Context, string, string, map[string]any) (Result, error)
}

type FileTracker interface {
	TouchFile(context.Context, string, string)
}

func (r Runtime) TouchFile(ctx context.Context, path, content string) {
	if r.FileTracker == nil || strings.TrimSpace(path) == "" {
		return
	}
	r.FileTracker.TouchFile(ctx, path, content)
}

type Tool interface {
	Kind() domain.ToolKind
	BypassesPermission() bool
	NormalizeArgs(map[string]string) (map[string]string, error)
	Preview(req Request) string
	Execute(ctx context.Context, runtime Runtime, req Request) (Result, error)
}

type Presenter interface {
	Presentation(req Request) Presentation
}

// ToolSpec describes a registered tool for local presentation and LLM exposure.
type ToolSpec struct {
	Title       string
	Description string
	Usage       string
	Parameters  string
	ExposeToLLM bool
}

type definitionProvider interface {
	Definition(Runtime, ToolSpec) (ToolSpec, bool)
}

type resultSummarizer interface {
	SummarizeResult(req Request, result Result) (summary string, body string)
}

type resultPersister interface {
	PersistResult(ctx context.Context, runtime Runtime, req Request, result Result) (<-chan domain.Event, error)
}

var (
	regMu    sync.RWMutex
	registry = map[domain.ToolKind]Tool{}
	specs    = map[domain.ToolKind]ToolSpec{}
	order    []domain.ToolKind
)

func Register(tool Tool, spec ToolSpec) {
	regMu.Lock()
	defer regMu.Unlock()
	kind := tool.Kind()
	if kind == 0 {
		panic("tools: empty tool kind")
	}
	if _, exists := registry[kind]; exists {
		panic(fmt.Sprintf("tools: duplicate tool registration %q", kind))
	}
	registry[kind] = tool
	specs[kind] = normalizeToolSpec(kind, spec)
	order = append(order, kind)
}

func Lookup(kind domain.ToolKind) (Tool, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	tool, ok := registry[kind]
	return tool, ok
}

func lookupWithSpec(kind domain.ToolKind) (Tool, ToolSpec, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	tool, ok := registry[kind]
	if !ok {
		return nil, ToolSpec{}, false
	}
	spec := specs[kind]
	return tool, spec, true
}

func Info(kind domain.ToolKind) ToolSpec {
	regMu.RLock()
	defer regMu.RUnlock()
	if spec, ok := specs[kind]; ok {
		return spec
	}
	return normalizeToolSpec(kind, ToolSpec{})
}

func Execute(ctx context.Context, runtime Runtime, req Request) (Result, error) {
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return Result{}, err
	}
	runtime = normalizeRuntime(runtime)
	if err := chatrole.CheckToolAllowed(runtime.ChatRole, req.Tool); err != nil {
		return Result{}, err
	}
	if err := checkRuntimeAccess(runtime, req); err != nil {
		return Result{}, err
	}
	return tool.Execute(ctx, runtime, req)
}

func checkRuntimeAccess(runtime Runtime, req Request) error {
	switch req.Tool {
	case domain.ToolKindWebFetch, domain.ToolKindWebSearch, domain.ToolKindMCP:
		return runtime.CheckNetworkAccess()
	case domain.ToolKindFileWrite, domain.ToolKindFileEdit:
		return checkRequestPath(runtime, req, accesssettings.AccessWrite)
	case domain.ToolKindFileRead, domain.ToolKindViewImage, domain.ToolKindShowImage, domain.ToolKindFileGlob, domain.ToolKindFileGrep, domain.ToolKindCodeSearch, domain.ToolKindLint:
		return checkRequestPath(runtime, req, accesssettings.AccessRead)
	default:
		return nil
	}
}

func checkRequestPath(runtime Runtime, req Request, kind accesssettings.AccessKind) error {
	path := strings.TrimSpace(FirstArg(req.Args, "path", "file", "file_path", "filepath", "root", "dir", "workdir"))
	if path == "" {
		path = "."
	}
	abs, _, err := ReadablePath(runtime.Workdir, path)
	if kind == accesssettings.AccessWrite {
		abs, _, err = WritablePath(runtime, path)
	}
	if err != nil {
		return err
	}
	return runtime.CheckPathAccess(kind, abs)
}

func normalizeRuntime(runtime Runtime) Runtime {
	if runtime.HTTPClient == nil {
		runtime.HTTPClient = &http.Client{}
	}
	if accesssettings.IsZero(runtime.AccessSettings) {
		runtime.AccessSettings = accesssettings.Default()
	} else {
		runtime.AccessSettings = accesssettings.Normalize(runtime.AccessSettings)
	}
	return runtime
}

func defaultChatForToolResult(ctx context.Context, st *store.Store, sessionID id.ID) (domain.Chat, error) {
	chats, err := toolChatCollection(st).List(ctx, store.ByIndex[domain.Chat]("session", string(sessionID)))
	if err != nil {
		return domain.Chat{}, err
	}
	slices.SortFunc(chats, func(a, b domain.Chat) int {
		switch {
		case a.Position < b.Position:
			return -1
		case a.Position > b.Position:
			return 1
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	for _, chat := range chats {
		if chat.ParentChatID == nil {
			return chat, nil
		}
	}
	if len(chats) > 0 {
		return chats[0], nil
	}
	return domain.Chat{}, fmt.Errorf("session %s has no chats", sessionID)
}

func toolChatCollection(st *store.Store) store.Collection[domain.Chat] {
	return store.NewCollection(st, store.CollectionSpec[domain.Chat]{
		Namespace: "chats",
		GetID:     func(v domain.Chat) string { return v.ID },
		SetID:     func(v *domain.Chat, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.Chat]{
			{Name: "session", Value: func(v domain.Chat) string { return v.SessionID }},
		},
	})
}

func toolTimelineCollection(st *store.Store) store.Collection[domain.TimelineItem] {
	return store.NewCollection(st, store.CollectionSpec[domain.TimelineItem]{
		Namespace: "timeline",
		GetID:     func(v domain.TimelineItem) string { return v.ID },
		SetID:     func(v *domain.TimelineItem, id string) { v.ID = id },
		Indexes: []store.IndexSpec[domain.TimelineItem]{
			{Name: "chat", Value: func(v domain.TimelineItem) string { return v.ChatID }},
		},
	})
}

func toolTimelineForChat(ctx context.Context, st *store.Store, chatID id.ID) ([]domain.TimelineItem, error) {
	items, err := toolTimelineCollection(st).List(ctx, store.ByIndex[domain.TimelineItem]("chat", string(chatID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b domain.TimelineItem) int {
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return items, nil
}

func toolAppendTimeline(ctx context.Context, st *store.Store, chatID id.ID, content domain.TimelineContent) (domain.TimelineItem, error) {
	items, err := toolTimelineForChat(ctx, st, chatID)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	now := time.Now().UTC()
	return toolTimelineCollection(st).Insert(ctx, domain.TimelineItem{
		ChatID:    chatID,
		Seq:       int64(len(items) + 1),
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func toolAttachToolResult(ctx context.Context, st *store.Store, chatID id.ID, toolCallID string, result domain.ToolResult) (domain.TimelineItem, error) {
	items, err := toolTimelineForChat(ctx, st, chatID)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	for idx := len(items) - 1; idx >= 0; idx-- {
		item := items[idx]
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		call := assistant.ToolByID(domain.ToolCallID(strings.TrimSpace(toolCallID)))
		if call == nil {
			continue
		}
		call.Result = &result
		call.Error = nil
		call.Approval = nil
		call.ApprovalID = ""
		if result.Status == domain.ToolResultStatusDenied {
			call.Status = domain.ToolStatusDenied
		} else {
			call.Status = domain.ToolStatusDone
		}
		if call.CompletedAt.IsZero() {
			call.CompletedAt = time.Now().UTC()
		}
		item.Content = assistant
		item.UpdatedAt = time.Now().UTC()
		if err := toolTimelineCollection(st).Put(ctx, item); err != nil {
			return domain.TimelineItem{}, err
		}
		return item, nil
	}
	return domain.TimelineItem{}, fmt.Errorf("tool call %q has no owning assistant item", toolCallID)
}

func (r Runtime) CheckNetworkAccess() error {
	return accesssettings.Allows(r.AccessSettings, accesssettings.Request{Kind: accesssettings.AccessNetwork})
}

func (r Runtime) CheckPathAccess(kind accesssettings.AccessKind, abs string) error {
	return accesssettings.Allows(r.AccessSettings, accesssettings.Request{Kind: kind, Path: abs, ProjectRoot: r.Workdir})
}

func (r Runtime) SessionTmpDir() string {
	if r.SessionID == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), "koder-session-tmp", string(r.SessionID))
}

func EnsureSessionTmpDir(settings accesssettings.Settings) error {
	settings = accesssettings.Normalize(settings)
	if settings.Tmp != accesssettings.TmpSession || strings.TrimSpace(settings.TmpDir) == "" {
		return nil
	}
	return os.MkdirAll(settings.TmpDir, 0o700)
}

func PersistResult(ctx context.Context, runtime Runtime, req Request, result Result) (<-chan domain.Event, error) {
	if req.Tool == 0 {
		return nil, errors.New("tool is empty")
	}
	tool, ok := Lookup(req.Tool)
	if !ok {
		return nil, fmt.Errorf("unsupported tool %q", req.Tool)
	}
	if req.Args == nil {
		req.Args = map[string]string{}
	}
	runtime = normalizeRuntime(runtime)
	ctx = WithChatID(ctx, runtime.ChatID)
	if persister, ok := tool.(resultPersister); ok {
		return persister.PersistResult(ctx, runtime, req, result)
	}
	return PersistStandardResult(ctx, runtime, req, result)
}

func Definitions(runtime Runtime) []provider.ToolDefinition {
	regMu.RLock()
	kinds := slices.Clone(order)
	regMu.RUnlock()
	defs := make([]provider.ToolDefinition, 0, len(kinds))
	for _, kind := range kinds {
		def, enabled := DefinitionFor(kind, runtime)
		if enabled {
			defs = append(defs, def)
		}
	}
	return defs
}

// DefinitionFor returns the provider tool definition for a registered tool.
func DefinitionFor(kind domain.ToolKind, runtime Runtime) (provider.ToolDefinition, bool) {
	tool, spec, ok := lookupWithSpec(kind)
	if !ok {
		return provider.ToolDefinition{}, false
	}
	if !chatrole.AllowsTool(runtime.ChatRole, kind) {
		return provider.ToolDefinition{}, false
	}
	if enabled, ok := runtime.AllowedTools[kind]; ok && !enabled {
		return provider.ToolDefinition{}, false
	}
	if dynamic, ok := tool.(definitionProvider); ok {
		var enabled bool
		spec, enabled = dynamic.Definition(runtime, spec)
		if !enabled {
			return provider.ToolDefinition{}, false
		}
	} else if !spec.ExposeToLLM {
		return provider.ToolDefinition{}, false
	}
	return providerDefinition(kind, spec), true
}

func ArgumentByteLimits() map[string]int {
	return map[string]int{
		domain.ToolKindFileWrite.String(): 64 * 1024,
		domain.ToolKindFileEdit.String():  32 * 1024,
		domain.ToolKindBash.String():      8 * 1024,
	}
}

func ParseProviderCall(call provider.ToolCall) (Request, error) {
	name := strings.TrimSpace(call.Function.Name)
	if name == "" {
		return Request{}, fmt.Errorf("provider tool call missing function name")
	}
	kind, err := toolkind.KindString(name)
	if err != nil {
		return Request{}, fmt.Errorf("unknown tool %q", name)
	}
	req := Request{
		Tool:       kind,
		ToolCallID: strings.TrimSpace(call.ID),
	}
	if req.ToolCallID == "" {
		return Request{}, fmt.Errorf("provider tool call for %s missing id", kind)
	}
	if limit := ArgumentByteLimits()[kind.String()]; limit > 0 && len(call.Function.Arguments) > limit {
		return Request{}, ProviderCallError{Request: req, Err: fmt.Errorf("%s tool arguments exceeded %s. Use smaller tool calls.", kind, formatArgumentByteLimit(limit))}
	}
	args, err := decodeStringMap([]byte(call.Function.Arguments))
	if err != nil {
		return Request{}, fmt.Errorf("decode tool arguments for %s: %w", kind, err)
	}
	req.Args = args
	normalized, err := Normalize(req)
	if err != nil {
		return Request{}, ProviderCallError{Request: req, Err: err}
	}
	return normalized, nil
}

func formatArgumentByteLimit(limit int) string {
	if limit > 0 && limit%1024 == 0 {
		return fmt.Sprintf("%d KiB", limit/1024)
	}
	return fmt.Sprintf("%d bytes", limit)
}

func RequestFromStored(kind domain.ToolKind, raw string) (Request, error) {
	args, err := decodeStringMap([]byte(raw))
	if err != nil {
		return Request{}, fmt.Errorf("decode stored tool arguments for %s: %w", kind, err)
	}
	req := Request{
		Tool:       kind,
		ToolCallID: strings.TrimSpace(args["tool_call_id"]),
		Args:       map[string]string{},
	}
	for key, value := range args {
		if key == "tool_call_id" {
			continue
		}
		req.Args[key] = value
	}
	return Normalize(req)
}

func RequestFromMeta(raw string) (Request, error) {
	if strings.TrimSpace(raw) == "" {
		return Request{}, errors.New("empty request metadata")
	}
	var req Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return Request{}, err
	}
	return Normalize(req)
}

func RequestFromMetaMap(raw map[string]string) (Request, error) {
	toolName := strings.TrimSpace(raw["tool"])
	if toolName == "" {
		return Request{}, fmt.Errorf("tool name is empty")
	}
	kind, err := toolkind.KindString(toolName)
	if err != nil {
		return Request{}, fmt.Errorf("unknown tool %q", toolName)
	}
	req := Request{
		Tool:       kind,
		ToolCallID: strings.TrimSpace(raw["tool_call_id"]),
		Args:       map[string]string{},
	}
	for key, value := range raw {
		if key == "tool" || key == "tool_call_id" {
			continue
		}
		req.Args[key] = value
	}
	return Normalize(req)
}

func Normalize(req Request) (Request, error) {
	req, _, err := normalizeRequest(req)
	return req, err
}

func Preview(req Request) string {
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return req.Tool.String()
	}
	return tool.Preview(req)
}

func PresentationForRequest(req Request) Presentation {
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return PresentationForTool(req.Tool, Preview(req))
	}
	if presenter, ok := tool.(Presenter); ok {
		return presenter.Presentation(req)
	}
	return SharedPresentation(req.Tool, tool.Preview(req))
}

func PresentationForTool(kind domain.ToolKind, preview string) Presentation {
	return SharedPresentation(kind, preview)
}

func SharedPresentation(kind domain.ToolKind, preview string) Presentation {
	preview = strings.TrimSpace(preview)
	return Presentation{Title: Info(kind).Title, Subtitle: preview, Preview: preview}
}

func normalizeToolSpec(kind domain.ToolKind, spec ToolSpec) ToolSpec {
	spec.Title = strings.TrimSpace(spec.Title)
	spec.Description = strings.TrimSpace(spec.Description)
	spec.Usage = strings.TrimSpace(spec.Usage)
	spec.Parameters = strings.TrimSpace(spec.Parameters)
	if spec.Title == "" {
		if kind == 0 {
			spec.Title = "Tool"
		} else {
			spec.Title = strings.ReplaceAll(kind.String(), "_", " ")
		}
	}
	return spec
}

func SummarizeResult(req Request, result Result) (string, string) {
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return defaultSummary(req.Tool, result)
	}
	if summarizer, ok := tool.(resultSummarizer); ok {
		return summarizer.SummarizeResult(req, result)
	}
	return defaultSummary(req.Tool, result)
}

func ToolCall(req Request) provider.ToolCall {
	return provider.ToolCall{
		ID:   req.ToolCallID,
		Type: "function",
		Function: provider.FunctionCall{
			Name:      req.Tool.String(),
			Arguments: req.ArgumentsJSON(),
		},
	}
}

func providerDefinition(kind domain.ToolKind, spec ToolSpec) provider.ToolDefinition {
	description := spec.Usage
	if description == "" {
		description = spec.Description
	}
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        kind.String(),
			Description: description,
			Parameters:  json.RawMessage(spec.Parameters),
		},
	}
}

func PersistStandardResult(ctx context.Context, runtime Runtime, req Request, result Result) (<-chan domain.Event, error) {
	_, body := SummarizeResult(req, result)
	st := runtime.Store
	sessionID := runtime.SessionID
	if st == nil {
		return nil, errors.New("persist tool result requires a chat runtime")
	}
	chatID, ok := ChatIDFromContext(ctx)
	if !ok || chatID == "" {
		chat, err := defaultChatForToolResult(ctx, st, sessionID)
		if err != nil {
			return nil, err
		}
		chatID = chat.ID
	}
	stored, err := domainToolResultPayload(req.Tool, domain.ToolResultStatusOK, result.Stored)
	if err != nil {
		return nil, err
	}
	toolResult := domain.ToolResult{
		Text:   body,
		Diff:   strings.TrimSpace(result.DiffText),
		Data:   stored,
		Status: domain.ToolResultStatusOK,
	}
	var item domain.TimelineItem
	if strings.TrimSpace(req.ToolCallID) == "" {
		item, err = toolAppendTimeline(ctx, st, chatID, domain.ToolExecution{
			Tool:   req.Tool,
			Args:   req.Meta(),
			Result: &toolResult,
		})
	} else {
		item, err = toolAttachToolResult(ctx, st, chatID, req.ToolCallID, toolResult)
	}
	if err != nil {
		return nil, err
	}
	return EmitOnce(domain.Event{Kind: domain.EventKindToolResult, Text: body, Tool: req.Tool, ToolCallID: req.ToolCallID, Item: item}), nil
}

func domainToolResultPayload(tool domain.ToolKind, status domain.ToolResultStatus, stored any) (domain.ToolResultPayload, error) {
	if stored == nil {
		return nil, nil
	}
	if payload, ok := stored.(domain.ToolResultPayload); ok {
		return payload, nil
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("marshal tool result %s: %w", tool, err)
	}
	return domain.DecodeToolResultPayload(tool, status, raw)
}

func WithChatID(ctx context.Context, chatID id.ID) context.Context {
	if chatID == "" {
		return ctx
	}
	return context.WithValue(ctx, chatIDContextKey{}, chatID)
}

func ChatIDFromContext(ctx context.Context) (id.ID, bool) {
	if ctx == nil {
		return "", false
	}
	value, ok := ctx.Value(chatIDContextKey{}).(id.ID)
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

func RequireChatControl(runtime Runtime) (ChatControl, error) {
	if runtime.ChatControl == nil || runtime.SessionID == "" || runtime.ChatID == "" {
		return nil, errors.New("chat orchestration requires an active persisted chat")
	}
	return runtime.ChatControl, nil
}

func RequireExecControl(runtime Runtime) (execruntime.Control, error) {
	if runtime.Exec == nil || runtime.SessionID == "" || runtime.ChatID == "" {
		return nil, errors.New("exec sessions require an active persisted chat")
	}
	return runtime.Exec, nil
}

func DefaultSummarizeResult(req Request, result Result) (string, string) {
	return defaultSummary(req.Tool, result)
}

func EmitOnce(evt domain.Event) <-chan domain.Event {
	out := make(chan domain.Event, 1)
	out <- evt
	close(out)
	return out
}

func normalizeRequest(req Request) (Request, Tool, error) {
	if req.Tool == 0 {
		return req, nil, errors.New("tool is empty")
	}
	tool, ok := Lookup(req.Tool)
	if !ok {
		return req, nil, fmt.Errorf("unsupported tool %q", req.Tool)
	}
	if req.Args == nil {
		req.Args = map[string]string{}
	}
	args, err := tool.NormalizeArgs(req.Args)
	if err != nil {
		return req, nil, err
	}
	req.Args = args
	return req, tool, nil
}

func defaultSummary(tool domain.ToolKind, result Result) (string, string) {
	output := strings.TrimSpace(result.Output)
	switch {
	case output != "":
		return tool.String(), result.Output
	case strings.TrimSpace(result.DiffText) != "":
		body := fmt.Sprintf("%s completed and produced a diff", tool.String())
		return body, body
	default:
		body := fmt.Sprintf("%s completed with no output", tool.String())
		return body, body
	}
}

func decodeStringMap(data []byte) (map[string]string, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return map[string]string{}, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		switch typed := value.(type) {
		case nil:
			continue
		case string:
			out[key] = typed
		case bool:
			if typed {
				out[key] = "true"
			} else {
				out[key] = "false"
			}
		case float64:
			out[key] = strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%f", typed), "0"), ".")
		default:
			encoded, err := json.Marshal(typed)
			if err != nil {
				return nil, err
			}
			out[key] = string(encoded)
		}
	}
	return out, nil
}
