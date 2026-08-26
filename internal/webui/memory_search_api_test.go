package webui

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestMemorySearchAndBoundedGraphSnapshot(t *testing.T) {
	ctrl := newTestController(t)
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatalf("new Memory service: %v", err)
	}
	ctrl.SetMemoryService(service)
	chunk := createAPIChunk(t, service, "Storage tools", memory.Scope{Kind: memory.ScopeKindGlobal})
	root := createAPIGraphEntry(t, service, chunk.ID, "Partition a disk safely")
	first := createAPIGraphEntry(t, service, chunk.ID, "Use sfdisk for scripts")
	second := createAPIGraphEntry(t, service, chunk.ID, "Back up the partition table")
	for _, target := range []memory.Entry{first, second} {
		if _, err := service.CreateLink(context.Background(), memoryService.CreateLinkRequest{Link: memory.Link{
			Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(root.ID)},
			Target: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(target.ID)},
			Kind:   memory.LinkKindRelatedTo,
		}}); err != nil {
			t.Fatalf("create graph link: %v", err)
		}
	}
	heartbeat := (&Server{controller: ctrl}).websocketHeartbeatPayload()
	heartbeatCheckpoint, ok := heartbeat["memory_checkpoint"].(memoryService.MutationCheckpoint)
	if !ok || heartbeatCheckpoint.StreamID == "" || heartbeatCheckpoint.Sequence == 0 {
		t.Fatalf("heartbeat checkpoint = %#v", heartbeat["memory_checkpoint"])
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindMemoryTestDevice(t, srv)
	response := memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.SearchPath, token, memoryapi.SearchRequest{
		Query: "partition", Scopes: []memory.Scope{{Kind: memory.ScopeKindGlobal}}, ExpandGraph: true, Limit: 1,
	})
	var search memoryapi.SearchResponse
	decodeMemoryResponse(t, response, &search)
	if response.StatusCode != http.StatusOK || search.OperationID == "" || len(search.Matches) != 1 || search.Matches[0].EntryID == "" || search.Matches[0].Document.Title == "" ||
		search.Page.Limit != 1 || search.Page.NextCursor == "" || search.GraphExpansion == nil || search.GraphExpansion.Connections != 2 {
		t.Fatalf("search status=%d response=%#v", response.StatusCode, search)
	}
	metrics := service.OperationMetrics()
	if len(metrics.Recent) != 1 || metrics.Recent[0].OperationID != search.OperationID || metrics.Recent[0].AuditID != search.RequestID {
		t.Fatalf("search operation correlation response=%#v metrics=%#v", search, metrics)
	}

	neighborRequest := memoryapi.NeighborRequest{
		Object: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(root.ID)}, Limit: 1,
	}
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.NeighborPath, token, neighborRequest)
	var firstNeighbors memoryapi.NeighborResponse
	decodeMemoryResponse(t, response, &firstNeighbors)
	if response.StatusCode != http.StatusOK || len(firstNeighbors.Neighbors) != 1 || firstNeighbors.Page.NextCursor == "" ||
		firstNeighbors.Neighbors[0].Object.Entry == nil {
		t.Fatalf("first neighbors status=%d response=%#v", response.StatusCode, firstNeighbors)
	}
	neighborRequest.Cursor = firstNeighbors.Page.NextCursor
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.NeighborPath, token, neighborRequest)
	var secondNeighbors memoryapi.NeighborResponse
	decodeMemoryResponse(t, response, &secondNeighbors)
	if response.StatusCode != http.StatusOK || len(secondNeighbors.Neighbors) != 1 || secondNeighbors.Page.NextCursor != "" ||
		secondNeighbors.Neighbors[0].Link.ID == firstNeighbors.Neighbors[0].Link.ID {
		t.Fatalf("second neighbors status=%d response=%#v", response.StatusCode, secondNeighbors)
	}

	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.GraphSnapshotPath, token, memoryapi.GraphSnapshotRequest{
		Root:     memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(root.ID)},
		MaxDepth: 1, MaxNodes: 2, MaxEdges: 1, TimeLimitMS: 1000,
	})
	var graph memoryapi.GraphSnapshotResponse
	decodeMemoryResponse(t, response, &graph)
	if response.StatusCode != http.StatusOK || graph.Generation == 0 || graph.Checkpoint.StreamID == "" || graph.Checkpoint.Sequence == 0 || len(graph.Nodes) != 2 || len(graph.Edges) != 1 ||
		!graph.Page.Truncated || !slices.Contains(graph.Page.TruncationReasons, "edge_limit") || graph.Nodes[0].Object.ID != string(root.ID) {
		t.Fatalf("graph status=%d response=%#v", response.StatusCode, graph)
	}
	for _, node := range graph.Nodes {
		if node.ID == "" || node.Object.ID == "" || node.Title == "" || node.Revision.Number == 0 {
			t.Fatalf("incomplete graph node = %#v", node)
		}
	}

	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.GraphSnapshotPath, token, memoryapi.GraphSnapshotRequest{
		Root: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(root.ID)}, MaxNodes: 1001,
	})
	var invalid memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &invalid)
	if response.StatusCode != http.StatusBadRequest || invalid.Error == nil || invalid.Error.Code != memoryService.ErrorCodeInvalid {
		t.Fatalf("oversized graph status=%d response=%#v", response.StatusCode, invalid)
	}
	neighborRequest.Limit = 101
	neighborRequest.Cursor = ""
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.NeighborPath, token, neighborRequest)
	decodeMemoryResponse(t, response, &invalid)
	if response.StatusCode != http.StatusBadRequest || invalid.Error == nil || invalid.Error.Code != memoryService.ErrorCodeInvalid {
		t.Fatalf("oversized neighbors status=%d response=%#v", response.StatusCode, invalid)
	}
}

func createAPIGraphEntry(t *testing.T, service *memoryService.Service, chunkID memory.ChunkID, title string) memory.Entry {
	t.Helper()
	created, err := service.CreateEntry(context.Background(), memoryService.CreateEntryRequest{
		ChunkID: chunkID,
		Entry: memory.Entry{
			Kind: memory.EntryKindProcedure, Title: title, Summary: "Bounded API graph fixture",
			Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
		},
	})
	if err != nil {
		t.Fatalf("create graph entry %q: %v", title, err)
	}
	return created.Entry
}
