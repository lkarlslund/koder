package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
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

type ChatControl interface {
	ListChats(context.Context, int64) ([]ChatStatus, error)
	StartDecomposition(context.Context, int64, int64, string, string) (ChatStatus, error)
	StartExecution(context.Context, int64, int64, string, string) (ChatStatus, error)
	PollChat(context.Context, int64, int64) (ChatStatus, error)
}

type Request struct {
	Tool       domain.ToolKind   `json:"tool"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Args       map[string]string `json:"-"`
}

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
	payload["tool"] = string(r.Tool)
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
		return fmt.Sprintf(`{"tool":"%s"}`, r.Tool)
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
	SessionID             int64
	ChatID                int64
	ChatRole              domain.WorkflowRole
	ActiveMilestoneRef    string
	AssignedTodoBucketRef string
	ChatControl           ChatControl
	Exec                  execruntime.Control
	MCP                   MCPExecutor
	EditForgiveness       int
}

type MCPExecutor interface {
	ExecuteTool(context.Context, string, string, map[string]any) (Result, error)
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
	PersistResult(ctx context.Context, st *store.Store, sessionID int64, req Request, result Result) (<-chan domain.Event, error)
}

type Registry struct {
	runtime Runtime
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
	if kind == "" {
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

func NewRegistry(workdir string) *Registry {
	return &Registry{
		runtime: Runtime{
			Workdir:         workdir,
			HTTPClient:      &http.Client{},
			EditForgiveness: config.Default().UI.EditForgiveness,
		},
	}
}

func (r *Registry) SetChatControl(control ChatControl) {
	r.runtime.ChatControl = control
}

func (r *Registry) SetExecControl(control execruntime.Control) {
	r.runtime.Exec = control
}

func (r *Registry) ExecControl() execruntime.Control {
	return r.runtime.Exec
}

func (r *Registry) SetMCP(executor MCPExecutor) {
	r.runtime.MCP = executor
}

func (r *Registry) SetEditForgiveness(level int) {
	r.runtime.EditForgiveness = config.NormalizeEditForgiveness(level)
}

func (r *Registry) Execute(ctx context.Context, req Request) (Result, error) {
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return Result{}, err
	}
	return tool.Execute(ctx, r.runtime, req)
}

func (r *Registry) ExecuteWithSession(ctx context.Context, st *store.Store, sessionID int64, req Request) (Result, error) {
	return r.ExecuteWithChat(ctx, st, sessionID, domain.Chat{}, req)
}

func (r *Registry) ExecuteWithChat(ctx context.Context, st *store.Store, sessionID int64, chat domain.Chat, req Request) (Result, error) {
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return Result{}, err
	}
	runtime := r.runtime
	runtime.Store = st
	runtime.SessionID = sessionID
	runtime.ChatID = chat.ID
	runtime.ChatRole = chat.WorkflowRole
	runtime.ActiveMilestoneRef = chat.ActiveMilestoneRef
	runtime.AssignedTodoBucketRef = chat.AssignedTodoBucketRef
	return tool.Execute(ctx, runtime, req)
}

func (r *Registry) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req Request, result Result) (<-chan domain.Event, error) {
	return r.PersistResultInChat(ctx, st, sessionID, 0, req, result)
}

func (r *Registry) PersistResultInChat(ctx context.Context, st *store.Store, sessionID, chatID int64, req Request, result Result) (<-chan domain.Event, error) {
	if req.Tool == "" {
		return nil, errors.New("tool is empty")
	}
	tool, ok := Lookup(req.Tool)
	if !ok {
		return nil, fmt.Errorf("unsupported tool %q", req.Tool)
	}
	if req.Args == nil {
		req.Args = map[string]string{}
	}
	ctx = WithChatID(ctx, chatID)
	if persister, ok := tool.(resultPersister); ok {
		return persister.PersistResult(ctx, st, sessionID, req, result)
	}
	return PersistStandardResult(ctx, st, sessionID, req, result)
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

func ParseProviderCall(call provider.ToolCall) (Request, error) {
	kind := domain.ToolKind(strings.TrimSpace(call.Function.Name))
	if kind == "" {
		return Request{}, fmt.Errorf("provider tool call missing function name")
	}
	args, err := decodeStringMap([]byte(call.Function.Arguments))
	if err != nil {
		return Request{}, fmt.Errorf("decode tool arguments for %s: %w", kind, err)
	}
	req := Request{
		Tool:       kind,
		ToolCallID: strings.TrimSpace(call.ID),
		Args:       args,
	}
	if req.ToolCallID == "" {
		return Request{}, fmt.Errorf("provider tool call for %s missing id", kind)
	}
	return req, nil
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
	req := Request{
		Tool:       domain.ToolKind(strings.TrimSpace(raw["tool"])),
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
		return string(req.Tool)
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
		if kind == "" {
			spec.Title = "Tool"
		} else {
			spec.Title = strings.ReplaceAll(string(kind), "_", " ")
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
			Name:      string(req.Tool),
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
			Name:        string(kind),
			Description: description,
			Parameters:  json.RawMessage(spec.Parameters),
		},
	}
}

func PersistStandardResult(ctx context.Context, st *store.Store, sessionID int64, req Request, result Result) (<-chan domain.Event, error) {
	_, body := SummarizeResult(req, result)
	chatID, ok := ChatIDFromContext(ctx)
	if !ok || chatID <= 0 {
		chat, err := st.DefaultChat(ctx, sessionID)
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
		item, err = st.AppendTimeline(ctx, chatID, domain.ToolExecution{
			Tool:   req.Tool,
			Args:   req.Meta(),
			Result: &toolResult,
		})
	} else {
		item, err = st.AttachToolResult(ctx, chatID, req.ToolCallID, toolResult)
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

func WithChatID(ctx context.Context, chatID int64) context.Context {
	if chatID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, chatIDContextKey{}, chatID)
}

func ChatIDFromContext(ctx context.Context) (int64, bool) {
	if ctx == nil {
		return 0, false
	}
	value, ok := ctx.Value(chatIDContextKey{}).(int64)
	if !ok || value <= 0 {
		return 0, false
	}
	return value, true
}

func RequireChatControl(runtime Runtime) (ChatControl, error) {
	if runtime.ChatControl == nil || runtime.SessionID == 0 || runtime.ChatID == 0 {
		return nil, errors.New("chat orchestration requires an active persisted chat")
	}
	return runtime.ChatControl, nil
}

func RequireExecControl(runtime Runtime) (execruntime.Control, error) {
	if runtime.Exec == nil || runtime.SessionID == 0 || runtime.ChatID == 0 {
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
	if req.Tool == "" {
		return Request{}, nil, errors.New("tool is empty")
	}
	tool, ok := Lookup(req.Tool)
	if !ok {
		return Request{}, nil, fmt.Errorf("unsupported tool %q", req.Tool)
	}
	if req.Args == nil {
		req.Args = map[string]string{}
	}
	args, err := tool.NormalizeArgs(req.Args)
	if err != nil {
		return Request{}, nil, err
	}
	req.Args = args
	return req, tool, nil
}

func defaultSummary(tool domain.ToolKind, result Result) (string, string) {
	output := strings.TrimSpace(result.Output)
	switch {
	case output != "":
		return string(tool), result.Output
	case strings.TrimSpace(result.DiffText) != "":
		body := fmt.Sprintf("%s completed and produced a diff", tool)
		return body, body
	default:
		body := fmt.Sprintf("%s completed with no output", tool)
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
