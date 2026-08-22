package webui

import (
	"context"
	"net/http"
	"strings"

	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

func (s *Server) handleKnowledgeGraphViews(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}
	if !s.requireNoKnowledgeQuery(w, r, requestID) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listKnowledgeGraphViews(w, requestID, ctx, service)
	case http.MethodPost:
		s.createKnowledgeGraphView(w, r, requestID, ctx, service)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
	}
}

func (s *Server) handleKnowledgeGraphView(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	viewID := strings.Trim(strings.TrimPrefix(r.URL.Path, knowledgeapi.GraphViewCollectionPath+"/"), "/")
	if viewID == "" || strings.Contains(viewID, "/") {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge object was not found.")
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}
	if !s.requireNoKnowledgeQuery(w, r, requestID) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		view, err := service.GetGraphView(ctx, viewID)
		if err != nil {
			s.writeKnowledgeServiceError(w, requestID, err)
			return
		}
		s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.GraphViewResponse{ResponseMetadata: knowledgeapi.Metadata(requestID), View: view})
	case http.MethodPut:
		s.updateKnowledgeGraphView(w, r, requestID, ctx, service, viewID)
	case http.MethodDelete:
		s.deleteKnowledgeGraphView(w, r, requestID, ctx, service, viewID)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
	}
}

func (s *Server) listKnowledgeGraphViews(w http.ResponseWriter, requestID string, ctx context.Context, service *knowledgeService.Service) {
	views, err := service.ListGraphViews(ctx)
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.GraphViewListResponse{ResponseMetadata: knowledgeapi.Metadata(requestID), Views: views})
}

func (s *Server) createKnowledgeGraphView(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service) {
	var request knowledgeapi.GraphViewSaveRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	view, err := service.CreateGraphView(ctx, knowledgeService.SaveGraphViewRequest{Name: request.Name, State: request.State, ExpectedRevision: request.ExpectedRevision})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	w.Header().Set("Location", knowledgeapi.GraphViewPath(view.ID))
	s.writeKnowledgeJSON(w, http.StatusCreated, knowledgeapi.GraphViewResponse{ResponseMetadata: knowledgeapi.Metadata(requestID), View: view})
}

func (s *Server) updateKnowledgeGraphView(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, viewID string) {
	var request knowledgeapi.GraphViewSaveRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	view, err := service.UpdateGraphView(ctx, viewID, knowledgeService.SaveGraphViewRequest{Name: request.Name, State: request.State, ExpectedRevision: request.ExpectedRevision})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.GraphViewResponse{ResponseMetadata: knowledgeapi.Metadata(requestID), View: view})
}

func (s *Server) deleteKnowledgeGraphView(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, viewID string) {
	var request knowledgeapi.GraphViewDeleteRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	if err := service.DeleteGraphView(ctx, viewID, request.ExpectedRevision); err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.GraphViewDeleteResponse{ResponseMetadata: knowledgeapi.Metadata(requestID), ID: viewID, Deleted: true})
}
