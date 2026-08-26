package webui

import (
	"net/http"
	"strings"

	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
)

func (s *Server) handleMemoryOperationalStatus(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	if !s.requireNoMemoryQuery(w, r, requestID) {
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	status, err := service.OperationalStatus(ctx)
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.OperationalStatusResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), Status: status,
	})
}

func (s *Server) handleMemoryIndexRebuild(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		w.Header().Set("Allow", http.MethodPost+", "+http.MethodDelete)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	if r.Method == http.MethodDelete {
		if !s.requireNoMemoryQuery(w, r, requestID) {
			return
		}
		result, err := service.CancelIndexRebuild(ctx)
		if err != nil {
			s.writeMemoryServiceError(w, requestID, err)
			return
		}
		s.writeMemoryJSON(w, http.StatusAccepted, memoryapi.IndexRebuildCancelResponse{
			ResponseMetadata: memoryapi.Metadata(requestID), Result: result,
		})
		return
	}
	var request memoryapi.IndexRebuildRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil || !validMemoryIndexName(request.Index) {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	result, err := service.StartIndexRebuild(ctx)
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusAccepted, memoryapi.IndexRebuildResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), Result: result,
	})
}

func validMemoryIndexName(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == "lexical"
}
