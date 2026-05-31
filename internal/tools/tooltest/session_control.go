package tooltest

import (
	"context"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
)

type SessionControl struct {
	Store *store.Store
}

func NewSessionControl(st *store.Store) SessionControl {
	return SessionControl{Store: st}
}

func (c SessionControl) GetMilestonePlan(ctx context.Context, sessionID domain.ID) (planning.Plan, error) {
	return planning.GetPlan(ctx, c.Store, sessionID)
}

func (c SessionControl) SetMilestonePlan(ctx context.Context, sessionID domain.ID, summary string, milestones []planning.Milestone) (planning.Plan, error) {
	plan := planning.Plan{SessionID: sessionID, Summary: strings.TrimSpace(summary), Milestones: append([]planning.Milestone(nil), milestones...), UpdatedAt: time.Now().UTC()}
	if err := planning.PutPlan(ctx, c.Store, plan); err != nil {
		return planning.Plan{}, err
	}
	return plan, nil
}

func (c SessionControl) AddTodoItems(ctx context.Context, sessionID domain.ID, ref string, items []string) ([]planning.TodoItem, error) {
	now := time.Now().UTC()
	existing, err := planning.ListTodos(ctx, c.Store, sessionID, ref)
	if err != nil {
		return nil, err
	}
	out := make([]planning.TodoItem, 0, len(items))
	for _, content := range items {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		out = append(out, planning.TodoItem{ID: domain.NewIDAt(now), SessionID: sessionID, MilestoneRef: strings.TrimSpace(ref), Content: content, Status: domain.TodoStatusPending, Position: len(existing) + len(out), CreatedAt: now, UpdatedAt: now})
	}
	for _, item := range out {
		if err := planning.PutTodo(ctx, c.Store, item); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c SessionControl) UpdateTodoItem(ctx context.Context, id domain.ID, status domain.TodoStatus, content string) (planning.TodoItem, error) {
	item, err := planning.TodoCollection(c.Store).Get(ctx, id)
	if err != nil {
		return planning.TodoItem{}, err
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = strings.TrimSpace(content)
	}
	item.UpdatedAt = time.Now().UTC()
	if err := planning.PutTodo(ctx, c.Store, item); err != nil {
		return planning.TodoItem{}, err
	}
	return item, nil
}

func (c SessionControl) ListTodos(ctx context.Context, sessionID domain.ID, ref string) ([]planning.TodoItem, error) {
	return planning.ListTodos(ctx, c.Store, sessionID, ref)
}

func (c SessionControl) AddTask(ctx context.Context, sessionID domain.ID, body string, status domain.TaskStatus) (planning.Task, error) {
	now := time.Now().UTC()
	task := planning.Task{ID: domain.NewIDAt(now), SessionID: sessionID, Body: strings.TrimSpace(body), Status: status, CreatedAt: now}
	if err := planning.PutTask(ctx, c.Store, task); err != nil {
		return planning.Task{}, err
	}
	return task, nil
}
