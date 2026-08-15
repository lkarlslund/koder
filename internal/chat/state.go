package chat

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

// ChatState owns the current chat's mutable in-memory records.
type ChatState struct {
	chat      domain.Chat
	timeline  []*TimelineRecord
	byItem    map[string]*TimelineRecord
	approvals []Approval
	pending   PendingAssistantTurn
}

// TimelineRecord stores one mutable timeline item.
type TimelineRecord struct {
	Item              domain.TimelineItem
	revision          uint64
	persistedRevision uint64
}

type dirtyTimelineItem struct {
	Item     domain.TimelineItem
	Revision uint64
}

type PendingAssistantTurn struct {
	Text      string
	Reasoning string
	CreatedAt time.Time
}

// NewTimelineState builds a chat state from persisted timeline snapshots.
func NewTimelineState(chat domain.Chat, timeline []domain.TimelineItem, approvals []Approval) *ChatState {
	state := &ChatState{}
	state.MergeTimelineLoaded(chat, timeline, approvals)
	return state
}

// MergeTimelineLoaded refreshes timeline records while preserving record identity by ID.
func (s *ChatState) MergeTimelineLoaded(chat domain.Chat, timeline []domain.TimelineItem, approvals []Approval) {
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
		record.revision++
		record.persistedRevision = record.revision
		nextTimeline = append(nextTimeline, record)
		nextByItem[item.ID] = record
	}
	s.timeline = nextTimeline
	s.byItem = nextByItem
	if len(timeline) == 0 && len(approvals) > 0 {
		s.approvals = slices.Clone(approvals)
		return
	}
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

// DiscardActiveAssistant removes the latest unsealed assistant item from the
// live timeline. Streaming creates this item before the model response is
// durable; hard cancellation should remove it instead of sealing a partial
// answer into the visible transcript.
func (s *ChatState) DiscardActiveAssistant() bool {
	if s == nil {
		return false
	}
	for idx := len(s.timeline) - 1; idx >= 0; idx-- {
		record := s.timeline[idx]
		if record == nil || record.Item.Sealed() {
			continue
		}
		if _, ok := record.Item.Content.(domain.AssistantMessage); !ok {
			break
		}
		if s.byItem != nil {
			delete(s.byItem, record.Item.ID)
		}
		s.timeline = slices.Delete(s.timeline, idx, idx+1)
		return true
	}
	return false
}

func (s *ChatState) HasPendingExecutableToolCalls() bool {
	if s == nil {
		return false
	}
	for idx := len(s.timeline) - 1; idx >= 0; idx-- {
		assistant, ok := s.timeline[idx].Item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range assistant.Tools {
			if call.Status == domain.ToolStatusPending && call.Result == nil && call.Error == nil && call.Approval == nil {
				return true
			}
		}
		return false
	}
	return false
}

func (s *ChatState) CurrentContextSize() domain.ContextUsage {
	if s == nil {
		return domain.ContextUsage{}
	}
	tokens := 0
	if s.chat.ContextTokensKnown {
		tokens = s.chat.LastKnownContextTokens
	}
	if timelineAnchor, ok := timelineContextAnchorTokens(s.SnapshotTimeline()); ok {
		tokens = timelineAnchor
	}
	if tokens < 0 {
		tokens = 0
	}
	return domain.ContextUsage{
		AnchorTokens: tokens,
		TotalTokens:  tokens,
	}
}

func latestTimelineContextAnchor(items []domain.TimelineItem) (int, int, bool) {
	for idx := len(items) - 1; idx >= 0; idx-- {
		switch payload := items[idx].Content.(type) {
		case domain.AssistantMessage:
			if payload.Usage != nil && payload.Usage.Normalized().HasAnyTokens() {
				contextTokens, ok := payload.Usage.Normalized().ContextTokens()
				if ok {
					return idx, contextTokens, true
				}
			}
		}
	}
	return 0, 0, false
}

func timelineContextAnchorTokens(items []domain.TimelineItem) (int, bool) {
	_, tokens, ok := latestTimelineContextAnchor(items)
	return tokens, ok
}

// Timeline returns the ordered timeline records for the current chat.
func (s *ChatState) Timeline() []*TimelineRecord {
	if s == nil {
		return nil
	}
	return s.timeline
}

