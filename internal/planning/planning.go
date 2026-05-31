package planning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

type Control interface {
	GetMilestonePlan(context.Context, domain.ID) (Plan, error)
	SetMilestonePlan(context.Context, domain.ID, string, []Milestone) (Plan, error)
	AddTodoItems(context.Context, domain.ID, string, []string) ([]TodoItem, error)
	UpdateTodoItem(context.Context, domain.ID, domain.TodoStatus, string) (TodoItem, error)
	ListTodos(context.Context, domain.ID, string) ([]TodoItem, error)
}

type Plan struct {
	SessionID  domain.ID
	Summary    string
	Milestones []Milestone
	UpdatedAt  time.Time
}

type Milestone struct {
	Ref         string
	Title       string
	Status      domain.MilestoneStatus
	Notes       string
	Position    int
	OwnerChatID *domain.ID
}

type TodoItem struct {
	ID           domain.ID
	SessionID    domain.ID
	MilestoneRef string
	Content      string
	Status       domain.TodoStatus
	Position     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Task struct {
	ID        domain.ID
	SessionID domain.ID
	Body      string
	Status    domain.TaskStatus
	CreatedAt time.Time
}

func PlanCollection(st *store.Store) store.Collection[Plan] {
	return store.NewCollection(st, store.CollectionSpec[Plan]{
		Namespace: "milestone-plans",
		GetID:     func(v Plan) string { return v.SessionID },
		SetID:     func(v *Plan, id string) { v.SessionID = id },
	})
}

func TodoCollection(st *store.Store) store.Collection[TodoItem] {
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

func TaskCollection(st *store.Store) store.Collection[Task] {
	return store.NewCollection(st, store.CollectionSpec[Task]{
		Namespace: "tasks",
		GetID:     func(v Task) string { return v.ID },
		SetID:     func(v *Task, id string) { v.ID = id },
		Indexes: []store.IndexSpec[Task]{
			{Name: "session", Value: func(v Task) string { return v.SessionID }},
		},
	})
}

func PutPlan(ctx context.Context, st *store.Store, plan Plan) error {
	if plan.SessionID == "" {
		return fmt.Errorf("put milestone plan: session id is required")
	}
	if plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = time.Now().UTC()
	}
	return PlanCollection(st).Put(ctx, plan)
}

func GetPlan(ctx context.Context, st *store.Store, sessionID domain.ID) (Plan, error) {
	plan, err := PlanCollection(st).Get(ctx, sessionID)
	if err != nil {
		return Plan{SessionID: sessionID}, nil
	}
	return plan, nil
}

func PutTodo(ctx context.Context, st *store.Store, item TodoItem) error {
	if item.ID == "" {
		return fmt.Errorf("put todo item: id is required")
	}
	if item.SessionID == "" {
		return fmt.Errorf("put todo item: session id is required")
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	return TodoCollection(st).Put(ctx, item)
}

func ListTodos(ctx context.Context, st *store.Store, sessionID domain.ID, milestoneRef string) ([]TodoItem, error) {
	query := store.ByIndex[TodoItem]("session", string(sessionID))
	milestoneRef = strings.TrimSpace(milestoneRef)
	if milestoneRef != "" {
		query = store.ByIndex[TodoItem]("milestone", string(sessionID)+"/"+milestoneRef)
	}
	items, err := TodoCollection(st).List(ctx, query)
	if err != nil {
		return nil, err
	}
	SortTodos(items)
	return items, nil
}

func AddTodoItems(ctx context.Context, st *store.Store, sessionID domain.ID, milestoneRef string, contents []string) ([]TodoItem, error) {
	now := time.Now().UTC()
	milestoneRef = strings.TrimSpace(milestoneRef)
	existing, err := ListTodos(ctx, st, sessionID, milestoneRef)
	if err != nil {
		return nil, err
	}
	items := make([]TodoItem, 0, len(contents))
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		items = append(items, TodoItem{
			ID:           domain.NewIDAt(now),
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       domain.TodoStatusPending,
			Position:     len(existing) + len(items),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}
	for _, item := range items {
		if err := PutTodo(ctx, st, item); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func UpdateTodoItem(ctx context.Context, st *store.Store, todoID domain.ID, status domain.TodoStatus, content string) (TodoItem, error) {
	item, err := TodoCollection(st).Get(ctx, todoID)
	if err != nil {
		return TodoItem{}, err
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = strings.TrimSpace(content)
	}
	item.UpdatedAt = time.Now().UTC()
	if err := PutTodo(ctx, st, item); err != nil {
		return TodoItem{}, err
	}
	return item, nil
}

func PutTask(ctx context.Context, st *store.Store, task Task) error {
	if task.ID == "" {
		return fmt.Errorf("put task: id is required")
	}
	if task.SessionID == "" {
		return fmt.Errorf("put task: session id is required")
	}
	return TaskCollection(st).Put(ctx, task)
}

func ListTasks(ctx context.Context, st *store.Store, sessionID domain.ID) ([]Task, error) {
	items, err := TaskCollection(st).List(ctx, store.ByIndex[Task]("session", string(sessionID)))
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

func SortTodos(items []TodoItem) {
	slices.SortFunc(items, func(a, b TodoItem) int {
		switch {
		case a.Position < b.Position:
			return -1
		case a.Position > b.Position:
			return 1
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
}

type MilestoneInput struct {
	Ref    string `json:"ref"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Notes  string `json:"notes,omitempty"`
}

type MilestoneAddInput struct {
	Ref   string `json:"ref"`
	Title string `json:"title"`
	Notes string `json:"notes,omitempty"`
}

type TodoAddInput struct {
	Content string `json:"content"`
}

func ParseMilestones(raw string) ([]Milestone, error) {
	var items []MilestoneInput
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, errors.New("milestones must be a JSON array of milestone objects")
	}
	out := make([]Milestone, 0, len(items))
	seenRefs := map[string]struct{}{}
	for idx, item := range items {
		ref := strings.TrimSpace(item.Ref)
		title := strings.TrimSpace(item.Title)
		status := domain.MilestoneStatus(strings.TrimSpace(item.Status))
		notes := strings.TrimSpace(item.Notes)
		if ref == "" || title == "" {
			return nil, errors.New("each milestone requires ref and title")
		}
		if _, exists := seenRefs[ref]; exists {
			return nil, fmt.Errorf("duplicate milestone ref %q", ref)
		}
		seenRefs[ref] = struct{}{}
		if !ValidMilestoneStatus(status) {
			return nil, fmt.Errorf("invalid milestone status %q", item.Status)
		}
		out = append(out, Milestone{
			Ref:      ref,
			Title:    title,
			Status:   status,
			Notes:    notes,
			Position: idx,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("milestones list is empty")
	}
	return out, nil
}

func ParseMilestoneAddItems(raw string) ([]Milestone, error) {
	var items []MilestoneAddInput
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, errors.New("items must be a JSON array of milestone objects")
	}
	out := make([]Milestone, 0, len(items))
	seenRefs := map[string]struct{}{}
	for _, item := range items {
		ref := strings.TrimSpace(item.Ref)
		title := strings.TrimSpace(item.Title)
		notes := strings.TrimSpace(item.Notes)
		if ref == "" || title == "" {
			return nil, errors.New("each milestone requires ref and title")
		}
		if _, exists := seenRefs[ref]; exists {
			return nil, fmt.Errorf("duplicate milestone ref %q", ref)
		}
		seenRefs[ref] = struct{}{}
		out = append(out, Milestone{
			Ref:    ref,
			Title:  title,
			Status: domain.MilestoneStatusPending,
			Notes:  notes,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("items list is empty")
	}
	return out, nil
}

func ParseMilestoneRef(raw string) (string, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return "", errors.New("ref is empty")
	}
	return ref, nil
}

func ParseMilestoneStatus(raw string) (domain.MilestoneStatus, error) {
	status := domain.MilestoneStatus(strings.TrimSpace(raw))
	if ValidMilestoneStatus(status) {
		return status, nil
	}
	return "", fmt.Errorf("invalid milestone status %q", raw)
}

func ValidMilestoneStatus(status domain.MilestoneStatus) bool {
	switch status {
	case domain.MilestoneStatusPending, domain.MilestoneStatusDecomposing, domain.MilestoneStatusReady, domain.MilestoneStatusExecuting, domain.MilestoneStatusCompleted, domain.MilestoneStatusBlocked, domain.MilestoneStatusCancelled:
		return true
	default:
		return false
	}
}

func ParseTodoAddItems(raw string) ([]string, error) {
	var items []TodoAddInput
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, errors.New("items must be a JSON array of todo item objects")
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		out = append(out, content)
	}
	if len(out) == 0 {
		return nil, errors.New("items list is empty")
	}
	return out, nil
}

func ParseTodoID(raw string) (domain.ID, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("id is required")
	}
	return value, nil
}

func ParseTodoStatus(raw string) (domain.TodoStatus, error) {
	status := domain.TodoStatus(strings.TrimSpace(raw))
	switch status {
	case domain.TodoStatusPending, domain.TodoStatusInProgress, domain.TodoStatusCompleted:
		return status, nil
	default:
		return "", fmt.Errorf("invalid todo status %q", raw)
	}
}

func ActiveMilestone(plan Plan) (Milestone, bool) {
	for _, item := range plan.Milestones {
		if item.Status == domain.MilestoneStatusExecuting {
			return item, true
		}
	}
	for _, item := range plan.Milestones {
		if item.Status == domain.MilestoneStatusDecomposing {
			return item, true
		}
	}
	return Milestone{}, false
}

func MilestoneTitle(plan Plan, ref string) string {
	for _, item := range plan.Milestones {
		if item.Ref == ref {
			return item.Title
		}
	}
	return ""
}

func ValidateTodoProgress(items []TodoItem) error {
	inProgress := 0
	for _, item := range items {
		if item.Status == domain.TodoStatusInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return errors.New("todo bucket may contain at most one in_progress item")
	}
	return nil
}

func ValidateMilestoneProgress(items []Milestone) error {
	seenRefs := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Ref == "" {
			return errors.New("milestone ref is empty")
		}
		if !ValidMilestoneStatus(item.Status) {
			return fmt.Errorf("invalid milestone status %q", item.Status)
		}
		if _, exists := seenRefs[item.Ref]; exists {
			return fmt.Errorf("duplicate milestone ref %q", item.Ref)
		}
		seenRefs[item.Ref] = struct{}{}
	}
	return nil
}

func PlanForRef(plan Plan, ref string) Plan {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return plan
	}
	scoped := plan
	scoped.Milestones = nil
	for _, milestone := range plan.Milestones {
		if milestone.Ref == ref {
			scoped.Milestones = []Milestone{milestone}
			return scoped
		}
	}
	return scoped
}
