package toolruntime

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/mcp"
	"github.com/lkarlslund/koder/internal/permissionprofile"
	"github.com/lkarlslund/koder/internal/provider"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/settings"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/chattool"
	"github.com/lkarlslund/koder/internal/tools/codesearchtool"
)

type Runtime struct {
	settings *settings.Store
	debug    *debugsrv.Recorder
	sessions *sessionpkg.Registry
	exec     *execruntime.Manager
	mcp      *mcp.Manager
}

type Config struct {
	Settings *settings.Store
	Debug    *debugsrv.Recorder
	Sessions *sessionpkg.Registry
	Exec     *execruntime.Manager
	MCP      *mcp.Manager
}

func New(cfg Config) *Runtime {
	exec := cfg.Exec
	if exec == nil {
		exec = execruntime.NewManager()
	}
	return &Runtime{
		settings: cfg.Settings,
		debug:    cfg.Debug,
		sessions: cfg.Sessions,
		exec:     exec,
		mcp:      cfg.MCP,
	}
}

func (r *Runtime) UpdateSettings(store *settings.Store) {
	if r == nil {
		return
	}
	r.settings = store
}

func (r *Runtime) ExecManager() *execruntime.Manager {
	if r == nil {
		return nil
	}
	return r.exec
}

func (r *Runtime) SetExecManager(manager *execruntime.Manager) {
	if r == nil || manager == nil {
		return
	}
	r.exec = manager
}

