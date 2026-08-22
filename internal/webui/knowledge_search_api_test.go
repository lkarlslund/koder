package webui

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestKnowledgeSearchAndBoundedGraphSnapshot(t *testing.T) {
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatalf("new Knowledge service: %v", err)
	}
	ctrl.SetKnowledgeService(service)
	chunk := createAPIChunk(t, service, "Storage tools", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	root := createAPIGraphEntry(t, service, chunk.ID, "Partition a disk safely")
	first := createAPIGraphEntry(t, service, chunk.ID, "Use sfdisk for scripts")
	second := createAPIGraphEntry(t, service, chunk.ID, "Back up the partition table")
	for _, target := range []knowledge.Entry{first, second} {
		if _, err := service.CreateLink(context.Background(), knowledgeService.CreateLinkRequest{Link: knowledge.Link{
			Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(root.ID)},
			Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(target.ID)},
			Kind:   knowledge.LinkKindRelatedTo,
		}}); err != nil {
			t.Fatalf("create graph link: %v", err)
		}
	}
	heartbeat := (&Server{controller: ctrl}).websocketHeartbeatPayload()
	heartbeatCheckpoint, ok := heartbeat["knowledge_checkpoint"].(knowledgeService.MutationCheckpoint)
	if !ok || heartbeatCheckpoint.StreamID == "" || heartbeatCheckpoint.Sequence == 0 {
		t.Fatalf("heartbeat checkpoint = %#v", heartbeat["knowledge_checkpoint"])
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindKnowledgeTestDevice(t, srv)
	response := knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.SearchPath, token, knowledgeapi.SearchRequest{
		Query: "partition", Scopes: []knowledge.Scope{{Kind: knowledge.ScopeKindGlobal}}, ExpandGraph: true, Limit: 1,
	})
	var search knowledgeapi.SearchResponse
	decodeKnowledgeResponse(t, response, &search)
	if response.StatusCode != http.StatusOK || search.OperationID == "" || len(search.Matches) != 1 || search.Matches[0].EntryID == "" || search.Matches[0].Document.Title == "" ||
		search.Page.Limit != 1 || search.Page.NextCursor == "" || search.GraphExpansion == nil || search.GraphExpansion.Connections != 2 {
		t.Fatalf("search status=%d response=%#v", response.StatusCode, search)
	}
	metrics := service.OperationMetrics()
	if len(metrics.Recent) != 1 || metrics.Recent[0].OperationID != search.OperationID || metrics.Recent[0].AuditID != search.RequestID {
		t.Fatalf("search operation correlation response=%#v metrics=%#v", search, metrics)
	}

	neighborRequest := knowledgeapi.NeighborRequest{
		Object: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(root.ID)}, Limit: 1,
	}
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.NeighborPath, token, neighborRequest)
	var firstNeighbors knowledgeapi.NeighborResponse
	decodeKnowledgeResponse(t, response, &firstNeighbors)
	if response.StatusCode != http.StatusOK || len(firstNeighbors.Neighbors) != 1 || firstNeighbors.Page.NextCursor == "" ||
		firstNeighbors.Neighbors[0].Object.Entry == nil {
		t.Fatalf("first neighbors status=%d response=%#v", response.StatusCode, firstNeighbors)
	}
	neighborRequest.Cursor = firstNeighbors.Page.NextCursor
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.NeighborPath, token, neighborRequest)
	var secondNeighbors knowledgeapi.NeighborResponse
	decodeKnowledgeResponse(t, response, &secondNeighbors)
	if response.StatusCode != http.StatusOK || len(secondNeighbors.Neighbors) != 1 || secondNeighbors.Page.NextCursor != "" ||
		secondNeighbors.Neighbors[0].Link.ID == firstNeighbors.Neighbors[0].Link.ID {
		t.Fatalf("second neighbors status=%d response=%#v", response.StatusCode, secondNeighbors)
	}

	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.GraphSnapshotPath, token, knowledgeapi.GraphSnapshotRequest{
		Root:     knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(root.ID)},
		MaxDepth: 1, MaxNodes: 2, MaxEdges: 1, TimeLimitMS: 1000,
	})
	var graph knowledgeapi.GraphSnapshotResponse
	decodeKnowledgeResponse(t, response, &graph)
	if response.StatusCode != http.StatusOK || graph.Generation == 0 || graph.Checkpoint.StreamID == "" || graph.Checkpoint.Sequence == 0 || len(graph.Nodes) != 2 || len(graph.Edges) != 1 ||
		!graph.Page.Truncated || !slices.Contains(graph.Page.TruncationReasons, "edge_limit") || graph.Nodes[0].Object.ID != string(root.ID) {
		t.Fatalf("graph status=%d response=%#v", response.StatusCode, graph)
	}
	for _, node := range graph.Nodes {
		if node.ID == "" || node.Object.ID == "" || node.Title == "" || node.Revision.Number == 0 {
			t.Fatalf("incomplete graph node = %#v", node)
		}
	}

	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.GraphSnapshotPath, token, knowledgeapi.GraphSnapshotRequest{
		Root: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(root.ID)}, MaxNodes: 1001,
	})
	var invalid knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &invalid)
	if response.StatusCode != http.StatusBadRequest || invalid.Error == nil || invalid.Error.Code != knowledgeService.ErrorCodeInvalid {
		t.Fatalf("oversized graph status=%d response=%#v", response.StatusCode, invalid)
	}
	neighborRequest.Limit = 101
	neighborRequest.Cursor = ""
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.NeighborPath, token, neighborRequest)
	decodeKnowledgeResponse(t, response, &invalid)
	if response.StatusCode != http.StatusBadRequest || invalid.Error == nil || invalid.Error.Code != knowledgeService.ErrorCodeInvalid {
		t.Fatalf("oversized neighbors status=%d response=%#v", response.StatusCode, invalid)
	}
}

func createAPIGraphEntry(t *testing.T, service *knowledgeService.Service, chunkID knowledge.ChunkID, title string) knowledge.Entry {
	t.Helper()
	created, err := service.CreateEntry(context.Background(), knowledgeService.CreateEntryRequest{
		ChunkID: chunkID,
		Entry: knowledge.Entry{
			Kind: knowledge.EntryKindProcedure, Title: title, Summary: "Bounded API graph fixture",
			Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		},
	})
	if err != nil {
		t.Fatalf("create graph entry %q: %v", title, err)
	}
	return created.Entry
}
