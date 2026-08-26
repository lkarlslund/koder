package webui

import (
	"net/http"

	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
)

func (s *Server) handleMemoryNeighbors(w http.ResponseWriter, r *http.Request) {
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
	var request memoryapi.NeighborRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	page, err := service.Neighbors(ctx, memoryService.NeighborRequest{
		Endpoint: request.Object, Direction: request.Direction, Kinds: request.Kinds,
		Limit: request.Limit, Cursor: request.Cursor,
	})
	if err != nil {
		s.writeMemoryReadError(w, requestID, err)
		return
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 25
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.NeighborResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), Object: request.Object, Neighbors: page.Neighbors,
		Page: memoryapi.Page{Limit: limit, Returned: len(page.Neighbors), NextCursor: page.NextCursor},
	})
}
