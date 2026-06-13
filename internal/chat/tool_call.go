package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/codediag"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type completedToolCall struct {
	events []domain.Event
	err    error
}

// RunToolCalls executes model-requested tools and records their results through
// this chat. Parallel tool calls are all completed before lint diagnostics are
// appended for files touched by the batch.
func (r *Chat) RunToolCalls(ctx context.Context, calls []tools.Request, out chan<- domain.Event) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("chat runtime is required")
	}
	if len(calls) == 0 {
		return false, nil
	}
	runtime, err := r.toolRuntimeForExecution(ctx)
	if err != nil {
		return false, err
	}
	results := make(chan completedToolCall, len(calls))
	for _, call := range calls {
		go func(call tools.Request) {
			events, err := r.runToolCallWithLifecycle(ctx, runtime, call, func(evt domain.Event) {
				if out != nil {
					out <- evt
				}
			})
			results <- completedToolCall{events: events, err: err}
		}(call)
	}

	var firstErr error
	waitingApproval := false
	touched := map[string]struct{}{}
	for i := 0; i < len(calls); i++ {
		completed := <-results
		if completed.err != nil {
			if errors.Is(completed.err, context.Canceled) {
				firstErr = completed.err
				continue
			}
			if firstErr == nil {
				firstErr = completed.err
			}
			continue
		}
		for _, evt := range completed.events {
			if out != nil {
				out <- evt
			}
			if touchedPath, ok := touchedPathFromToolResultEvent(evt); ok {
				touched[touchedPath] = struct{}{}
			}
			if evt.Kind == domain.EventKindApprovalAsk {
				waitingApproval = true
			}
		}
	}
	if firstErr != nil {
		return waitingApproval, firstErr
	}
	if ShouldStop(ctx) {
		return waitingApproval, nil
	}
	if err := r.appendLintMessageForTouchedFiles(ctx, orderedTouchedFiles(touched), out); err != nil {
		return waitingApproval, err
	}
	return waitingApproval, nil
}

// RunToolCall executes one prepared tool request and records the result through
// the live chat owner.
func (r *Chat) RunToolCall(ctx context.Context, runtime tools.Runtime, req tools.Request, emit func(domain.Event)) ([]domain.Event, error) {
	if r == nil {
		return nil, fmt.Errorf("chat runtime is required")
	}
	runtime = r.toolRuntime(runtime)
	if strings.TrimSpace(req.ToolCallID) != "" {
		item, err := r.MarkToolRunning(ctx, req.ToolCallID)
		if err != nil {
			return nil, err
		}
		if emit != nil {
			emit(domain.Event{
				Kind:       domain.EventKindToolStart,
				Tool:       req.Tool,
				ToolCallID: req.ToolCallID,
				Text:       tools.Preview(req),
				Item:       item,
			})
		}
	}
	result, err := tools.Call(ctx, tools.Options{Runtime: runtime, Request: req})
	if err != nil {
		if isInterruptedToolError(err) {
			return nil, err
		}
		if tools.IsDenied(err) {
			evt, recordErr := r.RecordToolDenied(ctx, req, tools.DeniedMessage(err))
			if recordErr != nil {
				return nil, recordErr
			}
			return []domain.Event{evt}, nil
		}
		evt, recordErr := r.RecordToolError(ctx, req, err)
		if recordErr != nil {
			return nil, recordErr
		}
		return []domain.Event{evt}, nil
	}
	evt, err := r.FinalizeToolResult(ctx, runtime, req, result)
	if err != nil {
		return nil, err
	}
	return []domain.Event{evt}, nil
}

func (r *Chat) toolRuntimeForExecution(ctx context.Context) (tools.Runtime, error) {
	if r.deps.Runtime == nil {
		return tools.Runtime{}, fmt.Errorf("tool runtime service is not configured")
	}
	return r.deps.Runtime.ToolRuntime(ctx, r)
}

func (r *Chat) runToolCallWithLifecycle(ctx context.Context, runtime tools.Runtime, req tools.Request, emit func(domain.Event)) ([]domain.Event, error) {
	if r.deps.Life != nil {
		r.deps.Life.ToolExecutionStarted(ctx, r, req)
	}
	events, err := r.RunToolCall(ctx, runtime, req, emit)
	if err != nil {
		if r.deps.Life != nil {
			r.deps.Life.ToolExecutionFailed(ctx, r, req, err)
		}
		return nil, err
	}
	if r.deps.Life != nil {
		r.deps.Life.ToolExecutionFinished(ctx, r, req)
	}
	return events, nil
}

