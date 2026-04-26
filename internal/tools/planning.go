package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

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

func ParseMilestones(raw string) ([]store.Milestone, error) {
	var items []MilestoneInput
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, errors.New("milestones must be a JSON array of milestone objects")
	}
	out := make([]store.Milestone, 0, len(items))
	seenRefs := map[string]struct{}{}
	inProgress := 0
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
		switch status {
		case domain.MilestoneStatusPending, domain.MilestoneStatusInProgress, domain.MilestoneStatusCompleted, domain.MilestoneStatusBlocked:
		default:
			return nil, fmt.Errorf("invalid milestone status %q", item.Status)
		}
		if status == domain.MilestoneStatusInProgress {
			inProgress++
		}
		out = append(out, store.Milestone{
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
	if inProgress > 1 {
		return nil, errors.New("milestones may contain at most one in_progress item")
	}
	return out, nil
}

func ParseMilestoneAddItems(raw string) ([]store.Milestone, error) {
	var items []MilestoneAddInput
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, errors.New("items must be a JSON array of milestone objects")
	}
	out := make([]store.Milestone, 0, len(items))
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
		out = append(out, store.Milestone{
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
	switch status {
	case domain.MilestoneStatusPending, domain.MilestoneStatusInProgress, domain.MilestoneStatusCompleted, domain.MilestoneStatusBlocked:
		return status, nil
	default:
		return "", fmt.Errorf("invalid milestone status %q", raw)
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

func ParseTodoID(raw string) (int64, error) {
	value, err := ParseFlexibleInt(raw)
	if err != nil || value <= 0 {
		return 0, errors.New("id must be a positive integer")
	}
	return int64(value), nil
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

func ActiveMilestone(plan store.MilestonePlan) (store.Milestone, bool) {
	for _, item := range plan.Milestones {
		if item.Status == domain.MilestoneStatusInProgress {
			return item, true
		}
	}
	return store.Milestone{}, false
}

func MilestoneTitle(plan store.MilestonePlan, ref string) string {
	for _, item := range plan.Milestones {
		if item.Ref == ref {
			return item.Title
		}
	}
	return ""
}

func ValidateTodoProgress(items []store.TodoItem) error {
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

func ValidateMilestoneProgress(items []store.Milestone) error {
	inProgress := 0
	seenRefs := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Ref == "" {
			return errors.New("milestone ref is empty")
		}
		if _, exists := seenRefs[item.Ref]; exists {
			return fmt.Errorf("duplicate milestone ref %q", item.Ref)
		}
		seenRefs[item.Ref] = struct{}{}
		if item.Status == domain.MilestoneStatusInProgress {
			inProgress++
		}
	}
	if inProgress > 1 {
		return errors.New("milestones may contain at most one in_progress item")
	}
	return nil
}

func RequireSessionStore(runtime Runtime) (*store.Store, error) {
	if runtime.Store == nil || runtime.SessionID == 0 {
		return nil, errors.New("planning tools require a persisted session")
	}
	return runtime.Store, nil
}

func PersistedTodoBucket(ctx context.Context, st *store.Store, sessionID int64, ref string) (store.MilestonePlan, []store.TodoItem, string, error) {
	plan, err := st.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return store.MilestonePlan{}, nil, "", err
	}
	if ref == "" {
		active, ok := ActiveMilestone(plan)
		if !ok {
			return store.MilestonePlan{}, nil, "", errors.New("no active milestone; read milestones first or provide milestone_ref")
		}
		ref = active.Ref
	}
	todos, err := st.ListTodos(ctx, sessionID, ref)
	if err != nil {
		return store.MilestonePlan{}, nil, "", err
	}
	return plan, todos, ref, nil
}

func MilestoneStoredResult(plan store.MilestonePlan) MilestonePlanStoredResult {
	items := make([]MilestoneStoredItem, 0, len(plan.Milestones))
	for _, item := range plan.Milestones {
		items = append(items, MilestoneStoredItem{
			Ref:    item.Ref,
			Title:  item.Title,
			Status: string(item.Status),
			Notes:  item.Notes,
		})
	}
	return MilestonePlanStoredResult{
		Summary:    plan.Summary,
		Milestones: items,
	}
}

func TodoStoredResult(plan store.MilestonePlan, ref string, todos []store.TodoItem, message string) TodoListStoredResult {
	items := make([]TodoStoredItem, 0, len(todos))
	for _, item := range todos {
		items = append(items, TodoStoredItem{
			ID:      item.ID,
			Content: item.Content,
			Status:  string(item.Status),
		})
	}
	return TodoListStoredResult{
		MilestoneRef:   ref,
		MilestoneTitle: MilestoneTitle(plan, ref),
		Message:        message,
		Items:          items,
	}
}

func MilestonePlanResult(plan store.MilestonePlan) Result {
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

func TodoBucketResult(plan store.MilestonePlan, ref string, todos []store.TodoItem, message string) Result {
	return TodoBucketResultWithTitle(ref, MilestoneTitle(plan, ref), todos, message)
}

func TodoBucketResultWithTitle(ref, title string, todos []store.TodoItem, message string) Result {
	stored := TodoStoredResult(store.MilestonePlan{Milestones: []store.Milestone{{Ref: ref, Title: title}}}, ref, todos, message)
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
		Kind:     domain.PartKindToolOutput,
		MetaJSON: JSONMeta(MetaWithStoredResult(nil, domain.PartKindToolOutput, domain.ToolKindMilestoneList, StoredResultStatusOK, result)),
	})
	return text
}

func FormatTodoOutput(result TodoListStoredResult) string {
	text, _ := DisplayTextForPart(domain.Part{
		Kind:     domain.PartKindToolOutput,
		MetaJSON: JSONMeta(MetaWithStoredResult(nil, domain.PartKindToolOutput, domain.ToolKindTodoList, StoredResultStatusOK, result)),
	})
	return text
}

func FormatTodoID(id int64) string { return strconv.FormatInt(id, 10) }
