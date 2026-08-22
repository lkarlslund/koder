package knowledgetool

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestKnowledgeToolRegisteredAsSingleBuiltin(t *testing.T) {
	registered, ok := tools.Lookup(tools.Knowledge)
	if !ok {
		t.Fatal("knowledge tool is not registered")
	}
	if registered.ID() != domain.ToolKindKnowledge {
		t.Fatalf("registered ID = %q, want %q", registered.ID(), domain.ToolKindKnowledge)
	}
	if !tools.IsBuiltinID(tools.Knowledge) {
		t.Fatal("knowledge tool is not a built-in tool")
	}
	count := 0
	for _, toolID := range tools.RegisteredIDs() {
		if toolID == tools.Knowledge {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("knowledge registration count = %d, want 1", count)
	}
}

func TestKnowledgeToolDefinitionRequiresServiceAndOffersReadActions(t *testing.T) {
	if _, enabled := tools.DefinitionFor(tools.Knowledge, tools.Runtime{}); enabled {
		t.Fatal("knowledge must not be offered without its runtime service")
	}
	definition, enabled := tools.DefinitionFor(tools.Knowledge, runtimeFor(newService(t)))
	if !enabled {
		t.Fatal("knowledge read actions must be offered with an available service")
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(schema.Properties["action"].Enum, ","); got != "search,get,neighbors,chunk_list,chunk_get,chunk_create,chunk_update,chunk_archive,chunk_restore,chunk_delete,entry_create,entry_update,entry_supersede,entry_archive,entry_restore,entry_delete,verify" {
		t.Fatalf("knowledge actions = %q", got)
	}
}

func TestKnowledgeToolRequiresRuntimeService(t *testing.T) {
	_, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{},
		Request: tools.Request{Tool: tools.Knowledge, Args: map[string]string{"action": "search", "query": "linux"}},
	})
	if err == nil || !strings.Contains(err.Error(), "knowledge service is not configured") {
		t.Fatalf("Call() error = %v, want missing service", err)
	}
}

