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
	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/curation"
	"github.com/lkarlslund/koder/internal/knowledge/curationadapter"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgePebble "github.com/lkarlslund/koder/internal/knowledge/store/pebble"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/knowledgetool"
)

func TestKnowledgeFDiskResearchLearnAndReuseVertical(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	store, err := knowledgePebble.Open(stateDir)
	if err != nil {
		t.Fatalf("open Knowledge store: %v", err)
	}
	service := newLearnReuseKnowledgeService(t, store)
	chunk, err := service.CreateChunk(ctx, knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "Linux partition tools", Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		Tags:  []string{"linux", "partitioning"},
	}})
	if err != nil {
		t.Fatalf("create learning destination: %v", err)
	}

	firstRuntime := tools.Runtime{
		SessionID: "00000000-0000-7000-8000-000000000091",
		ChatID:    "00000000-0000-7000-8000-000000000092",
		Services:  knowledgetool.RuntimeService(service),
	}
	initial, err := callKnowledgeSearch(ctx, firstRuntime, "Linux partition tools")
	if err != nil {
		t.Fatalf("initial Knowledge search: %v", err)
	}
	initialSearch, ok := initial.Stored.(knowledgeService.LexicalSearchResult)
	if !ok || len(initialSearch.Matches) != 0 || initialSearch.CorpusDocumentCount != 0 {
		t.Fatalf("initial Knowledge search = %#v", initial.Stored)
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
			{ToolCallID: "knowledge-before", Tool: domain.ToolKindKnowledge, Args: map[string]string{"action": "search", "query": "Linux partition tools"}, Status: domain.ToolStatusDone, Result: &domain.ToolResult{Text: initial.Output}},
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
	if !hasCurationSignal(signals, knowledge.CurationSignalKindFailedThenSucceeded) ||
		!hasCurationSignal(signals, knowledge.CurationSignalKindResearchedThenSucceeded) {
		t.Fatalf("completed turn signals = %#v", signals)
	}

	material := curation.TurnMaterial{
		Destinations: []curation.Destination{{ID: chunk.Chunk.ID, Title: chunk.Chunk.Title, Kind: chunk.Chunk.Kind, Scope: chunk.Chunk.Scope}},
	}
	for _, item := range turn.Items {
		role, text := curationTimelineText(item)
		material.Items = append(material.Items, curation.TurnItem{ID: string(item.ID), Role: role, Text: text})
	}
	loader := learnReuseLoaderFunc(func(_ context.Context, source knowledge.CompletedTurnRef, itemIDs []string) (curation.TurnMaterial, error) {
		if source.SessionID != string(firstRuntime.SessionID) || source.ChatID != string(firstRuntime.ChatID) {
			return curation.TurnMaterial{}, fmt.Errorf("unexpected curation source: %#v", source)
		}
		if len(itemIDs) != 2 || itemIDs[0] != string(user.ID) || itemIDs[1] != string(assistant.ID) {
			return curation.TurnMaterial{}, fmt.Errorf("unexpected curation items: %v", itemIDs)
		}
		return material, nil
	})
	var draftCalls atomic.Int32
	model := learnReuseDraftModelFunc(func(_ context.Context, _ knowledge.CurationRecord, loaded curation.TurnMaterial, schema json.RawMessage) ([]byte, error) {
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
		Loader: loader, Model: model, Sink: sink, Classifier: knowledge.RuleClassifier{},
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
		engine.StopKnowledgeCuration()
		cancelCoordinator()
		t.Fatal("timed out waiting for automatic durable learning")
	}
	engine.StopKnowledgeCuration()
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
		t.Fatalf("stop Knowledge operations: %v", err)
	}
	cancelShutdown()
	if err := store.Close(); err != nil {
		t.Fatalf("close Knowledge store: %v", err)
	}
	reopenedStore, err := knowledgePebble.Open(stateDir)
	if err != nil {
		t.Fatalf("reopen Knowledge store: %v", err)
	}
	t.Cleanup(func() { _ = reopenedStore.Close() })
	reopenedService := newLearnReuseKnowledgeService(t, reopenedStore)
	laterRuntime := tools.Runtime{
		SessionID: "00000000-0000-7000-8000-000000000101",
		ChatID:    "00000000-0000-7000-8000-000000000102",
		Services:  knowledgetool.RuntimeService(reopenedService),
	}
	reused, err := callKnowledgeSearch(ctx, laterRuntime, "Linux partition tools fdisk")
	if err != nil {
		t.Fatalf("later-chat Knowledge search: %v", err)
	}
	reusedSearch, ok := reused.Stored.(knowledgeService.LexicalSearchResult)
	if !ok || len(reusedSearch.Matches) != 1 || reusedSearch.Matches[0].EntryID != receipt.EntryID {
		t.Fatalf("later-chat Knowledge search = %#v", reused.Stored)
	}
	match := reusedSearch.Matches[0]
	if match.Rank.Evidence <= 0 || match.Document.Verification != knowledge.VerificationStatusVerified ||
		!strings.Contains(match.Document.Summary, "sfdisk") {
		t.Fatalf("reused Knowledge match = %#v", match)
	}
	full, err := tools.Call(ctx, tools.Options{Runtime: laterRuntime, Request: tools.Request{Tool: tools.Knowledge, Args: map[string]string{
		"action": "get", "object_kind": "entry", "id": string(receipt.EntryID),
	}}})
	if err != nil {
		t.Fatalf("later-chat Knowledge get: %v", err)
	}
	for _, required := range []string{"sfdisk --dump", "man-pages/man8/sfdisk.8.html"} {
		if !strings.Contains(full.Output, required) {
			t.Fatalf("later-chat Knowledge body is missing %q: %s", required, full.Output)
		}
	}
	if researchCalls.Load() != 1 || draftCalls.Load() != 1 {
		t.Fatalf("reuse repeated remote work: research=%d drafting=%d", researchCalls.Load(), draftCalls.Load())
	}
}

