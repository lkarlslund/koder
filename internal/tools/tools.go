package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/chatinteraction"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/offeredfile"
	"github.com/lkarlslund/koder/internal/provider"
)

type chatIDContextKey struct{}

type Request struct {
	Tool       ID                `json:"tool"`
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

type DeniedError struct {
	Tool   ID
	Reason string
}

func (e DeniedError) Error() string {
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "denied"
	}
	if e.Tool == "" {
		return reason
	}
	return fmt.Sprintf("%s: %s", e.Tool, reason)
}

func IsDenied(err error) bool {
	var denied DeniedError
	return errors.As(err, &denied)
}

func DeniedMessage(err error) string {
	var denied DeniedError
	if errors.As(err, &denied) {
		return denied.Error()
	}
	return ""
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

// Identity returns the permission and observability identity for a request.
// Resource tools include their selected action so materially different effects
// remain distinguishable even though they share one model-facing tool name.
func (r Request) Identity() string {
	tool := strings.TrimSpace(r.Tool.String())
	action := strings.TrimSpace(r.Args["action"])
	if tool == "" || action == "" {
		return tool
	}
	return tool + "." + action
}

type Result = domain.ToolResult

type Presentation struct {
	Title    string
	Subtitle string
	Preview  string
}

type Runtime struct {
	Workdir               string
	HTTPClient            *http.Client
	SessionID             id.ID
	SessionKind           domain.SessionKind
	ChatID                id.ID
	ChatRole              chatrole.Role
	InteractionMode       chatinteraction.Mode
	ActiveMilestoneKey    string
	AssignedTaskBucketKey string
	AssignedTaskRef       string
	SessionControl        SessionControl
	TaskControl           TaskControl
	ChatStatusControl     ChatStatusControl
	Services              map[string]any
	AllowedTools          map[ID]bool
	ManagedSkillsDir      string
	DisabledSkillPaths    []string
	SkillCatalogMaxChars  int
	Exec                  execruntime.Control
	MCP                   MCPExecutor
	Browser               browserapi.Service
	Attachments           *attachment.Manager
	OfferedFiles          *offeredfile.Manager
	FileTracker           FileTracker
	AccessSettings        accesssettings.Settings
}

// VoiceInteraction reports whether tools are being offered to a voice
// conversation. The legacy role fallback keeps stored/test callers compatible
// while voice migrates to an orthogonal interaction mode.
func (r Runtime) VoiceInteraction() bool {
	return r.InteractionMode == chatinteraction.Voice || (r.InteractionMode == "" && r.ChatRole == chatrole.Voice)
}

type Options struct {
	Runtime Runtime
	Request Request
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
	ID() ID
	BypassesPermission() bool
	NormalizeArgs(map[string]string) (map[string]string, error)
	Preview(req Request) string
	Call(ctx context.Context, options Options) (Result, error)
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
	// Legacy keeps a tool executable for stored transcripts and compatibility
	// while excluding its definition from new model requests.
	Legacy bool
}

type definitionProvider interface {
	Definition(Runtime, ToolSpec) (ToolSpec, bool)
}

type resultSummarizer interface {
	SummarizeResult(req Request, result Result) (summary string, body string)
}

type resultFinalizer interface {
	FinalizeResult(ctx context.Context, runtime Runtime, req Request, result Result) (Result, error)
}

var (
	regMu    sync.RWMutex
	registry = map[ID]Tool{}
	specs    = map[ID]ToolSpec{}
	order    []ID
)

func Register(tool Tool, spec ToolSpec) {
	regMu.Lock()
	defer regMu.Unlock()
	toolID := tool.ID()
	if toolID == "" {
		panic("tools: empty tool id")
	}
	if _, exists := registry[toolID]; exists {
		panic(fmt.Sprintf("tools: duplicate tool registration %q", toolID))
	}
	registry[toolID] = tool
	specs[toolID] = normalizeToolSpec(toolID, spec)
	order = append(order, toolID)
}

func Lookup(kind ID) (Tool, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	tool, ok := registry[kind]
	return tool, ok
}

func lookupWithSpec(kind ID) (Tool, ToolSpec, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	tool, ok := registry[kind]
	if !ok {
		return nil, ToolSpec{}, false
	}
	spec := specs[kind]
	return tool, spec, true
}

func Info(kind ID) ToolSpec {
	regMu.RLock()
	defer regMu.RUnlock()
	if spec, ok := specs[kind]; ok {
		return spec
	}
	return normalizeToolSpec(kind, ToolSpec{})
}

func RegisteredIDs() []ID {
	regMu.RLock()
	defer regMu.RUnlock()
	return slices.Clone(order)
}

func Call(ctx context.Context, options Options) (Result, error) {
	runtime, req := options.Runtime, options.Request
	req, tool, err := normalizeRequest(req)
	if err != nil {
		return Result{}, err
	}
	runtime = normalizeRuntime(runtime)
	if err := checkSessionToolAllowed(runtime, req.Tool); err != nil {
		return Result{}, err
	}
	if err := chatrole.CheckToolAllowed(runtime.ChatRole, req.Tool); err != nil {
		return Result{}, DeniedError{Tool: req.Tool, Reason: err.Error()}
	}
	if !chatinteraction.AllowsTool(runtime.InteractionMode, req.Tool) {
		return Result{}, DeniedError{Tool: req.Tool, Reason: fmt.Sprintf("%s is not available in %s interaction mode", req.Tool, runtime.InteractionMode)}
	}
	if err := checkToolEnabled(runtime, req.Tool); err != nil {
		return Result{}, err
	}
	if err := checkRuntimeAccess(runtime, req); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{}, err
		}
		return Result{}, DeniedError{Tool: req.Tool, Reason: err.Error()}
	}
	return tool.Call(ctx, Options{Runtime: runtime, Request: req})
}