func (r *Chat) toolRuntime(runtime tools.Runtime) tools.Runtime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if runtime.SessionID == "" {
		runtime.SessionID = r.session.ID
	}
	if runtime.ChatID == "" {
		runtime.ChatID = r.chat.ID
	}
	if runtime.ChatRole == "" {
		runtime.ChatRole = r.chat.WorkflowRole
	}
	if runtime.ActiveMilestoneRef == "" {
		runtime.ActiveMilestoneRef = r.chat.ActiveMilestoneRef
	}
	if runtime.AssignedTaskBucketRef == "" {
		runtime.AssignedTaskBucketRef = r.chat.AssignedTaskBucketRef
	}
	if runtime.AssignedTaskRef == "" {
		runtime.AssignedTaskRef = r.chat.AssignedTaskRef
	}
	return runtime
}

func (r *Chat) AppendAssistantToolRequests(ctx context.Context, item domain.TimelineItem, calls []tools.Request, text string, reasoning domain.ReasoningContent, usage domain.Usage) (domain.TimelineItem, error) {
	toolCalls := make([]domain.ToolCall, 0, len(calls))
	for _, call := range calls {
		toolCalls = append(toolCalls, toolCallRecord(call))
	}
	return r.AppendAssistantToolCalls(ctx, item, toolCalls, text, reasoning, usage)
}

func toolCallRecord(call tools.Request) domain.ToolCall {
	return domain.ToolCall{
		ToolCallID: domain.ToolCallID(call.ToolCallID),
		Tool:       call.Tool,
		Args:       call.Args,
		Status:     domain.ToolStatusPending,
	}
}

// FinalizeToolResult finalizes a raw tool result and attaches it to this chat.
func (r *Chat) FinalizeToolResult(ctx context.Context, runtime tools.Runtime, req tools.Request, result tools.Result) (domain.Event, error) {
	if r == nil {
		return domain.Event{}, fmt.Errorf("chat runtime is required")
	}
	toolResult, body, err := tools.FinalizeResult(ctx, r.toolRuntime(runtime), req, result)
	if err != nil {
		return domain.Event{}, err
	}
	item, err := r.attachToolOutcome(ctx, req, &toolResult, nil)
	if err != nil {
		return domain.Event{}, err
	}
	return domain.Event{Kind: domain.EventKindToolResult, Text: body, Tool: req.Tool, ToolCallID: req.ToolCallID, Item: item}, nil
}

// RecordToolDenied attaches one denied tool result to this chat.
func (r *Chat) RecordToolDenied(ctx context.Context, req tools.Request, message string) (domain.Event, error) {
	if r == nil {
		return domain.Event{}, fmt.Errorf("chat runtime is required")
	}
	text := strings.TrimSpace(message)
	if text == "" {
		text = fmt.Sprintf("%s denied", req.Tool)
	}
	result := domain.ToolResult{
		Text:   text,
		Status: domain.ToolResultStatusDenied,
		Data:   tools.DeniedStoredResult{Message: text},
	}
	item, err := r.attachToolOutcome(ctx, req, &result, nil)
	if err != nil {
		return domain.Event{}, err
	}
	return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, ToolCallID: req.ToolCallID, Text: text, Item: item}, nil
}

// RecordToolError attaches one tool execution error to this chat.
func (r *Chat) RecordToolError(ctx context.Context, req tools.Request, execErr error) (domain.Event, error) {
	if r == nil {
		return domain.Event{}, fmt.Errorf("chat runtime is required")
	}
	if execErr == nil {
		return domain.Event{}, errors.New("tool failure error is nil")
	}
	text := fmt.Sprintf("%s failed: %v", req.Tool, execErr)
	item, err := r.attachToolOutcome(ctx, req, nil, &domain.ToolError{Message: text})
	if err != nil {
		return domain.Event{}, err
	}
	return domain.Event{Kind: domain.EventKindToolResult, Tool: req.Tool, ToolCallID: req.ToolCallID, Text: text, Item: item}, nil
}

