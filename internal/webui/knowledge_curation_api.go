package webui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	"github.com/lkarlslund/koder/internal/knowledge/curation"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

func (s *Server) handleKnowledgeCurationCandidates(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	manager := s.controller.KnowledgeCuration()
	if manager == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge curation is temporarily unavailable.")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	statuses, limit, err := parseKnowledgeCurationListQuery(r)
	if err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	candidates, err := manager.List(ctx, statuses, limit)
	if err != nil {
		s.writeKnowledgeCurationError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.CurationCandidateListResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Candidates: candidates,
		Page: knowledgeapi.Page{Limit: limit, Returned: len(candidates)},
	})
}

func (s *Server) handleKnowledgeCurationCandidate(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	manager := s.controller.KnowledgeCuration()
	if manager == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge curation is temporarily unavailable.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	candidateID, action, valid := parseKnowledgeCurationActionPath(r.URL.Path)
	if !valid || r.URL.RawQuery != "" {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge endpoint was not found.")
		return
	}
	var request knowledgeapi.CurationCandidateDecisionRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil || request.ExpectedVersion == 0 {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	var candidate curation.StoredCandidate
	var err error
	switch action {
	case "accept":
		candidate, err = manager.Accept(ctx, candidateID, request.ExpectedVersion)
	case "reject":
		candidate, err = manager.Reject(ctx, candidateID, request.ExpectedVersion, request.Reason)
	case "undo":
		candidate, err = manager.Undo(ctx, candidateID, request.ExpectedVersion)
	default:
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge endpoint was not found.")
		return
	}
	if err != nil {
		s.writeKnowledgeCurationError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.CurationCandidateResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Candidate: candidate,
	})
}

func parseKnowledgeCurationListQuery(r *http.Request) ([]curation.CandidateStatus, int, error) {
	query := r.URL.Query()
	for key := range query {
		if key != "status" && key != "limit" {
			return nil, 0, knowledge.ErrInvalidRecord
		}
	}
	statuses := make([]curation.CandidateStatus, 0, len(query["status"]))
	for _, raw := range query["status"] {
		status := curation.CandidateStatus(strings.TrimSpace(raw))
		switch status {
		case curation.CandidateStatusPendingAutomatic, curation.CandidateStatusPendingReview,
			curation.CandidateStatusApplied, curation.CandidateStatusRejected, curation.CandidateStatusUndone:
			statuses = append(statuses, status)
		default:
			return nil, 0, knowledge.ErrInvalidRecord
		}
	}
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return nil, 0, knowledge.ErrInvalidRecord
		}
		limit = value
	}
	return statuses, limit, nil
}

func parseKnowledgeCurationActionPath(path string) (curation.CandidateID, string, bool) {
	remainder := strings.TrimPrefix(path, knowledgeapi.CurationCandidatePath+"/")
	parts := strings.Split(remainder, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	if err := (knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: parts[0]}).Validate(); err != nil {
		return "", "", false
	}
	return curation.CandidateID(parts[0]), parts[1], true
}

func (s *Server) writeKnowledgeCurationError(w http.ResponseWriter, requestID string, err error) {
	switch {
	case errors.Is(err, curation.ErrNotFound):
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The curation candidate was not found.")
	case errors.Is(err, curation.ErrCandidateConflict):
		s.writeKnowledgeError(w, requestID, http.StatusConflict, knowledgeService.ErrorCodeConflict, "The curation candidate changed; refresh and try again.")
	case errors.Is(err, curation.ErrUnavailable):
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge curation is temporarily unavailable.")
	default:
		s.writeKnowledgeServiceError(w, requestID, err)
	}
}