func checkToolEnabled(runtime Runtime, kind ID) error {
	if enabled, ok := runtime.AllowedTools[kind]; ok && !enabled {
		return DeniedError{Tool: kind, Reason: "disabled for this session"}
	}
	return nil
}

func checkSessionToolAllowed(runtime Runtime, kind ID) error {
	if runtime.SessionKind == domain.SessionKindQuick && isMilestoneOrTaskTool(kind) {
		return DeniedError{Tool: kind, Reason: "milestone and task tools are not available in quick chats"}
	}
	if runtime.ChatRole != chatrole.Execution {
		return nil
	}
	if strings.TrimSpace(runtime.AssignedTaskRef) != "" {
		if isMilestoneTool(kind) {
			return DeniedError{Tool: kind, Reason: "milestone tools are not available to a task-assigned execution chat"}
		}
		return nil
	}
	if AssignedMilestoneKey(runtime) != "" {
		return nil
	}
	if isMilestoneOrTaskTool(kind) {
		return DeniedError{Tool: kind, Reason: "milestone and task tools require an assigned execution chat"}
	}
	return nil
}

func isMilestoneOrTaskTool(kind ID) bool {
	return isMilestoneTool(kind) || isTaskTool(kind)
}

func isMilestoneTool(kind ID) bool {
	switch kind {
	case Milestones, MilestoneList, MilestoneAdd, MilestoneUpdate, MilestoneDepend,
		MilestoneArchive, MilestoneDelete, MilestonePlan, MilestoneWrite:
		return true
	default:
		return false
	}
}

func isTaskTool(kind ID) bool {
	switch kind {
	case Task, Tasks, TaskList, TaskAddItems, TaskUpdateItem, TaskFetchNext,
		TaskArchive, TaskDelete, TasksAdd, TasksUpdate:
		return true
	default:
		return false
	}
}

