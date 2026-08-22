package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestClassifyErrorMapsStableKnowledgeOutcomes(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		err       error
		code      ErrorCode
		retryable bool
	}{
		"unavailable": {err: fmt.Errorf("private path: %w", knowledgeStore.ErrClosed), code: ErrorCodeUnavailable, retryable: true},
		"forbidden":   {err: fmt.Errorf("policy detail: %w", ErrChunkPolicyDenied), code: ErrorCodeForbidden},
		"not found":   {err: knowledgeStore.ErrNotFound, code: ErrorCodeNotFound},
		"conflict":    {err: knowledgeStore.ErrConflict, code: ErrorCodeConflict, retryable: true},
		"stale":       {err: knowledgeStore.ErrStaleCursor, code: ErrorCodeStale, retryable: true},
		"invalid":     {err: knowledge.ErrInvalidRecord, code: ErrorCodeInvalid},
		"internal":    {err: errors.New("private internal detail"), code: ErrorCodeInternal},
	} {
		t.Run(name, func(t *testing.T) {
			got := ClassifyError(test.err)
			if got.Code != test.code || got.Retryable != test.retryable || got.Message == "" || !errors.Is(got, test.err) {
				t.Fatalf("ClassifyError() = %#v", got)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "policy detail") {
				t.Fatalf("serialized error leaked cause: %s", encoded)
			}
		})
	}
}

func TestClassifyErrorPreservesStructuredDependencyBlockers(t *testing.T) {
	t.Parallel()
	want := &ChunkDeletionBlockedError{
		ChunkID: "019f132e-4f3a-739a-9ab2-5198dcd19e67",
		Blockers: knowledgeStore.ChunkDeletionBlockers{
			EntryIDs: []knowledge.EntryID{"01a01f76-1ff6-7c1d-967a-66ad5703dd33"},
			LinkIDs:  []knowledge.LinkID{"01a020a6-84d5-7b03-a995-bb2cfb4528b0"},
		},
	}
	got := ClassifyError(fmt.Errorf("delete failed: %w", want))
	if got.Code != ErrorCodeDependency || got.Details == nil || got.Details.ChunkID != want.ChunkID ||
		got.Details.ChunkBlockers == nil || !slices.Equal(got.Details.ChunkBlockers.EntryIDs, want.Blockers.EntryIDs) {
		t.Fatalf("ClassifyError(dependency) = %#v", got)
	}
	want.Blockers.EntryIDs[0] = "changed-after-classification"
	if got.Details.ChunkBlockers.EntryIDs[0] == want.Blockers.EntryIDs[0] {
		t.Fatal("dependency details alias mutable cause data")
	}
}

func TestNewTruncatedErrorSanitizesAndOrdersReasonCodes(t *testing.T) {
	t.Parallel()
	got := NewTruncatedError([]string{"node_limit", " private content ", "depth_limit", "node_limit"})
	if got.Code != ErrorCodeTruncated || got.Details == nil ||
		!slices.Equal(got.Details.TruncationReasons, []string{"depth_limit", "node_limit"}) {
		t.Fatalf("NewTruncatedError() = %#v", got)
	}
}

func TestClassifyErrorIsNilSafeAndIdempotent(t *testing.T) {
	t.Parallel()
	if ClassifyError(nil) != nil {
		t.Fatal("ClassifyError(nil) returned an error")
	}
	want := NewTruncatedError([]string{"time_limit"})
	if got := ClassifyError(fmt.Errorf("wrapped: %w", want)); got != want {
		t.Fatalf("ClassifyError(existing) = %p, want %p", got, want)
	}
}
