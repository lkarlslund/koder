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
	LegacyRef    string `json:",omitempty"`
	Title        string
	Status       MilestoneStatus
	Notes        string
	DependsOnKey string
	Position     int
	OwnerChatID  *id.ID
}

type Task struct {
	ID           id.ID
	Key          string
	SessionID    id.ID
	MilestoneKey string
	Content      string
	Note         string
	Status       TaskStatus
	Position     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (m *Milestone) UnmarshalJSON(data []byte) error {
	type milestoneJSON struct {
		ID           id.ID
		Key          string
		Ref          string
		LegacyRef    string
		Title        string
		Status       MilestoneStatus
		Notes        string
		DependsOnKey string
		DependsOnRef string
		Position     int
		OwnerChatID  *id.ID
	}
	var raw milestoneJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	key := strings.TrimSpace(raw.Key)
	legacyRef := strings.TrimSpace(raw.LegacyRef)
	if legacy := strings.TrimSpace(raw.Ref); legacy != "" {
		if key == "" {
			key = legacy
		}
		if legacyRef == "" && legacy != key {
			legacyRef = legacy
		}
	}
	dependsOnKey := strings.TrimSpace(raw.DependsOnKey)
	if dependsOnKey == "" {
		dependsOnKey = strings.TrimSpace(raw.DependsOnRef)
	}
	*m = Milestone{
		ID:           raw.ID,
		Key:          key,
		LegacyRef:    legacyRef,
		Title:        raw.Title,
		Status:       raw.Status,
		Notes:        raw.Notes,
		DependsOnKey: dependsOnKey,
		Position:     raw.Position,
		OwnerChatID:  raw.OwnerChatID,
	}
	return nil
}

func (t *Task) UnmarshalJSON(data []byte) error {
	type taskJSON struct {
		ID           id.ID
		Key          string
		SessionID    id.ID
		MilestoneKey string
		MilestoneRef string
		Content      string
		Note         string
		Status       TaskStatus
		Position     int
		CreatedAt    time.Time
		UpdatedAt    time.Time
	}
	var raw taskJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	milestoneKey := strings.TrimSpace(raw.MilestoneKey)
	if milestoneKey == "" {
		milestoneKey = strings.TrimSpace(raw.MilestoneRef)
	}
	*t = Task{
		ID:           raw.ID,
		Key:          raw.Key,
		SessionID:    raw.SessionID,
		MilestoneKey: milestoneKey,
		Content:      raw.Content,
		Note:         raw.Note,
		Status:       raw.Status,
		Position:     raw.Position,
		CreatedAt:    raw.CreatedAt,
		UpdatedAt:    raw.UpdatedAt,
	}
	return nil
}

type LegacyTask struct {
	ID        id.ID
	SessionID id.ID
	Body      string
	Status    LegacyTaskStatus
	CreatedAt time.Time
}

func SortTasks(items []Task) {
	slices.SortFunc(items, func(a, b Task) int {
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
	return strings.TrimSpace(item.LegacyRef)
}

func MilestoneDependsOnKey(item Milestone) string {
	return strings.TrimSpace(item.DependsOnKey)
}

func TaskKey(item Task) string {
	key := strings.TrimSpace(item.Key)
	if key != "" {
		return key
	}
	return strings.TrimSpace(string(item.ID))
}

func ScopedTaskKey(milestoneKey string, n int) string {
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
		legacyRef := strings.TrimSpace(item.LegacyRef)
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
			legacyToKey[legacyRef] = item.Key
		}
	}
	for idx := range next.Milestones {
		depends := strings.TrimSpace(next.Milestones[idx].DependsOnKey)
		if replacement := legacyToKey[depends]; replacement != "" && replacement != depends {
			next.Milestones[idx].DependsOnKey = replacement
			changed = true
		}
	}
	return next, changed
}

func NormalizeTaskKeys(items []Task, milestoneKeys map[string]string) ([]Task, bool) {
	next := slices.Clone(items)
	changed := false
	for idx := range next {
		item := &next[idx]
		milestoneKey := strings.TrimSpace(item.MilestoneKey)
		if replacement := milestoneKeys[milestoneKey]; replacement != "" && replacement != milestoneKey {
			item.MilestoneKey = replacement
			milestoneKey = replacement
			changed = true
		}
		oldKey := strings.TrimSpace(item.Key)
		item.Key = normalizeTaskKeyForMilestone(item.Key, milestoneKey)
		if item.Key != oldKey {
			changed = true
		}
		if item.Key == "" {
			item.Key = nextTaskKey(next, milestoneKey)
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
		if ref := strings.TrimSpace(milestone.LegacyRef); ref != "" {
			out[ref] = key
		}
	}
	return out
}

func nextPlanningKeyNumber(items []Milestone, prefix string) int {
	next := 1
	for _, item := range items {
		for _, key := range []string{item.Key, item.LegacyRef} {
			if n, ok := parsePlanningKey(key, prefix); ok && n >= next {
				next = n + 1
			}
		}
	}
	return next
}

func nextTaskKey(items []Task, milestoneKey string) string {
	return ScopedTaskKey(milestoneKey, nextTaskKeyNumber(items, milestoneKey))
}

func nextTaskKeyNumber(items []Task, milestoneKey string) int {
	next := 1
	for _, item := range items {
		if n, ok := parseTaskKeyNumber(item.Key, milestoneKey); ok && n >= next {
			next = n + 1
		}
	}
	return next
}

func normalizeTaskKeyForMilestone(raw, milestoneKey string) string {
	key := strings.TrimSpace(raw)
	if key == "" {
		return ""
	}
	if _, ok := parseTaskKeyNumber(key, milestoneKey); ok {
		return key
	}
	if n, ok := parsePlanningKey(key, "T"); ok {
		return ScopedTaskKey(milestoneKey, n)
	}
	return key
}

func parseTaskKeyNumber(raw, milestoneKey string) (int, bool) {
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
	Key                string `json:"key,omitempty"`
	LegacyRef          string `json:"ref,omitempty"`
	Title              string `json:"title"`
	Status             string `json:"status"`
	Notes              string `json:"notes,omitempty"`
	DependsOnKey       string `json:"depends_on_key,omitempty"`
	LegacyDependsOnKey string `json:"depends_on_ref,omitempty"`
}

type MilestoneAddInput struct {
	Key                string `json:"key,omitempty"`
	LegacyRef          string `json:"ref,omitempty"`
	Title              string `json:"title"`
	Notes              string `json:"notes,omitempty"`
	DependsOnKey       string `json:"depends_on_key,omitempty"`
	LegacyDependsOnKey string `json:"depends_on_ref,omitempty"`
}

type TaskAddInput struct {
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
		legacyRef := strings.TrimSpace(item.LegacyRef)
		title := strings.TrimSpace(item.Title)
		status, err := MilestoneStatusString(strings.TrimSpace(item.Status))
		if err != nil {
			return nil, fmt.Errorf("invalid milestone status %q", item.Status)
		}
		notes := strings.TrimSpace(item.Notes)
		dependsOnKey := strings.TrimSpace(item.DependsOnKey)
		if dependsOnKey == "" {
			dependsOnKey = strings.TrimSpace(item.LegacyDependsOnKey)
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
			LegacyRef:    legacyRef,
			Title:        title,
			Status:       status,
			Notes:        notes,
			DependsOnKey: dependsOnKey,
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
		legacyRef := strings.TrimSpace(item.LegacyRef)
		title := strings.TrimSpace(item.Title)
		notes := strings.TrimSpace(item.Notes)
		dependsOnKey := strings.TrimSpace(item.DependsOnKey)
		if dependsOnKey == "" {
			dependsOnKey = strings.TrimSpace(item.LegacyDependsOnKey)
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
			LegacyRef:    legacyRef,
			Title:        title,
			Status:       MilestoneStatusPending,
			Notes:        notes,
			DependsOnKey: dependsOnKey,
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

func ParseTaskAddItems(raw string) ([]string, error) {
	var items []TaskAddInput
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

func ParseTaskKey(raw string) (string, error) {
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

func ParseTaskStatus(raw string) (TaskStatus, error) {
	status, err := TaskStatusString(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid task status %q", raw)
	}
	return status, nil
}

func NormalizeTaskContent(content string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(content))), " ")
}

func ValidateNoDuplicateTaskContent(existing []Task, added []string) error {
	seen := make(map[string]struct{}, len(existing)+len(added))
	for _, item := range existing {
		content := NormalizeTaskContent(item.Content)
		if content != "" {
			seen[content] = struct{}{}
		}
	}
	for _, item := range added {
		content := NormalizeTaskContent(item)
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

func ValidateTaskProgress(items []Task) error {
	inProgress := 0
	for _, item := range items {
		if item.Status == TaskStatusInProgress {
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
		dependsOnKey := MilestoneDependsOnKey(item)
		if dependsOnKey == "" {
			continue
		}
		if dependsOnKey == key {
			return fmt.Errorf("milestone %q cannot depend on itself", key)
		}
		if _, exists := seenRefs[dependsOnKey]; !exists {
			return fmt.Errorf("milestone %q depends on unknown milestone %q", key, dependsOnKey)
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

func PlanForKey(plan Plan, ref string) Plan {
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
