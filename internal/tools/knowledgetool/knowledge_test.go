package knowledgetool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
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
	if got := strings.Join(schema.Properties["action"].Enum, ","); got != "search,get,neighbors" {
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

func call(ctx context.Context, runtime tools.Runtime, args map[string]string) (tools.Result, error) {
	return tools.Call(ctx, tools.Options{Runtime: runtime, Request: tools.Request{Tool: tools.Knowledge, Args: args}})
}

func runtimeFor(service *knowledgeService.Service) tools.Runtime {
	return tools.Runtime{Services: RuntimeService(service)}
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
