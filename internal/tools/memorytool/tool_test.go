package memorytool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatinteraction"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestMemoryToolRegisteredAsSingleBuiltin(t *testing.T) {
	registered, ok := tools.Lookup(tools.Memory)
	if !ok {
		t.Fatal("memory tool is not registered")
	}
	if registered.ID() != domain.ToolKindMemory {
		t.Fatalf("registered ID = %q, want %q", registered.ID(), domain.ToolKindMemory)
	}
	if !tools.IsBuiltinID(tools.Memory) {
		t.Fatal("memory tool is not a built-in tool")
	}
	count := 0
	for _, toolID := range tools.RegisteredIDs() {
		if toolID == tools.Memory {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("memory registration count = %d, want 1", count)
	}
}

func TestMemoryToolDefinitionRequiresServiceAndOffersSimpleMemoryActions(t *testing.T) {
	if _, enabled := tools.DefinitionFor(tools.Memory, tools.Runtime{}); enabled {
		t.Fatal("memory must not be offered without its runtime service")
	}
	definition, enabled := tools.DefinitionFor(tools.Memory, runtimeFor(newService(t)))
	if !enabled {
		t.Fatal("memory read actions must be offered with an available service")
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(schema.Properties["action"].Enum, ","); got != "recall,remember" {
		t.Fatalf("memory actions = %q", got)
	}
	for _, guidance := range []string{
		"Recall before repeating research", "Remember only an established reusable result",
		"Never persist passwords",
	} {
		if !strings.Contains(definition.Function.Description, guidance) {
			t.Fatalf("Memory definition is missing %q guidance: %s", guidance, definition.Function.Description)
		}
	}
}

func TestMemoryToolRequiresRuntimeService(t *testing.T) {
	_, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{},
		Request: tools.Request{Tool: tools.Memory, Args: map[string]string{"action": "search", "query": "linux"}},
	})
	if err == nil || !strings.Contains(err.Error(), "memory service is not configured") {
		t.Fatalf("Call() error = %v, want missing service", err)
	}
}

