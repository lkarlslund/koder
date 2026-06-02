package planning

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/id"
)

type Plan struct {
	SessionID  id.ID
	Summary    string
	Milestones []Milestone
	UpdatedAt  time.Time
}

type Milestone struct {
	Ref          string
	Title        string
	Status       MilestoneStatus
	Notes        string
	DependsOnRef string
	Position     int
	OwnerChatID  *id.ID
}

type TodoItem struct {
	ID           id.ID
	SessionID    id.ID
	MilestoneRef string
	Content      string
	Note         string
	Status       TodoStatus
	Position     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Task struct {
	ID        id.ID
	SessionID id.ID
	Body      string
	Status    TaskStatus
	CreatedAt time.Time
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
	Ref          string `json:"ref"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Notes        string `json:"notes,omitempty"`
	DependsOnRef string `json:"depends_on_ref,omitempty"`
}

type MilestoneAddInput struct {
	Ref          string `json:"ref"`
	Title        string `json:"title"`
	Notes        string `json:"notes,omitempty"`
	DependsOnRef string `json:"depends_on_ref,omitempty"`
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
		status, err := MilestoneStatusString(strings.TrimSpace(item.Status))
		if err != nil {
			return nil, fmt.Errorf("invalid milestone status %q", item.Status)
		}
		notes := strings.TrimSpace(item.Notes)
		dependsOnRef := strings.TrimSpace(item.DependsOnRef)
		if ref == "" || title == "" {
			return nil, errors.New("each milestone requires ref and title")
		}
		if _, exists := seenRefs[ref]; exists {
			return nil, fmt.Errorf("duplicate milestone ref %q", ref)
		}
		seenRefs[ref] = struct{}{}
		out = append(out, Milestone{
			Ref:          ref,
			Title:        title,
			Status:       status,
			Notes:        notes,
			DependsOnRef: dependsOnRef,
			Position:     idx,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("milestones list is empty")
	}
	if err := ValidateMilestoneProgress(out); err != nil {
		return nil, err
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
		dependsOnRef := strings.TrimSpace(item.DependsOnRef)
		if ref == "" || title == "" {
			return nil, errors.New("each milestone requires ref and title")
		}
		if _, exists := seenRefs[ref]; exists {
			return nil, fmt.Errorf("duplicate milestone ref %q", ref)
		}
		seenRefs[ref] = struct{}{}
		out = append(out, Milestone{
			Ref:          ref,
			Title:        title,
			Status:       MilestoneStatusPending,
			Notes:        notes,
			DependsOnRef: dependsOnRef,
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

func ParseMilestoneStatus(raw string) (MilestoneStatus, error) {
	status, err := MilestoneStatusString(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid milestone status %q", raw)
	}
	return status, nil
}

func ValidMilestoneStatus(status MilestoneStatus) bool {
	switch status {
	case MilestoneStatusPending, MilestoneStatusDecomposing, MilestoneStatusReady, MilestoneStatusExecuting, MilestoneStatusCompleted, MilestoneStatusBlocked, MilestoneStatusCancelled:
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

func ParseTodoID(raw string) (id.ID, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("id is required")
	}
	return value, nil
}

func ParseTodoStatus(raw string) (TodoStatus, error) {
	status, err := TodoStatusString(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid todo status %q", raw)
	}
	return status, nil
}

func NormalizeTodoContent(content string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(content))), " ")
}

func ValidateNoDuplicateTodoContent(existing []TodoItem, added []string) error {
	seen := make(map[string]struct{}, len(existing)+len(added))
	for _, item := range existing {
		content := NormalizeTodoContent(item.Content)
		if content != "" {
			seen[content] = struct{}{}
		}
	}
	for _, item := range added {
		content := NormalizeTodoContent(item)
		if content == "" {
			continue
		}
		if _, ok := seen[content]; ok {
			return fmt.Errorf("duplicate todo content %q", item)
		}
		seen[content] = struct{}{}
	}
	return nil
}

func ActiveMilestone(plan Plan) (Milestone, bool) {
	for _, item := range plan.Milestones {
		if item.Status == MilestoneStatusExecuting {
			return item, true
		}
	}
	for _, item := range plan.Milestones {
		if item.Status == MilestoneStatusDecomposing {
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
		if item.Status == TodoStatusInProgress {
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
	seenTitles := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Ref == "" {
			return errors.New("milestone ref is empty")
		}
		if strings.TrimSpace(item.Title) == "" {
			return errors.New("milestone title is empty")
		}
		if !ValidMilestoneStatus(item.Status) {
			return fmt.Errorf("invalid milestone status %q", item.Status.String())
		}
		if _, exists := seenRefs[item.Ref]; exists {
			return fmt.Errorf("duplicate milestone ref %q", item.Ref)
		}
		seenRefs[item.Ref] = struct{}{}
		title := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(item.Title))), " ")
		if _, exists := seenTitles[title]; exists {
			return fmt.Errorf("duplicate milestone title %q", item.Title)
		}
		seenTitles[title] = struct{}{}
	}
	for _, item := range items {
		dependsOnRef := strings.TrimSpace(item.DependsOnRef)
		if dependsOnRef == "" {
			continue
		}
		if dependsOnRef == item.Ref {
			return fmt.Errorf("milestone %q cannot depend on itself", item.Ref)
		}
		if _, exists := seenRefs[dependsOnRef]; !exists {
			return fmt.Errorf("milestone %q depends on unknown milestone %q", item.Ref, dependsOnRef)
		}
	}
	if err := validateMilestoneDependencyCycles(items); err != nil {
		return err
	}
	return nil
}

func validateMilestoneDependencyCycles(items []Milestone) error {
	depends := make(map[string]string, len(items))
	for _, item := range items {
		depends[item.Ref] = strings.TrimSpace(item.DependsOnRef)
	}
	for _, item := range items {
		seen := map[string]struct{}{}
		for ref := item.Ref; ref != ""; ref = depends[ref] {
			if _, exists := seen[ref]; exists {
				return fmt.Errorf("milestone dependency cycle includes %q", ref)
			}
			seen[ref] = struct{}{}
		}
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