func (r *Chat) attachToolOutcome(ctx context.Context, req tools.Request, result *domain.ToolResult, toolErr *domain.ToolError) (domain.TimelineItem, error) {
	if strings.TrimSpace(req.ToolCallID) != "" {
		if result != nil {
			return r.AttachToolResult(ctx, req.ToolCallID, *result)
		}
		if toolErr != nil {
			return r.AttachToolError(ctx, req.ToolCallID, *toolErr)
		}
		return domain.TimelineItem{}, fmt.Errorf("tool outcome is required")
	}
	now := time.Now().UTC()
	content := domain.ToolExecution{
		Tool:      req.Tool,
		Args:      req.Meta(),
		Result:    result,
		Error:     toolErr,
		StartedAt: now,
		EndedAt:   now,
	}
	item, err := r.AppendTimelineContent(ctx, content)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(now)
	return r.UpsertTimelineItem(ctx, item)
}

func isInterruptedToolError(err error) bool {
	return errors.Is(err, context.Canceled)
}

func touchedPathFromToolResultEvent(evt domain.Event) (string, bool) {
	if evt.Kind != domain.EventKindToolResult {
		return "", false
	}
	switch evt.Tool {
	case domain.ToolKindFileEdit, domain.ToolKindFileWrite:
	default:
		return "", false
	}
	path := strings.TrimSpace(toolResultPath(evt.Item, evt.ToolCallID))
	if path == "" {
		return "", false
	}
	return path, true
}

func toolResultPath(item domain.TimelineItem, toolCallID string) string {
	assistant, ok := item.Content.(domain.AssistantMessage)
	if !ok {
		if execution, ok := item.Content.(domain.ToolExecution); ok && execution.Result != nil {
			return pathFromToolResultData(execution.Result.Data)
		}
		return ""
	}
	for _, call := range assistant.Tools {
		if strings.TrimSpace(toolCallID) != "" && string(call.ToolCallID) != toolCallID {
			continue
		}
		if call.Result == nil {
			continue
		}
		if path := pathFromToolResultData(call.Result.Data); path != "" {
			return path
		}
	}
	return ""
}

func pathFromToolResultData(data any) string {
	switch result := data.(type) {
	case tools.EditStoredResult:
		return strings.TrimSpace(result.Path)
	case tools.WriteStoredResult:
		return strings.TrimSpace(result.Path)
	default:
		return ""
	}
}

func orderedTouchedFiles(files map[string]struct{}) []string {
	if len(files) == 0 {
		return nil
	}
	out := make([]string, 0, len(files))
	for file := range files {
		out = append(out, file)
	}
	slices.Sort(out)
	return out
}

func (r *Chat) appendLintMessageForTouchedFiles(ctx context.Context, paths []string, out chan<- domain.Event) error {
	if len(paths) == 0 {
		return nil
	}
	root := strings.TrimSpace(r.Snapshot().Session.ProjectRoot)
	if root == "" {
		return nil
	}
	report := lintTouchedFiles(ctx, root, paths)
	text := codediag.NewProblemsText(report)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	item, err := r.appendLintTimelineItem(ctx, domain.LintMessage{Text: text, Files: paths})
	if err != nil {
		return err
	}
	if out != nil {
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Lint diagnostics detected", Item: item}
	}
	return nil
}

func lintTouchedFiles(ctx context.Context, root string, paths []string) codediag.Report {
	touched := make(map[string]struct{}, len(paths))
	var report codediag.Report
	for _, path := range paths {
		rel := tools.NormalizePathInput(path)
		if rel == "" {
			continue
		}
		abs, cleanRel, err := tools.WorkspacePath(root, rel)
		if err != nil {
			continue
		}
		touched[cleanRel] = struct{}{}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		fileReport := codediag.CheckFile(ctx, root, cleanRel, string(data), codediag.Options{
			Mode:            "auto",
			IncludeExisting: true,
			Timeout:         2 * time.Second,
		})
		for _, diagnostic := range fileReport.Diagnostics {
			if _, ok := touched[tools.NormalizePathInput(diagnostic.Path)]; ok {
				report.Diagnostics = append(report.Diagnostics, diagnostic)
			}
		}
	}
	return report
}

func (r *Chat) appendLintTimelineItem(ctx context.Context, lint domain.LintMessage) (domain.TimelineItem, error) {
	now := time.Now().UTC()
	item, err := r.AppendTimelineContent(ctx, lint)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(now)
	if _, err := r.UpsertTimelineItem(ctx, item); err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}
