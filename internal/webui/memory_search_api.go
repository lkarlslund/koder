package webui

import (
	"net/http"
	"time"

	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	var request memoryapi.SearchRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	serviceRequest := memoryService.LexicalSearchRequest{
		Query: request.Query, ChunkIDs: request.ChunkIDs, Scopes: request.Scopes, ScopeKinds: request.ScopeKinds,
		IncludeInvalid: request.IncludeInvalid, IncludeSuperseded: request.IncludeSuperseded,
		Limit: request.Limit, Cursor: request.Cursor,
	}
	if request.ExpandGraph {
		serviceRequest.GraphExpansion = &memoryService.GraphExpansionOptions{}
	}
	result, err := service.SearchLexical(ctx, serviceRequest)
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 25
	}
	page := memoryapi.Page{Limit: limit, Returned: len(result.Matches), NextCursor: result.NextCursor}
	if result.GraphExpansion != nil && result.GraphExpansion.Truncated {
		page.Truncated = true
		page.TruncationReasons = result.GraphExpansion.Reasons
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.SearchResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), OperationID: result.OperationID,
		Terms: result.Terms, Matches: result.Matches,
		Warnings: result.Warnings, Contradictions: result.Contradictions, AsOf: result.AsOf,
		CorpusDocumentCount: result.CorpusDocumentCount, MatchedDocumentCount: result.MatchedDocumentCount,
		GraphExpansion: result.GraphExpansion, Page: page,
	})
}

func (s *Server) handleMemoryGraphSnapshot(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	var request memoryapi.GraphSnapshotRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	if request.TimeLimitMS < 0 || request.TimeLimitMS > 10_000 {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	result, err := service.GraphSnapshot(ctx, memoryService.TraversalRequest{
		Root: request.Root, Direction: request.Direction, Kinds: request.Kinds,
		MaxDepth: request.MaxDepth, MaxNodes: request.MaxNodes, MaxEdges: request.MaxEdges,
		TimeLimit: time.Duration(request.TimeLimitMS) * time.Millisecond,
	})
	if err != nil {
		s.writeMemoryReadError(w, requestID, err)
		return
	}
	nodes := make([]memoryapi.GraphNode, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		adapted, ok := memoryGraphNode(node.Object)
		if !ok {
			continue
		}
		nodes = append(nodes, adapted)
	}
	edges := make([]memoryapi.GraphEdge, 0, len(result.Edges))
	for _, link := range result.Edges {
		edges = append(edges, memoryapi.GraphEdge{
			ID: string(link.ID), Source: link.Source, Target: link.Target, Kind: link.Kind,
			Label: link.Label, State: link.State, Revision: link.Revision,
		})
	}
	limit := request.MaxNodes
	if limit <= 0 {
		limit = 100
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.GraphSnapshotResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), Generation: result.Generation, Checkpoint: result.Checkpoint,
		Nodes: nodes, Edges: edges,
		Page: memoryapi.Page{Limit: limit, Returned: len(nodes), Truncated: result.Truncated, TruncationReasons: result.TruncationReasons},
	})
}

func memoryGraphNode(record memoryStoreAPI.CanonicalRecord) (memoryapi.GraphNode, bool) {
	object := record.ObjectRef()
	node := memoryapi.GraphNode{ID: object.Kind.String() + ":" + object.ID, Object: object}
	switch record.Kind {
	case memoryStoreAPI.RecordKindChunk:
		if record.Chunk == nil {
			return memoryapi.GraphNode{}, false
		}
		node.SemanticKind = record.Chunk.Kind.String()
		node.Title = record.Chunk.Title
		node.Summary = record.Chunk.Description
		node.Scope = record.Chunk.Scope
		node.State = record.Chunk.State.String()
		node.Revision = record.Chunk.Revision
		node.Risk = record.Chunk.Risk
	case memoryStoreAPI.RecordKindEntry:
		if record.Entry == nil {
			return memoryapi.GraphNode{}, false
		}
		node.SemanticKind = record.Entry.Kind.String()
		node.Title = record.Entry.Title
		node.Summary = record.Entry.Summary
		node.Scope = record.Entry.Scope
		node.State = record.Entry.State.String()
		node.Revision = record.Entry.Revision
		node.Verification = record.Entry.Verification.Status
		node.Risk = record.Entry.Risk
	default:
		return memoryapi.GraphNode{}, false
	}
	return node, true
}
