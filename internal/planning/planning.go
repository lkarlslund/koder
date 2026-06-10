package planning

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	ID           id.ID
	Key          string
	Ref          string
	LegacyRef    string
	Title        string
	Status       MilestoneStatus
	Notes        string
	DependsOnRef string
	Position     int
	OwnerChatID  *id.ID
}

type TodoItem struct {
	ID           id.ID
	Key          string
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

func MilestoneKey(item Milestone) string {
	key := strings.TrimSpace(item.Key)
	if key != "" {
		return key
	}
	return strings.TrimSpace(item.Ref)
}

func MilestoneDependsOnKey(item Milestone) string {
	return strings.TrimSpace(item.DependsOnRef)
}

func TodoKey(item TodoItem) string {
	key := strings.TrimSpace(item.Key)
	if key != "" {
		return key
	}
	return strings.TrimSpace(string(item.ID))
}

func ScopedTodoKey(milestoneKey string, n int) string {
	milestoneKey = strings.TrimSpace(milestoneKey)
	if milestoneKey == "" || n <= 0 {
		return ""
	}
	return fmt.Sprintf("%sT%03d", milestoneKey, n)
}

func NormalizePlanKeys(plan Plan) (Plan, bool) {
	next := plan
	next.Milestones = slices.Clone(plan.Milestones)
	changed := false
	nextMilestoneNumber := nextPlanningKeyNumber(next.Milestones, "M")
	legacyToKey := make(map[string]string, len(next.Milestones))
	for idx := range next.Milestones {
		item := &next.Milestones[idx]
		legacyRef := strings.TrimSpace(item.Ref)
		if item.ID == "" {
			item.ID = id.New()
			changed = true
		}
		if strings.TrimSpace(item.Key) == "" {
			item.Key = formatPlanningKey("M", nextMilestoneNumber)
			nextMilestoneNumber++
			changed = true
		}
		item.Key = strings.TrimSpace(item.Key)
		if legacyRef != "" {
			if strings.TrimSpace(item.LegacyRef) == "" && legacyRef != item.Key {
				item.LegacyRef = legacyRef
				changed = true
			}
			legacyToKey[legacyRef] = item.Key
		}
		if item.Ref != item.Key {
			item.Ref = item.Key
			changed = true
		}
	}
	for idx := range next.Milestones {
		depends := strings.TrimSpace(next.Milestones[idx].DependsOnRef)
		if replacement := legacyToKey[depends]; replacement != "" && replacement != depends {
			next.Milestones[idx].DependsOnRef = replacement
			changed = true
		}
	}
	return next, changed
}

func NormalizeTodosKeys(items []TodoItem, milestoneKeys map[string]string) ([]TodoItem, bool) {
	next := slices.Clone(items)
	changed := false
	for idx := range next {
		item := &next[idx]
		milestoneRef := strings.TrimSpace(item.MilestoneRef)
		if replacement := milestoneKeys[milestoneRef]; replacement != "" && replacement != milestoneRef {
			item.MilestoneRef = replacement
			milestoneRef = replacement
			changed = true
		}
		oldKey := strings.TrimSpace(item.Key)
		item.Key = normalizeTodoKeyForMilestone(item.Key, milestoneRef)
		if item.Key != oldKey {
			changed = true
		}
		if item.Key == "" {
			item.Key = nextTodoKey(next, milestoneRef)
			changed = true
		}
	}
	return next, changed
}

func MilestoneKeyAliases(plan Plan) map[string]string {
	out := make(map[string]string, len(plan.Milestones)*2)
	for _, milestone := range plan.Milestones {
		key := MilestoneKey(milestone)
		if key == "" {
			continue
		}
		out[key] = key
		if ref := strings.TrimSpace(milestone.Ref); ref != "" {
			out[ref] = key
		}
		if ref := strings.TrimSpace(milestone.LegacyRef); ref != "" {
			out[ref] = key
		}
	}
	return out
}

func nextPlanningKeyNumber(items []Milestone, prefix string) int {
	next := 1
	for _, item := range items {
		for _, key := range []string{item.Key, item.Ref} {
			if n, ok := parsePlanningKey(key, prefix); ok && n >= next {
				next = n + 1
			}
		}
	}
	return next
}

func nextTodoKey(items []TodoItem, milestoneKey string) string {
	return ScopedTodoKey(milestoneKey, nextTodoKeyNumber(items, milestoneKey))
}

func nextTodoKeyNumber(items []TodoItem, milestoneKey string) int {
	next := 1
	for _, item := range items {
		if n, ok := parseTodoKeyNumber(item.Key, milestoneKey); ok && n >= next {
			next = n + 1
		}
	}
	return next
}

func normalizeTodoKeyForMilestone(raw, milestoneKey string) string {
	key := strings.TrimSpace(raw)
	if key == "" {
		return ""
	}
	if _, ok := parseTodoKeyNumber(key, milestoneKey); ok {
		return key
	}
	if n, ok := parsePlanningKey(key, "T"); ok {
		return ScopedTodoKey(milestoneKey, n)
	}
	return key
}

func parseTodoKeyNumber(raw, milestoneKey string) (int, bool) {
	milestoneKey = strings.TrimSpace(milestoneKey)
	if milestoneKey == "" {
		return 0, false
	}
	return parsePlanningKey(raw, milestoneKey+"T")
}

func formatPlanningKey(prefix string, n int) string {
	return fmt.Sprintf("%s%03d", prefix, n)
}

func parsePlanningKey(raw, prefix string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(raw, prefix))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

type MilestoneInput struct {
	Key          string `json:"key,omitempty"`
	Ref          string `json:"ref"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Notes        string `json:"notes,omitempty"`
	DependsOnKey string `json:"depends_on_key,omitempty"`
	DependsOnRef string `json:"depends_on_ref,omitempty"`
}

type MilestoneAddInput struct {
	Key          string `json:"key,omitempty"`
	Ref          string `json:"ref"`
	Title        string `json:"title"`
	Notes        string `json:"notes,omitempty"`
	DependsOnKey string `json:"depends_on_key,omitempty"`
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
		key := strings.TrimSpace(item.Key)
		legacyRef := strings.TrimSpace(item.Ref)
		title := strings.TrimSpace(item.Title)
		status, err := MilestoneStatusString(strings.TrimSpace(item.Status))
		if err != nil {
			return nil, fmt.Errorf("invalid milestone status %q", item.Status)
		}
		notes := strings.TrimSpace(item.Notes)
		dependsOnRef := strings.TrimSpace(item.DependsOnKey)
		if dependsOnRef == "" {
			dependsOnRef = strings.TrimSpace(item.DependsOnRef)
		}
		if title == "" {
			return nil, errors.New("each milestone requires title")
		}
		if key != "" {
			if _, exists := seenRefs[key]; exists {
				return nil, fmt.Errorf("duplicate milestone key %q", key)
			}
			seenRefs[key] = struct{}{}
		}
		out = append(out, Milestone{
			Key:          key,
			Ref:          legacyRef,
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
	plan, _ := NormalizePlanKeys(Plan{Milestones: out})
	out = plan.Milestones
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
		key := strings.TrimSpace(item.Key)
		legacyRef := strings.TrimSpace(item.Ref)
		title := strings.TrimSpace(item.Title)
		notes := strings.TrimSpace(item.Notes)
		dependsOnRef := strings.TrimSpace(item.DependsOnKey)
		if dependsOnRef == "" {
			dependsOnRef = strings.TrimSpace(item.DependsOnRef)
		}
		if title == "" {
			return nil, errors.New("each milestone requires title")
		}
		if key != "" {
			if _, exists := seenRefs[key]; exists {
				return nil, fmt.Errorf("duplicate milestone key %q", key)
			}
			seenRefs[key] = struct{}{}
		}
		out = append(out, Milestone{
			Key:          key,
			Ref:          legacyRef,
			Title:        title,
			Status:       MilestoneStatusPending,
			Notes:        notes,
			DependsOnRef: dependsOnRef,
		})
	}
	if len(out) == 0 {
		return nil, errors.New("items list is empty")
	}
	plan, _ := NormalizePlanKeys(Plan{Milestones: out})
	out = plan.Milestones
	return out, nil
}

func ParseMilestoneKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", errors.New("milestone_key is empty")
	}
	return key, nil
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
		return nil, errors.New("items must be a JSON array of task objects")
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

func ParseTodoKey(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("task_key is required")
	}
	milestonePart, taskPart, ok := strings.Cut(value, "T")
	if !ok || milestonePart == "" || taskPart == "" {
		return "", fmt.Errorf("invalid task_key %q: expected M###T###", raw)
	}
	if _, ok := parsePlanningKey(milestonePart, "M"); !ok {
		return "", fmt.Errorf("invalid task_key %q: expected M###T###", raw)
	}
	if _, ok := parsePlanningKey("T"+taskPart, "T"); !ok {
		return "", fmt.Errorf("invalid task_key %q: expected M###T###", raw)
	}
	return value, nil
}

func ParseTodoStatus(raw string) (TodoStatus, error) {
	status, err := TodoStatusString(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid task status %q", raw)
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
			return fmt.Errorf("duplicate task content %q", item)
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
		if MilestoneKey(item) == ref {
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
		return errors.New("task list may contain at most one in_progress task")
	}
	return nil
}

func ValidateMilestoneProgress(items []Milestone) error {
	seenRefs := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := MilestoneKey(item)
		if key == "" {
			return errors.New("milestone key is empty")
		}
		if strings.TrimSpace(item.Title) == "" {
			return errors.New("milestone title is empty")
		}
		if !ValidMilestoneStatus(item.Status) {
			return fmt.Errorf("invalid milestone status %q", item.Status.String())
		}
		if _, exists := seenRefs[key]; exists {
			return fmt.Errorf("duplicate milestone key %q", key)
		}
		seenRefs[key] = struct{}{}
	}
	for _, item := range items {
		key := MilestoneKey(item)
		dependsOnRef := MilestoneDependsOnKey(item)
		if dependsOnRef == "" {
			continue
		}
		if dependsOnRef == key {
			return fmt.Errorf("milestone %q cannot depend on itself", key)
		}
		if _, exists := seenRefs[dependsOnRef]; !exists {
			return fmt.Errorf("milestone %q depends on unknown milestone %q", key, dependsOnRef)
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
		depends[MilestoneKey(item)] = MilestoneDependsOnKey(item)
	}
	for _, item := range items {
		seen := map[string]struct{}{}
		for ref := MilestoneKey(item); ref != ""; ref = depends[ref] {
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
		if MilestoneKey(milestone) == ref {
			scoped.Milestones = []Milestone{milestone}
			return scoped
		}
	}
	return scoped
}
