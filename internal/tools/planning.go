package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
)

type TaskControl interface {
	AddTask(context.Context, id.ID, string, planning.LegacyTaskStatus) (planning.LegacyTask, error)
}

type SessionControl interface {
	GetMilestonePlan(context.Context, id.ID) (planning.Plan, error)
	SetMilestonePlan(context.Context, id.ID, string, []planning.Milestone) (planning.Plan, error)
	AddTasks(context.Context, id.ID, string, []string) ([]planning.Task, error)
	UpdateTask(context.Context, string, planning.TaskStatus, string, string) (planning.Task, error)
	ListTasks(context.Context, id.ID, string) ([]planning.Task, error)
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

func AllowedMilestoneKey(runtime Runtime, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	assigned := AssignedMilestoneKey(runtime)
	if assigned == "" {
		return requested, nil
	}
	if requested == "" || requested == assigned {
		return assigned, nil
	}
	return "", fmt.Errorf("chat is scoped to milestone %q", assigned)
}

func AssignedMilestoneKey(runtime Runtime) string {
	assigned := strings.TrimSpace(runtime.ActiveMilestoneKey)
	if assigned == "" {
		assigned = strings.TrimSpace(runtime.AssignedTaskBucketKey)
	}
	return assigned
}

func AssignedTaskRef(runtime Runtime) string {
	return strings.TrimSpace(runtime.AssignedTaskRef)
}

func TaskScopeAllows(runtime Runtime, taskID string) error {
	assigned := AssignedTaskRef(runtime)
	if assigned == "" || taskID == assigned {
		return nil
	}
	return fmt.Errorf("chat is scoped to task %q", assigned)
}

func ScopedTasks(runtime Runtime, tasks []planning.Task) []planning.Task {
	assigned := AssignedTaskRef(runtime)
	if assigned == "" {
		return tasks
	}
	out := make([]planning.Task, 0, 1)
	for _, item := range tasks {
		if planning.TaskKey(item) == assigned {
			out = append(out, item)
			break
		}
	}
	return out
}

func ScopedMilestonePlan(runtime Runtime, plan planning.Plan) planning.Plan {
	assigned := AssignedMilestoneKey(runtime)
	if assigned == "" {
		return plan
	}
	scoped := plan
	scoped.Milestones = nil
	for _, milestone := range plan.Milestones {
		if planning.MilestoneKey(milestone) == assigned {
			scoped.Milestones = []planning.Milestone{milestone}
			return scoped
		}
	}
	return scoped
}

func MilestonePlanForKey(plan planning.Plan, ref string) planning.Plan {
	return planning.PlanForKey(plan, ref)
}

func MilestoneStoredResult(plan planning.Plan) MilestonePlanStoredResult {
	return MilestoneStoredResultWithTaskSummaries(plan, nil)
}

func MilestoneStoredResultWithTaskSummaries(plan planning.Plan, summaries map[string]string) MilestonePlanStoredResult {
	items := make([]MilestoneStoredItem, 0, len(plan.Milestones))
	for _, item := range plan.Milestones {
		ownerChatID := ""
		if item.OwnerChatID != nil {
			ownerChatID = *item.OwnerChatID
		}
		items = append(items, MilestoneStoredItem{
			ID:           item.ID,
			Key:          planning.MilestoneKey(item),
			Title:        item.Title,
			Status:       item.Status.String(),
			Notes:        item.Notes,
			DependsOnKey: planning.MilestoneDependsOnKey(item),
			OwnerChatID:  ownerChatID,
			TaskSummary:  summaries[planning.MilestoneKey(item)],
		})
	}
	return MilestonePlanStoredResult{
		Summary:    plan.Summary,
		Milestones: items,
	}
}

func BuildTaskListStoredResult(plan planning.Plan, ref string, tasks []planning.Task, message string) TaskListStoredResult {
	items := make([]TaskStoredItem, 0, len(tasks))
	for _, item := range tasks {
		items = append(items, TaskStoredItem{
			ID:      item.ID,
			Key:     planning.TaskKey(item),
			Content: item.Content,
			Note:    item.Note,
			Status:  item.Status.String(),
		})
	}
	return TaskListStoredResult{
		MilestoneKey:   ref,
		MilestoneTitle: planning.MilestoneTitle(plan, ref),
		Message:        message,
		Items:          items,
	}
}

func MilestonePlanResult(plan planning.Plan) Result {
	return MilestonePlanResultWithTaskSummaries(plan, nil)
}

func MilestonePlanResultWithTaskSummaries(plan planning.Plan, summaries map[string]string) Result {
	stored := MilestoneStoredResultWithTaskSummaries(plan, summaries)
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

func TaskBucketResult(plan planning.Plan, ref string, tasks []planning.Task, message string) Result {
	return TaskBucketResultWithTitle(ref, planning.MilestoneTitle(plan, ref), tasks, message)
}

func TaskBucketResultWithTitle(ref, title string, tasks []planning.Task, message string) Result {
	stored := BuildTaskListStoredResult(planning.Plan{Milestones: []planning.Milestone{{Key: ref, Title: title}}}, ref, tasks, message)
	output := FormatTaskOutput(stored)
	if strings.TrimSpace(output) == "" {
		output = "No tasks found."
	}
	return Result{
		Output: output,
		Meta: map[string]string{
			"milestone_key": ref,
			"task_count":    fmt.Sprintf("%d", len(stored.Items)),
		},
		Stored: stored,
	}
}

func FormatMilestoneOutput(result MilestonePlanStoredResult) string {
	text, _ := DisplayTextForPart(domain.Part{
		Kind: domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:   MilestoneList,
			Status: domain.ToolResultStatusOK,
			Result: result,
		},
	})
	return text
}

func FormatTaskOutput(result TaskListStoredResult) string {
	text, _ := DisplayTextForPart(domain.Part{
		Kind: domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:   TaskList,
			Status: domain.ToolResultStatusOK,
			Result: result,
		},
	})
	return text
}

func FormatTaskID(key string) string { return key }
