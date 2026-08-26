package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	"github.com/lkarlslund/koder/internal/memory/curation"
)

type webCurationApplier struct{}

func (webCurationApplier) ApplyCandidate(context.Context, memory.CurationRecord, curation.CandidateDraft) (curation.ApplyReceipt, error) {
	return curation.ApplyReceipt{EntryID: "00000000-0000-7000-8000-000000000091", AfterRevision: 1, Created: true}, nil
}

func (webCurationApplier) UndoCandidate(context.Context, curation.StoredCandidate) error { return nil }

func TestMemoryCurationAPIListsAcceptsAndUndoesCandidates(t *testing.T) {
	ctrl := newTestController(t)
	manager := webCurationManager(t)
	ctrl.SetMemoryCuration(manager)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	token := bindMemoryTestDevice(t, srv)

	response := memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.CurationCandidatePath+"?status=pending_review&limit=10", "")
	if response.StatusCode != http.StatusUnauthorized {
		closeMemoryHTTPResponse(t, response)
		t.Fatalf("unauthenticated status = %d", response.StatusCode)
	}
	closeMemoryHTTPResponse(t, response)

	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.CurationCandidatePath+"?status=pending_review&limit=10", token)
	var listed memoryapi.CurationCandidateListResponse
	decodeMemoryResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || len(listed.Candidates) != 1 || listed.Candidates[0].Status != curation.CandidateStatusPendingReview {
		t.Fatalf("list response status=%d value=%#v", response.StatusCode, listed)
	}
	candidate := listed.Candidates[0]

	response = memoryCurationDecisionRequest(t, srv.URL()+memoryapi.CurationCandidateActionPath(candidate.ID, "accept"), token, memoryapi.CurationCandidateDecisionRequest{ExpectedVersion: candidate.Version})
	var accepted memoryapi.CurationCandidateResponse
	decodeMemoryResponse(t, response, &accepted)
	if response.StatusCode != http.StatusOK || accepted.Candidate.Status != curation.CandidateStatusApplied {
		t.Fatalf("accept response status=%d value=%#v", response.StatusCode, accepted)
	}

	response = memoryCurationDecisionRequest(t, srv.URL()+memoryapi.CurationCandidateActionPath(candidate.ID, "accept"), token, memoryapi.CurationCandidateDecisionRequest{ExpectedVersion: candidate.Version})
	if response.StatusCode != http.StatusConflict {
		closeMemoryHTTPResponse(t, response)
		t.Fatalf("stale accept status = %d", response.StatusCode)
	}
	closeMemoryHTTPResponse(t, response)

	response = memoryCurationDecisionRequest(t, srv.URL()+memoryapi.CurationCandidateActionPath(candidate.ID, "undo"), token, memoryapi.CurationCandidateDecisionRequest{ExpectedVersion: accepted.Candidate.Version})
	var undone memoryapi.CurationCandidateResponse
	decodeMemoryResponse(t, response, &undone)
	if response.StatusCode != http.StatusOK || undone.Candidate.Status != curation.CandidateStatusUndone {
		t.Fatalf("undo response status=%d value=%#v", response.StatusCode, undone)
	}
}

func webCurationManager(t *testing.T) *curation.ReviewManager {
	t.Helper()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	recordID := memory.CurationRecordID("00000000-0000-7000-8000-000000000080")
	records := curation.NewMemoryStore()
	record := memory.CurationRecord{
		ID: recordID,
		Source: memory.CompletedTurnRef{
			SessionID: "00000000-0000-7000-8000-000000000081", ChatID: "00000000-0000-7000-8000-000000000082",
			UserItemID: "00000000-0000-7000-8000-000000000083", AssistantItemID: "00000000-0000-7000-8000-000000000084", SealedAt: now,
		},
		Signals: []memory.CurationSignal{{Kind: memory.CurationSignalKindUserCorrection, SourceItemIDs: []string{"00000000-0000-7000-8000-000000000083"}, Confidence: 1}},
		State:   memory.CurationStateQueued, CreatedAt: now, UpdatedAt: now,
	}
	if _, _, err := records.Submit(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := records.Claim(context.Background(), now.Add(time.Second)); err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	candidates := curation.NewMemoryCandidateStore()
	draft := curation.CandidateDraft{
		Action: curation.CandidateActionCreateEntry, ChunkID: "00000000-0000-7000-8000-000000000085",
		Entry:  curation.EntryDraft{Kind: memory.EntryKindFact, Title: "Use sfdisk", Summary: "Use sfdisk when fdisk is unavailable.", Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Confidence: 0.9},
		Reason: "Repeated successful workaround", SourceItemIDs: []string{"00000000-0000-7000-8000-000000000083"},
		Route: curation.CandidateRoutePendingReview, ReviewReasons: []string{"A person should confirm durable memory."},
	}
	if count, err := candidates.StoreCandidates(context.Background(), recordID, []curation.CandidateDraft{draft}); err != nil || count != 1 {
		t.Fatalf("store candidates = %d, %v", count, err)
	}
	if _, err := records.Complete(context.Background(), recordID, curation.ExtractionResult{CandidateCount: 1}, "", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	manager, err := curation.NewReviewManager(candidates, records, webCurationApplier{}, webCurationApplier{})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func memoryCurationDecisionRequest(t *testing.T, url, token string, body memoryapi.CurationCandidateDecisionRequest) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