func TestKnowledgeReadActionsSearchGetAndTraverse(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	chunk, err := service.CreateChunk(ctx, knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "Linux partition tools", Kind: knowledge.ChunkKindEnvironment,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindEnvironment, Selector: "linux"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID,
		Entry: knowledge.Entry{
			Title: "Use sfdisk when fdisk is unavailable", Summary: "sfdisk can partition disks non-interactively.",
			Body: "Run sfdisk with a reviewed partition specification.", Kind: knowledge.EntryKindProcedure,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	related, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID,
		Entry: knowledge.Entry{
			Title: "Back up the partition table", Summary: "Save the current layout first.",
			Body: "Capture the existing layout before changing the disk.", Kind: knowledge.EntryKindWarning,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateLink(ctx, knowledgeService.CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(related.Entry.ID)},
		Kind:   knowledge.LinkKindRequires,
	}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := runtimeFor(service)

	search, err := call(ctx, runtime, map[string]string{"action": "search", "query": "sfdisk", "limit": "5"})
	if err != nil {
		t.Fatal(err)
	}
	searchResult, ok := search.Stored.(knowledgeService.LexicalSearchResult)
	if !ok || len(searchResult.Matches) != 1 || searchResult.Matches[0].EntryID != entry.Entry.ID {
		t.Fatalf("search result = %#v", search.Stored)
	}
	if searchResult.Matches[0].Document.Summary != entry.Entry.Summary {
		t.Fatalf("search document = %#v", searchResult.Matches[0].Document)
	}
	if strings.Contains(search.Output, entry.Entry.Body) {
		t.Fatal("search response exposed the full entry body")
	}

	get, err := call(ctx, runtime, map[string]string{
		"action": "get", "object_kind": "entry", "id": string(entry.Entry.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	getResult, ok := get.Stored.(recordResult)
	if !ok || getResult.Entry == nil || getResult.Entry.Body != entry.Entry.Body || !strings.Contains(get.Output, entry.Entry.Body) {
		t.Fatalf("get result = %#v, output = %s", get.Stored, get.Output)
	}

	neighbors, err := call(ctx, runtime, map[string]string{
		"action": "neighbors", "object_kind": "entry", "id": string(entry.Entry.ID), "direction": "outgoing",
	})
	if err != nil {
		t.Fatal(err)
	}
	neighborResult, ok := neighbors.Stored.(neighborPageResult)
	if !ok || len(neighborResult.Neighbors) != 1 || neighborResult.Neighbors[0].Object.ID != string(related.Entry.ID) {
		t.Fatalf("neighbors result = %#v", neighbors.Stored)
	}
	if strings.Contains(neighbors.Output, related.Entry.Body) {
		t.Fatal("neighbors response exposed a full entry body")
	}
}

func TestKnowledgeReadActionReturnsStructuredServiceError(t *testing.T) {
	_, err := call(context.Background(), runtimeFor(newService(t)), map[string]string{
		"action": "get", "object_kind": "entry", "id": "01a01688-fc5d-7f7d-8bb8-de244977f8a1",
	})
	var serviceError *knowledgeService.ServiceError
	if !errors.As(err, &serviceError) || serviceError.Code != knowledgeService.ErrorCodeNotFound {
		t.Fatalf("get missing error = %T %v", err, err)
	}
}

func TestKnowledgeChunkLifecycleActions(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	runtime := runtimeFor(service)

	createdCall, err := call(ctx, runtime, map[string]string{
		"action": "chunk_create",
		"chunk":  `{"title":"Linux tools","kind":"environment","scope":{"kind":"environment","selector":"linux"},"tags":["CLI Tools"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := createdCall.Stored.(chunkMutationResult)
	if !ok || !created.Created || created.Chunk.Title != "Linux tools" || created.Chunk.Revision.Number != 1 {
		t.Fatalf("chunk_create = %#v", createdCall.Stored)
	}
	if created.Chunk.Revision.Actor.Kind != knowledge.ActorKindChat || created.Chunk.Revision.Actor.ID != string(runtime.ChatID) {
		t.Fatalf("chunk_create actor = %#v, want trusted chat actor", created.Chunk.Revision.Actor)
	}

	listedCall, err := call(ctx, runtime, map[string]string{"action": "chunk_list"})
	if err != nil {
		t.Fatal(err)
	}
	listed, ok := listedCall.Stored.(chunkPageResult)
	if !ok || len(listed.Chunks) != 1 || listed.Chunks[0].ID != created.Chunk.ID {
		t.Fatalf("chunk_list = %#v", listedCall.Stored)
	}

	gotCall, err := call(ctx, runtime, map[string]string{"action": "chunk_get", "id": string(created.Chunk.ID)})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := gotCall.Stored.(recordResult)
	if !ok || got.Chunk == nil || got.Chunk.Title != created.Chunk.Title {
		t.Fatalf("chunk_get = %#v", gotCall.Stored)
	}

	updatedCall, err := call(ctx, runtime, map[string]string{
		"action": "chunk_update", "id": string(created.Chunk.ID), "expected_revision": "1",
		"reason": "add operational detail", "chunk": `{"description":"Durable Linux command knowledge."}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := updatedCall.Stored.(chunkMutationResult)
	if !ok || !updated.Updated || updated.Chunk.Revision.Number != 2 || updated.Chunk.Title != created.Chunk.Title || updated.Chunk.Description == "" {
		t.Fatalf("chunk_update = %#v", updatedCall.Stored)
	}
	_, err = call(ctx, runtime, map[string]string{
		"action": "chunk_update", "id": string(created.Chunk.ID), "expected_revision": "1", "chunk": `{"description":"stale"}`,
	})
	var conflict *knowledgeService.ServiceError
	if !errors.As(err, &conflict) || conflict.Code != knowledgeService.ErrorCodeConflict {
		t.Fatalf("stale chunk_update error = %T %v", err, err)
	}

	archivedCall, err := call(ctx, runtime, map[string]string{
		"action": "chunk_archive", "id": string(created.Chunk.ID), "expected_revision": "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	archived := archivedCall.Stored.(chunkMutationResult)
	if archived.Chunk.State != knowledge.ChunkStateArchived || archived.Chunk.Revision.Number != 3 {
		t.Fatalf("chunk_archive = %#v", archived)
	}
	activeCall, err := call(ctx, runtime, map[string]string{"action": "chunk_list"})
	if err != nil {
		t.Fatal(err)
	}
	if active := activeCall.Stored.(chunkPageResult); len(active.Chunks) != 0 {
		t.Fatalf("default chunk_list included archive: %#v", active)
	}
	archivesCall, err := call(ctx, runtime, map[string]string{"action": "chunk_list", "states": `["archived"]`})
	if err != nil {
		t.Fatal(err)
	}
	if archives := archivesCall.Stored.(chunkPageResult); len(archives.Chunks) != 1 {
		t.Fatalf("archived chunk_list = %#v", archives)
	}

	restoredCall, err := call(ctx, runtime, map[string]string{
		"action": "chunk_restore", "id": string(created.Chunk.ID), "expected_revision": "3",
	})
	if err != nil {
		t.Fatal(err)
	}
	restored := restoredCall.Stored.(chunkMutationResult)
	if restored.Chunk.State != knowledge.ChunkStateActive || restored.Chunk.Revision.Number != 4 {
		t.Fatalf("chunk_restore = %#v", restored)
	}
	archivedCall, err = call(ctx, runtime, map[string]string{
		"action": "chunk_archive", "id": string(created.Chunk.ID), "expected_revision": "4",
	})
	if err != nil {
		t.Fatal(err)
	}
	archived = archivedCall.Stored.(chunkMutationResult)

	_, err = call(ctx, runtime, map[string]string{
		"action": "chunk_delete", "id": string(created.Chunk.ID),
		"expected_revision": strconv.FormatUint(archived.Chunk.Revision.Number, 10),
	})
	var unconfirmed *knowledgeService.ServiceError
	if !errors.As(err, &unconfirmed) || unconfirmed.Code != knowledgeService.ErrorCodeInvalid {
		t.Fatalf("unconfirmed chunk_delete error = %T %v", err, err)
	}
	deletedCall, err := call(ctx, runtime, map[string]string{
		"action": "chunk_delete", "id": string(created.Chunk.ID),
		"expected_revision": strconv.FormatUint(archived.Chunk.Revision.Number, 10), "confirmed": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted, ok := deletedCall.Stored.(chunkDeleteResult)
	if !ok || !deleted.Deleted || deleted.Cascade {
		t.Fatalf("chunk_delete = %#v", deletedCall.Stored)
	}
}

func TestKnowledgeChunkCascadeDeleteAction(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	created, err := service.CreateChunk(ctx, knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "Temporary", Kind: knowledge.ChunkKindReference, Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
	}})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: created.Chunk.ID, Entry: knowledge.Entry{Title: "Temporary fact", Kind: knowledge.EntryKindFact},
	})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := service.ArchiveChunk(ctx, knowledgeService.ChunkLifecycleRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}

	_, err = call(ctx, runtimeFor(service), map[string]string{
		"action": "chunk_delete", "id": string(created.Chunk.ID),
		"expected_revision": strconv.FormatUint(archived.Chunk.Revision.Number, 10), "confirmed": "true",
	})
	var dependency *knowledgeService.ServiceError
	if !errors.As(err, &dependency) || dependency.Code != knowledgeService.ErrorCodeDependency || dependency.Details == nil ||
		dependency.Details.ChunkBlockers == nil || len(dependency.Details.ChunkBlockers.EntryIDs) != 1 {
		t.Fatalf("blocked chunk_delete error = %#v (%v)", dependency, err)
	}

	deletedCall, err := call(ctx, runtimeFor(service), map[string]string{
		"action": "chunk_delete", "id": string(created.Chunk.ID),
		"expected_revision": strconv.FormatUint(archived.Chunk.Revision.Number, 10),
		"confirmed":         "true", "cascade": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted := deletedCall.Stored.(chunkDeleteResult)
	if !deleted.Deleted || !deleted.Cascade || len(deleted.DeletedEntryIDs) != 1 || deleted.DeletedEntryIDs[0] != entry.Entry.ID {
		t.Fatalf("cascade chunk_delete = %#v", deleted)
	}
}

func TestKnowledgeEntryLifecycleAndVerificationActions(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	chunk, err := service.CreateChunk(ctx, knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "Linux partitioning", Kind: knowledge.ChunkKindEnvironment,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindEnvironment, Selector: "linux"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := runtimeFor(service)

	createdCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_create", "chunk_id": string(chunk.Chunk.ID),
		"entry": `{"kind":"procedure","title":"Use sfdisk","summary":"Partition disks non-interactively.","tags":["Linux Tools"],"applicability":{"operating_systems":["linux"]}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := createdCall.Stored.(entryMutationResult)
	if !ok || !created.Created || created.Entry.Title != "Use sfdisk" || created.Entry.Scope != chunk.Chunk.Scope ||
		created.Entry.Revision.Actor.Kind != knowledge.ActorKindChat || created.Entry.Revision.Actor.ID != string(runtime.ChatID) {
		t.Fatalf("entry_create = %#v", createdCall.Stored)
	}

	updatedCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_update", "id": string(created.Entry.ID), "expected_revision": "1",
		"reason": "record safe usage", "entry": `{"body":"Back up the partition table first.","confidence":0.8}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := updatedCall.Stored.(entryMutationResult)
	if !updated.Updated || updated.Entry.Revision.Number != 2 || updated.Entry.Title != created.Entry.Title ||
		updated.Entry.Body != "Back up the partition table first." || updated.Entry.Confidence != 0.8 {
		t.Fatalf("entry_update = %#v", updated)
	}
	_, err = call(ctx, runtime, map[string]string{
		"action": "entry_update", "id": string(created.Entry.ID), "expected_revision": "1", "entry": `{"summary":"stale"}`,
	})
	var conflict *knowledgeService.ServiceError
	if !errors.As(err, &conflict) || conflict.Code != knowledgeService.ErrorCodeConflict {
		t.Fatalf("stale entry_update error = %T %v", err, err)
	}

	evidence, err := service.CreateEvidence(ctx, knowledgeService.CreateEvidenceRequest{Evidence: knowledge.Evidence{
		Type: knowledge.EvidenceTypePackage, Quality: knowledge.EvidenceQualityPrimary,
		Source: knowledge.Source{ID: "man:sfdisk", Title: "sfdisk manual"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	verifiedCall, err := call(ctx, runtime, map[string]string{
		"action": "verify", "id": string(created.Entry.ID), "expected_revision": "2",
		"verification": `{"status":"verified","method":"checked manual","evidence_ids":["` + string(evidence.Evidence.ID) + `"]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	verified := verifiedCall.Stored.(entryMutationResult)
	if !verified.Updated || verified.Entry.Revision.Number != 3 || verified.Entry.Verification.Status != knowledge.VerificationStatusVerified ||
		verified.Entry.Verification.Actor.Kind != knowledge.ActorKindChat || verified.Entry.Verification.Actor.ID != string(runtime.ChatID) {
		t.Fatalf("verify = %#v", verified)
	}

	replacementCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_create", "chunk_id": string(chunk.Chunk.ID),
		"entry": `{"kind":"procedure","title":"Use sfdisk with JSON input"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement := replacementCall.Stored.(entryMutationResult)
	supersededCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_supersede", "id": string(created.Entry.ID), "expected_revision": "3",
		"replacement_entry_id": string(replacement.Entry.ID), "reason": "replace vague procedure",
	})
	if err != nil {
		t.Fatal(err)
	}
	superseded := supersededCall.Stored.(entryMutationResult)
	if !superseded.Updated || superseded.Entry.State != knowledge.EntryStateSuperseded || superseded.Replacement == nil ||
		superseded.Replacement.ID != replacement.Entry.ID {
		t.Fatalf("entry_supersede = %#v", superseded)
	}

	disposableCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_create", "chunk_id": string(chunk.Chunk.ID),
		"entry": `{"kind":"fact","title":"Disposable fact"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	disposable := disposableCall.Stored.(entryMutationResult)
	archivedCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_archive", "id": string(disposable.Entry.ID), "expected_revision": "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	archived := archivedCall.Stored.(entryMutationResult)
	if archived.Entry.State != knowledge.EntryStateArchived || archived.Entry.Revision.Number != 2 {
		t.Fatalf("entry_archive = %#v", archived)
	}
	restoredCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_restore", "id": string(disposable.Entry.ID), "expected_revision": "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	restored := restoredCall.Stored.(entryMutationResult)
	if restored.Entry.State != knowledge.EntryStateActive || restored.Entry.Revision.Number != 3 {
		t.Fatalf("entry_restore = %#v", restored)
	}
	archivedCall, err = call(ctx, runtime, map[string]string{
		"action": "entry_archive", "id": string(disposable.Entry.ID), "expected_revision": "3",
	})
	if err != nil {
		t.Fatal(err)
	}
	archived = archivedCall.Stored.(entryMutationResult)
	_, err = call(ctx, runtime, map[string]string{
		"action": "entry_delete", "id": string(disposable.Entry.ID),
		"expected_revision": strconv.FormatUint(archived.Entry.Revision.Number, 10),
	})
	var invalid *knowledgeService.ServiceError
	if !errors.As(err, &invalid) || invalid.Code != knowledgeService.ErrorCodeInvalid {
		t.Fatalf("unconfirmed entry_delete error = %T %v", err, err)
	}
	deletedCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_delete", "id": string(disposable.Entry.ID),
		"expected_revision": strconv.FormatUint(archived.Entry.Revision.Number, 10), "confirmed": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted := deletedCall.Stored.(entryDeleteResult)
	if !deleted.Deleted || deleted.ID != disposable.Entry.ID {
		t.Fatalf("entry_delete = %#v", deleted)
	}
}

func TestKnowledgeEntryDeleteReportsGraphBlockers(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	chunk, _ := service.CreateChunk(ctx, knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "Linked", Kind: knowledge.ChunkKindReference, Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
	}})
	first, _ := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: knowledge.Entry{Title: "First", Kind: knowledge.EntryKindFact},
	})
	second, _ := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: knowledge.Entry{Title: "Second", Kind: knowledge.EntryKindFact},
	})
	_, _ = service.CreateLink(ctx, knowledgeService.CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(first.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(second.Entry.ID)}, Kind: knowledge.LinkKindRelatedTo,
	}})
	archived, err := service.ArchiveEntry(ctx, knowledgeService.EntryLifecycleRequest{EntryID: first.Entry.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(ctx, runtimeFor(service), map[string]string{
		"action": "entry_delete", "id": string(first.Entry.ID),
		"expected_revision": strconv.FormatUint(archived.Entry.Revision.Number, 10), "confirmed": "true",
	})
	var dependency *knowledgeService.ServiceError
	if !errors.As(err, &dependency) || dependency.Code != knowledgeService.ErrorCodeDependency || dependency.Details == nil ||
		dependency.Details.EntryBlockers == nil || len(dependency.Details.EntryBlockers.LinkIDs) != 1 {
		t.Fatalf("blocked entry_delete error = %#v (%v)", dependency, err)
	}
}

func TestKnowledgeChunkInputRejectsUnknownFieldsAndOversizedCalls(t *testing.T) {
	_, err := call(context.Background(), runtimeFor(newService(t)), map[string]string{
		"action": "chunk_create", "chunk": `{"title":"Invalid","kind":"reference","scope":{"kind":"global"},"server_owned":true}`,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown chunk field error = %v", err)
	}
	_, err = tools.ParseProviderCall(provider.ToolCall{
		ID: "knowledge_oversized",
		Function: provider.FunctionCall{
			Name:      tools.Knowledge.String(),
			Arguments: `{"action":"chunk_create","chunk":{"title":"` + strings.Repeat("x", 129*1024) + `"}}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "knowledge tool arguments exceeded 128 KiB") {
		t.Fatalf("oversized knowledge call error = %v", err)
	}
}

func call(ctx context.Context, runtime tools.Runtime, args map[string]string) (tools.Result, error) {
	return tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: tools.Knowledge, Args: args}})
}

func runtimeFor(service *knowledgeService.Service) tools.Runtime {
	return tools.Runtime{ChatID: "01a01688-fc5d-7f7d-8bb8-de244977fee1", Services: RuntimeService(service)}
}

func newService(t *testing.T) *knowledgeService.Service {
	t.Helper()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{
			Kind: knowledge.ActorKindSystem,
			ID:   "system:test",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
