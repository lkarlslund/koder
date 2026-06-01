package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/planning"
)

type TaskControl interface {
	AddTask(context.Context, domain.ID, string, domain.TaskStatus) (planning.Task, error)
}

type SessionControl interface {
	GetMilestonePlan(context.Context, domain.ID) (planning.Plan, error)
	SetMilestonePlan(context.Context, domain.ID, string, []planning.Milestone) (planning.Plan, error)
	AddTodoItems(context.Context, domain.ID, string, []string) ([]planning.TodoItem, error)
	UpdateTodoItem(context.Context, domain.ID, domain.TodoStatus, string) (planning.TodoItem, error)
	ListTodos(context.Context, domain.ID, string) ([]planning.TodoItem, error)
}

func RequireSessionControl(runtime Runtime) (SessionControl, error) {
	if runtime.SessionControl == nil || runtime.SessionID == "" {
		return nil, errors.New("planning tools require a loaded session")
	}
	return runtime.SessionControl, nil
}

func RequireTaskControl(runtime Runtime) (TaskControl, error) {
	if runtime.TaskControl == nil || runtime.SessionID == "" {
		return nil, errors.New("task tools require a loaded session")
	}
	return runtime.TaskControl, nil
}

func AllowedMilestoneRef(runtime Runtime, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	assigned := AssignedMilestoneRef(runtime)
	if assigned == "" {
		return requested, nil
	}
	if requested == "" || requested == assigned {
		return assigned, nil
	}
	return "", fmt.Errorf("chat is scoped to milestone %q", assigned)
}

func AssignedMilestoneRef(runtime Runtime) string {
	assigned := strings.TrimSpace(runtime.ActiveMilestoneRef)
	if assigned == "" {
		assigned = strings.TrimSpace(runtime.AssignedTodoBucketRef)
	}
	return assigned
}

func AssignedTodoRef(runtime Runtime) domain.ID {
	return domain.ID(strings.TrimSpace(string(runtime.AssignedTodoRef)))
}

func TodoScopeAllows(runtime Runtime, todoID domain.ID) error {
	assigned := AssignedTodoRef(runtime)
	if assigned == "" || todoID == assigned {
		return nil
	}
	return fmt.Errorf("chat is scoped to todo %q", assigned)
}

func ScopedTodos(runtime Runtime, todos []planning.TodoItem) []planning.TodoItem {
	assigned := AssignedTodoRef(runtime)
	if assigned == "" {
		return todos
	}
	out := make([]planning.TodoItem, 0, 1)
	for _, item := range todos {
		if item.ID == assigned {
			out = append(out, item)
			break
		}
	}
	return out
}

func ScopedMilestonePlan(runtime Runtime, plan planning.Plan) planning.Plan {
	assigned := AssignedMilestoneRef(runtime)
	if assigned == "" {
		return plan
	}
	scoped := plan
	scoped.Milestones = nil
	for _, milestone := range plan.Milestones {
		if milestone.Ref == assigned {
			scoped.Milestones = []planning.Milestone{milestone}
			return scoped
		}
	}
	return scoped
}

func MilestonePlanForRef(plan planning.Plan, ref string) planning.Plan {
	return planning.PlanForRef(plan, ref)
}

func MilestoneStoredResult(plan planning.Plan) MilestonePlanStoredResult {
	items := make([]MilestoneStoredItem, 0, len(plan.Milestones))
	for _, item := range plan.Milestones {
		ownerChatID := ""
		if item.OwnerChatID != nil {
			ownerChatID = *item.OwnerChatID
		}
		items = append(items, MilestoneStoredItem{
			Ref:         item.Ref,
			Title:       item.Title,
			Status:      item.Status.String(),
			Notes:       item.Notes,
			OwnerChatID: ownerChatID,
		})
	}
	return MilestonePlanStoredResult{
		Summary:    plan.Summary,
		Milestones: items,
	}
}

func TodoStoredResult(plan planning.Plan, ref string, todos []planning.TodoItem, message string) TodoListStoredResult {
	items := make([]TodoStoredItem, 0, len(todos))
	for _, item := range todos {
		items = append(items, TodoStoredItem{
			ID:      item.ID,
			Content: item.Content,
			Status:  item.Status.String(),
		})
	}
	return TodoListStoredResult{
		MilestoneRef:   ref,
		MilestoneTitle: planning.MilestoneTitle(plan, ref),
		Message:        message,
		Items:          items,
	}
}

func ChatListStored(statuses []ChatStatus) ChatListStoredResult {
	items := make([]ChatStoredItem, 0, len(statuses))
	for _, status := range statuses {
		items = append(items, ChatStoredItem{
			ID:                 status.Chat.ID,
			Title:              status.Chat.Title,
			Role:               string(status.Chat.WorkflowRole),
			State:              string(status.State),
			Archived:           status.Chat.Archived,
			ActiveMilestoneRef: status.Chat.ActiveMilestoneRef,
			AssignedTodoRef:    status.Chat.AssignedTodoRef,
			StatusText:         status.StatusText,
		})
	}
	return ChatListStoredResult{Items: items}
}

func MilestonePlanResult(plan planning.Plan) Result {
	stored := MilestoneStoredResult(plan)
	output := FormatMilestoneOutput(stored)
	if strings.TrimSpace(output) == "" {
		output = "No milestones defined."
	}
	return Result{
		Output: output,
		Meta:   map[string]string{"milestone_count": fmt.Sprintf("%d", len(stored.Milestones))},
		Stored: stored,
	}
}

func TodoBucketResult(plan planning.Plan, ref string, todos []planning.TodoItem, message string) Result {
	return TodoBucketResultWithTitle(ref, planning.MilestoneTitle(plan, ref), todos, message)
}

func TodoBucketResultWithTitle(ref, title string, todos []planning.TodoItem, message string) Result {
	stored := TodoStoredResult(planning.Plan{Milestones: []planning.Milestone{{Ref: ref, Title: title}}}, ref, todos, message)
	output := FormatTodoOutput(stored)
	if strings.TrimSpace(output) == "" {
		output = "No todo items found."
	}
	return Result{
		Output: output,
		Meta: map[string]string{
			"milestone_ref": ref,
			"todo_count":    fmt.Sprintf("%d", len(stored.Items)),
		},
		Stored: stored,
	}
}

func FormatMilestoneOutput(result MilestonePlanStoredResult) string {
	text, _ := DisplayTextForPart(domain.Part{
		Kind: domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:   domain.ToolKindMilestoneList,
			Status: domain.ToolResultStatusOK,
			Result: result,
		},
	})
	return text
}

func FormatTodoOutput(result TodoListStoredResult) string {
	text, _ := DisplayTextForPart(domain.Part{
		Kind: domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:   domain.ToolKindTodoList,
			Status: domain.ToolResultStatusOK,
			Result: result,
		},
	})
	return text
}

func FormatTodoID(id domain.ID) string { return id }