func checkRuntimeAccess(runtime Runtime, req Request) error {
	switch req.Tool {
	case WebFetch, WebSearch, MCP,
		BrowserTabNew, BrowserTabClaim, BrowserTabSelect, BrowserTabClose,
		BrowserNavigate, BrowserBack, BrowserForward, BrowserReload,
		BrowserClick, BrowserFill, BrowserType, BrowserPress, BrowserSelect, BrowserCheck, BrowserUncheck,
		BrowserHover, BrowserDrag, BrowserScroll:
		return runtime.CheckNetworkAccess()
	case BrowserStatus, BrowserTabList, BrowserSnapshot, BrowserFind, BrowserWait, BrowserEvaluate, BrowserScreenshot, BrowserImage,
		BrowserPDF, BrowserConsole, BrowserRequests, BrowserRequest, BrowserResponseBody, BrowserDownloads,
		BrowserDownload:
		if err := runtime.CheckNetworkAccess(); err != nil {
			return err
		}
		if strings.TrimSpace(req.Args["save_to_file"]) == "" {
			return nil
		}
		return checkRequestOutputPath(runtime, req.Args["save_to_file"])
	case BrowserUpload:
		if err := runtime.CheckNetworkAccess(); err != nil {
			return err
		}
		return checkBrowserUploadPaths(runtime, req)
	case FileWrite, FileEdit, PhonePhotoTransfer:
		return checkRequestPath(runtime, req, accesssettings.AccessWrite)
	case FileRead, ViewImage, ViewPDF, ShowImage, ShowMedia, OfferFile, FileGlob, FileGrep, CodeSearch, Lint:
		return checkRequestPath(runtime, req, accesssettings.AccessRead)
	default:
		return nil
	}
}

func checkRequestOutputPath(runtime Runtime, path string) error {
	_, _, err := ResolvePath(runtime, path, accesssettings.AccessWrite)
	return err
}

func checkBrowserUploadPaths(runtime Runtime, req Request) error {
	var paths []string
	if err := json.Unmarshal([]byte(req.Args["paths"]), &paths); err != nil {
		return fmt.Errorf("decode browser upload paths: %w", err)
	}
	for _, path := range paths {
		if _, _, err := ResolvePath(runtime, path, accesssettings.AccessRead); err != nil {
			return err
		}
	}
	return nil
}

