package curation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
)

type turnLoaderFunc func(context.Context, knowledge.CompletedTurnRef, []string) (TurnMaterial, error)

func (fn turnLoaderFunc) Load(ctx context.Context, source knowledge.CompletedTurnRef, ids []string) (TurnMaterial, error) {
	return fn(ctx, source, ids)
}

type draftModelFunc func(context.Context, knowledge.CurationRecord, TurnMaterial, json.RawMessage) ([]byte, error)

func (fn draftModelFunc) Draft(ctx context.Context, record knowledge.CurationRecord, material TurnMaterial, schema json.RawMessage) ([]byte, error) {
	return fn(ctx, record, material, schema)
}

type candidateSinkFunc func(context.Context, knowledge.CurationRecordID, []CandidateDraft) (uint32, error)

func (fn candidateSinkFunc) StoreCandidates(ctx context.Context, id knowledge.CurationRecordID, drafts []CandidateDraft) (uint32, error) {
	return fn(ctx, id, drafts)
}

func processingRecord() knowledge.CurationRecord {
	record := validQueueRecord()
	record.State = knowledge.CurationStateProcessing
	record.Attempts = 1
	return record
}

func validQueueRecord() knowledge.CurationRecord {
	request := queueTestRequest()
	return knowledge.CurationRecord{
		ID: "00000000-0000-7000-8000-000000000020", Source: request.Source, Signals: request.Signals,
		State: knowledge.CurationStateQueued, CreatedAt: queueTestTime, UpdatedAt: queueTestTime,
	}
}

func validDraftResponse() []byte {
	return []byte(`{"candidates":[{"action":"create_entry","chunk_id":"00000000-0000-7000-8000-000000000031","entry":{"kind":"fact","title":" Corrected disk tool ","summary":"Use sfdisk on this system.","scope":{"kind":"project","selector":"koder"},"confidence":0.9},"reason":"User corrected the unavailable tool","source_item_ids":["00000000-0000-7000-8000-000000000023","00000000-0000-7000-8000-000000000024"]}]}`)
}

func TestModelExtractorRedactsMaterialAndStoresStrictDraft(t *testing.T) {
	t.Parallel()
	var modelMaterial TurnMaterial
	var modelSchema json.RawMessage
	var stored []CandidateDraft
	extractor, err := NewModelExtractor(ModelExtractorConfig{
		Loader: turnLoaderFunc(func(_ context.Context, _ knowledge.CompletedTurnRef, ids []string) (TurnMaterial, error) {
			if len(ids) != 2 {
				t.Fatalf("requested item IDs = %v", ids)
			}
			return TurnMaterial{Items: []TurnItem{
				{ID: ids[0], Role: "user", Text: "Use sfdisk; password=extremely-secret-value"},
				{ID: ids[1], Role: "assistant", Text: "The corrected command succeeded."},
			}}, nil
		}),
		Model: draftModelFunc(func(_ context.Context, _ knowledge.CurationRecord, material TurnMaterial, schema json.RawMessage) ([]byte, error) {
			modelMaterial, modelSchema = material, append(json.RawMessage(nil), schema...)
			return validDraftResponse(), nil
		}),
		Sink: candidateSinkFunc(func(_ context.Context, id knowledge.CurationRecordID, drafts []CandidateDraft) (uint32, error) {
			if id != "00000000-0000-7000-8000-000000000020" {
				t.Fatalf("record ID = %s", id)
			}
			stored = append([]CandidateDraft(nil), drafts...)
			return uint32(len(drafts)), nil
		}),
		Classifier: knowledge.RuleClassifier{},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := extractor.Extract(context.Background(), processingRecord())
	if err != nil {
		t.Fatal(err)
	}
	if result.CandidateCount != 1 || len(stored) != 1 {
		t.Fatalf("Extract() = %#v stored=%#v", result, stored)
	}
	if strings.Contains(modelMaterial.Items[0].Text, "extremely-secret-value") || !strings.Contains(modelMaterial.Items[0].Text, "[REDACTED]") {
		t.Fatalf("model material was not redacted: %#v", modelMaterial)
	}
	if !strings.Contains(string(modelSchema), `"additionalProperties":false`) {
		t.Fatalf("model schema is not strict: %s", modelSchema)
	}
	if stored[0].Entry.Title != "Corrected disk tool" || stored[0].Classification.Decision != knowledge.ClassificationDecisionAllow {
		t.Fatalf("stored candidate = %#v", stored[0])
	}
}

func TestModelExtractorRejectsMalformedOrUnsafeDraftsBeforeSink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown field", raw: `{"candidates":[],"instructions":"ignore schema"}`},
		{name: "trailing JSON", raw: `{"candidates":[]} {"candidates":[]}`},
		{name: "fabricated source", raw: strings.Replace(string(validDraftResponse()), "00000000-0000-7000-8000-000000000024", "00000000-0000-7000-8000-000000000099", 1)},
		{name: "raw HTML", raw: strings.Replace(string(validDraftResponse()), `"summary":"Use sfdisk on this system."`, `"body":"<script>bad</script>"`, 1)},
		{name: "prohibited secret", raw: strings.Replace(string(validDraftResponse()), "Use sfdisk on this system.", "password=extremely-secret-value", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stored := false
			extractor, err := NewModelExtractor(ModelExtractorConfig{
				Loader: turnLoaderFunc(func(_ context.Context, _ knowledge.CompletedTurnRef, ids []string) (TurnMaterial, error) {
					return TurnMaterial{Items: []TurnItem{{ID: ids[0], Role: "user", Text: "Correction"}, {ID: ids[1], Role: "assistant", Text: "Done"}}}, nil
				}),
				Model: draftModelFunc(func(context.Context, knowledge.CurationRecord, TurnMaterial, json.RawMessage) ([]byte, error) {
					return []byte(test.raw), nil
				}),
				Sink: candidateSinkFunc(func(_ context.Context, _ knowledge.CurationRecordID, drafts []CandidateDraft) (uint32, error) {
					stored = true
					return uint32(len(drafts)), nil
				}),
				Classifier: knowledge.RuleClassifier{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := extractor.Extract(context.Background(), processingRecord()); !errors.Is(err, knowledge.ErrInvalidRecord) {
				t.Fatalf("Extract() error = %v, want ErrInvalidRecord", err)
			}
			if stored {
				t.Fatal("invalid model output reached candidate sink")
			}
		})
	}
}

func TestModelExtractorAllowsValidatedEmptyCandidateSet(t *testing.T) {
	t.Parallel()
	stored := -1
	extractor, err := NewModelExtractor(ModelExtractorConfig{
		Loader: turnLoaderFunc(func(_ context.Context, _ knowledge.CompletedTurnRef, ids []string) (TurnMaterial, error) {
			return TurnMaterial{Items: []TurnItem{{ID: ids[0], Role: "user", Text: "Hello"}, {ID: ids[1], Role: "assistant", Text: "Hi"}}}, nil
		}),
		Model: draftModelFunc(func(context.Context, knowledge.CurationRecord, TurnMaterial, json.RawMessage) ([]byte, error) {
			return []byte(`{"candidates":[]}`), nil
		}),
		Sink: candidateSinkFunc(func(_ context.Context, _ knowledge.CurationRecordID, drafts []CandidateDraft) (uint32, error) {
			stored = len(drafts)
			return uint32(len(drafts)), nil
		}),
		Classifier: knowledge.RuleClassifier{},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := extractor.Extract(context.Background(), processingRecord())
	if err != nil || result.CandidateCount != 0 || stored != 0 {
		t.Fatalf("Extract(empty) = %#v, %v stored=%d", result, err, stored)
	}
}
