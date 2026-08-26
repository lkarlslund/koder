package webui

import (
	"context"
	"net/http"
	"strings"

	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
)

func (s *Server) handleMemoryGraphViews(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	if !s.requireNoMemoryQuery(w, r, requestID) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listMemoryGraphViews(w, requestID, ctx, service)
	case http.MethodPost:
		s.createMemoryGraphView(w, r, requestID, ctx, service)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
	}
}

func (s *Server) handleMemoryGraphView(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	viewID := strings.Trim(strings.TrimPrefix(r.URL.Path, memoryapi.GraphViewCollectionPath+"/"), "/")
	if viewID == "" || strings.Contains(viewID, "/") {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory object was not found.")
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	if !s.requireNoMemoryQuery(w, r, requestID) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		view, err := service.GetGraphView(ctx, viewID)
		if err != nil {
			s.writeMemoryServiceError(w, requestID, err)
			return
		}
		s.writeMemoryJSON(w, http.StatusOK, memoryapi.GraphViewResponse{ResponseMetadata: memoryapi.Metadata(requestID), View: view})
	case http.MethodPut:
		s.updateMemoryGraphView(w, r, requestID, ctx, service, viewID)
	case http.MethodDelete:
		s.deleteMemoryGraphView(w, r, requestID, ctx, service, viewID)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
	}
}

func (s *Server) listMemoryGraphViews(w http.ResponseWriter, requestID string, ctx context.Context, service *memoryService.Service) {
	views, err := service.ListGraphViews(ctx)
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.GraphViewListResponse{ResponseMetadata: memoryapi.Metadata(requestID), Views: views})
}

func (s *Server) createMemoryGraphView(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service) {
	var request memoryapi.GraphViewSaveRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	view, err := service.CreateGraphView(ctx, memoryService.SaveGraphViewRequest{Name: request.Name, State: request.State, ExpectedRevision: request.ExpectedRevision})
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	w.Header().Set("Location", memoryapi.GraphViewPath(view.ID))
	s.writeMemoryJSON(w, http.StatusCreated, memoryapi.GraphViewResponse{ResponseMetadata: memoryapi.Metadata(requestID), View: view})
}

func (s *Server) updateMemoryGraphView(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, viewID string) {
	var request memoryapi.GraphViewSaveRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	view, err := service.UpdateGraphView(ctx, viewID, memoryService.SaveGraphViewRequest{Name: request.Name, State: request.State, ExpectedRevision: request.ExpectedRevision})
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.GraphViewResponse{ResponseMetadata: memoryapi.Metadata(requestID), View: view})
}

func (s *Server) deleteMemoryGraphView(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, viewID string) {
	var request memoryapi.GraphViewDeleteRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	if err := service.DeleteGraphView(ctx, viewID, request.ExpectedRevision); err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.GraphViewDeleteResponse{ResponseMetadata: memoryapi.Metadata(requestID), ID: viewID, Deleted: true})
}