func (r *Runtime) ToolRuntime(ctx context.Context, rt *chatpkg.Chat) (tools.Runtime, error) {
	if rt == nil {
		return tools.Runtime{}, fmt.Errorf("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	session := snapshot.Session
	chat := snapshot.Chat
	if session.ID != "" && r.sessions != nil {
		owner, err := r.sessions.Load(ctx, session.ID)
		if err != nil {
			return tools.Runtime{}, err
		}
		session = owner.Snapshot().Session
		rt.SetSession(session)
	}
	return r.Runtime(session, chat), nil
}

func (r *Runtime) Runtime(session domain.Session, chat domain.Chat) tools.Runtime {
	projectRoot := sessionProjectRoot(session)
	runtime := tools.Runtime{
		Workdir:               projectRoot,
		SessionID:             session.ID,
		ChatID:                chat.ID,
		ChatRole:              chat.WorkflowRole,
		ActiveMilestoneKey:    chat.ActiveMilestoneKey,
		AssignedTaskBucketKey: chat.AssignedTaskBucketKey,
		AssignedTaskRef:       chat.AssignedTaskRef,
		Exec:                  r.exec,
		MCP:                   r.mcp,
		AllowedTools:          r.toolStates(session),
		FileTracker:           codeIntelFileTracker{root: projectRoot},
		AccessSettings:        r.accessSettings(session),
	}
	if owner := r.loadedSession(session.ID); owner != nil {
		runtime.SessionControl = owner.PlanningForChat(chat)
		runtime.TaskControl = owner
		runtime.Services = chattool.RuntimeService(owner.ChatToolControl(chat.ID))
	}
	return runtime
}

func (r *Runtime) Definitions(session domain.Session, chat domain.Chat) []provider.ToolDefinition {
	defs := tools.Definitions(r.Runtime(session, chat))
	if r == nil || r.mcp == nil {
		return defs
	}
	if r.toolEnabled(session, domain.ToolKindMCP) && chatrole.AllowsTool(chat.WorkflowRole, domain.ToolKindMCP) {
		defs = append(defs, r.mcp.ToolDefinitionsWithReserved(defs)...)
	}
	return defs
}

func (r *Runtime) ToolExecutionStarted(_ context.Context, rt *chatpkg.Chat, req tools.Request) {
	if r == nil || rt == nil {
		return
	}
	snapshot := rt.Snapshot()
	r.recordLifecycle(snapshot.Session.ID, "tool_execution_started", req.ContextString(), map[string]string{"tool": req.Tool.String(), "tool_call_id": req.ToolCallID})
}

func (r *Runtime) ToolExecutionFinished(_ context.Context, rt *chatpkg.Chat, req tools.Request) {
	if r == nil || rt == nil {
		return
	}
	snapshot := rt.Snapshot()
	r.recordLifecycle(snapshot.Session.ID, "tool_execution_finished", req.ContextString(), map[string]string{"tool": req.Tool.String(), "tool_call_id": req.ToolCallID})
}

func (r *Runtime) ToolExecutionFailed(_ context.Context, rt *chatpkg.Chat, req tools.Request, err error) {
	if r == nil || rt == nil || err == nil {
		return
	}
	snapshot := rt.Snapshot()
	r.recordLifecycle(snapshot.Session.ID, "tool_execution_failed", err.Error(), map[string]string{"tool": req.Tool.String(), "tool_call_id": req.ToolCallID})
}

func (r *Runtime) ApproveToolForTurn(ctx context.Context, rt *chatpkg.Chat, toolCallID string, rule *accesssettings.PermissionOverride, out chan<- domain.Event) (bool, error) {
	if rt == nil {
		return false, fmt.Errorf("chat runtime is required")
	}
	snapshot := rt.Snapshot()
	session := snapshot.Session
	if rule != nil {
		next := *rule
		next.Pattern = strings.TrimSpace(next.Pattern)
		if next.Pattern == "" {
			next.Pattern = "*"
		}
		if err := permissionprofile.Validate(next.Action); err != nil {
			return false, err
		}
		if r.sessions == nil {
			return false, fmt.Errorf("session registry is required")
		}
		owner, err := r.sessions.Load(ctx, session.ID)
		if err != nil {
			return false, err
		}
		session, err = owner.UpdateSession(ctx, func(session *domain.Session) {
			session.PermissionRules = sessionpkg.AppendPermissionRule(session.PermissionRules, next)
		})
		if err != nil {
			return false, err
		}
		rt.SetSession(session)
		if out != nil {
			out <- domain.Event{
				Kind: domain.EventKindStatus,
				Text: fmt.Sprintf("approved all %s requests matching %s for this session", next.Tool, next.Pattern),
				Meta: map[string]string{
					"permission_tool":    next.Tool,
					"permission_pattern": next.Pattern,
				},
			}
		}
	}
	chat := rt.Snapshot().Chat
	req, err := r.requestForToolCall(ctx, chat.ID, toolCallID)
	if err != nil {
		return false, err
	}
	needsApproval, execErr := rt.RunToolCalls(ctx, []tools.Request{req}, out)
	if execErr != nil {
		return false, execErr
	}
	if needsApproval {
		return false, nil
	}
	if chatpkg.ShouldStop(ctx) {
		return false, context.Canceled
	}
	return true, nil
}

func (r *Runtime) DenyToolForTurn(ctx context.Context, rt *chatpkg.Chat, toolCallID string, out chan<- domain.Event) error {
	if rt == nil {
		return fmt.Errorf("chat runtime is required")
	}
	chat := rt.Snapshot().Chat
	req, err := r.requestForToolCall(ctx, chat.ID, toolCallID)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s denied", req.Tool)
	evt, err := rt.RecordToolDenied(ctx, req, text)
	if err != nil {
		return err
	}
	if out != nil {
		out <- evt
	}
	return nil
}

func (r *Runtime) ResumePendingToolsForTurn(ctx context.Context, rt *chatpkg.Chat, out chan<- domain.Event) (bool, error) {
	if rt == nil {
		return false, fmt.Errorf("chat runtime is required")
	}
	calls, err := pendingExecutableToolCallsForTurn(ctx, rt)
	if err != nil || len(calls) == 0 {
		return false, err
	}
	needsApproval, err := rt.RunToolCalls(ctx, calls, out)
	if err != nil {
		return false, err
	}
	if needsApproval || chatpkg.ShouldStop(ctx) {
		return false, nil
	}
	return true, nil
}

func (r *Runtime) PendingExecutableToolCalls(ctx context.Context, chatID id.ID) ([]tools.Request, error) {
	if chatID == "" {
		return nil, nil
	}
	chatRecord, err := r.chatByID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	rt, err := r.chatOwner(ctx, chatRecord.SessionID, chatID)
	if err != nil {
		return nil, err
	}
	return pendingExecutableToolCallsForTurn(ctx, rt)
}

func (r *Runtime) requestForToolCall(ctx context.Context, chatID id.ID, toolCallID string) (tools.Request, error) {
	toolCallID = strings.TrimSpace(toolCallID)
	if chatID == "" {
		return tools.Request{}, fmt.Errorf("chat id is required")
	}
	if toolCallID == "" {
		return tools.Request{}, fmt.Errorf("tool call id is required")
	}
	chatRecord, err := r.chatByID(ctx, chatID)
	if err != nil {
		return tools.Request{}, err
	}
	rt, err := r.chatOwner(ctx, chatRecord.SessionID, chatID)
	if err != nil {
		return tools.Request{}, err
	}
	call, err := rt.RequestForToolCall(ctx, toolCallID)
	if err != nil {
		return tools.Request{}, err
	}
	return tools.Normalize(tools.Request{
		Tool:       call.Tool,
		ToolCallID: string(call.ToolCallID),
		Args:       maps.Clone(call.Args),
	})
}

func pendingExecutableToolCallsForTurn(ctx context.Context, rt *chatpkg.Chat) ([]tools.Request, error) {
	calls, err := rt.PendingExecutableToolCalls(ctx)
	if err != nil {
		return nil, err
	}
	requests := make([]tools.Request, 0, len(calls))
	for _, call := range calls {
		req, err := tools.Normalize(tools.Request{
			Tool:       call.Tool,
			ToolCallID: string(call.ToolCallID),
			Args:       maps.Clone(call.Args),
		})
		if err != nil {
			return nil, err
		}
		requests = append(requests, req)
	}
	return requests, nil
}

func (r *Runtime) chatOwner(ctx context.Context, sessionID, chatID id.ID) (*chatpkg.Chat, error) {
	if r == nil || r.sessions == nil {
		return nil, fmt.Errorf("session registry is required")
	}
	return r.sessions.Chat(ctx, sessionID, chatID)
}

func (r *Runtime) chatByID(ctx context.Context, chatID id.ID) (domain.Chat, error) {
	if r == nil || r.sessions == nil {
		return domain.Chat{}, fmt.Errorf("session registry is required")
	}
	return r.sessions.ChatByID(ctx, chatID)
}

func (r *Runtime) loadedSession(sessionID id.ID) *sessionpkg.Session {
	if r == nil || r.sessions == nil || sessionID == "" {
		return nil
	}
	for _, owner := range r.sessions.Loaded() {
		if owner != nil && owner.Snapshot().Session.ID == sessionID {
			return owner
		}
	}
	return nil
}

func (r *Runtime) recordLifecycle(sessionID id.ID, kind, text string, meta map[string]string) {
	if r == nil || r.debug == nil {
		return
	}
	r.debug.RecordLifecycle(sessionID, kind, text, meta)
}

type codeIntelFileTracker struct {
	root string
}

func (t codeIntelFileTracker) TouchFile(ctx context.Context, path, content string) {
	if strings.TrimSpace(t.root) == "" || strings.TrimSpace(path) == "" {
		return
	}
	_ = codesearchtool.TouchFile(ctx, t.root, path, content)
}

func sessionProjectRoot(session domain.Session) string {
	return strings.TrimSpace(session.ProjectRoot)
}

func (r *Runtime) accessSettings(session domain.Session) accesssettings.Settings {
	if r == nil || r.settings == nil {
		return accesssettings.Normalize(session.AccessSettings)
	}
	return r.settings.Access(session)
}

func (r *Runtime) toolEnabled(session domain.Session, kind domain.ToolKind) bool {
	if r == nil || r.settings == nil {
		return true
	}
	enabled, ok := r.settings.Tools(session).Enabled[kind]
	if !ok {
		return true
	}
	return enabled
}

func (r *Runtime) toolStates(session domain.Session) map[domain.ToolKind]bool {
	registered := tools.RegisteredIDs()
	out := make(map[domain.ToolKind]bool, len(registered))
	global := settings.ToolSettings{}
	if r != nil && r.settings != nil {
		global = r.settings.Tools(session)
	}
	for _, kind := range registered {
		enabled, ok := global.Enabled[kind]
		if !ok {
			enabled = true
		}
		out[kind] = enabled
	}
	return out
}
