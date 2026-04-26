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

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
)

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
	Stored   StoredResultPayload
}

type Presentation struct {
	Title    string
	Subtitle string
	Preview  string
}

type Runtime struct {
	Workdir    string
	HTTPClient *http.Client
	Store      *store.Store
	SessionID  int64
}

type Tool interface {
	Kind() domain.ToolKind
	Definition(Runtime) (provider.ToolDefinition, bool)
	BypassesPermission() bool
	NormalizeArgs(map[string]string) (map[string]string, error)
	LegacyArgs(raw string) map[string]string
	Preview(req Request) string
	PresentationForPreview(preview string) Presentation
	Presentation(req Request) Presentation
	Execute(ctx context.Context, runtime Runtime, req Request) (Result, error)
	SummarizeResult(req Request, result Result) (summary string, body string)
	PersistResult(ctx context.Context, st *store.Store, sessionID int64, req Request, result Result) (<-chan domain.Event, error)
}

type Registry struct {
	runtime Runtime
}

var (
	regMu    sync.RWMutex
	registry = map[domain.ToolKind]Tool{}
	order    []domain.ToolKind
)

func Register(tool Tool) {
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
	order = append(order, kind)
}

func Lookup(kind domain.ToolKind) (Tool, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	tool, ok := registry[kind]
	return tool, ok
}

func NewRegistry(workdir string) *Registry {
	return &Registry{
		runtime: Runtime{
			Workdir:    workdir,
			HTTPClient: &http.Client{},
		},
	}
}

func (r *Registry) Execute(ctx context.Context, req Request) (Result, error) {
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return Result{}, err
	}
	return tool.Execute(ctx, r.runtime, req)
}

func (r *Registry) ExecuteWithSession(ctx context.Context, st *store.Store, sessionID int64, req Request) (Result, error) {
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return Result{}, err
	}
	runtime := r.runtime
	runtime.Store = st
	runtime.SessionID = sessionID
	return tool.Execute(ctx, runtime, req)
}

func (r *Registry) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req Request, result Result) (<-chan domain.Event, error) {
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
	return tool.PersistResult(ctx, st, sessionID, req, result)
}

func Definitions(runtime Runtime) []provider.ToolDefinition {
	regMu.RLock()
	kinds := slices.Clone(order)
	regMu.RUnlock()
	defs := make([]provider.ToolDefinition, 0, len(kinds))
	for _, kind := range kinds {
		tool, ok := Lookup(kind)
		if !ok {
			continue
		}
		def, enabled := tool.Definition(runtime)
		if enabled {
			defs = append(defs, def)
		}
	}
	return defs
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
		req.ToolCallID = "call_" + strings.ToLower(string(kind))
	}
	return req, nil
}

func RequestFromStored(kind domain.ToolKind, raw string) (Request, error) {
	args, err := decodeStringMap([]byte(raw))
	if err != nil {
		tool, ok := Lookup(kind)
		if !ok {
			return Request{}, fmt.Errorf("unsupported tool %q", kind)
		}
		args = tool.LegacyArgs(raw)
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
	return tool.Presentation(req)
}

func PresentationForTool(kind domain.ToolKind, preview string) Presentation {
	tool, ok := Lookup(kind)
	if !ok {
		return Presentation{Title: "Tool", Subtitle: strings.TrimSpace(preview), Preview: strings.TrimSpace(preview)}
	}
	return tool.PresentationForPreview(preview)
}

func SummarizeResult(req Request, result Result) (string, string) {
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return defaultSummary(req.Tool, result)
	}
	return tool.SummarizeResult(req, result)
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

func FunctionDefinition(kind domain.ToolKind, description, schema string) provider.ToolDefinition {
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        string(kind),
			Description: description,
			Parameters:  json.RawMessage(schema),
		},
	}
}

func PersistStandardResult(ctx context.Context, st *store.Store, sessionID int64, req Request, result Result) (<-chan domain.Event, error) {
	summary, body := SummarizeResult(req, result)
	msg, err := st.AddMessage(ctx, sessionID, domain.MessageRoleTool, summary)
	if err != nil {
		return nil, err
	}
	payload := req.Meta()
	for key, value := range result.Meta {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		payload[key] = value
	}
	payload = MetaWithStoredResult(payload, domain.PartKindToolOutput, req.Tool, StoredResultStatusOK, result.Stored)
	meta, _ := json.Marshal(payload)
	if _, err := st.AddPart(ctx, msg.ID, domain.PartKindToolOutput, body, string(meta)); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.DiffText) != "" {
		if _, err := st.AddPart(ctx, msg.ID, domain.PartKindDiff, result.DiffText, ""); err != nil {
			return nil, err
		}
	}
	return emitOnce(domain.Event{Kind: domain.EventKindToolResult, Text: body, Tool: req.Tool}), nil
}

func DefaultSummarizeResult(req Request, result Result) (string, string) {
	return defaultSummary(req.Tool, result)
}

func emitOnce(evt domain.Event) <-chan domain.Event {
	out := make(chan domain.Event, 1)
	out <- evt
	close(out)
	return out
}

func EmitOnce(evt domain.Event) <-chan domain.Event {
	return emitOnce(evt)
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
