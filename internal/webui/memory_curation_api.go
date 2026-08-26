package webui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	"github.com/lkarlslund/koder/internal/memory/curation"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
)

func (s *Server) handleMemoryCurationCandidates(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	manager := s.controller.MemoryCuration()
	if manager == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory curation is temporarily unavailable.")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	statuses, limit, err := parseMemoryCurationListQuery(r)
	if err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	candidates, err := manager.List(ctx, statuses, limit)
	if err != nil {
		s.writeMemoryCurationError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.CurationCandidateListResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), Candidates: candidates,
		Page: memoryapi.Page{Limit: limit, Returned: len(candidates)},
	})
}

func (s *Server) handleMemoryCurationCandidate(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	manager := s.controller.MemoryCuration()
	if manager == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory curation is temporarily unavailable.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	candidateID, action, valid := parseMemoryCurationActionPath(r.URL.Path)
	if !valid || r.URL.RawQuery != "" {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory endpoint was not found.")
		return
	}
	var request memoryapi.CurationCandidateDecisionRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil || request.ExpectedVersion == 0 {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
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
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory endpoint was not found.")
		return
	}
	if err != nil {
		s.writeMemoryCurationError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.CurationCandidateResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), Candidate: candidate,
	})
}

func parseMemoryCurationListQuery(r *http.Request) ([]curation.CandidateStatus, int, error) {
	query := r.URL.Query()
	for key := range query {
		if key != "status" && key != "limit" {
			return nil, 0, memory.ErrInvalidRecord
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
			return nil, 0, memory.ErrInvalidRecord
		}
	}
	limit := 100
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return nil, 0, memory.ErrInvalidRecord
		}
		limit = value
	}
	return statuses, limit, nil
}

func parseMemoryCurationActionPath(path string) (curation.CandidateID, string, bool) {
	remainder := strings.TrimPrefix(path, memoryapi.CurationCandidatePath+"/")
	parts := strings.Split(remainder, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	if err := (memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: parts[0]}).Validate(); err != nil {
		return "", "", false
	}
	return curation.CandidateID(parts[0]), parts[1], true
}

func (s *Server) writeMemoryCurationError(w http.ResponseWriter, requestID string, err error) {
	switch {
	case errors.Is(err, curation.ErrNotFound):
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The curation candidate was not found.")
	case errors.Is(err, curation.ErrCandidateConflict):
		s.writeMemoryError(w, requestID, http.StatusConflict, memoryService.ErrorCodeConflict, "The curation candidate changed; refresh and try again.")
	case errors.Is(err, curation.ErrUnavailable):
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory curation is temporarily unavailable.")
	default:
		s.writeMemoryServiceError(w, requestID, err)
	}
}
