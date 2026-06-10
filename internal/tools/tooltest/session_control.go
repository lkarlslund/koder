package tooltest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
)

type SessionControl struct {
	Store *store.Store
}

func NewSessionControl(st *store.Store) SessionControl {
	return SessionControl{Store: st}
}

func (c SessionControl) GetMilestonePlan(ctx context.Context, sessionID id.ID) (planning.Plan, error) {
	return modeltest.GetPlan(ctx, c.Store, sessionID)
}

func (c SessionControl) SetMilestonePlan(ctx context.Context, sessionID id.ID, summary string, milestones []planning.Milestone) (planning.Plan, error) {
	plan := planning.Plan{SessionID: sessionID, Summary: strings.TrimSpace(summary), Milestones: append([]planning.Milestone(nil), milestones...), UpdatedAt: time.Now().UTC()}
	plan, _ = planning.NormalizePlanKeys(plan)
	if err := modeltest.PutPlan(ctx, c.Store, plan); err != nil {
		return planning.Plan{}, err
	}
	return plan, nil
}

func (c SessionControl) AddTodoItems(ctx context.Context, sessionID id.ID, ref string, items []string) ([]planning.TodoItem, error) {
	now := time.Now().UTC()
	milestoneRef := strings.TrimSpace(ref)
	existing, err := modeltest.ListTodos(ctx, c.Store, sessionID, milestoneRef)
	if err != nil {
		return nil, err
	}
	all, err := modeltest.ListTodos(ctx, c.Store, sessionID, "")
	if err != nil {
		return nil, err
	}
	out := make([]planning.TodoItem, 0, len(items))
	nextKey := nextTodoKey(all, milestoneRef)
	for _, content := range items {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		out = append(out, planning.TodoItem{ID: id.NewAt(now), Key: nextKey, SessionID: sessionID, MilestoneRef: milestoneRef, Content: content, Status: planning.TodoStatusPending, Position: len(existing) + len(out), CreatedAt: now, UpdatedAt: now})
		nextKey = incrementTodoKey(nextKey, milestoneRef)
	}
	for _, item := range out {
		if err := modeltest.PutTodo(ctx, c.Store, item); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c SessionControl) UpdateTodoItem(ctx context.Context, key string, status planning.TodoStatus, content, note string) (planning.TodoItem, error) {
	item, err := modeltest.GetTodo(ctx, c.Store, id.ID(key))
	if err != nil {
		return planning.TodoItem{}, err
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = strings.TrimSpace(content)
	}
	if strings.TrimSpace(note) != "" {
		item.Note = strings.TrimSpace(note)
	}
	item.UpdatedAt = time.Now().UTC()
	if err := modeltest.PutTodo(ctx, c.Store, item); err != nil {
		return planning.TodoItem{}, err
	}
	return item, nil
}

func (c SessionControl) ListTodos(ctx context.Context, sessionID id.ID, ref string) ([]planning.TodoItem, error) {
	return modeltest.ListTodos(ctx, c.Store, sessionID, ref)
}

func nextTodoKey(items []planning.TodoItem, milestoneKey string) string {
	next := 1
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		prefix := strings.TrimSpace(milestoneKey) + "T"
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimPrefix(key, prefix), "%d", &n); err == nil && n >= next {
			next = n + 1
		}
	}
	return planning.ScopedTodoKey(milestoneKey, next)
}

func incrementTodoKey(key, milestoneKey string) string {
	prefix := strings.TrimSpace(milestoneKey) + "T"
	var n int
	if _, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(key), prefix), "%d", &n); err != nil || n <= 0 {
		return planning.ScopedTodoKey(milestoneKey, 1)
	}
	return planning.ScopedTodoKey(milestoneKey, n+1)
}

func (c SessionControl) AddTask(ctx context.Context, sessionID id.ID, body string, status planning.TaskStatus) (planning.Task, error) {
	now := time.Now().UTC()
	task := planning.Task{ID: id.NewAt(now), SessionID: sessionID, Body: strings.TrimSpace(body), Status: status, CreatedAt: now}
	if err := modeltest.PutTask(ctx, c.Store, task); err != nil {
		return planning.Task{}, err
	}
	return task, nil
}
