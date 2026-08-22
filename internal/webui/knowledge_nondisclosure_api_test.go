package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestKnowledgeAPIIsolatesDeniedScopesWithoutDisclosingExistence(t *testing.T) {
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		ChunkPolicy: knowledgeService.ChunkPolicyFunc(func(_ context.Context, actor knowledge.Actor, _ knowledgeService.ChunkPolicyAction, chunk knowledge.Chunk) error {
			if actor.Kind == knowledge.ActorKindUser && chunk.Scope.Kind == knowledge.ScopeKindProject && chunk.Scope.Selector == "classified" {
				return errors.New("classified project policy with private details")
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetKnowledgeService(service)
	visible := createAPIChunk(t, service, "Visible root", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	createAPIGraphEntry(t, service, visible.ID, "Ordinary visible entry")
	hidden := createAPIChunk(t, service, "Classified project", knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: "classified"})
	hiddenEntry := createAPIGraphEntry(t, service, hidden.ID, "Sealed albatross knowledge")
	link, err := service.CreateLink(context.Background(), knowledgeService.CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(visible.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(hidden.ID)},
		Kind:   knowledge.LinkKindRelatedTo,
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
	token := bindKnowledgeTestDevice(t, srv)

	response := knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.SearchPath, token, knowledgeapi.SearchRequest{
		Query: "sealed albatross", ScopeKinds: []knowledge.ScopeKind{knowledge.ScopeKindGlobal, knowledge.ScopeKindProject},
	})
	var search knowledgeapi.SearchResponse
	decodeKnowledgeResponse(t, response, &search)
	if response.StatusCode != http.StatusOK || len(search.Matches) != 0 || search.CorpusDocumentCount != 1 || search.MatchedDocumentCount != 0 {
		t.Fatalf("scope-isolated search status=%d response=%#v", response.StatusCode, search)
	}
	assertKnowledgePayloadOmits(t, search, string(hidden.ID), string(hiddenEntry.ID), hidden.Title, hiddenEntry.Title)

	visibleRoot := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(visible.ID)}
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.GraphSnapshotPath, token, knowledgeapi.GraphSnapshotRequest{
		Root: visibleRoot, Direction: knowledgeStore.LinkDirectionBoth, MaxDepth: 2,
	})
	var graph knowledgeapi.GraphSnapshotResponse
	decodeKnowledgeResponse(t, response, &graph)
	if response.StatusCode != http.StatusOK || len(graph.Nodes) != 1 || graph.Nodes[0].Object != visibleRoot || len(graph.Edges) != 0 {
		t.Fatalf("scope-isolated graph status=%d response=%#v", response.StatusCode, graph)
	}
	assertKnowledgePayloadOmits(t, graph, string(hidden.ID), string(hiddenEntry.ID), string(link.Link.ID), hidden.Title, hiddenEntry.Title)

	hiddenRoot := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(hidden.ID)}
	missingRoot := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: "01a02b00-0000-7000-8000-00000000ffff"}
	denied := knowledgeGraphReadError(t, srv.URL()+knowledgeapi.GraphSnapshotPath, token, hiddenRoot)
	missing := knowledgeGraphReadError(t, srv.URL()+knowledgeapi.GraphSnapshotPath, token, missingRoot)
	if denied.Code != knowledgeService.ErrorCodeNotFound || denied.Code != missing.Code || denied.Message != missing.Message || denied.Details != nil || missing.Details != nil {
		t.Fatalf("denied graph error %#v differs from missing %#v", denied, missing)
	}

	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.NeighborPath, token, knowledgeapi.NeighborRequest{Object: hiddenRoot})
	var neighborDenied knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &neighborDenied)
	if response.StatusCode != http.StatusNotFound || neighborDenied.Error == nil || neighborDenied.Error.Code != knowledgeService.ErrorCodeNotFound || neighborDenied.Error.Details != nil {
		t.Fatalf("denied neighbor status=%d response=%#v", response.StatusCode, neighborDenied)
	}
}

func knowledgeGraphReadError(t *testing.T, url, token string, root knowledge.ObjectRef) *knowledgeService.ServiceError {
	t.Helper()
	response := knowledgeJSONRequest(t, http.MethodPost, url, token, knowledgeapi.GraphSnapshotRequest{Root: root})
	var body knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &body)
	if response.StatusCode != http.StatusNotFound || body.Error == nil {
		t.Fatalf("graph read status=%d response=%#v", response.StatusCode, body)
	}
	return body.Error
}

func assertKnowledgePayloadOmits(t *testing.T, value any, forbidden ...string) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.ToLower(string(data))
	for _, candidate := range forbidden {
		if candidate != "" && strings.Contains(encoded, strings.ToLower(candidate)) {
			t.Fatalf("Knowledge response leaked %q: %s", candidate, encoded)
		}
	}
}
