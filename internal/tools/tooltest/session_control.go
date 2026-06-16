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

func (c SessionControl) AddTasks(ctx context.Context, sessionID id.ID, ref string, items []string) ([]planning.Task, error) {
	now := time.Now().UTC()
	milestoneKey := strings.TrimSpace(ref)
	existing, err := modeltest.ListTasks(ctx, c.Store, sessionID, milestoneKey)
	if err != nil {
		return nil, err
	}
	all, err := modeltest.ListTasks(ctx, c.Store, sessionID, "")
	if err != nil {
		return nil, err
	}
	out := make([]planning.Task, 0, len(items))
	nextKey := nextTaskKey(all, milestoneKey)
	for _, content := range items {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		out = append(out, planning.Task{ID: id.NewAt(now), Key: nextKey, SessionID: sessionID, MilestoneKey: milestoneKey, Content: content, Status: planning.TaskStatusPending, Position: len(existing) + len(out), CreatedAt: now, UpdatedAt: now})
		nextKey = incrementTaskKey(nextKey, milestoneKey)
	}
	for _, item := range out {
		if err := modeltest.PutTask(ctx, c.Store, item); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c SessionControl) UpdateTask(ctx context.Context, key string, status planning.TaskStatus, content, note string) (planning.Task, error) {
	item, err := modeltest.GetTask(ctx, c.Store, id.ID(key))
	if err != nil {
		return planning.Task{}, err
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = strings.TrimSpace(content)
	}
	if strings.TrimSpace(note) != "" {
		item.Note = strings.TrimSpace(note)
	}
	item.UpdatedAt = time.Now().UTC()
	if err := modeltest.PutTask(ctx, c.Store, item); err != nil {
		return planning.Task{}, err
	}
	return item, nil
}

func (c SessionControl) ListTasks(ctx context.Context, sessionID id.ID, ref string) ([]planning.Task, error) {
	return modeltest.ListTasks(ctx, c.Store, sessionID, ref)
}

func nextTaskKey(items []planning.Task, milestoneKey string) string {
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
	return planning.ScopedTaskKey(milestoneKey, next)
}

func incrementTaskKey(key, milestoneKey string) string {
	prefix := strings.TrimSpace(milestoneKey) + "T"
	var n int
	if _, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(key), prefix), "%d", &n); err != nil || n <= 0 {
		return planning.ScopedTaskKey(milestoneKey, 1)
	}
	return planning.ScopedTaskKey(milestoneKey, n+1)
}

func (c SessionControl) AddTask(ctx context.Context, sessionID id.ID, body string, status planning.LegacyTaskStatus) (planning.LegacyTask, error) {
	now := time.Now().UTC()
	task := planning.LegacyTask{ID: id.NewAt(now), SessionID: sessionID, Body: strings.TrimSpace(body), Status: status, CreatedAt: now}
	if err := modeltest.PutLegacyTask(ctx, c.Store, task); err != nil {
		return planning.LegacyTask{}, err
	}
	return task, nil
}
