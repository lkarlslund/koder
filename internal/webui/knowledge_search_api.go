package webui

import (
	"net/http"
	"time"

	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Server) handleKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}
	var request knowledgeapi.SearchRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	serviceRequest := knowledgeService.LexicalSearchRequest{
		Query: request.Query, ChunkIDs: request.ChunkIDs, Scopes: request.Scopes, ScopeKinds: request.ScopeKinds,
		IncludeInvalid: request.IncludeInvalid, IncludeSuperseded: request.IncludeSuperseded,
		Limit: request.Limit, Cursor: request.Cursor,
	}
	if request.ExpandGraph {
		serviceRequest.GraphExpansion = &knowledgeService.GraphExpansionOptions{}
	}
	result, err := service.SearchLexical(ctx, serviceRequest)
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 25
	}
	page := knowledgeapi.Page{Limit: limit, Returned: len(result.Matches), NextCursor: result.NextCursor}
	if result.GraphExpansion != nil && result.GraphExpansion.Truncated {
		page.Truncated = true
		page.TruncationReasons = result.GraphExpansion.Reasons
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.SearchResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Terms: result.Terms, Matches: result.Matches,
		Warnings: result.Warnings, Contradictions: result.Contradictions, AsOf: result.AsOf,
		CorpusDocumentCount: result.CorpusDocumentCount, MatchedDocumentCount: result.MatchedDocumentCount,
		GraphExpansion: result.GraphExpansion, Page: page,
	})
}

func (s *Server) handleKnowledgeGraphSnapshot(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}
	var request knowledgeapi.GraphSnapshotRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	if request.TimeLimitMS < 0 || request.TimeLimitMS > 10_000 {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	result, err := service.GraphSnapshot(ctx, knowledgeService.TraversalRequest{
		Root: request.Root, Direction: request.Direction, Kinds: request.Kinds,
		MaxDepth: request.MaxDepth, MaxNodes: request.MaxNodes, MaxEdges: request.MaxEdges,
		TimeLimit: time.Duration(request.TimeLimitMS) * time.Millisecond,
	})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	nodes := make([]knowledgeapi.GraphNode, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		adapted, ok := knowledgeGraphNode(node.Object)
		if !ok {
			continue
		}
		nodes = append(nodes, adapted)
	}
	edges := make([]knowledgeapi.GraphEdge, 0, len(result.Edges))
	for _, link := range result.Edges {
		edges = append(edges, knowledgeapi.GraphEdge{
			ID: string(link.ID), Source: link.Source, Target: link.Target, Kind: link.Kind,
			Label: link.Label, State: link.State, Revision: link.Revision,
		})
	}
	limit := request.MaxNodes
	if limit <= 0 {
		limit = 100
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.GraphSnapshotResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Generation: result.Generation,
		Nodes: nodes, Edges: edges,
		Page: knowledgeapi.Page{Limit: limit, Returned: len(nodes), Truncated: result.Truncated, TruncationReasons: result.TruncationReasons},
	})
}

func knowledgeGraphNode(record knowledgeStore.CanonicalRecord) (knowledgeapi.GraphNode, bool) {
	object := record.ObjectRef()
	node := knowledgeapi.GraphNode{ID: object.Kind.String() + ":" + object.ID, Object: object}
	switch record.Kind {
	case knowledgeStore.RecordKindChunk:
		if record.Chunk == nil {
			return knowledgeapi.GraphNode{}, false
		}
		node.SemanticKind = record.Chunk.Kind.String()
		node.Title = record.Chunk.Title
		node.Summary = record.Chunk.Description
		node.Scope = record.Chunk.Scope
		node.State = record.Chunk.State.String()
		node.Revision = record.Chunk.Revision
		node.Risk = record.Chunk.Risk
	case knowledgeStore.RecordKindEntry:
		if record.Entry == nil {
			return knowledgeapi.GraphNode{}, false
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
		return knowledgeapi.GraphNode{}, false
	}
	return node, true
}