type learnReuseLoaderFunc func(context.Context, knowledge.CompletedTurnRef, []string) (curation.TurnMaterial, error)

func (fn learnReuseLoaderFunc) Load(ctx context.Context, source knowledge.CompletedTurnRef, itemIDs []string) (curation.TurnMaterial, error) {
	return fn(ctx, source, itemIDs)
}

type learnReuseDraftModelFunc func(context.Context, knowledge.CurationRecord, curation.TurnMaterial, json.RawMessage) ([]byte, error)

func (fn learnReuseDraftModelFunc) Draft(ctx context.Context, record knowledge.CurationRecord, material curation.TurnMaterial, schema json.RawMessage) ([]byte, error) {
	return fn(ctx, record, material, schema)
}

type notifyingLearnReuseApplier struct {
	Next    curation.CandidateApplier
	Applied chan<- curation.ApplyReceipt
}

func (a notifyingLearnReuseApplier) ApplyCandidate(ctx context.Context, record knowledge.CurationRecord, draft curation.CandidateDraft) (curation.ApplyReceipt, error) {
	receipt, err := a.Next.ApplyCandidate(ctx, record, draft)
	if err == nil {
		a.Applied <- receipt
	}
	return receipt, err
}

func newLearnReuseKnowledgeService(t *testing.T, store *knowledgePebble.Store) *knowledgeService.Service {
	t.Helper()
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:learn-reuse-e2e"}),
	})
	if err != nil {
		t.Fatalf("create Knowledge service: %v", err)
	}
	return service
}

func callKnowledgeSearch(ctx context.Context, runtime tools.Runtime, query string) (tools.Result, error) {
	return tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: tools.Knowledge, Args: map[string]string{
		"action": "search", "query": query, "limit": "5",
	}}})
}

func hasCurationSignal(signals []knowledge.CurationSignal, kind knowledge.CurationSignalKind) bool {
	for _, signal := range signals {
		if signal.Kind == kind {
			return true
		}
	}
	return false
}
