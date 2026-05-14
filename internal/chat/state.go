package chat

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/sessionctx"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tokenestimate"
	"github.com/lkarlslund/koder/internal/ui"
)

// ChatState owns the current chat's mutable in-memory records.
type ChatState struct {
	chat      domain.Chat
	timeline  []*TimelineRecord
	byItem    map[string]*TimelineRecord
	approvals []store.Approval
	toolRuns  []*ToolRunRecord
	pending   PendingAssistantTurn

	byToolRunID      map[string]*ToolRunRecord
	byToolCallID     map[string]*ToolRunRecord
	byToolApprovalID map[domain.ID]*ToolRunRecord
}

// TimelineRecord stores one mutable timeline item.
type TimelineRecord struct {
	Item domain.TimelineItem
}

// ToolRunRecord stores one mutable tool run view model for the current chat.
type ToolRunRecord struct {
	Run ui.ToolRun
}

type PendingAssistantTurn struct {
	Text      string
	Reasoning string
	CreatedAt time.Time
}

// NewTimelineState builds a chat state from persisted timeline snapshots.
func NewTimelineState(chat domain.Chat, timeline []domain.TimelineItem, approvals []store.Approval) *ChatState {
	state := &ChatState{}
	state.MergeTimelineLoaded(chat, timeline, approvals)
	return state
}

// MergeTimelineLoaded refreshes timeline records while preserving record identity by ID.
func (s *ChatState) MergeTimelineLoaded(chat domain.Chat, timeline []domain.TimelineItem, approvals []store.Approval) {
	s.chat = chat
	if s.byItem == nil {
		s.byItem = map[string]*TimelineRecord{}
	}
	nextTimeline := make([]*TimelineRecord, 0, len(timeline))
	nextByItem := make(map[string]*TimelineRecord, len(timeline))
	for _, item := range timeline {
		record := s.byItem[item.ID]
		if record == nil {
			record = &TimelineRecord{}
		}
		record.Item = item
		nextTimeline = append(nextTimeline, record)
		nextByItem[item.ID] = record
	}
	s.timeline = nextTimeline
	s.byItem = nextByItem
	_ = approvals
	s.approvals = deriveApprovals(chat, timeline)
}

func (s *ChatState) Chat() domain.Chat {
	if s == nil {
		return domain.Chat{}
	}
	return s.chat
}

func (s *ChatState) SetChat(chat domain.Chat) {
	if s == nil {
		return
	}
	s.chat = chat
}

func (s *ChatState) UpdateChat(update func(*domain.Chat)) {
	if s == nil || update == nil {
		return
	}
	update(&s.chat)
}

func (s *ChatState) PendingAssistant() PendingAssistantTurn {
	if s == nil {
		return PendingAssistantTurn{}
	}
	return s.pending
}

func (s *ChatState) AppendPendingAssistantText(text string) {
	if s == nil || text == "" {
		return
	}
	if s.pending.CreatedAt.IsZero() {
		s.pending.CreatedAt = time.Now().UTC()
	}
	s.pending.Text += text
}

func (s *ChatState) AppendPendingAssistantReasoning(text string) {
	if s == nil || text == "" {
		return
	}
	if s.pending.CreatedAt.IsZero() {
		s.pending.CreatedAt = time.Now().UTC()
	}
	s.pending.Reasoning += text
}

func (s *ChatState) ClearPendingAssistant() {
	if s == nil {
		return
	}
	s.pending = PendingAssistantTurn{}
}

func (s *ChatState) PendingAssistantContextTokens() int {
	if s == nil {
		return 0
	}
	total := 0
	if text := strings.TrimSpace(s.pending.Reasoning); text != "" {
		total += tokenestimate.Text(text)
	}
	if text := strings.TrimSpace(s.pending.Text); text != "" {
		total += tokenestimate.Text(text)
	}
	return total
}

func (s *ChatState) CurrentContextSize() domain.ContextUsage {
	if s == nil {
		return domain.ContextUsage{}
	}
	tailEstimate, anchored := sessionctx.EstimateTimelineTailTokens(s.SnapshotTimeline())
	if tailEstimate < 0 {
		tailEstimate = 0
	}
	liveTokens := s.PendingAssistantContextTokens()
	anchor := s.chat.LastKnownContextTokens
	if anchor < 0 {
		anchor = 0
	}
	usage := domain.ContextUsage{
		AnchorTokens: anchor,
		TailTokens:   tailEstimate,
		LiveTokens:   liveTokens,
		TotalTokens:  anchor + tailEstimate + liveTokens,
		Estimated:    !s.chat.ContextTokensKnown || tailEstimate > 0 || liveTokens > 0,
	}
	if !anchored && !s.chat.ContextTokensKnown {
		usage.Estimated = true
	}
	return usage
}

