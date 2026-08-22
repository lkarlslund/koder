package webui

import (
	"net/http"

	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

func (s *Server) handleKnowledgeNeighbors(w http.ResponseWriter, r *http.Request) {
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
	var request knowledgeapi.NeighborRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	page, err := service.Neighbors(ctx, knowledgeService.NeighborRequest{
		Endpoint: request.Object, Direction: request.Direction, Kinds: request.Kinds,
		Limit: request.Limit, Cursor: request.Cursor,
	})
	if err != nil {
		s.writeKnowledgeReadError(w, requestID, err)
		return
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 25
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.NeighborResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Object: request.Object, Neighbors: page.Neighbors,
		Page: knowledgeapi.Page{Limit: limit, Returned: len(page.Neighbors), NextCursor: page.NextCursor},
	})
}