func TestMemoryRememberAndRecallInferProjectDefaults(t *testing.T) {
	service := newService(t)
	runtime := runtimeFor(service)
	runtime.Workdir = "/work/koder"

	stored, err := call(context.Background(), runtime, map[string]string{
		"action": "remember", "content": "Use sfdisk when fdisk is unavailable in this project.",
	})
	if err != nil {
		t.Fatal(err)
	}
	remembered, ok := stored.Stored.(rememberResult)
	if !ok || !remembered.Created || remembered.Entry.Title == "" {
		t.Fatalf("remember result = %#v", stored.Stored)
	}
	if remembered.Entry.Kind != memory.EntryKindFact || remembered.Entry.Scope != (memory.Scope{Kind: memory.ScopeKindProject, Selector: "/work/koder"}) {
		t.Fatalf("inferred entry metadata = %#v", remembered.Entry)
	}

	recalled, err := call(context.Background(), runtime, map[string]string{"action": "recall", "query": "sfdisk"})
	if err != nil {
		t.Fatal(err)
	}
	matches, ok := recalled.Stored.(memoryService.LexicalSearchResult)
	if !ok || len(matches.Matches) != 1 || matches.Matches[0].EntryID != remembered.Entry.ID {
		t.Fatalf("recall result = %#v", recalled.Stored)
	}

	again, err := call(context.Background(), runtime, map[string]string{
		"action": "remember", "content": "Use sfdisk when fdisk is unavailable in this project.",
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := again.Stored.(rememberResult)
	if !duplicate.Duplicate || duplicate.Created || duplicate.Entry.ID != remembered.Entry.ID {
		t.Fatalf("duplicate remember result = %#v", duplicate)
	}
}

func TestMemoryRememberPersonalUsesPrivatePersonalChunk(t *testing.T) {
	service := newService(t)
	runtime := runtimeFor(service)
	runtime.Workdir = "/work/koder"

	stored, err := call(context.Background(), runtime, map[string]string{
		"action": "remember", "content": "The user prefers concise status updates.", "personal": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	remembered := stored.Stored.(rememberResult)
	if remembered.Entry.ChunkID != memoryService.PersonalMeChunkID ||
		remembered.Entry.Scope != (memory.Scope{Kind: memory.ScopeKindPersonal, Selector: "me"}) ||
		remembered.Entry.PersonalOrigin != memory.PersonalOriginExplicit {
		t.Fatalf("personal memory = %#v", remembered.Entry)
	}
}

func TestRememberNormalizationRequiresOnlyCommonFields(t *testing.T) {
	remember, err := normalizeRememberArgs(map[string]string{"action": "remember", "content": "  durable result  "})
	if err != nil || remember["content"] != "durable result" || len(remember) != 2 {
		t.Fatalf("normalize remember = %#v, %v", remember, err)
	}
	if _, err := normalizeRecallArgs(map[string]string{"action": "recall"}); err == nil {
		t.Fatal("recall accepted an empty query")
	}
	if _, err := normalizeRememberArgs(map[string]string{"action": "remember", "content": "x", "personal": "sometimes"}); err == nil {
		t.Fatal("remember accepted an invalid personal flag")
	}
}

func TestMemoryPackageToolActionsRequirePersistentWorkspaceAndRoundTrip(t *testing.T) {
	source := newService(t)
	created, err := source.CreateChunk(context.Background(), memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Tool package", Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Visibility: memory.VisibilityPrivate,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if _, err := source.ExportPackage(context.Background(), &archive, memoryService.ExportPackageRequest{ChunkID: created.Chunk.ID}); err != nil {
		t.Fatal(err)
	}
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "incoming.kmemory"), archive.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	target := newService(t)
	runtime := runtimeFor(target)
	runtime.Workdir = workdir

	definition, enabled := tools.DefinitionFor(tools.Memory, runtime)
	if !enabled || strings.Contains(string(definition.Function.Parameters), `"package_preview"`) {
		t.Fatalf("persistent Memory definition should keep package management internal: enabled=%v definition=%#v", enabled, definition)
	}
	previewResult, err := call(context.Background(), runtime, map[string]string{"action": "package_preview", "path": "incoming.kmemory"})
	if err != nil {
		t.Fatal(err)
	}
	preview, ok := previewResult.Stored.(memoryService.ImportPreview)
	if !ok || preview.ChunkID != created.Chunk.ID || !preview.ReadyToStage {
		t.Fatalf("package preview = %#v", previewResult.Stored)
	}
	stageResult, err := call(context.Background(), runtime, map[string]string{"action": "package_stage", "path": "incoming.kmemory"})
	if err != nil {
		t.Fatal(err)
	}
	stage, ok := stageResult.Stored.(memoryService.ImportStage)
	if !ok || stage.ID == "" {
		t.Fatalf("package stage = %#v", stageResult.Stored)
	}
	activatedResult, err := call(context.Background(), runtime, map[string]string{"action": "package_activate", "stage_id": stage.ID})
	if err != nil {
		t.Fatal(err)
	}
	activated, ok := activatedResult.Stored.(memoryService.ActivateImportResult)
	if !ok || activated.ChunkID != created.Chunk.ID {
		t.Fatalf("package activation = %#v", activatedResult.Stored)
	}
	exportedResult, err := call(context.Background(), runtime, map[string]string{
		"action": "package_export", "id": string(created.Chunk.ID), "path": "exports/tool.kmemory",
	})
	if err != nil {
		t.Fatal(err)
	}
	exported, ok := exportedResult.Stored.(packageExportResult)
	if !ok || exported.Path != "exports/tool.kmemory" || exported.Size == 0 {
		t.Fatalf("package export = %#v", exportedResult.Stored)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "exports", "tool.kmemory"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ValidateImportArchive(context.Background(), data); err != nil {
		t.Fatalf("validate tool export: %v", err)
	}
	if _, err := call(context.Background(), runtime, map[string]string{
		"action": "package_export", "id": string(created.Chunk.ID), "path": "exports/tool.kmemory",
	}); err == nil || memoryService.ClassifyError(err).Code != memoryService.ErrorCodeConflict {
		t.Fatalf("second package export error = %v", err)
	}

	withoutWorkspace := runtimeFor(target)
	if _, err := call(context.Background(), withoutWorkspace, map[string]string{"action": "package_activate", "stage_id": stage.ID}); err == nil {
		t.Fatal("package action was accepted without a persistent workspace")
	}
}

func TestMemoryPackageExportNormalizesPersonalConsent(t *testing.T) {
	chunkID := "00000000-0000-7000-8000-000000000001"
	normalized, err := normalizePackageExportArgs(map[string]string{
		"id": chunkID, "path": "exports/personal.kmemory", "include_personal": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized["include_personal"] != "true" {
		t.Fatalf("include_personal = %q, want true", normalized["include_personal"])
	}
	if _, err := normalizePackageExportArgs(map[string]string{
		"id": chunkID, "path": "exports/personal.kmemory", "include_personal": "sometimes",
	}); err == nil {
		t.Fatal("normalizePackageExportArgs accepted a non-boolean include_personal value")
	}
}

func TestMemoryToolDefinitionFiltersActionsAndScopesFromPolicy(t *testing.T) {
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
		ToolPolicy: memoryService.ToolOfferPolicyFunc(func(_ context.Context, actor memory.Actor, _ memoryService.ToolOffer) (memoryService.ToolOffer, error) {
			if actor.Kind != memory.ActorKindChat {
				t.Fatalf("policy actor = %#v, want chat", actor)
			}
			return memoryService.ToolOffer{
				Actions: []string{"search"}, ScopeKinds: []memory.ScopeKind{memory.ScopeKindProject},
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	definition, enabled := tools.DefinitionFor(tools.Memory, runtimeFor(service))
	if !enabled {
		t.Fatal("restricted non-empty Memory offer was hidden")
	}
	var schema map[string]any
	if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	actions := properties["action"].(map[string]any)["enum"].([]any)
	if got := strings.Join(anyStrings(actions), ","); got != "recall" {
		t.Fatalf("filtered actions = %q", got)
	}
	if _, exposed := properties["scope"]; exposed {
		t.Fatal("model-facing memory schema exposed storage scope metadata")
	}
	if !strings.Contains(definition.Function.Description, "actions: recall") || !strings.Contains(definition.Function.Description, "scopes: project") {
		t.Fatalf("runtime policy description = %q", definition.Function.Description)
	}
	if !strings.Contains(definition.Function.Description, "Recall before repeating research") {
		t.Fatalf("read-only guidance = %q", definition.Function.Description)
	}
	for _, withheld := range []string{"Remember only", "Never persist passwords"} {
		if strings.Contains(definition.Function.Description, withheld) {
			t.Fatalf("read-only offer included write guidance %q: %s", withheld, definition.Function.Description)
		}
	}

	_, err = call(context.Background(), runtimeFor(service), map[string]string{"action": "remember", "content": "Use Linux."})
	var forbidden *memoryService.ServiceError
	if !errors.As(err, &forbidden) || forbidden.Code != memoryService.ErrorCodeForbidden {
		t.Fatalf("withheld action error = %T %v", err, err)
	}
}

func TestMemoryToolDefinitionHidesEmptyPolicyOffer(t *testing.T) {
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
		ToolPolicy: memoryService.ToolOfferPolicyFunc(func(context.Context, memory.Actor, memoryService.ToolOffer) (memoryService.ToolOffer, error) {
			return memoryService.ToolOffer{}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, enabled := tools.DefinitionFor(tools.Memory, runtimeFor(service)); enabled {
		t.Fatal("empty Memory policy offer must hide the tool")
	}
}

func TestMemoryToolObeysChatProfileInteractionAndSessionToolState(t *testing.T) {
	service := newService(t)
	base := runtimeFor(service)

	compaction := base
	compaction.ChatRole = chatrole.Compaction
	if _, enabled := tools.DefinitionFor(tools.Memory, compaction); enabled {
		t.Fatal("compaction profile exposed Memory")
	}
	_, err := call(context.Background(), compaction, map[string]string{"action": "search", "query": "linux"})
	if err == nil || !tools.IsDenied(err) {
		t.Fatalf("compaction Memory call = %T %v, want role denial", err, err)
	}

	disabled := base
	disabled.AllowedTools = map[tools.ID]bool{tools.Memory: false}
	if _, enabled := tools.DefinitionFor(tools.Memory, disabled); enabled {
		t.Fatal("session-disabled Memory was exposed")
	}
	_, err = call(context.Background(), disabled, map[string]string{"action": "search", "query": "linux"})
	if err == nil || !tools.IsDenied(err) {
		t.Fatalf("session-disabled Memory call = %T %v, want denial", err, err)
	}

	voice := base
	voice.ChatRole = chatrole.Orchestrator
	voice.InteractionMode = chatinteraction.Voice
	voiceDefinition, enabled := tools.DefinitionFor(tools.Memory, voice)
	if !enabled {
		t.Fatal("voice interaction unexpectedly withheld Memory")
	}
	if !strings.Contains(voiceDefinition.Function.Description, "Voice result: tell the user only the few relevant conclusions") ||
		!strings.Contains(voiceDefinition.Function.Description, "complete structured result remains available in the transcript") {
		t.Fatalf("voice Memory guidance = %q", voiceDefinition.Function.Description)
	}
	textDefinition, enabled := tools.DefinitionFor(tools.Memory, base)
	if !enabled || strings.Contains(textDefinition.Function.Description, "Voice result:") {
		t.Fatalf("text Memory guidance = enabled %v, %q", enabled, textDefinition.Function.Description)
	}
}

func TestMemoryToolEnforcesPolicyScopesAcrossQueriesAndMutations(t *testing.T) {
	ctx := context.Background()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
		ToolPolicy: memoryService.ToolOfferPolicyFunc(func(_ context.Context, _ memory.Actor, offer memoryService.ToolOffer) (memoryService.ToolOffer, error) {
			offer.ScopeKinds = []memory.ScopeKind{memory.ScopeKindProject}
			return offer, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	global, err := service.CreateChunk(ctx, memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Global tools", Kind: memory.ChunkKindReference, Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
	}})
	if err != nil {
		t.Fatal(err)
	}
	project, err := service.CreateChunk(ctx, memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Project tools", Kind: memory.ChunkKindProject,
		Scope: memory.Scope{Kind: memory.ScopeKindProject, Selector: "project:test"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	globalEntry, _ := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: global.Chunk.ID, Entry: memory.Entry{Title: "sfdisk global", Summary: "global result", Kind: memory.EntryKindFact},
	})
	projectEntry, _ := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: project.Chunk.ID, Entry: memory.Entry{Title: "sfdisk project", Summary: "project result", Kind: memory.EntryKindFact},
	})
	runtime := runtimeFor(service)

	listedCall, err := call(ctx, runtime, map[string]string{"action": "chunk_list", "states": `["active"]`})
	if err != nil {
		t.Fatal(err)
	}
	listed := listedCall.Stored.(chunkPageResult)
	if len(listed.Chunks) != 1 || listed.Chunks[0].ID != project.Chunk.ID {
		t.Fatalf("policy-scoped chunk_list = %#v", listed)
	}
	searchCall, err := call(ctx, runtime, map[string]string{"action": "search", "query": "sfdisk"})
	if err != nil {
		t.Fatal(err)
	}
	search := searchCall.Stored.(memoryService.LexicalSearchResult)
	if len(search.Matches) != 1 || search.Matches[0].EntryID != projectEntry.Entry.ID || strings.Contains(searchCall.Output, "global result") {
		t.Fatalf("policy-scoped search = %#v, output=%s", search, searchCall.Output)
	}

	_, err = call(ctx, runtime, map[string]string{
		"action": "get", "object_kind": "entry", "id": string(globalEntry.Entry.ID),
	})
	var forbidden *memoryService.ServiceError
	if !errors.As(err, &forbidden) || forbidden.Code != memoryService.ErrorCodeForbidden {
		t.Fatalf("global get error = %T %v, want forbidden", err, err)
	}
	if _, err := call(ctx, runtime, map[string]string{
		"action": "get", "object_kind": "entry", "id": string(projectEntry.Entry.ID),
	}); err != nil {
		t.Fatalf("project get error = %v", err)
	}

	_, err = call(ctx, runtime, map[string]string{
		"action": "chunk_create",
		"chunk":  `{"title":"Forbidden global","kind":"reference","scope":{"kind":"global"}}`,
	})
	if !errors.As(err, &forbidden) || forbidden.Code != memoryService.ErrorCodeForbidden {
		t.Fatalf("global chunk_create error = %T %v, want forbidden", err, err)
	}
	_, err = call(ctx, runtime, map[string]string{
		"action": "chunk_update", "id": string(project.Chunk.ID), "expected_revision": "1",
		"chunk": `{"scope":{"kind":"global"}}`,
	})
	if !errors.As(err, &forbidden) || forbidden.Code != memoryService.ErrorCodeForbidden {
		t.Fatalf("scope-widening chunk_update error = %T %v, want forbidden", err, err)
	}
	unchanged, err := service.Chunk(ctx, project.Chunk.ID)
	if err != nil || unchanged.Scope.Kind != memory.ScopeKindProject || unchanged.Revision.Number != 1 {
		t.Fatalf("forbidden update changed project chunk: %#v, %v", unchanged, err)
	}
}

func TestMemoryReadActionsSearchGetAndTraverse(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	chunk, err := service.CreateChunk(ctx, memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Linux partition tools", Kind: memory.ChunkKindEnvironment,
		Scope: memory.Scope{Kind: memory.ScopeKindEnvironment, Selector: "linux"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID,
		Entry: memory.Entry{
			Title: "Use sfdisk when fdisk is unavailable", Summary: "sfdisk can partition disks non-interactively.",
			Body: "Run sfdisk with a reviewed partition specification.", Kind: memory.EntryKindProcedure,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	related, err := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID,
		Entry: memory.Entry{
			Title: "Back up the partition table", Summary: "Save the current layout first.",
			Body: "Capture the existing layout before changing the disk.", Kind: memory.EntryKindWarning,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateLink(ctx, memoryService.CreateLinkRequest{Link: memory.Link{
		Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(related.Entry.ID)},
		Kind:   memory.LinkKindRequires,
	}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := runtimeFor(service)

	search, err := call(ctx, runtime, map[string]string{"action": "search", "query": "sfdisk", "limit": "5"})
	if err != nil {
		t.Fatal(err)
	}
	searchResult, ok := search.Stored.(memoryService.LexicalSearchResult)
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

func TestMemoryReadActionReturnsStructuredServiceError(t *testing.T) {
	_, err := call(context.Background(), runtimeFor(newService(t)), map[string]string{
		"action": "get", "object_kind": "entry", "id": "01a01688-fc5d-7f7d-8bb8-de244977f8a1",
	})
	var serviceError *memoryService.ServiceError
	if !errors.As(err, &serviceError) || serviceError.Code != memoryService.ErrorCodeNotFound {
		t.Fatalf("get missing error = %T %v", err, err)
	}
}

func TestMemoryChunkLifecycleActions(t *testing.T) {
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
	if created.Chunk.Revision.Actor.Kind != memory.ActorKindChat || created.Chunk.Revision.Actor.ID != string(runtime.ChatID) {
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
		"reason": "add operational detail", "chunk": `{"description":"Durable Linux command memory."}`,
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
	var conflict *memoryService.ServiceError
	if !errors.As(err, &conflict) || conflict.Code != memoryService.ErrorCodeConflict {
		t.Fatalf("stale chunk_update error = %T %v", err, err)
	}

	archivedCall, err := call(ctx, runtime, map[string]string{
		"action": "chunk_archive", "id": string(created.Chunk.ID), "expected_revision": "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	archived := archivedCall.Stored.(chunkMutationResult)
	if archived.Chunk.State != memory.ChunkStateArchived || archived.Chunk.Revision.Number != 3 {
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
	if restored.Chunk.State != memory.ChunkStateActive || restored.Chunk.Revision.Number != 4 {
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
	var unconfirmed *memoryService.ServiceError
	if !errors.As(err, &unconfirmed) || unconfirmed.Code != memoryService.ErrorCodeInvalid {
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

func TestMemoryChunkCascadeDeleteAction(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	created, err := service.CreateChunk(ctx, memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Temporary", Kind: memory.ChunkKindReference, Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
	}})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: created.Chunk.ID, Entry: memory.Entry{Title: "Temporary fact", Kind: memory.EntryKindFact},
	})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := service.ArchiveChunk(ctx, memoryService.ChunkLifecycleRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}

	_, err = call(ctx, runtimeFor(service), map[string]string{
		"action": "chunk_delete", "id": string(created.Chunk.ID),
		"expected_revision": strconv.FormatUint(archived.Chunk.Revision.Number, 10), "confirmed": "true",
	})
	var dependency *memoryService.ServiceError
	if !errors.As(err, &dependency) || dependency.Code != memoryService.ErrorCodeDependency || dependency.Details == nil ||
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

func TestMemoryEntryLifecycleAndVerificationActions(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	chunk, err := service.CreateChunk(ctx, memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Linux partitioning", Kind: memory.ChunkKindEnvironment,
		Scope: memory.Scope{Kind: memory.ScopeKindEnvironment, Selector: "linux"},
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
		created.Entry.Revision.Actor.Kind != memory.ActorKindChat || created.Entry.Revision.Actor.ID != string(runtime.ChatID) {
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
	var conflict *memoryService.ServiceError
	if !errors.As(err, &conflict) || conflict.Code != memoryService.ErrorCodeConflict {
		t.Fatalf("stale entry_update error = %T %v", err, err)
	}

	evidence, err := service.CreateEvidence(ctx, memoryService.CreateEvidenceRequest{Evidence: memory.Evidence{
		Type: memory.EvidenceTypePackage, Quality: memory.EvidenceQualityPrimary,
		Source: memory.Source{ID: "man:sfdisk", Title: "sfdisk manual"},
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
	if !verified.Updated || verified.Entry.Revision.Number != 3 || verified.Entry.Verification.Status != memory.VerificationStatusVerified ||
		verified.Entry.Verification.Actor.Kind != memory.ActorKindChat || verified.Entry.Verification.Actor.ID != string(runtime.ChatID) {
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
	if !superseded.Updated || superseded.Entry.State != memory.EntryStateSuperseded || superseded.Replacement == nil ||
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
	if archived.Entry.State != memory.EntryStateArchived || archived.Entry.Revision.Number != 2 {
		t.Fatalf("entry_archive = %#v", archived)
	}
	restoredCall, err := call(ctx, runtime, map[string]string{
		"action": "entry_restore", "id": string(disposable.Entry.ID), "expected_revision": "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	restored := restoredCall.Stored.(entryMutationResult)
	if restored.Entry.State != memory.EntryStateActive || restored.Entry.Revision.Number != 3 {
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
	var invalid *memoryService.ServiceError
	if !errors.As(err, &invalid) || invalid.Code != memoryService.ErrorCodeInvalid {
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

func TestMemoryEntryDeleteReportsGraphBlockers(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	chunk, _ := service.CreateChunk(ctx, memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Linked", Kind: memory.ChunkKindReference, Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
	}})
	first, _ := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: memory.Entry{Title: "First", Kind: memory.EntryKindFact},
	})
	second, _ := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: memory.Entry{Title: "Second", Kind: memory.EntryKindFact},
	})
	_, _ = service.CreateLink(ctx, memoryService.CreateLinkRequest{Link: memory.Link{
		Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(first.Entry.ID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(second.Entry.ID)}, Kind: memory.LinkKindRelatedTo,
	}})
	archived, err := service.ArchiveEntry(ctx, memoryService.EntryLifecycleRequest{EntryID: first.Entry.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = call(ctx, runtimeFor(service), map[string]string{
		"action": "entry_delete", "id": string(first.Entry.ID),
		"expected_revision": strconv.FormatUint(archived.Entry.Revision.Number, 10), "confirmed": "true",
	})
	var dependency *memoryService.ServiceError
	if !errors.As(err, &dependency) || dependency.Code != memoryService.ErrorCodeDependency || dependency.Details == nil ||
		dependency.Details.EntryBlockers == nil || len(dependency.Details.EntryBlockers.LinkIDs) != 1 {
		t.Fatalf("blocked entry_delete error = %#v (%v)", dependency, err)
	}
}

func TestMemoryLinkUnlinkRestoreAndHistoryActions(t *testing.T) {
	ctx := context.Background()
	service := newService(t)
	chunk, err := service.CreateChunk(ctx, memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Partition memory", Kind: memory.ChunkKindReference, Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
	}})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: memory.Entry{Title: "Use sfdisk", Summary: "Current guidance", Body: "Full current body must not appear in history.", Kind: memory.EntryKindProcedure},
	})
	second, _ := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: chunk.Chunk.ID, Entry: memory.Entry{Title: "Back up first", Kind: memory.EntryKindWarning},
	})
	runtime := runtimeFor(service)
	createdCall, err := call(ctx, runtime, map[string]string{
		"action":       "link",
		"relationship": `{"source":{"kind":"entry","id":"` + string(first.Entry.ID) + `"},"target":{"kind":"entry","id":"` + string(second.Entry.ID) + `"},"kind":"requires","label":"safety prerequisite"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := createdCall.Stored.(linkMutationResult)
	if !ok || !created.Created || created.Link.Kind != memory.LinkKindRequires || created.Link.Revision.Number != 1 ||
		created.Link.Revision.Actor.Kind != memory.ActorKindChat || created.Link.Revision.Actor.ID != string(runtime.ChatID) {
		t.Fatalf("link = %#v", createdCall.Stored)
	}

	_, err = call(ctx, runtime, map[string]string{
		"action":       "link",
		"relationship": `{"source":{"kind":"entry","id":"` + string(first.Entry.ID) + `"},"target":{"kind":"entry","id":"` + string(second.Entry.ID) + `"},"kind":"requires"}`,
	})
	var duplicate *memoryService.ServiceError
	if !errors.As(err, &duplicate) || duplicate.Code != memoryService.ErrorCodeConflict {
		t.Fatalf("duplicate link error = %T %v", err, err)
	}

	unlinkedCall, err := call(ctx, runtime, map[string]string{
		"action": "unlink", "id": string(created.Link.ID), "expected_revision": "1", "reason": "temporarily inapplicable",
	})
	if err != nil {
		t.Fatal(err)
	}
	unlinked := unlinkedCall.Stored.(linkMutationResult)
	if !unlinked.Updated || unlinked.Link.State != memory.LinkStateArchived || unlinked.Link.Revision.Number != 2 {
		t.Fatalf("unlink = %#v", unlinked)
	}

	restoredCall, err := call(ctx, runtime, map[string]string{
		"action": "link", "id": string(created.Link.ID), "expected_revision": "2", "reason": "applies again",
	})
	if err != nil {
		t.Fatal(err)
	}
	restored := restoredCall.Stored.(linkMutationResult)
	if !restored.Updated || restored.Link.State != memory.LinkStateActive || restored.Link.Revision.Number != 3 {
		t.Fatalf("link restore = %#v", restored)
	}

	historyCall, err := call(ctx, runtime, map[string]string{
		"action": "history", "object_kind": "link", "id": string(created.Link.ID), "limit": "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	history, ok := historyCall.Stored.(historyPageResult)
	if !ok || len(history.Revisions) != 2 || history.Revisions[0].Revision.Number != 3 || history.Revisions[1].Revision.Number != 2 || history.NextCursor == "" ||
		history.Revisions[0].RelationshipKind != memory.LinkKindRequires || history.Revisions[0].Source == nil {
		t.Fatalf("link history = %#v", historyCall.Stored)
	}
	nextCall, err := call(ctx, runtime, map[string]string{
		"action": "history", "object_kind": "link", "id": string(created.Link.ID), "limit": "2", "cursor": history.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	next := nextCall.Stored.(historyPageResult)
	if len(next.Revisions) != 1 || next.Revisions[0].Revision.Number != 1 {
		t.Fatalf("second link history page = %#v", next)
	}

	entryHistoryCall, err := call(ctx, runtime, map[string]string{
		"action": "history", "object_kind": "entry", "id": string(first.Entry.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	entryHistory := entryHistoryCall.Stored.(historyPageResult)
	if len(entryHistory.Revisions) != 1 || entryHistory.Revisions[0].Summary != first.Entry.Summary ||
		strings.Contains(entryHistoryCall.Output, first.Entry.Body) {
		t.Fatalf("entry history exposed wrong projection: %#v, output=%s", entryHistory, entryHistoryCall.Output)
	}
}

func TestMemoryLinkAndHistoryInputValidation(t *testing.T) {
	runtime := runtimeFor(newService(t))
	_, err := call(context.Background(), runtime, map[string]string{
		"action": "link", "id": "01a01688-fc5d-7f7d-8bb8-de244977f8a1", "expected_revision": "1",
		"relationship": `{"source":{"kind":"chunk","id":"01a01688-fc5d-7f7d-8bb8-de244977f8a2"},"target":{"kind":"chunk","id":"01a01688-fc5d-7f7d-8bb8-de244977f8a3"},"kind":"related_to"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "either relationship") {
		t.Fatalf("ambiguous link error = %v", err)
	}
	_, err = call(context.Background(), runtime, map[string]string{
		"action": "history", "object_kind": "entry", "id": "01a01688-fc5d-7f7d-8bb8-de244977f8a1", "limit": "51",
	})
	if err == nil || !strings.Contains(err.Error(), "between 1 and 50") {
		t.Fatalf("oversized history error = %v", err)
	}
}

func TestMemoryChunkInputRejectsUnknownFieldsAndOversizedCalls(t *testing.T) {
	_, err := call(context.Background(), runtimeFor(newService(t)), map[string]string{
		"action": "chunk_create", "chunk": `{"title":"Invalid","kind":"reference","scope":{"kind":"global"},"server_owned":true}`,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown chunk field error = %v", err)
	}
	_, err = tools.ParseProviderCall(provider.ToolCall{
		ID: "memory_oversized",
		Function: provider.FunctionCall{
			Name:      tools.Memory.String(),
			Arguments: `{"action":"chunk_create","chunk":{"title":"` + strings.Repeat("x", 129*1024) + `"}}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "memory tool arguments exceeded 128 KiB") {
		t.Fatalf("oversized memory call error = %v", err)
	}
}

func call(ctx context.Context, runtime tools.Runtime, args map[string]string) (tools.Result, error) {
	return tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: tools.Memory, Args: args}})
}

func anyStrings(values []any) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.(string))
	}
	return result
}

func runtimeFor(service *memoryService.Service) tools.Runtime {
	return tools.Runtime{ChatID: "01a01688-fc5d-7f7d-8bb8-de244977fee1", Services: RuntimeService(service)}
}

func newService(t *testing.T) *memoryService.Service {
	t.Helper()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{
			Kind: memory.ActorKindSystem,
			ID:   "system:test",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
