package curation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/observability"
)

var queueTestTime = time.Date(2026, 8, 22, 19, 0, 0, 0, time.UTC)

type extractorFunc func(context.Context, knowledge.CurationRecord) (ExtractionResult, error)

func (fn extractorFunc) Extract(ctx context.Context, record knowledge.CurationRecord) (ExtractionResult, error) {
	return fn(ctx, record)
}

func queueTestRequest() SubmitRequest {
	return SubmitRequest{
		Source: knowledge.CompletedTurnRef{
			SessionID: "00000000-0000-7000-8000-000000000021", ChatID: "00000000-0000-7000-8000-000000000022",
			UserItemID: "00000000-0000-7000-8000-000000000023", AssistantItemID: "00000000-0000-7000-8000-000000000024",
			SealedAt: queueTestTime,
		},
		Signals: []knowledge.CurationSignal{{
			Kind: knowledge.CurationSignalKindUserCorrection,
			SourceItemIDs: []string{
				"00000000-0000-7000-8000-000000000023", "00000000-0000-7000-8000-000000000024",
			},
			Confidence: 1,
		}},
	}
}

func newTestQueue(t *testing.T, store Store, extractor Extractor) *Queue {
	t.Helper()
	queue, err := New(Config{
		Store: store, Extractor: extractor,
		NewID: func() string { return "00000000-0000-7000-8000-000000000020" },
		Now:   func() time.Time { return queueTestTime },
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func TestQueueSubmitsAndExtractsCandidates(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	var extracted knowledge.CurationRecord
	queue := newTestQueue(t, store, extractorFunc(func(_ context.Context, record knowledge.CurationRecord) (ExtractionResult, error) {
		extracted = record
		return ExtractionResult{CandidateCount: 2}, nil
	}))
	submitted, err := queue.Submit(context.Background(), queueTestRequest())
	if err != nil || !submitted.Created {
		t.Fatalf("Submit() = %#v, %v", submitted, err)
	}
	processed, err := queue.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext() = %v, %v", processed, err)
	}
	if extracted.State != knowledge.CurationStateProcessing || extracted.Attempts != 1 {
		t.Fatalf("extractor record = %#v", extracted)
	}
	completed, err := queue.Get(context.Background(), submitted.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != knowledge.CurationStateCandidatesReady || completed.CandidateCount != 2 || completed.CompletedAt.IsZero() {
		t.Fatalf("completed record = %#v", completed)
	}
	if processed, err := queue.ProcessNext(context.Background()); err != nil || processed {
		t.Fatalf("ProcessNext(empty) = %v, %v", processed, err)
	}
}

func TestQueueRecordsSafeOperationMetrics(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	now := queueTestTime
	operationNumber := 0
	recorder := observability.NewRecorder(observability.Config{
		Now: func() time.Time {
			current := now
			now = now.Add(time.Millisecond)
			return current
		},
		NewID: func() string {
			operationNumber++
			return "curation-operation-" + string(rune('0'+operationNumber))
		},
	})
	queue, err := New(Config{
		Store: store,
		Extractor: extractorFunc(func(context.Context, knowledge.CurationRecord) (ExtractionResult, error) {
			return ExtractionResult{CandidateCount: 2}, nil
		}),
		NewID:      func() string { return "00000000-0000-7000-8000-000000000020" },
		Now:        func() time.Time { return queueTestTime },
		Operations: recorder,
	})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := queue.Submit(context.Background(), queueTestRequest())
	if err != nil || submitted.OperationID != "curation-operation-1" {
		t.Fatalf("Submit() = %#v, %v", submitted, err)
	}
	if processed, err := queue.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("ProcessNext() = %v, %v", processed, err)
	}
	if processed, err := queue.ProcessNext(context.Background()); err != nil || processed {
		t.Fatalf("ProcessNext(empty) = %v, %v", processed, err)
	}
	snapshot := recorder.Snapshot()
	if len(snapshot.Operations) != 2 || len(snapshot.Recent) != 3 ||
		snapshot.Operations[0].Operation != observability.OperationCurationProcess ||
		snapshot.Operations[0].Succeeded != 1 || snapshot.Operations[0].Empty != 1 ||
		snapshot.Operations[1].Operation != observability.OperationCurationSubmit || snapshot.Operations[1].Succeeded != 1 {
		t.Fatalf("curation metrics = %#v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{queueTestRequest().Source.SessionID, queueTestRequest().Source.ChatID, "user_correction"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("curation metrics disclosed source content %q: %s", private, encoded)
		}
	}
}

func TestQueueSubmissionIsIdempotentByCompletedTurn(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	queue := newTestQueue(t, store, extractorFunc(func(context.Context, knowledge.CurationRecord) (ExtractionResult, error) {
		return ExtractionResult{}, nil
	}))
	first, err := queue.Submit(context.Background(), queueTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	request := queueTestRequest()
	request.Signals[0].Confidence = 0.5
	second, err := queue.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || second.Record.ID != first.Record.ID || second.Record.Signals[0].Confidence != 1 {
		t.Fatalf("idempotent submissions first=%#v second=%#v", first, second)
	}
}

func TestQueueRecordsSafeExtractionFailure(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	queue, err := New(Config{
		Store: store,
		Extractor: extractorFunc(func(context.Context, knowledge.CurationRecord) (ExtractionResult, error) {
			return ExtractionResult{CandidateCount: 99}, errors.New("provider included private transcript text")
		}),
		NewID:         func() string { return "00000000-0000-7000-8000-000000000020" },
		Now:           func() time.Time { return queueTestTime },
		SafeErrorCode: func(error) string { return "unsafe provider detail: token=secret" },
	})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := queue.Submit(context.Background(), queueTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	processed, err := queue.ProcessNext(context.Background())
	if !processed || err == nil {
		t.Fatalf("ProcessNext() = %v, %v", processed, err)
	}
	failed, err := queue.Get(context.Background(), submitted.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != knowledge.CurationStateFailed || failed.LastErrorCode != "extraction_failed" || failed.CandidateCount != 0 {
		t.Fatalf("failed record = %#v", failed)
	}
}

func TestMemoryStoreClaimsOneRecordOnceAcrossWorkers(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	record := knowledge.CurationRecord{
		ID: "00000000-0000-7000-8000-000000000020", Source: queueTestRequest().Source,
		Signals: queueTestRequest().Signals, State: knowledge.CurationStateQueued,
		CreatedAt: queueTestTime, UpdatedAt: queueTestTime,
	}
	if _, _, err := store.Submit(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	wait.Add(2)
	claimed := make(chan bool, 2)
	for range 2 {
		go func() {
			defer wait.Done()
			_, ok, err := store.Claim(context.Background(), queueTestTime)
			if err != nil {
				t.Errorf("Claim() error = %v", err)
			}
			claimed <- ok
		}()
	}
	wait.Wait()
	close(claimed)
	count := 0
	for ok := range claimed {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("successful claims = %d, want 1", count)
	}
}
