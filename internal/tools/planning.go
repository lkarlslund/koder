package tools

import (
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

func FormatTodoID(id int64) string { return strconv.FormatInt(id, 10) }