func (s *ChatState) NextTimelineSequence() int64 {
	if s == nil {
		return 1
	}
	var latest int64
	for _, record := range s.timeline {
		if record != nil && record.Item.Seq > latest {
			latest = record.Item.Seq
		}
	}
	return latest + 1
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
		item.ID = NewTimelineID(item.CreatedAt)
	}
	record := &TimelineRecord{Item: item, revision: 1}
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
		item.ID = NewTimelineID(item.CreatedAt)
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
	record.revision++
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
		item.ID = NewTimelineID(item.CreatedAt)
	}
	if record := s.byItem[item.ID]; record != nil {
		return record, false
	}
	if record := s.replaceTemporaryActiveAssistant(item); record != nil {
		return record, false
	}
	record := &TimelineRecord{Item: item, revision: 1}
	s.timeline = append(s.timeline, record)
	s.byItem[item.ID] = record
	return record, true
}

// TimelineItem returns the current in-memory value for one timeline item.
func (s *ChatState) TimelineItem(itemID id.ID) (domain.TimelineItem, bool) {
	if s == nil || itemID == "" || s.byItem == nil {
		return domain.TimelineItem{}, false
	}
	record := s.byItem[itemID]
	if record == nil {
		return domain.TimelineItem{}, false
	}
	return record.Item, true
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
	active.revision++
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

// DirtyTimeline returns timeline values whose latest in-memory revision has
// not been confirmed durable yet.
func (s *ChatState) DirtyTimeline() []dirtyTimelineItem {
	if s == nil {
		return nil
	}
	out := make([]dirtyTimelineItem, 0)
	for _, record := range s.timeline {
		if record == nil || record.revision == record.persistedRevision {
			continue
		}
		out = append(out, dirtyTimelineItem{Item: record.Item, Revision: record.revision})
	}
	return out
}

// MarkTimelinePersisted marks revision durable without hiding a newer edit
// that may have arrived while storage I/O was in progress.
func (s *ChatState) MarkTimelinePersisted(itemID id.ID, revision uint64) {
	if s == nil || itemID == "" || revision == 0 {
		return
	}
	record := s.byItem[itemID]
	if record == nil || revision > record.revision {
		return
	}
	if revision > record.persistedRevision {
		record.persistedRevision = revision
	}
}

// MarkTimelineItemDirty records an in-place mutation made through a timeline
// record pointer.
func (s *ChatState) MarkTimelineItemDirty(itemID id.ID) uint64 {
	if s == nil || itemID == "" {
		return 0
	}
	record := s.byItem[itemID]
	if record == nil {
		return 0
	}
	record.revision++
	return record.revision
}

// TimelineRevision returns the current in-memory revision for itemID.
func (s *ChatState) TimelineRevision(itemID id.ID) uint64 {
	if s == nil || itemID == "" {
		return 0
	}
	record := s.byItem[itemID]
	if record == nil {
		return 0
	}
	return record.revision
}

// ReplaceTimeline replaces the loaded timeline while preserving current chat metadata.
func (s *ChatState) ReplaceTimeline(timeline []domain.TimelineItem) {
	if s == nil {
		return
	}
	s.MergeTimelineLoaded(s.chat, timeline, nil)
}

// ActiveAssistant returns the latest unsealed assistant item, creating one when absent.
func (s *ChatState) ActiveAssistant(chatID id.ID, now time.Time) *TimelineRecord {
	if record := s.LatestActiveAssistant(); record != nil {
		return record
	}
	if s == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	seq := s.NextTimelineSequence()
	item := domain.TimelineItem{
		ID:        NewTimelineID(now),
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
func (s *ChatState) AppendAssistantText(chatID id.ID, text string) error {
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
	record.revision++
	return nil
}

// AppendAssistantReasoning appends reasoning to the active assistant item.
func (s *ChatState) AppendAssistantReasoning(chatID id.ID, text string) error {
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
	record.revision++
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
		record.revision++
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
func (s *ChatState) Approvals() []Approval {
	if s == nil {
		return nil
	}
	return slices.Clone(s.approvals)
}

func deriveApprovals(chat domain.Chat, timeline []domain.TimelineItem) []Approval {
	var approvals []Approval
	for _, item := range timeline {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range assistant.Tools {
			if call.Status != domain.ToolStatusAwaitingApproval {
				continue
			}
			approvals = append(approvals, Approval{
				ID:         SyntheticApprovalID(string(call.ToolCallID)),
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
	return strings.TrimSpace(call.Tool.String())
}

// UpsertApproval adds or replaces one approval snapshot.
func (s *ChatState) UpsertApproval(approval Approval) {
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
func (s *ChatState) RemoveApproval(approvalID id.ID) {
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
