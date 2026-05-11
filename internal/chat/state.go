package chat

import (
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
	messages  []*MessageRecord
	byMessage map[int64]*MessageRecord
	byPart    map[int64]*PartRecord
	approvals []store.Approval
	toolRuns  []*ToolRunRecord
	pending   PendingAssistantTurn

	byToolRunID      map[string]*ToolRunRecord
	byToolCallID     map[string]*ToolRunRecord
	byToolApprovalID map[int64]*ToolRunRecord
}

// MessageRecord stores one message and its ordered parts.
type MessageRecord struct {
	Message domain.Message
	Parts   []*PartRecord
}

// PartRecord stores one mutable part.
type PartRecord struct {
	Part domain.Part
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

// NewChatState builds a chat state from persisted snapshots.
func NewChatState(chat domain.Chat, messages []domain.Message, parts map[int64][]domain.Part, approvals []store.Approval) *ChatState {
	state := &ChatState{}
	state.MergeLoaded(chat, messages, parts, approvals)
	return state
}

// MergeLoaded refreshes chat records from loaded store snapshots while preserving record identity by ID.
func (s *ChatState) MergeLoaded(chat domain.Chat, messages []domain.Message, parts map[int64][]domain.Part, approvals []store.Approval) {
	s.chat = chat
	if s.byMessage == nil {
		s.byMessage = map[int64]*MessageRecord{}
	}
	if s.byPart == nil {
		s.byPart = map[int64]*PartRecord{}
	}
	nextMessages := make([]*MessageRecord, 0, len(messages))
	nextByMessage := make(map[int64]*MessageRecord, len(messages))
	nextByPart := make(map[int64]*PartRecord)
	for _, msg := range messages {
		record := s.byMessage[msg.ID]
		if record == nil {
			record = &MessageRecord{}
		}
		record.Message = msg
		loadedParts := parts[msg.ID]
		record.Parts = make([]*PartRecord, 0, len(loadedParts))
		for _, part := range loadedParts {
			partRecord := s.byPart[part.ID]
			if partRecord == nil {
				partRecord = &PartRecord{}
			}
			partRecord.Part = part
			record.Parts = append(record.Parts, partRecord)
			nextByPart[part.ID] = partRecord
		}
		nextMessages = append(nextMessages, record)
		nextByMessage[msg.ID] = record
	}
	s.messages = nextMessages
	s.byMessage = nextByMessage
	s.byPart = nextByPart
	s.approvals = slices.Clone(approvals)
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
	tailEstimate, anchored := sessionctx.EstimateTailTokens(s.SnapshotMessages(), s.SnapshotParts())
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

// Messages returns the ordered message records for the current chat.
func (s *ChatState) Messages() []*MessageRecord {
	if s == nil {
		return nil
	}
	return s.messages
}

// Approvals returns the current approval snapshot.
func (s *ChatState) Approvals() []store.Approval {
	if s == nil {
		return nil
	}
	return slices.Clone(s.approvals)
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
		s.byToolApprovalID = map[int64]*ToolRunRecord{}
	}
	nextRuns := make([]*ToolRunRecord, 0, len(runs))
	nextByID := make(map[string]*ToolRunRecord, len(runs))
	nextByCall := make(map[string]*ToolRunRecord, len(runs))
	nextByApproval := make(map[int64]*ToolRunRecord, len(runs))
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
		if run.ApprovalID > 0 {
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

// AppendMessage adds a new message record to the current chat state.
func (s *ChatState) AppendMessage(message domain.Message, parts []domain.Part) *MessageRecord {
	if s == nil {
		return nil
	}
	if s.byMessage == nil {
		s.byMessage = map[int64]*MessageRecord{}
	}
	if s.byPart == nil {
		s.byPart = map[int64]*PartRecord{}
	}
	record := &MessageRecord{Message: message, Parts: make([]*PartRecord, 0, len(parts))}
	for _, part := range parts {
		partRecord := &PartRecord{Part: part}
		record.Parts = append(record.Parts, partRecord)
		s.byPart[part.ID] = partRecord
	}
	s.messages = append(s.messages, record)
	s.byMessage[message.ID] = record
	return record
}

// UpsertMessageParts merges one persisted message and its parts into the chat state.
func (s *ChatState) UpsertMessageParts(message domain.Message, parts []domain.Part) (*MessageRecord, []PartMutation, bool) {
	if s == nil {
		return nil, nil, false
	}
	if s.byMessage == nil {
		s.byMessage = map[int64]*MessageRecord{}
	}
	if s.byPart == nil {
		s.byPart = map[int64]*PartRecord{}
	}
	record := s.byMessage[message.ID]
	created := false
	if record == nil {
		record = &MessageRecord{}
		s.messages = append(s.messages, record)
		s.byMessage[message.ID] = record
		created = true
	}
	record.Message = message
	mutations := make([]PartMutation, 0, len(parts))
	if len(parts) == 0 {
		return record, mutations, created
	}
	if len(record.Parts) == 0 {
		record.Parts = make([]*PartRecord, 0, len(parts))
	}
	for _, part := range parts {
		partRecord := s.byPart[part.ID]
		if partRecord == nil {
			partRecord = &PartRecord{}
			record.Parts = append(record.Parts, partRecord)
			s.byPart[part.ID] = partRecord
			partRecord.Part = part
			mutations = append(mutations, PartMutation{Record: partRecord, Created: true})
			continue
		}
		partRecord.Part = part
		if !messageHasPartRecord(record, partRecord) {
			record.Parts = append(record.Parts, partRecord)
			mutations = append(mutations, PartMutation{Record: partRecord, Created: true})
			continue
		}
		mutations = append(mutations, PartMutation{Record: partRecord, Created: false})
	}
	return record, mutations, created
}

// MessageByID returns a message record by ID.
func (s *ChatState) MessageByID(messageID int64) *MessageRecord {
	if s == nil || messageID == 0 {
		return nil
	}
	return s.byMessage[messageID]
}

// SnapshotMessages returns detached message values.
func (s *ChatState) SnapshotMessages() []domain.Message {
	if s == nil {
		return nil
	}
	out := make([]domain.Message, 0, len(s.messages))
	for _, record := range s.messages {
		if record == nil {
			continue
		}
		out = append(out, record.Message)
	}
	return out
}

// SnapshotParts returns detached part values keyed by message ID.
func (s *ChatState) SnapshotParts() map[int64][]domain.Part {
	if s == nil {
		return nil
	}
	out := make(map[int64][]domain.Part, len(s.messages))
	for _, record := range s.messages {
		if record == nil {
			continue
		}
		out[record.Message.ID] = record.PartSnapshots()
	}
	return out
}

// Update mutates a message record in place.
func (r *MessageRecord) Update(update func(*domain.Message)) {
	if r == nil || update == nil {
		return
	}
	update(&r.Message)
}

// MessageValue returns the current message value.
func (r *MessageRecord) MessageValue() domain.Message {
	if r == nil {
		return domain.Message{}
	}
	return r.Message
}

// PartRecords returns the current part records for the message.
func (r *MessageRecord) PartRecords() []*PartRecord {
	if r == nil {
		return nil
	}
	return r.Parts
}

// PartSnapshots returns detached part values for the message.
func (r *MessageRecord) PartSnapshots() []domain.Part {
	if r == nil || len(r.Parts) == 0 {
		return nil
	}
	out := make([]domain.Part, 0, len(r.Parts))
	for _, part := range r.Parts {
		if part == nil {
			continue
		}
		out = append(out, part.Part)
	}
	return out
}

// Update mutates a part record in place.
func (r *PartRecord) Update(update func(*domain.Part)) {
	if r == nil || update == nil {
		return
	}
	update(&r.Part)
}

// PartValue returns the current part value.
func (r *PartRecord) PartValue() domain.Part {
	if r == nil {
		return domain.Part{}
	}
	return r.Part
}

// PartByID returns a part record by ID.
func (s *ChatState) PartByID(partID int64) *PartRecord {
	if s == nil || partID == 0 {
		return nil
	}
	return s.byPart[partID]
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
		s.byToolApprovalID = map[int64]*ToolRunRecord{}
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
	if s == nil || approval.ID == 0 {
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
func (s *ChatState) RemoveApproval(approvalID int64) {
	if s == nil || approvalID == 0 {
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
	if run.ApprovalID > 0 {
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
	if run.ApprovalID > 0 {
		s.byToolApprovalID[run.ApprovalID] = record
	}
}

type PartMutation struct {
	Record  *PartRecord
	Created bool
}

func messageHasPartRecord(record *MessageRecord, candidate *PartRecord) bool {
	if record == nil || candidate == nil {
		return false
	}
	for _, item := range record.Parts {
		if item == candidate {
			return true
		}
	}
	return false
}
