package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

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
	if runtime.AssignedTodoBucketRef == "" {
		runtime.AssignedTodoBucketRef = r.chat.AssignedTodoBucketRef
	}
	if runtime.AssignedTodoRef == "" {
		runtime.AssignedTodoRef = r.chat.AssignedTodoRef
	}
	return runtime
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
