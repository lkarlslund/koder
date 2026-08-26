package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/curation"
	"github.com/lkarlslund/koder/internal/memory/curationadapter"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/memorytool"
)

func TestMemoryFDiskResearchLearnAndReuseVertical(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	store, err := memoryPebble.Open(stateDir)
	if err != nil {
		t.Fatalf("open Memory store: %v", err)
	}
	service := newLearnReuseMemoryService(t, store)
	chunk, err := service.CreateChunk(ctx, memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Linux partition tools", Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
		Tags:  []string{"linux", "partitioning"},
	}})
	if err != nil {
		t.Fatalf("create learning destination: %v", err)
	}

	firstRuntime := tools.Runtime{
		SessionID: "00000000-0000-7000-8000-000000000091",
		ChatID:    "00000000-0000-7000-8000-000000000092",
		Services:  memorytool.RuntimeService(service),
	}
	initial, err := callMemorySearch(ctx, firstRuntime, "Linux partition tools")
	if err != nil {
		t.Fatalf("initial Memory search: %v", err)
	}
	initialSearch, ok := initial.Stored.(memoryService.LexicalSearchResult)
	if !ok || len(initialSearch.Matches) != 0 || initialSearch.CorpusDocumentCount != 0 {
		t.Fatalf("initial Memory search = %#v", initial.Stored)
	}

	var researchCalls atomic.Int32
	mockedResearch := func(query string) string {
		researchCalls.Add(1)
		if query != "fdisk alternatives util-linux" {
			t.Fatalf("mocked web research query = %q", query)
		}
		return "Authoritative util-linux sfdisk manual: sfdisk is script-oriented; save the current table with sfdisk --dump before writing. Source: https://man7.org/linux/man-pages/man8/sfdisk.8.html"
	}
	researchResult := mockedResearch("fdisk alternatives util-linux")
	now := time.Date(2026, time.August, 22, 20, 0, 0, 0, time.UTC)
	user := domain.TimelineItem{
		ID: "00000000-0000-7000-8000-000000000093", ChatID: firstRuntime.ChatID,
		Content:   domain.UserMessage{Text: "Partition the Linux test disk without losing the current table."},
		CreatedAt: now, UpdatedAt: now, SealedAt: now,
	}
	assistant := domain.TimelineItem{
		ID: "00000000-0000-7000-8000-000000000097", ChatID: firstRuntime.ChatID,
		Content: domain.AssistantMessage{Text: "The disk was partitioned with sfdisk after preserving the old table.", Tools: []domain.ToolCall{
			{ToolCallID: "memory-before", Tool: domain.ToolKindMemory, Args: map[string]string{"action": "search", "query": "Linux partition tools"}, Status: domain.ToolStatusDone, Result: &domain.ToolResult{Text: initial.Output}},
			{ToolCallID: "fdisk-missing", Tool: domain.ToolKindExecCommand, Args: map[string]string{"cmd": "fdisk /dev/vdb"}, Status: domain.ToolStatusErrored, Error: &domain.ToolError{Code: "not_found", Message: "fdisk: command not found"}},
			{ToolCallID: "research-sfdisk", Tool: domain.ToolKindWebSearch, Args: map[string]string{"query": "fdisk alternatives util-linux"}, Status: domain.ToolStatusDone, Result: &domain.ToolResult{Text: researchResult}},
			{ToolCallID: "sfdisk-success", Tool: domain.ToolKindExecCommand, Args: map[string]string{"cmd": "sfdisk --dump /dev/vdb && sfdisk /dev/vdb"}, Status: domain.ToolStatusDone, Result: &domain.ToolResult{Text: "sfdisk succeeded; old partition table saved"}},
		}},
		CreatedAt: now, UpdatedAt: now, SealedAt: now,
	}
	turn := chatpkg.CompletedTurn{
		Session: domain.Session{ID: firstRuntime.SessionID},
		Chat:    domain.Chat{ID: firstRuntime.ChatID, SessionID: firstRuntime.SessionID},
		User:    user, Assistant: assistant, Items: []domain.TimelineItem{user, assistant},
	}
	signals := completedTurnSignals(turn)
	if !hasCurationSignal(signals, memory.CurationSignalKindFailedThenSucceeded) ||
		!hasCurationSignal(signals, memory.CurationSignalKindResearchedThenSucceeded) {
		t.Fatalf("completed turn signals = %#v", signals)
	}

	material := curation.TurnMaterial{
		Destinations: []curation.Destination{{ID: chunk.Chunk.ID, Title: chunk.Chunk.Title, Kind: chunk.Chunk.Kind, Scope: chunk.Chunk.Scope}},
	}
	for _, item := range turn.Items {
		role, text := curationTimelineText(item)
		material.Items = append(material.Items, curation.TurnItem{ID: string(item.ID), Role: role, Text: text})
	}
	loader := learnReuseLoaderFunc(func(_ context.Context, source memory.CompletedTurnRef, itemIDs []string) (curation.TurnMaterial, error) {
		if source.SessionID != string(firstRuntime.SessionID) || source.ChatID != string(firstRuntime.ChatID) {
			return curation.TurnMaterial{}, fmt.Errorf("unexpected curation source: %#v", source)
		}
		if len(itemIDs) != 2 || itemIDs[0] != string(user.ID) || itemIDs[1] != string(assistant.ID) {
			return curation.TurnMaterial{}, fmt.Errorf("unexpected curation items: %v", itemIDs)
		}
		return material, nil
	})
	var draftCalls atomic.Int32
	model := learnReuseDraftModelFunc(func(_ context.Context, _ memory.CurationRecord, loaded curation.TurnMaterial, schema json.RawMessage) ([]byte, error) {
		draftCalls.Add(1)
		encoded, err := json.Marshal(loaded)
		if err != nil {
			return nil, err
		}
		text := string(encoded)
		for _, required := range []string{"fdisk: command not found", "sfdisk succeeded", "man-pages/man8/sfdisk.8.html"} {
			if !strings.Contains(text, required) {
				return nil, fmt.Errorf("curation material is missing %q", required)
			}
		}
		if !strings.Contains(string(schema), `"additionalProperties":false`) {
			return nil, fmt.Errorf("curation schema is not strict")
		}
		return json.Marshal(map[string]any{"candidates": []any{map[string]any{
			"action": "create_entry", "chunk_id": chunk.Chunk.ID,
			"entry": map[string]any{
				"kind": "procedure", "title": "Use sfdisk when fdisk is unavailable",
				"summary": "Use sfdisk for scriptable Linux partition changes when fdisk is absent.",
				"body":    "Save the current table with `sfdisk --dump`, review the output, then apply the new layout with `sfdisk`. Source: https://man7.org/linux/man-pages/man8/sfdisk.8.html",
				"aliases": []string{"scriptable fdisk alternative"}, "tags": []string{"linux", "partitioning", "sfdisk"},
				"scope": map[string]any{"kind": "global"}, "applicability": map[string]any{"operating_systems": []string{"linux"}}, "confidence": 0.95,
			},
			"reason":          "fdisk was unavailable and researched sfdisk completed the task",
			"source_item_ids": []string{string(user.ID), string(assistant.ID)},
		}}})
	})

	candidateStore := curation.NewMemoryCandidateStore()
	routed, err := curation.NewReviewRoutingSink(candidateStore)
	if err != nil {
		t.Fatal(err)
	}
	sink, err := curation.NewDeduplicatingSink(curationadapter.ServiceEntrySource{Service: service}, routed)
	if err != nil {
		t.Fatal(err)
	}
	extractor, err := curation.NewModelExtractor(curation.ModelExtractorConfig{
		Loader: loader, Model: model, Sink: sink, Classifier: memory.RuleClassifier{},
	})
	if err != nil {
		t.Fatal(err)
	}
	recordStore := curation.NewMemoryStore()
	queue, err := curation.New(curation.Config{Store: recordStore, Extractor: extractor, NewID: id.New})
	if err != nil {
		t.Fatal(err)
	}
	applied := make(chan curation.ApplyReceipt, 1)
	reviewed := curationadapter.ReviewedApplier{Service: service}
	automatic := notifyingLearnReuseApplier{Next: curationadapter.LowRiskApplier{Service: service}, Applied: applied}
	manager, err := curation.NewReviewManagerWithAutomatic(candidateStore, recordStore, reviewed, automatic, reviewed)
	if err != nil {
		t.Fatal(err)
	}
	coordinatorCtx, cancelCoordinator := context.WithCancel(context.Background())
	coordinator, err := curation.NewCoordinator(coordinatorCtx, queue, 1, manager)
	if err != nil {
		cancelCoordinator()
		t.Fatal(err)
	}
	engine := &Engine{curation: coordinator, curationReview: manager}
	engine.ObserveCompletedTurn(turn)
	var receipt curation.ApplyReceipt
	select {
	case receipt = <-applied:
	case <-time.After(3 * time.Second):
		engine.StopMemoryCuration()
		cancelCoordinator()
		t.Fatal("timed out waiting for automatic durable learning")
	}
	engine.StopMemoryCuration()
	cancelCoordinator()
	if !receipt.Created || receipt.EntryID == "" || receipt.AfterRevision != 1 {
		t.Fatalf("automatic curation receipt = %#v", receipt)
	}
	if researchCalls.Load() != 1 || draftCalls.Load() != 1 {
		t.Fatalf("research calls=%d curation draft calls=%d", researchCalls.Load(), draftCalls.Load())
	}
	appliedCandidates, err := manager.List(ctx, []curation.CandidateStatus{curation.CandidateStatusApplied}, 10)
	if err != nil || len(appliedCandidates) != 1 || appliedCandidates[0].Receipt.EntryID != receipt.EntryID {
		t.Fatalf("applied curation candidates = %#v, %v", appliedCandidates, err)
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	if err := service.ShutdownOperations(shutdownCtx); err != nil {
		cancelShutdown()
		t.Fatalf("stop Memory operations: %v", err)
	}
	cancelShutdown()
	if err := store.Close(); err != nil {
		t.Fatalf("close Memory store: %v", err)
	}
	reopenedStore, err := memoryPebble.Open(stateDir)
	if err != nil {
		t.Fatalf("reopen Memory store: %v", err)
	}
	t.Cleanup(func() { _ = reopenedStore.Close() })
	reopenedService := newLearnReuseMemoryService(t, reopenedStore)
	laterRuntime := tools.Runtime{
		SessionID: "00000000-0000-7000-8000-000000000101",
		ChatID:    "00000000-0000-7000-8000-000000000102",
		Services:  memorytool.RuntimeService(reopenedService),
	}
	reused, err := callMemorySearch(ctx, laterRuntime, "Linux partition tools fdisk")
	if err != nil {
		t.Fatalf("later-chat Memory search: %v", err)
	}
	reusedSearch, ok := reused.Stored.(memoryService.LexicalSearchResult)
	if !ok || len(reusedSearch.Matches) != 1 || reusedSearch.Matches[0].EntryID != receipt.EntryID {
		t.Fatalf("later-chat Memory search = %#v", reused.Stored)
	}
	match := reusedSearch.Matches[0]
	if match.Rank.Evidence <= 0 || match.Document.Verification != memory.VerificationStatusVerified ||
		!strings.Contains(match.Document.Summary, "sfdisk") {
		t.Fatalf("reused Memory match = %#v", match)
	}
	full, err := tools.Call(ctx, tools.Options{Runtime: laterRuntime, Request: tools.Request{Tool: tools.Memory, Args: map[string]string{
		"action": "get", "object_kind": "entry", "id": string(receipt.EntryID),
	}}})
	if err != nil {
		t.Fatalf("later-chat Memory get: %v", err)
	}
	for _, required := range []string{"sfdisk --dump", "man-pages/man8/sfdisk.8.html"} {
		if !strings.Contains(full.Output, required) {
			t.Fatalf("later-chat Memory body is missing %q: %s", required, full.Output)
		}
	}
	if researchCalls.Load() != 1 || draftCalls.Load() != 1 {
		t.Fatalf("reuse repeated remote work: research=%d drafting=%d", researchCalls.Load(), draftCalls.Load())
	}
}

