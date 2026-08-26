package memory

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	maxCurationSignals       = 32
	maxCurationSourceItemIDs = 64
)

var curationErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// CurationRecordID identifies an auditable background-curation work record.
type CurationRecordID string

// CompletedTurnRef identifies the user/assistant boundary that made a turn eligible for
// background curation. It deliberately stores references rather than transcript text.
type CompletedTurnRef struct {
	SessionID       string    `json:"session_id"`
	ChatID          string    `json:"chat_id"`
	UserItemID      string    `json:"user_item_id"`
	AssistantItemID string    `json:"assistant_item_id"`
	SealedAt        time.Time `json:"sealed_at"`
}

// CurationSignal is a cheap, provider-independent reason to inspect a completed turn.
// SourceItemIDs identify the bounded timeline records that produced the signal. Confidence
// ranks work only; it never grants permission to write canonical memory.
type CurationSignal struct {
	Kind          CurationSignalKind `json:"kind"`
	SourceItemIDs []string           `json:"source_item_ids"`
	Confidence    float32            `json:"confidence"`
}

// CurationRecord is the privacy-minimal, auditable queue record for one sealed turn.
// CandidateCount describes extracted candidate records without embedding their contents.
// LastErrorCode is a safe machine class and must never contain provider or transcript text.
type CurationRecord struct {
	ID             CurationRecordID `json:"id"`
	Source         CompletedTurnRef `json:"source"`
	Signals        []CurationSignal `json:"signals"`
	State          CurationState    `json:"state"`
	Attempts       uint32           `json:"attempts"`
	CandidateCount uint32           `json:"candidate_count"`
	LastErrorCode  string           `json:"last_error_code,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	CompletedAt    time.Time        `json:"completed_at,omitzero"`
}

// Validate checks that the source is a genuinely sealed user/assistant turn boundary.
func (r CompletedTurnRef) Validate() error {
	identifiers := [...]struct{ field, value string }{
		{field: "source.session_id", value: r.SessionID},
		{field: "source.chat_id", value: r.ChatID},
		{field: "source.user_item_id", value: r.UserItemID},
		{field: "source.assistant_item_id", value: r.AssistantItemID},
	}
	for _, identifier := range identifiers {
		if err := validateUUIDv7(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if r.UserItemID == r.AssistantItemID {
		return invalid("source.assistant_item_id", "must differ from the user item ID")
	}
	return validateTime("source.sealed_at", r.SealedAt, true)
}

// Validate checks a completed-turn candidate signal.
func (s CurationSignal) Validate() error {
	if s.Kind == CurationSignalKindUnspecified || !s.Kind.IsACurationSignalKind() {
		return invalid("signal.kind", "is not a known curation signal")
	}
	if s.Confidence <= 0 || s.Confidence > 1 {
		return invalid("signal.confidence", "must be greater than 0 and at most 1")
	}
	if len(s.SourceItemIDs) == 0 || len(s.SourceItemIDs) > maxCurationSourceItemIDs {
		return invalid("signal.source_item_ids", fmt.Sprintf("must contain 1 to %d items", maxCurationSourceItemIDs))
	}
	seen := make(map[string]struct{}, len(s.SourceItemIDs))
	for index, itemID := range s.SourceItemIDs {
		if err := validateUUIDv7(fmt.Sprintf("signal.source_item_ids[%d]", index), itemID); err != nil {
			return err
		}
		if _, exists := seen[itemID]; exists {
			return invalid("signal.source_item_ids", "contains a duplicate item ID")
		}
		seen[itemID] = struct{}{}
	}
	return nil
}

// Validate checks a privacy-minimal curation queue record and its lifecycle invariants.
func (r CurationRecord) Validate() error {
	if err := validateUUIDv7("curation.id", string(r.ID)); err != nil {
		return err
	}
	if err := r.Source.Validate(); err != nil {
		return err
	}
	if len(r.Signals) == 0 || len(r.Signals) > maxCurationSignals {
		return invalid("curation.signals", fmt.Sprintf("must contain 1 to %d signals", maxCurationSignals))
	}
	seenKinds := make(map[CurationSignalKind]struct{}, len(r.Signals))
	for index, signal := range r.Signals {
		if err := signal.Validate(); err != nil {
			return invalid(fmt.Sprintf("curation.signals[%d]", index), strings.TrimPrefix(err.Error(), ErrInvalidRecord.Error()+": "))
		}
		if _, exists := seenKinds[signal.Kind]; exists {
			return invalid("curation.signals", "contains a duplicate signal kind")
		}
		seenKinds[signal.Kind] = struct{}{}
	}
	if r.State == CurationStateUnspecified || !r.State.IsACurationState() {
		return invalid("curation.state", "is not a known curation state")
	}
	if err := validateTimes(r.CreatedAt, r.UpdatedAt); err != nil {
		return err
	}
	terminal := r.State == CurationStateNoCandidates || r.State == CurationStateCandidatesReady || r.State == CurationStateFailed
	if terminal {
		if err := validateTime("curation.completed_at", r.CompletedAt, true); err != nil {
			return err
		}
		if r.CompletedAt.Before(r.CreatedAt) || r.CompletedAt.After(r.UpdatedAt) {
			return invalid("curation.completed_at", "must fall between created_at and updated_at")
		}
	} else if !r.CompletedAt.IsZero() {
		return invalid("curation.completed_at", "is only allowed for a terminal state")
	}
	if r.State == CurationStateCandidatesReady && r.CandidateCount == 0 {
		return invalid("curation.candidate_count", "must be positive when candidates are ready")
	}
	if r.State != CurationStateCandidatesReady && r.CandidateCount != 0 {
		return invalid("curation.candidate_count", "must be zero unless candidates are ready")
	}
	if r.State == CurationStateFailed {
		if !curationErrorCodePattern.MatchString(r.LastErrorCode) {
			return invalid("curation.last_error_code", "must be a safe machine error code when failed")
		}
	} else if r.LastErrorCode != "" {
		return invalid("curation.last_error_code", "is only allowed for a failed record")
	}
	return nil
}
