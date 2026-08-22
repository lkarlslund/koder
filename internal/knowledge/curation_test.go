package knowledge

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var curationTestTime = time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)

func validCurationRecord() CurationRecord {
	return CurationRecord{
		ID: CurationRecordID("00000000-0000-7000-8000-000000000010"),
		Source: CompletedTurnRef{
			SessionID: "00000000-0000-7000-8000-000000000011", ChatID: "00000000-0000-7000-8000-000000000012",
			UserItemID: "00000000-0000-7000-8000-000000000013", AssistantItemID: "00000000-0000-7000-8000-000000000014",
			SealedAt: curationTestTime,
		},
		Signals: []CurationSignal{{
			Kind: CurationSignalKindFailedThenSucceeded,
			SourceItemIDs: []string{
				"00000000-0000-7000-8000-000000000015", "00000000-0000-7000-8000-000000000016",
			},
			Confidence: 0.9,
		}},
		State: CurationStateQueued, CreatedAt: curationTestTime, UpdatedAt: curationTestTime,
	}
}

func TestCurationRecordValidatesCompletedTurnSignals(t *testing.T) {
	t.Parallel()
	record := validCurationRecord()
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	record.State = CurationStateCandidatesReady
	record.Attempts = 1
	record.CandidateCount = 2
	record.CompletedAt = curationTestTime.Add(time.Second)
	record.UpdatedAt = record.CompletedAt
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate(completed) error = %v", err)
	}
}

func TestCurationRecordRejectsInvalidLifecycleAndSignals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*CurationRecord)
		field  string
	}{
		{name: "unsealed source", mutate: func(value *CurationRecord) { value.Source.SealedAt = time.Time{} }, field: "source.sealed_at"},
		{name: "same turn items", mutate: func(value *CurationRecord) { value.Source.AssistantItemID = value.Source.UserItemID }, field: "source.assistant_item_id"},
		{name: "unknown signal", mutate: func(value *CurationRecord) { value.Signals[0].Kind = CurationSignalKind(255) }, field: "signal.kind"},
		{name: "zero confidence", mutate: func(value *CurationRecord) { value.Signals[0].Confidence = 0 }, field: "signal.confidence"},
		{name: "duplicate source item", mutate: func(value *CurationRecord) { value.Signals[0].SourceItemIDs[1] = value.Signals[0].SourceItemIDs[0] }, field: "signal.source_item_ids"},
		{name: "duplicate signal kind", mutate: func(value *CurationRecord) { value.Signals = append(value.Signals, value.Signals[0]) }, field: "curation.signals"},
		{name: "premature completion", mutate: func(value *CurationRecord) { value.CompletedAt = value.CreatedAt }, field: "curation.completed_at"},
		{name: "ready without candidates", mutate: func(value *CurationRecord) {
			value.State = CurationStateCandidatesReady
			value.CompletedAt = value.CreatedAt
		}, field: "curation.candidate_count"},
		{name: "count before ready", mutate: func(value *CurationRecord) { value.CandidateCount = 1 }, field: "curation.candidate_count"},
		{name: "failed without code", mutate: func(value *CurationRecord) { value.State = CurationStateFailed; value.CompletedAt = value.CreatedAt }, field: "curation.last_error_code"},
		{name: "unsafe failure detail", mutate: func(value *CurationRecord) {
			value.State = CurationStateFailed
			value.CompletedAt = value.CreatedAt
			value.LastErrorCode = "provider said token=secret"
		}, field: "curation.last_error_code"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validCurationRecord()
			test.mutate(&record)
			err := record.Validate()
			if !errors.Is(err, ErrInvalidRecord) || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("Validate() error = %v, want invalid %s", err, test.field)
			}
		})
	}
}

func TestCurationRecordJSONUsesStableSignalAndStateNames(t *testing.T) {
	t.Parallel()
	record := validCurationRecord()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{`"kind":"failed_then_succeeded"`, `"state":"queued"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Marshal() = %s, missing %s", text, expected)
		}
	}
	if strings.Contains(text, "completed_at") || strings.Contains(text, "last_error_code") {
		t.Fatalf("Marshal() included empty terminal metadata: %s", text)
	}
}