// Timeline returns the ordered timeline records for the current chat.
func (s *ChatState) Timeline() []*TimelineRecord {
	if s == nil {
		return nil
	}
	return s.timeline
}

// AppendTimelineItem adds a new timeline record to the current chat state.
func (s *ChatState) AppendTimelineItem(item domain.TimelineItem) *TimelineRecord {
	if s == nil {
		return nil
	}
	if s.byItem == nil {
		s.byItem = map[string]*TimelineRecord{}
	}
	if item.ID == "" {
		item.ID = domain.NewTimelineID(item.CreatedAt)
	}
	record := &TimelineRecord{Item: item}
	s.timeline = append(s.timeline, record)
	s.byItem[item.ID] = record
	return record
}

// UpsertTimelineItem merges one persisted timeline item into the current chat state.
func (s *ChatState) UpsertTimelineItem(item domain.TimelineItem) (*TimelineRecord, bool) {
	if s == nil {
		return nil, false
	}
	if s.byItem == nil {
		s.byItem = map[string]*TimelineRecord{}
	}
	if item.ID == "" {
		item.ID = domain.NewTimelineID(item.CreatedAt)
	}
	if record := s.replaceTemporaryActiveAssistant(item); record != nil {
		return record, false
	}
	record := s.byItem[item.ID]
	created := false
	if record == nil {
		record = &TimelineRecord{}
		s.timeline = append(s.timeline, record)
		s.byItem[item.ID] = record
		created = true
	}
	record.Item = item
	return record, created
}

// EnsureTimelineItem adds item if it does not already exist, without replacing
// existing mutable content.
func (s *ChatState) EnsureTimelineItem(item domain.TimelineItem) (*TimelineRecord, bool) {
	if s == nil {
		return nil, false
	}
	if s.byItem == nil {
		s.byItem = map[string]*TimelineRecord{}
	}
	if item.ID == "" {
		item.ID = domain.NewTimelineID(item.CreatedAt)
	}
	if record := s.byItem[item.ID]; record != nil {
		return record, false
	}
	if record := s.replaceTemporaryActiveAssistant(item); record != nil {
		return record, false
	}
	record := &TimelineRecord{Item: item}
	s.timeline = append(s.timeline, record)
	s.byItem[item.ID] = record
	return record, true
}

func (s *ChatState) replaceTemporaryActiveAssistant(item domain.TimelineItem) *TimelineRecord {
	if !isDurableTimelineItem(item) {
		return nil
	}
	if _, ok := item.Content.(domain.AssistantMessage); !ok {
		return nil
	}
	active := s.latestReplaceableAssistant(item)
	if active == nil {
		return nil
	}
	delete(s.byItem, active.Item.ID)
	active.Item = item
	s.byItem[item.ID] = active
	return active
}

func (s *ChatState) latestReplaceableAssistant(item domain.TimelineItem) *TimelineRecord {
	if s == nil {
		return nil
	}
	for idx := len(s.timeline) - 1; idx >= 0; idx-- {
		record := s.timeline[idx]
		if record == nil {
			continue
		}
		assistant, ok := record.Item.Content.(domain.AssistantMessage)
		if !ok {
			break
		}
		if isDurableTimelineItem(record.Item) {
			return nil
		}
		if record.Item.ChatID != "" && item.ChatID != "" && record.Item.ChatID != item.ChatID {
			return nil
		}
		if item.Seq > 0 && record.Item.Seq > 0 && item.Seq < record.Item.Seq {
			return nil
		}
		if !streamedAssistantMatchesFinal(assistant, item.Content.(domain.AssistantMessage)) {
			return nil
		}
		return record
	}
	return nil
}

func streamedAssistantMatchesFinal(streamed, final domain.AssistantMessage) bool {
	streamedText := strings.TrimSpace(streamed.Text)
	finalText := strings.TrimSpace(final.Text)
	if streamedText != "" && finalText != "" && streamedText != finalText {
		return false
	}
	streamedReasoning := strings.TrimSpace(streamed.Reasoning.Text)
	finalReasoning := strings.TrimSpace(final.Reasoning.Text)
	if streamedReasoning != "" && finalReasoning != "" && streamedReasoning != finalReasoning {
		return false
	}
	return true
}

func isDurableTimelineItem(item domain.TimelineItem) bool {
	return item.ID != ""
}

// SnapshotTimeline returns detached timeline values.
func (s *ChatState) SnapshotTimeline() []domain.TimelineItem {
	if s == nil {
		return nil
	}
	out := make([]domain.TimelineItem, 0, len(s.timeline))
	for _, record := range s.timeline {
		if record == nil {
			continue
		}
		out = append(out, record.Item)
	}
	return out
}

