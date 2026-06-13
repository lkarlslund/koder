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

type Source struct {
	store *store.Store
}

func NewSource(st *store.Store) *Source {
	return &Source{store: st}
}

func (s *Source) requireStore() (*store.Store, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("planning source store is required")
	}
	return s.store, nil
}

func (s *Source) LoadPlan(ctx context.Context, sessionID id.ID) (Plan, error) {
	st, err := s.requireStore()
	if err != nil {
		return Plan{}, err
	}
	return loadPlan(ctx, st, sessionID)
}

func (s *Source) SavePlan(ctx context.Context, plan Plan) error {
	st, err := s.requireStore()
	if err != nil {
		return err
	}
	return savePlan(ctx, st, plan)
}

func (s *Source) SaveTask(ctx context.Context, item Task) error {
	st, err := s.requireStore()
	if err != nil {
		return err
	}
	return saveTask(ctx, st, item)
}

func (s *Source) ListTasks(ctx context.Context, sessionID id.ID, milestoneRef string) ([]Task, error) {
	st, err := s.requireStore()
	if err != nil {
		return nil, err
	}
	return listTasks(ctx, st, sessionID, milestoneRef)
}

func (s *Source) SaveLegacyTask(ctx context.Context, task LegacyTask) error {
	st, err := s.requireStore()
	if err != nil {
		return err
	}
	return saveLegacyTask(ctx, st, task)
}

func (s *Source) ListLegacyTasks(ctx context.Context, sessionID id.ID) ([]LegacyTask, error) {
	st, err := s.requireStore()
	if err != nil {
		return nil, err
	}
	return listLegacyTasks(ctx, st, sessionID)
}

func (s *Source) DeleteSessionData(ctx context.Context, sessionID id.ID) error {
	st, err := s.requireStore()
	if err != nil {
		return err
	}
	return deleteSessionData(ctx, st, sessionID)
}

func planCollection(st *store.Store) store.Collection[Plan] {
	return store.NewCollection(st, store.CollectionSpec[Plan]{
		Namespace: "milestone-plans",
		GetID:     func(v Plan) string { return v.SessionID },
		SetID:     func(v *Plan, id string) { v.SessionID = id },
	})
}

func taskCollection(st *store.Store) store.Collection[Task] {
	return store.NewCollection(st, store.CollectionSpec[Task]{
		Namespace: "tasks",
		GetID:     func(v Task) string { return v.ID },
		SetID:     func(v *Task, id string) { v.ID = id },
		Indexes: []store.IndexSpec[Task]{
			{Name: "session", Value: func(v Task) string { return v.SessionID }},
			{Name: "milestone", Value: func(v Task) string { return v.SessionID + "/" + v.MilestoneRef }},
		},
	})
}

func legacyTaskCollection(st *store.Store) store.Collection[LegacyTask] {
	return store.NewCollection(st, store.CollectionSpec[LegacyTask]{
		Namespace: "legacy-tasks",
		GetID:     func(v LegacyTask) string { return v.ID },
		SetID:     func(v *LegacyTask, id string) { v.ID = id },
		Indexes: []store.IndexSpec[LegacyTask]{
			{Name: "session", Value: func(v LegacyTask) string { return v.SessionID }},
		},
	})
}

func loadPlan(ctx context.Context, st *store.Store, sessionID id.ID) (Plan, error) {
	plan, err := planCollection(st).Get(ctx, sessionID)
	if err != nil {
		return Plan{SessionID: sessionID}, nil
	}
	return plan, nil
}

func savePlan(ctx context.Context, st *store.Store, plan Plan) error {
	if plan.SessionID == "" {
		return fmt.Errorf("put milestone plan: session id is required")
	}
	if plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = time.Now().UTC()
	}
	return planCollection(st).Put(ctx, plan)
}

func saveTask(ctx context.Context, st *store.Store, item Task) error {
	if item.ID == "" {
		return fmt.Errorf("put task: id is required")
	}
	if item.SessionID == "" {
		return fmt.Errorf("put task: session id is required")
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	return taskCollection(st).Put(ctx, item)
}

func listTasks(ctx context.Context, st *store.Store, sessionID id.ID, milestoneRef string) ([]Task, error) {
	query := store.ByIndex[Task]("session", string(sessionID))
	milestoneRef = strings.TrimSpace(milestoneRef)
	if milestoneRef != "" {
		query = store.ByIndex[Task]("milestone", string(sessionID)+"/"+milestoneRef)
	}
	items, err := taskCollection(st).List(ctx, query)
	if err != nil {
		return nil, err
	}
	SortTasks(items)
	return items, nil
}

func saveLegacyTask(ctx context.Context, st *store.Store, task LegacyTask) error {
	if task.ID == "" {
		return fmt.Errorf("put task: id is required")
	}
	if task.SessionID == "" {
		return fmt.Errorf("put task: session id is required")
	}
	return legacyTaskCollection(st).Put(ctx, task)
}

func listLegacyTasks(ctx context.Context, st *store.Store, sessionID id.ID) ([]LegacyTask, error) {
	items, err := legacyTaskCollection(st).List(ctx, store.ByIndex[LegacyTask]("session", string(sessionID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b LegacyTask) int {
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

func deleteSessionData(ctx context.Context, st *store.Store, sessionID id.ID) error {
	legacyTasks, err := listLegacyTasks(ctx, st, sessionID)
	if err != nil {
		return err
	}
	for _, task := range legacyTasks {
		if err := legacyTaskCollection(st).Delete(ctx, task.ID); err != nil {
			return err
		}
	}
	tasks, err := listTasks(ctx, st, sessionID, "")
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := taskCollection(st).Delete(ctx, task.ID); err != nil {
			return err
		}
	}
	_ = planCollection(st).Delete(ctx, sessionID)
	return nil
}
