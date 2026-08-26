package memorytool

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
)

func TestMemoryDanishOTCSourcePolicyAndFreshnessVertical(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 23, 8, 0, 0, 0, time.UTC)
	reviewAfter := now.AddDate(0, 1, 0)
	store, err := memoryPebble.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store, Now: func() time.Time { return now },
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:danish-otc-e2e"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := runtimeFor(service)

	underspecified, err := json.Marshal(map[string]any{
		"title": "Danish OTC medicine", "kind": "reference", "scope": map[string]any{"kind": "global"},
		"risk": []string{"medical"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(ctx, runtime, map[string]string{
		"action": "chunk_create", "chunk": string(underspecified), "review_approved": "true",
	})
	var invalid *memoryService.ServiceError
	if !errors.As(err, &invalid) || invalid.Code != memoryService.ErrorCodeInvalid {
		t.Fatalf("underspecified medical chunk error = %T %v", err, err)
	}

	const sourcePolicy = "Require a current authoritative Danish source such as the Danish Medicines Agency or the official medicine leaflet; record access date, preserve uncertainty, and recheck before health guidance."
	chunkJSON, err := json.Marshal(map[string]any{
		"title": "Over-the-counter medicine in Denmark", "description": "Danish OTC reference material; not individual medical advice.",
		"kind": "reference", "scope": map[string]any{"kind": "global"}, "language": "da", "locale": "da-DK", "domain": "medicine",
		"risk": []string{"medical"}, "source_policy": sourcePolicy, "review_after": reviewAfter.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	createdCall, err := call(ctx, runtime, map[string]string{
		"action": "chunk_create", "chunk": string(chunkJSON), "review_approved": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	created := createdCall.Stored.(chunkMutationResult)
	if created.Chunk.Locale != "da-DK" || created.Chunk.Domain != "medicine" || created.Chunk.SourcePolicy != sourcePolicy ||
		!created.Chunk.ReviewAfter.Equal(reviewAfter) || !slices.Contains(created.Chunk.Risk, memory.RiskClassMedical) {
		t.Fatalf("Danish OTC chunk = %#v", created.Chunk)
	}

	evidence, err := service.CreateEvidence(ctx, memoryService.CreateEvidenceRequest{Evidence: memory.Evidence{
		Type: memory.EvidenceTypeWeb, Quality: memory.EvidenceQualityAuthoritative,
		Source: memory.Source{
			ID: "source:laegemiddelstyrelsen", URI: "https://laegemiddelstyrelsen.dk/",
			Title: "Danish Medicines Agency", ContentHash: "sha256:danish-otc-fixture", AccessedAt: now,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	entryJSON, err := json.Marshal(map[string]any{
		"kind": "reference", "title": "Danish OTC medicine safety boundary",
		"summary": "OTC availability and instructions in Denmark must be checked against current authoritative Danish sources.",
		"body":    "Product availability, dosing, contraindications, and interactions can change or depend on the person. This stored summary is uncertain and is not a substitute for the current official leaflet, a pharmacist, or a doctor.",
		"tags":    []string{"denmark", "otc", "medicine"}, "risk": []string{"medical"}, "confidence": 0.8,
		"applicability": map[string]any{"locales": []string{"da-DK"}}, "review_after": reviewAfter.Format(time.RFC3339),
		"evidence_ids": []string{string(evidence.Evidence.ID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	entryCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_create", "chunk_id": string(created.Chunk.ID), "entry": string(entryJSON), "review_approved": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := entryCall.Stored.(entryMutationResult).Entry
	verificationJSON, err := json.Marshal(map[string]any{
		"status": "verified", "method": "Checked the bounded claim against the named authoritative Danish source.",
		"evidence_ids": []string{string(evidence.Evidence.ID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	verifiedCall, err := call(ctx, runtime, map[string]string{
		"action": "verify", "id": string(entry.ID), "expected_revision": "1", "verification": string(verificationJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	verified := verifiedCall.Stored.(entryMutationResult).Entry
	if verified.Verification.Status != memory.VerificationStatusVerified ||
		!slices.Contains(verified.Verification.EvidenceIDs, evidence.Evidence.ID) {
		t.Fatalf("verified OTC entry = %#v", verified)
	}

	fresh, err := service.SearchLexical(ctx, memoryService.LexicalSearchRequest{
		Query: "Danish OTC medicine", ValidAt: reviewAfter.Add(-time.Nanosecond), Limit: 5,
	})
	if err != nil || len(fresh.Matches) != 1 || fresh.Matches[0].EntryID != verified.ID ||
		fresh.Matches[0].Rank.Verification <= 0 || fresh.Matches[0].Rank.Evidence <= 0 ||
		hasSearchWarning(fresh.Warnings, memoryService.SearchWarningReviewDue, verified.ID) {
		t.Fatalf("fresh OTC search = %#v, %v", fresh, err)
	}
	stale, err := service.SearchLexical(ctx, memoryService.LexicalSearchRequest{
		Query: "Danish OTC medicine", ValidAt: reviewAfter, Limit: 5,
	})
	if err != nil || len(stale.Matches) != 1 || !hasSearchWarning(stale.Warnings, memoryService.SearchWarningReviewDue, verified.ID) ||
		stale.Matches[0].Rank.Freshness > fresh.Matches[0].Rank.Freshness {
		t.Fatalf("review-due OTC search = %#v, %v", stale, err)
	}
	getCall, err := call(ctx, runtime, map[string]string{
		"action": "chunk_get", "id": string(created.Chunk.ID),
	})
	if err != nil || !strings.Contains(getCall.Output, sourcePolicy) || !strings.Contains(getCall.Output, "da-DK") ||
		!strings.Contains(getCall.Output, reviewAfter.Format(time.RFC3339)) {
		t.Fatalf("model-facing OTC policy = %s, %v", getCall.Output, err)
	}
}

func hasSearchWarning(warnings []memoryService.SearchWarning, code memoryService.SearchWarningCode, entryID memory.EntryID) bool {
	return slices.ContainsFunc(warnings, func(warning memoryService.SearchWarning) bool {
		return warning.Code == code && warning.EntryID == entryID
	})
}