// ActiveAssistant returns the latest unsealed assistant item, creating one when absent.
func (s *ChatState) ActiveAssistant(chatID domain.ID, now time.Time) *TimelineRecord {
	if record := s.LatestActiveAssistant(); record != nil {
		return record
	}
	if s == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	seq := int64(len(s.timeline) + 1)
	item := domain.TimelineItem{
		ID:        domain.NewTimelineID(now),
		ChatID:    chatID,
		Seq:       seq,
		Content:   domain.AssistantMessage{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	return s.AppendTimelineItem(item)
}

// LatestActiveAssistant returns the latest unsealed assistant item.
func (s *ChatState) LatestActiveAssistant() *TimelineRecord {
	if s == nil {
		return nil
	}
	for idx := len(s.timeline) - 1; idx >= 0; idx-- {
		record := s.timeline[idx]
		if record == nil || record.Item.Sealed() {
			continue
		}
		if _, ok := record.Item.Content.(domain.AssistantMessage); ok {
			return record
		}
		break
	}
	return nil
}

// AppendAssistantText appends text to the active assistant item.
func (s *ChatState) AppendAssistantText(chatID domain.ID, text string) error {
	if s == nil || text == "" {
		return nil
	}
	record := s.ActiveAssistant(chatID, time.Now().UTC())
	if record == nil {
		return nil
	}
	if record.Item.Sealed() {
		return fmt.Errorf("assistant item %s is sealed", record.Item.ID)
	}
	assistant, ok := record.Item.Content.(domain.AssistantMessage)
	if !ok {
		return fmt.Errorf("timeline item %s is not assistant", record.Item.ID)
	}
	assistant.AppendText(text)
	record.Item.Content = assistant
	record.Item.UpdatedAt = time.Now().UTC()
	return nil
}

// AppendAssistantReasoning appends reasoning to the active assistant item.
func (s *ChatState) AppendAssistantReasoning(chatID domain.ID, text string) error {
	if s == nil || text == "" {
		return nil
	}
	record := s.ActiveAssistant(chatID, time.Now().UTC())
	if record == nil {
		return nil
	}
	if record.Item.Sealed() {
		return fmt.Errorf("assistant item %s is sealed", record.Item.ID)
	}
	assistant, ok := record.Item.Content.(domain.AssistantMessage)
	if !ok {
		return fmt.Errorf("timeline item %s is not assistant", record.Item.ID)
	}
	assistant.AppendReasoning(text)
	record.Item.Content = assistant
	record.Item.UpdatedAt = time.Now().UTC()
	return nil
}

// SealActiveAssistant marks the active assistant item complete.
func (s *ChatState) SealActiveAssistant(status domain.ToolStatus) {
	if s == nil {
		return
	}
	_ = status
	if record := s.LatestActiveAssistant(); record != nil && !record.Item.Sealed() {
		record.Item.Seal(time.Now().UTC())
	}
}

// TimelineValue returns the current timeline item value.
func (r *TimelineRecord) TimelineValue() domain.TimelineItem {
	if r == nil {
		return domain.TimelineItem{}
	}
	return r.Item
}

// Approvals returns the current approval snapshot.
func (s *ChatState) Approvals() []store.Approval {
	if s == nil {
		return nil
	}
	return slices.Clone(s.approvals)
}

func deriveApprovals(chat domain.Chat, timeline []domain.TimelineItem) []store.Approval {
	var approvals []store.Approval
	for _, item := range timeline {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range assistant.Tools {
			if call.Status != domain.ToolStatusAwaitingApproval {
				continue
			}
			approvals = append(approvals, store.Approval{
				ID:         store.SyntheticApprovalID(string(call.ToolCallID)),
				SessionID:  chat.SessionID,
				ChatID:     chat.ID,
				Tool:       call.Tool,
				ToolCallID: string(call.ToolCallID),
				Command:    approvalCommand(call),
				Status:     domain.ApprovalStatusPending,
				CreatedAt:  item.UpdatedAt,
			})
		}
	}
	return approvals
}

func approvalCommand(call domain.ToolCall) string {
	if command := strings.TrimSpace(call.Args["command"]); command != "" {
		return command
	}
	if path := strings.TrimSpace(call.Args["path"]); path != "" {
		return path
	}
	return strings.TrimSpace(string(call.Tool))
}

// ReplaceToolRuns refreshes tool-run records while preserving identity by tool call, approval, or run ID.
func (s *ChatState) ReplaceToolRuns(runs []ui.ToolRun) {
	if s == nil {
		return
	}
	if s.byToolRunID == nil {
		s.byToolRunID = map[string]*ToolRunRecord{}
	}
	if s.byToolCallID == nil {
		s.byToolCallID = map[string]*ToolRunRecord{}
	}
	if s.byToolApprovalID == nil {
		s.byToolApprovalID = map[domain.ID]*ToolRunRecord{}
	}
	nextRuns := make([]*ToolRunRecord, 0, len(runs))
	nextByID := make(map[string]*ToolRunRecord, len(runs))
	nextByCall := make(map[string]*ToolRunRecord, len(runs))
	nextByApproval := make(map[domain.ID]*ToolRunRecord, len(runs))
	for _, run := range runs {
		record := s.lookupToolRun(run)
		if record == nil {
			record = &ToolRunRecord{}
		}
		record.Run = run
		nextRuns = append(nextRuns, record)
		if run.ID != "" {
			nextByID[run.ID] = record
		}
		if run.ToolCallID != "" {
			nextByCall[run.ToolCallID] = record
		}
		if run.ApprovalID != "" {
			nextByApproval[run.ApprovalID] = record
		}
	}
	s.toolRuns = nextRuns
	s.byToolRunID = nextByID
	s.byToolCallID = nextByCall
	s.byToolApprovalID = nextByApproval
}

// ToolRuns returns the current ordered tool-run records for the current chat.
func (s *ChatState) ToolRuns() []*ToolRunRecord {
	if s == nil {
		return nil
	}
	return s.toolRuns
}

// ToolRunByCallID returns the tool-run record for a tool call when present.
func (s *ChatState) ToolRunByCallID(toolCallID string) *ToolRunRecord {
	if s == nil || toolCallID == "" {
		return nil
	}
	return s.byToolCallID[toolCallID]
}

// Update mutates a tool-run record in place.
func (r *ToolRunRecord) Update(update func(*ui.ToolRun)) {
	if r == nil || update == nil {
		return
	}
	update(&r.Run)
}

// RunValue returns the current tool-run value.
func (r *ToolRunRecord) RunValue() ui.ToolRun {
	if r == nil {
		return ui.ToolRun{}
	}
	return r.Run
}

// UpsertToolRun merges one tool run into the current chat state.
func (s *ChatState) UpsertToolRun(run ui.ToolRun) (*ToolRunRecord, bool) {
	if s == nil || strings.TrimSpace(run.ID) == "" {
		return nil, false
	}
	if s.byToolRunID == nil {
		s.byToolRunID = map[string]*ToolRunRecord{}
	}
	if s.byToolCallID == nil {
		s.byToolCallID = map[string]*ToolRunRecord{}
	}
	if s.byToolApprovalID == nil {
		s.byToolApprovalID = map[domain.ID]*ToolRunRecord{}
	}
	record := s.lookupToolRun(run)
	created := false
	if record == nil {
		record = &ToolRunRecord{Run: run}
		s.toolRuns = append(s.toolRuns, record)
		created = true
	} else {
		record.Run = run
	}
	s.indexToolRunRecord(record)
	return record, created
}

// UpsertApproval adds or replaces one approval snapshot.
func (s *ChatState) UpsertApproval(approval store.Approval) {
	if s == nil || approval.ID == "" {
		return
	}
	for idx := range s.approvals {
		if s.approvals[idx].ID == approval.ID {
			s.approvals[idx] = approval
			return
		}
	}
	s.approvals = append(s.approvals, approval)
}

// RemoveApproval removes one approval snapshot by ID.
func (s *ChatState) RemoveApproval(approvalID domain.ID) {
	if s == nil || approvalID == "" {
		return
	}
	for idx := range s.approvals {
		if s.approvals[idx].ID != approvalID {
			continue
		}
		s.approvals = append(s.approvals[:idx], s.approvals[idx+1:]...)
		return
	}
}

func (s *ChatState) lookupToolRun(run ui.ToolRun) *ToolRunRecord {
	if s == nil {
		return nil
	}
	if run.ToolCallID != "" {
		if record := s.byToolCallID[run.ToolCallID]; record != nil {
			return record
		}
	}
	if run.ApprovalID != "" {
		if record := s.byToolApprovalID[run.ApprovalID]; record != nil {
			return record
		}
	}
	if run.ID != "" {
		if record := s.byToolRunID[run.ID]; record != nil {
			return record
		}
	}
	return nil
}

func (s *ChatState) indexToolRunRecord(record *ToolRunRecord) {
	if s == nil || record == nil {
		return
	}
	run := record.Run
	if strings.TrimSpace(run.ID) != "" {
		s.byToolRunID[run.ID] = record
	}
	if strings.TrimSpace(run.ToolCallID) != "" {
		s.byToolCallID[run.ToolCallID] = record
	}
	if run.ApprovalID != "" {
		s.byToolApprovalID[run.ApprovalID] = record
	}
}
