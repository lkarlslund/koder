package appstate

import (
	"slices"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

// ChatState owns the current chat's mutable in-memory records.
type ChatState struct {
	messages  []*MessageRecord
	byMessage map[int64]*MessageRecord
	byPart    map[int64]*PartRecord
	approvals []store.Approval
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

// NewChatState builds a chat state from persisted snapshots.
func NewChatState(messages []domain.Message, parts map[int64][]domain.Part, approvals []store.Approval) *ChatState {
	state := &ChatState{}
	state.MergeLoaded(messages, parts, approvals)
	return state
}

// MergeLoaded refreshes chat records from loaded store snapshots while preserving record identity by ID.
func (s *ChatState) MergeLoaded(messages []domain.Message, parts map[int64][]domain.Part, approvals []store.Approval) {
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