type learnReuseLoaderFunc func(context.Context, memory.CompletedTurnRef, []string) (curation.TurnMaterial, error)

func (fn learnReuseLoaderFunc) Load(ctx context.Context, source memory.CompletedTurnRef, itemIDs []string) (curation.TurnMaterial, error) {
	return fn(ctx, source, itemIDs)
}

type learnReuseDraftModelFunc func(context.Context, memory.CurationRecord, curation.TurnMaterial, json.RawMessage) ([]byte, error)

func (fn learnReuseDraftModelFunc) Draft(ctx context.Context, record memory.CurationRecord, material curation.TurnMaterial, schema json.RawMessage) ([]byte, error) {
	return fn(ctx, record, material, schema)
}

type notifyingLearnReuseApplier struct {
	Next    curation.CandidateApplier
	Applied chan<- curation.ApplyReceipt
}

func (a notifyingLearnReuseApplier) ApplyCandidate(ctx context.Context, record memory.CurationRecord, draft curation.CandidateDraft) (curation.ApplyReceipt, error) {
	receipt, err := a.Next.ApplyCandidate(ctx, record, draft)
	if err == nil {
		a.Applied <- receipt
	}
	return receipt, err
}

func newLearnReuseMemoryService(t *testing.T, store *memoryPebble.Store) *memoryService.Service {
	t.Helper()
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:learn-reuse-e2e"}),
	})
	if err != nil {
		t.Fatalf("create Memory service: %v", err)
	}
	return service
}

func callMemorySearch(ctx context.Context, runtime tools.Runtime, query string) (tools.Result, error) {
	return tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: tools.Memory, Args: map[string]string{
		"action": "search", "query": query, "limit": "5",
	}}})
}

func hasCurationSignal(signals []memory.CurationSignal, kind memory.CurationSignalKind) bool {
	for _, signal := range signals {
		if signal.Kind == kind {
			return true
		}
	}
	return false
}
