package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestMemoryAPIIsolatesDeniedScopesWithoutDisclosingExistence(t *testing.T) {
	ctrl := newTestController(t)
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
		ChunkPolicy: memoryService.ChunkPolicyFunc(func(_ context.Context, actor memory.Actor, _ memoryService.ChunkPolicyAction, chunk memory.Chunk) error {
			if actor.Kind == memory.ActorKindUser && chunk.Scope.Kind == memory.ScopeKindProject && chunk.Scope.Selector == "classified" {
				return errors.New("classified project policy with private details")
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetMemoryService(service)
	visible := createAPIChunk(t, service, "Visible root", memory.Scope{Kind: memory.ScopeKindGlobal})
	createAPIGraphEntry(t, service, visible.ID, "Ordinary visible entry")
	hidden := createAPIChunk(t, service, "Classified project", memory.Scope{Kind: memory.ScopeKindProject, Selector: "classified"})
	hiddenEntry := createAPIGraphEntry(t, service, hidden.ID, "Sealed albatross memory")
	link, err := service.CreateLink(context.Background(), memoryService.CreateLinkRequest{Link: memory.Link{
		Source: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(visible.ID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(hidden.ID)},
		Kind:   memory.LinkKindRelatedTo,
	}})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	token := bindMemoryTestDevice(t, srv)

	response := memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.SearchPath, token, memoryapi.SearchRequest{
		Query: "sealed albatross", ScopeKinds: []memory.ScopeKind{memory.ScopeKindGlobal, memory.ScopeKindProject},
	})
	var search memoryapi.SearchResponse
	decodeMemoryResponse(t, response, &search)
	if response.StatusCode != http.StatusOK || len(search.Matches) != 0 || search.CorpusDocumentCount != 1 || search.MatchedDocumentCount != 0 {
		t.Fatalf("scope-isolated search status=%d response=%#v", response.StatusCode, search)
	}
	assertMemoryPayloadOmits(t, search, string(hidden.ID), string(hiddenEntry.ID), hidden.Title, hiddenEntry.Title)

	visibleRoot := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(visible.ID)}
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.GraphSnapshotPath, token, memoryapi.GraphSnapshotRequest{
		Root: visibleRoot, Direction: memoryStoreAPI.LinkDirectionBoth, MaxDepth: 2,
	})
	var graph memoryapi.GraphSnapshotResponse
	decodeMemoryResponse(t, response, &graph)
	if response.StatusCode != http.StatusOK || len(graph.Nodes) != 1 || graph.Nodes[0].Object != visibleRoot || len(graph.Edges) != 0 {
		t.Fatalf("scope-isolated graph status=%d response=%#v", response.StatusCode, graph)
	}
	assertMemoryPayloadOmits(t, graph, string(hidden.ID), string(hiddenEntry.ID), string(link.Link.ID), hidden.Title, hiddenEntry.Title)

	hiddenRoot := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(hidden.ID)}
	missingRoot := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: "01a02b00-0000-7000-8000-00000000ffff"}
	denied := memoryGraphReadError(t, srv.URL()+memoryapi.GraphSnapshotPath, token, hiddenRoot)
	missing := memoryGraphReadError(t, srv.URL()+memoryapi.GraphSnapshotPath, token, missingRoot)
	if denied.Code != memoryService.ErrorCodeNotFound || denied.Code != missing.Code || denied.Message != missing.Message || denied.Details != nil || missing.Details != nil {
		t.Fatalf("denied graph error %#v differs from missing %#v", denied, missing)
	}

	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.NeighborPath, token, memoryapi.NeighborRequest{Object: hiddenRoot})
	var neighborDenied memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &neighborDenied)
	if response.StatusCode != http.StatusNotFound || neighborDenied.Error == nil || neighborDenied.Error.Code != memoryService.ErrorCodeNotFound || neighborDenied.Error.Details != nil {
		t.Fatalf("denied neighbor status=%d response=%#v", response.StatusCode, neighborDenied)
	}
}

func memoryGraphReadError(t *testing.T, url, token string, root memory.ObjectRef) *memoryService.ServiceError {
	t.Helper()
	response := memoryJSONRequest(t, http.MethodPost, url, token, memoryapi.GraphSnapshotRequest{Root: root})
	var body memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &body)
	if response.StatusCode != http.StatusNotFound || body.Error == nil {
		t.Fatalf("graph read status=%d response=%#v", response.StatusCode, body)
	}
	return body.Error
}

func assertMemoryPayloadOmits(t *testing.T, value any, forbidden ...string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.ToLower(string(data))
	for _, candidate := range forbidden {
		if candidate != "" && strings.Contains(encoded, strings.ToLower(candidate)) {
			t.Fatalf("Memory response leaked %q: %s", candidate, encoded)
		}
	}
}