func checkRequestPath(runtime Runtime, req Request, kind accesssettings.AccessKind) error {
	path := strings.TrimSpace(req.Args["path"])
	if req.Tool == Bash || req.Tool == ExecCommand {
		path = strings.TrimSpace(req.Args["workdir"])
	}
	if path == "" {
		path = "."
	}
	_, _, err := ResolvePath(runtime, path, kind)
	return err
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

func (r Runtime) CheckNetworkAccess() error {
	return accesssettings.Allows(r.AccessSettings, accesssettings.Request{Kind: accesssettings.AccessNetwork})
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

func FinalizeResult(ctx context.Context, runtime Runtime, req Request, result Result) (domain.ToolResult, string, error) {
	if req.Tool == "" {
		return domain.ToolResult{}, "", errors.New("tool is empty")
	}
	tool, ok := Lookup(req.Tool)
	if !ok {
		return domain.ToolResult{}, "", fmt.Errorf("unsupported tool %q", req.Tool)
	}
	if req.Args == nil {
		req.Args = map[string]string{}
	}
	runtime = normalizeRuntime(runtime)
	if finalizer, ok := tool.(resultFinalizer); ok {
		var err error
		result, err = finalizer.FinalizeResult(ctx, runtime, req, result)
		if err != nil {
			return domain.ToolResult{}, "", err
		}
	}
	return BuildToolResult(req, result)
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
func DefinitionFor(kind ID, runtime Runtime) (provider.ToolDefinition, bool) {
	tool, spec, ok := lookupWithSpec(kind)
	if !ok {
		return provider.ToolDefinition{}, false
	}
	if spec.Legacy {
		return provider.ToolDefinition{}, false
	}
	if checkSessionToolAllowed(runtime, kind) != nil {
		return provider.ToolDefinition{}, false
	}
	if !chatrole.AllowsTool(runtime.ChatRole, kind) {
		return provider.ToolDefinition{}, false
	}
	if !chatinteraction.AllowsTool(runtime.InteractionMode, kind) {
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

// ActionRoute maps one model-facing resource action to an existing operation.
// Keeping the operation as a normal registered tool preserves its validation,
// policy, result, and stored-history behavior during migrations.
type ActionRoute struct {
	Action    string
	Tool      ID
	FixedArgs map[string]string
}

// ActionTool exposes a coherent resource API while delegating each action to
// an existing operation handler. Its Parameters schema must contain an action
// string enum; unavailable actions are removed from that enum at definition
// time according to role, interaction mode, session settings, and runtime
// availability.
type ActionTool struct {
	Kind              ID
	Routes            []ActionRoute
	BypassPermissions bool
	// RequirePersistedChat hides and rejects resources whose results need a
	// durable client-visible destination.
	RequirePersistedChat bool
	// DefaultAction accepts historical calls that predate the action field when
	// a canonical resource reuses an older single-operation name.
	DefaultAction string
}

func (t ActionTool) ID() ID { return t.Kind }

func (t ActionTool) BypassesPermission() bool { return t.BypassPermissions }

func (t ActionTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	action := strings.TrimSpace(args["action"])
	if action == "" {
		action = strings.TrimSpace(t.DefaultAction)
	}
	legacy, ok := t.legacyTool(action)
	if !ok {
		return nil, fmt.Errorf("unsupported %s action %q", t.Kind, action)
	}
	delegate, _, ok := lookupWithSpec(legacy)
	if !ok {
		return nil, fmt.Errorf("%s action %q is unavailable", t.Kind, action)
	}
	delegatedArgs := maps.Clone(args)
	delete(delegatedArgs, "action")
	for key, value := range t.routeFixedArgs(action) {
		delegatedArgs[key] = value
	}
	normalized, err := delegate.NormalizeArgs(delegatedArgs)
	if err != nil {
		return nil, err
	}
	normalized["action"] = action
	return normalized, nil
}

func (t ActionTool) Preview(req Request) string {
	legacyReq, delegate, err := t.delegatedRequest(req)
	if err != nil {
		return strings.ReplaceAll(req.Args["action"], "_", " ")
	}
	return delegate.Preview(legacyReq)
}

func (t ActionTool) Presentation(req Request) Presentation {
	legacyReq, delegate, err := t.delegatedRequest(req)
	if err != nil {
		return SharedPresentation(t.Kind, t.Preview(req))
	}
	if presenter, ok := delegate.(Presenter); ok {
		return presenter.Presentation(legacyReq)
	}
	return SharedPresentation(legacyReq.Tool, delegate.Preview(legacyReq))
}

func (t ActionTool) SummarizeResult(req Request, result Result) (string, string) {
	legacyReq, delegate, err := t.delegatedRequest(req)
	if err != nil {
		return defaultSummary(t.Kind, result)
	}
	if summarizer, ok := delegate.(resultSummarizer); ok {
		return summarizer.SummarizeResult(legacyReq, result)
	}
	return defaultSummary(legacyReq.Tool, result)
}

func (t ActionTool) Call(ctx context.Context, opts Options) (Result, error) {
	if t.RequirePersistedChat && (opts.Runtime.SessionID == "" || opts.Runtime.ChatID == "") {
		return Result{}, fmt.Errorf("%s requires an active persisted chat with a presentation destination", t.Kind)
	}
	legacyReq, _, err := t.delegatedRequest(opts.Request)
	if err != nil {
		return Result{}, err
	}
	return Call(ctx, Options{Runtime: opts.Runtime, Request: legacyReq})
}

func (t ActionTool) FinalizeResult(ctx context.Context, runtime Runtime, req Request, result Result) (Result, error) {
	legacyReq, delegate, err := t.delegatedRequest(req)
	if err != nil {
		return Result{}, err
	}
	if finalizer, ok := delegate.(resultFinalizer); ok {
		return finalizer.FinalizeResult(ctx, runtime, legacyReq, result)
	}
	return result, nil
}

func (t ActionTool) Definition(runtime Runtime, spec ToolSpec) (ToolSpec, bool) {
	if t.RequirePersistedChat && (runtime.SessionID == "" || runtime.ChatID == "") {
		return ToolSpec{}, false
	}
	actions := make([]string, 0, len(t.Routes))
	for _, route := range t.Routes {
		if legacyOperationAvailable(route.Tool, runtime) {
			actions = append(actions, route.Action)
		}
	}
	if len(actions) == 0 {
		return ToolSpec{}, false
	}
	parameters, err := schemaWithActionEnum(spec.Parameters, actions)
	if err != nil {
		return ToolSpec{}, false
	}
	spec.Parameters = parameters
	return spec, true
}

func (t ActionTool) legacyTool(action string) (ID, bool) {
	for _, route := range t.Routes {
		if route.Action == action {
			return route.Tool, true
		}
	}
	return "", false
}

func (t ActionTool) routeFixedArgs(action string) map[string]string {
	for _, route := range t.Routes {
		if route.Action == action {
			return route.FixedArgs
		}
	}
	return nil
}

func (t ActionTool) delegatedRequest(req Request) (Request, Tool, error) {
	action := strings.TrimSpace(req.Args["action"])
	if action == "" {
		action = strings.TrimSpace(t.DefaultAction)
	}
	legacy, ok := t.legacyTool(action)
	if !ok {
		return Request{}, nil, fmt.Errorf("unsupported %s action %q", t.Kind, action)
	}
	delegate, _, ok := lookupWithSpec(legacy)
	if !ok {
		return Request{}, nil, fmt.Errorf("%s action %q is unavailable", t.Kind, req.Args["action"])
	}
	args := maps.Clone(req.Args)
	delete(args, "action")
	for key, value := range t.routeFixedArgs(action) {
		args[key] = value
	}
	return Request{Tool: legacy, ToolCallID: req.ToolCallID, Args: args}, delegate, nil
}

// CanonicalRequest translates a historical operation call into the current
// resource/action shape for provider replay. Persisted data and execution keep
// their original identity; only newly built model context is canonicalized.
func CanonicalRequest(req Request) Request {
	if req.Args == nil {
		req.Args = map[string]string{}
	}
	if canonical, ok := canonicalPhoneRequest(req); ok {
		return canonical
	}
	if req.Tool == Bash {
		canonical := req
		canonical.Tool = ExecCommand
		canonical.Args = maps.Clone(req.Args)
		if command := strings.TrimSpace(canonical.Args["command"]); command != "" {
			canonical.Args["cmd"] = command
		}
		delete(canonical.Args, "command")
		return canonical
	}

	regMu.RLock()
	registered := make([]Tool, 0, len(order))
	for _, kind := range order {
		registered = append(registered, registry[kind])
	}
	regMu.RUnlock()
	for _, registeredTool := range registered {
		actionTool, ok := registeredTool.(ActionTool)
		if !ok {
			continue
		}
		if req.Tool == actionTool.Kind && strings.TrimSpace(req.Args["action"]) == "" && actionTool.DefaultAction != "" {
			canonical := req
			canonical.Args = maps.Clone(req.Args)
			canonical.Args["action"] = actionTool.DefaultAction
			return canonical
		}
		if canonical, ok := actionTool.canonicalizeLegacy(req); ok {
			return canonical
		}
	}
	return req
}

func (t ActionTool) canonicalizeLegacy(req Request) (Request, bool) {
	best := -1
	for index, route := range t.Routes {
		if route.Tool != req.Tool || !fixedArgsMatch(req.Args, route.FixedArgs) {
			continue
		}
		if best < 0 || len(route.FixedArgs) > len(t.Routes[best].FixedArgs) {
			best = index
		}
	}
	if best < 0 {
		return Request{}, false
	}
	route := t.Routes[best]
	canonical := req
	canonical.Tool = t.Kind
	canonical.Args = maps.Clone(req.Args)
	for key := range route.FixedArgs {
		delete(canonical.Args, key)
	}
	canonical.Args["action"] = route.Action
	return canonical, true
}

func fixedArgsMatch(args, fixed map[string]string) bool {
	for key, value := range fixed {
		if strings.TrimSpace(args[key]) != value {
			return false
		}
	}
	return true
}

func canonicalPhoneRequest(req Request) (Request, bool) {
	if req.Tool != Phone {
		return Request{}, false
	}
	action := strings.TrimSpace(req.Args["action"])
	target, canonicalAction := ID(""), ""
	switch action {
	case "device_status":
		target, canonicalAction = PhoneDevice, "status"
	case "get_location":
		target, canonicalAction = PhoneLocation, "get"
	case "search_contacts":
		target, canonicalAction = PhoneContacts, "search"
	case "create_contact":
		target, canonicalAction = PhoneContacts, "create"
	case "edit_contact":
		target, canonicalAction = PhoneContacts, "edit"
	case "upcoming_calendar":
		target, canonicalAction = PhoneCalendar, "list"
	case "create_calendar_event":
		target, canonicalAction = PhoneCalendar, "create"
	case "edit_calendar_event":
		target, canonicalAction = PhoneCalendar, strings.TrimSpace(req.Args["operation"])
		if canonicalAction != "cancel" {
			canonicalAction = "edit"
		}
	case "search_sms":
		target, canonicalAction = PhoneMessages, "search"
	case "send_sms":
		target, canonicalAction = PhoneMessages, "send"
	case "search_call_history":
		target, canonicalAction = PhoneCalls, "history"
	case "place_call":
		target, canonicalAction = PhoneCalls, "place"
	case "recent_notifications":
		target, canonicalAction = PhoneNotifications, "list"
	case "set_alarm":
		target, canonicalAction = PhoneClock, "set_alarm"
	case "set_timer":
		target, canonicalAction = PhoneClock, "set_timer"
	case "read_clipboard":
		target, canonicalAction = PhoneClipboard, "read"
	case "write_clipboard":
		target, canonicalAction = PhoneClipboard, "write"
	case "list_apps":
		target, canonicalAction = PhoneApps, "list"
	case "open_app":
		target, canonicalAction = PhoneApps, "open"
	case "media_control":
		target, canonicalAction = PhoneMedia, strings.TrimSpace(req.Args["media_action"])
	case "compose_email":
		target, canonicalAction = PhoneShare, "compose_email"
	case "share_text":
		target, canonicalAction = PhoneShare, "share_text"
	case "open_map":
		target, canonicalAction = PhoneOpen, "map"
	case "open_url":
		target, canonicalAction = PhoneOpen, "url"
	case "phone_photos_search":
		target, canonicalAction = PhonePhotos, "search"
	case "phone_photos_thumbs":
		target, canonicalAction = PhonePhotos, "thumbnails"
	case "phone_photo_view":
		target, canonicalAction = PhonePhotos, "view"
	case "phone_photo_transfer":
		target, canonicalAction = PhonePhotos, "transfer"
	default:
		return Request{}, false
	}
	if canonicalAction == "" {
		return Request{}, false
	}
	canonical := req
	canonical.Tool = target
	canonical.Args = maps.Clone(req.Args)
	canonical.Args["action"] = canonicalAction
	delete(canonical.Args, "operation")
	delete(canonical.Args, "media_action")
	return canonical, true
}

func legacyOperationAvailable(kind ID, runtime Runtime) bool {
	tool, spec, ok := lookupWithSpec(kind)
	if !ok || checkSessionToolAllowed(runtime, kind) != nil || !chatrole.AllowsTool(runtime.ChatRole, kind) || !chatinteraction.AllowsTool(runtime.InteractionMode, kind) {
		return false
	}
	if enabled, ok := runtime.AllowedTools[kind]; ok && !enabled {
		return false
	}
	if dynamic, ok := tool.(definitionProvider); ok {
		_, enabled := dynamic.Definition(runtime, spec)
		return enabled
	}
	return true
}

func schemaWithActionEnum(raw string, actions []string) (string, error) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return "", fmt.Errorf("decode action tool schema: %w", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return "", errors.New("action tool schema has no properties")
	}
	action, ok := properties["action"].(map[string]any)
	if !ok {
		return "", errors.New("action tool schema has no action property")
	}
	action["enum"] = actions
	data, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("encode action tool schema: %w", err)
	}
	return string(data), nil
}

func ArgumentByteLimits() map[string]int {
	return map[string]int{
		FileWrite.String():   64 * 1024,
		FileEdit.String():    32 * 1024,
		Bash.String():        8 * 1024,
		ExecCommand.String(): 8 * 1024,
		Knowledge.String():   128 * 1024,
	}
}

func ParseProviderCall(call provider.ToolCall) (Request, error) {
	name := strings.TrimSpace(call.Function.Name)
	if name == "" {
		return Request{}, fmt.Errorf("provider tool call missing function name")
	}
	kind := ID(name)
	req := Request{
		Tool:       kind,
		ToolCallID: strings.TrimSpace(call.ID),
	}
	if req.ToolCallID == "" {
		return Request{}, fmt.Errorf("provider tool call for %s missing id", kind)
	}
	if limit := ArgumentByteLimits()[kind.String()]; limit > 0 && len(call.Function.Arguments) > limit {
		return Request{}, ProviderCallError{Request: req, Err: fmt.Errorf("%s tool arguments exceeded %s; use smaller tool calls", kind, formatArgumentByteLimit(limit))}
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

func RequestFromStored(kind ID, raw string) (Request, error) {
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
	kind := ID(toolName)
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

func PresentationForTool(kind ID, preview string) Presentation {
	return SharedPresentation(kind, preview)
}

func SharedPresentation(kind ID, preview string) Presentation {
	preview = strings.TrimSpace(preview)
	return Presentation{Title: Info(kind).Title, Subtitle: preview, Preview: preview}
}

func normalizeToolSpec(kind ID, spec ToolSpec) ToolSpec {
	spec.Title = strings.TrimSpace(spec.Title)
	spec.Description = strings.TrimSpace(spec.Description)
	spec.Usage = strings.TrimSpace(spec.Usage)
	spec.Parameters = strings.TrimSpace(spec.Parameters)
	if spec.Title == "" {
		if kind == "" {
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

func providerDefinition(kind ID, spec ToolSpec) provider.ToolDefinition {
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

func BuildToolResult(req Request, result Result) (domain.ToolResult, string, error) {
	_, body := SummarizeResult(req, result)
	status := result.Status
	if status == "" {
		status = domain.ToolResultStatusOK
	}
	toolResult := domain.ToolResult{
		Text:   body,
		Diff:   strings.TrimSpace(result.DiffText),
		Data:   result.Stored,
		Status: status,
	}
	return toolResult, body, nil
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

func RequireExecControl(runtime Runtime) (execruntime.Control, error) {
	if runtime.Exec == nil || runtime.SessionID == "" || runtime.ChatID == "" {
		return nil, errors.New("exec sessions require an active persisted chat")
	}
	return runtime.Exec, nil
}

func RequireService[T any](runtime Runtime, key string) (T, error) {
	var zero T
	service, ok := runtime.Services[strings.TrimSpace(key)]
	if !ok {
		return zero, fmt.Errorf("%s service is not configured", key)
	}
	typed, ok := service.(T)
	if !ok {
		return zero, fmt.Errorf("%s service has unexpected type", key)
	}
	return typed, nil
}

func DefaultSummarizeResult(req Request, result Result) (string, string) {
	return defaultSummary(req.Tool, result)
}

func normalizeRequest(req Request) (Request, Tool, error) {
	if req.Tool == "" {
		return req, nil, errors.New("tool is empty")
	}
	tool, spec, ok := lookupWithSpec(req.Tool)
	if !ok {
		return req, nil, fmt.Errorf("unsupported tool %q", req.Tool)
	}
	if req.Args == nil {
		req.Args = map[string]string{}
	}
	args, err := normalizeSchemaIntegers(spec, req.Args)
	if err != nil {
		return req, tool, err
	}
	args, err = tool.NormalizeArgs(args)
	if err != nil {
		return req, nil, err
	}
	req.Args = args
	return req, tool, nil
}

func defaultSummary(tool ID, result Result) (string, string) {
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
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
		case json.Number:
			out[key] = typed.String()
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
