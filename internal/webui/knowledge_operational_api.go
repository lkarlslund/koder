package webui

import (
	"net/http"
	"strings"

	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

func (s *Server) handleKnowledgeOperationalStatus(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	if !s.requireNoKnowledgeQuery(w, r, requestID) {
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}
	status, err := service.OperationalStatus(ctx)
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.OperationalStatusResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Status: status,
	})
}

func (s *Server) handleKnowledgeIndexRebuild(w http.ResponseWriter, r *http.Request) {
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
	var request knowledgeapi.IndexRebuildRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil || !validKnowledgeIndexName(request.Index) {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	result, err := service.StartIndexRebuild(ctx)
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusAccepted, knowledgeapi.IndexRebuildResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Result: result,
	})
}

func validKnowledgeIndexName(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == "lexical"
}
