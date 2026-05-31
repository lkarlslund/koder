package tooltest

import (
	"context"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

type SessionControl struct {
	Store *store.Store
}

func NewSessionControl(st *store.Store) SessionControl {
	return SessionControl{Store: st}
}

func (c SessionControl) GetMilestonePlan(ctx context.Context, sessionID domain.ID) (store.MilestonePlan, error) {
	return c.Store.GetMilestonePlan(ctx, sessionID)
}

func (c SessionControl) SetMilestonePlan(ctx context.Context, sessionID domain.ID, summary string, milestones []store.Milestone) (store.MilestonePlan, error) {
	return c.Store.SetMilestonePlan(ctx, sessionID, summary, milestones)
}

func (c SessionControl) AddTodoItems(ctx context.Context, sessionID domain.ID, ref string, items []string) ([]store.TodoItem, error) {
	return c.Store.AddTodoItems(ctx, sessionID, ref, items)
}

func (c SessionControl) UpdateTodoItem(ctx context.Context, id domain.ID, status domain.TodoStatus, content string) (store.TodoItem, error) {
	return c.Store.UpdateTodoItem(ctx, id, status, content)
}

func (c SessionControl) ListTodos(ctx context.Context, sessionID domain.ID, ref string) ([]store.TodoItem, error) {
	return c.Store.ListTodos(ctx, sessionID, ref)
}

func (c SessionControl) AddTask(ctx context.Context, sessionID domain.ID, body string, status domain.TaskStatus) (store.Task, error) {
	return c.Store.AddTask(ctx, sessionID, body, status)
}
