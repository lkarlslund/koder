package planning

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

func planCollection(st *store.Store) store.Collection[Plan] {
	return store.NewCollection(st, store.CollectionSpec[Plan]{
		Namespace: "milestone-plans",
		GetID:     func(v Plan) string { return v.SessionID },
		SetID:     func(v *Plan, id string) { v.SessionID = id },
	})
}

func todoCollection(st *store.Store) store.Collection[TodoItem] {
	return store.NewCollection(st, store.CollectionSpec[TodoItem]{
		Namespace: "todos",
		GetID:     func(v TodoItem) string { return v.ID },
		SetID:     func(v *TodoItem, id string) { v.ID = id },
		Indexes: []store.IndexSpec[TodoItem]{
			{Name: "session", Value: func(v TodoItem) string { return v.SessionID }},
			{Name: "milestone", Value: func(v TodoItem) string { return v.SessionID + "/" + v.MilestoneRef }},
		},
	})
}

func taskCollection(st *store.Store) store.Collection[Task] {
	return store.NewCollection(st, store.CollectionSpec[Task]{
		Namespace: "tasks",
		GetID:     func(v Task) string { return v.ID },
		SetID:     func(v *Task, id string) { v.ID = id },
		Indexes: []store.IndexSpec[Task]{
			{Name: "session", Value: func(v Task) string { return v.SessionID }},
		},
	})
}

func LoadPlan(ctx context.Context, st *store.Store, sessionID id.ID) (Plan, error) {
	plan, err := planCollection(st).Get(ctx, sessionID)
	if err != nil {
		return Plan{SessionID: sessionID}, nil
	}
	return plan, nil
}

func SavePlan(ctx context.Context, st *store.Store, plan Plan) error {
	if plan.SessionID == "" {
		return fmt.Errorf("put milestone plan: session id is required")
	}
	if plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = time.Now().UTC()
	}
	return planCollection(st).Put(ctx, plan)
}

func SaveTodo(ctx context.Context, st *store.Store, item TodoItem) error {
	if item.ID == "" {
		return fmt.Errorf("put task: id is required")
	}
	if item.SessionID == "" {
		return fmt.Errorf("put task: session id is required")
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	return todoCollection(st).Put(ctx, item)
}

func ListTodos(ctx context.Context, st *store.Store, sessionID id.ID, milestoneRef string) ([]TodoItem, error) {
	query := store.ByIndex[TodoItem]("session", string(sessionID))
	milestoneRef = strings.TrimSpace(milestoneRef)
	if milestoneRef != "" {
		query = store.ByIndex[TodoItem]("milestone", string(sessionID)+"/"+milestoneRef)
	}
	items, err := todoCollection(st).List(ctx, query)
	if err != nil {
		return nil, err
	}
	SortTodos(items)
	return items, nil
}

func SaveTask(ctx context.Context, st *store.Store, task Task) error {
	if task.ID == "" {
		return fmt.Errorf("put task: id is required")
	}
	if task.SessionID == "" {
		return fmt.Errorf("put task: session id is required")
	}
	return taskCollection(st).Put(ctx, task)
}

func ListTasks(ctx context.Context, st *store.Store, sessionID id.ID) ([]Task, error) {
	items, err := taskCollection(st).List(ctx, store.ByIndex[Task]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b Task) int {
		switch {
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return items, nil
}

func DeleteSessionData(ctx context.Context, st *store.Store, sessionID id.ID) error {
	tasks, err := ListTasks(ctx, st, sessionID)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := taskCollection(st).Delete(ctx, task.ID); err != nil {
			return err
		}
	}
	todos, err := ListTodos(ctx, st, sessionID, "")
	if err != nil {
		return err
	}
	for _, todo := range todos {
		if err := todoCollection(st).Delete(ctx, todo.ID); err != nil {
			return err
		}
	}
	_ = planCollection(st).Delete(ctx, sessionID)
	return nil
}
