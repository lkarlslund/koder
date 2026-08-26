package webui

import (
	"context"
	"net/http"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
)

func (s *Server) handleMemoryLinks(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	var request memoryapi.LinkCreateRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	result, err := service.CreateLink(ctx, memoryService.CreateLinkRequest{
		Link: linkCandidate(request.Link), ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	w.Header().Set("Location", memoryapi.LinkPath(result.Link.ID))
	s.writeMemoryLink(w, requestID, http.StatusCreated, result.Link, &result.Classification)
}

func (s *Server) handleMemoryLink(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, memoryapi.LinkCollectionPath+"/"), "/")
	parts := strings.Split(path, "/")
	if path == "" || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory object was not found.")
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	linkID := memory.LinkID(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		switch parts[1] {
		case "history":
			s.getMemoryHistory(w, r, requestID, ctx, service, memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(linkID)})
		case "unlink", "restore":
			s.changeMemoryLinkLifecycle(w, r, requestID, ctx, service, linkID, parts[1])
		default:
			s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory endpoint was not found.")
		}
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	s.getMemoryLink(w, r, requestID, ctx, service, linkID)
}

func (s *Server) getMemoryLink(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, linkID memory.LinkID) {
	if !s.requireNoMemoryQuery(w, r, requestID) {
		return
	}
	reference := memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(linkID)}
	record, err := service.Get(ctx, reference)
	if err != nil {
		s.writeMemoryReadError(w, requestID, err)
		return
	}
	if record.Link == nil {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory object was not found.")
		return
	}
	metadata := memoryapi.Resource(reference, record.Link.Revision)
	if metadata.ETag != "" && r.Header.Get("If-None-Match") == metadata.ETag {
		w.Header().Set("ETag", metadata.ETag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	s.writeMemoryLink(w, requestID, http.StatusOK, *record.Link, nil)
}

func (s *Server) changeMemoryLinkLifecycle(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, linkID memory.LinkID, action string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	var request memoryapi.LifecycleRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	serviceRequest := memoryService.LinkLifecycleRequest{LinkID: linkID, ExpectedRevision: request.ExpectedRevision, Reason: request.Reason}
	var result memoryService.LinkLifecycleResult
	var err error
	if action == "unlink" {
		result, err = service.Unlink(ctx, serviceRequest)
	} else {
		result, err = service.RestoreLink(ctx, serviceRequest)
	}
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryLink(w, requestID, http.StatusOK, result.Link, nil)
}

func linkCandidate(content memoryapi.LinkContent) memory.Link {
	return memory.Link{
		Source: content.Source, Target: content.Target, Kind: content.Kind,
		Label: content.Label, Notes: content.Notes, EvidenceIDs: content.EvidenceIDs,
	}
}

func (s *Server) writeMemoryLink(w http.ResponseWriter, requestID string, status int, link memory.Link, classification *memory.ClassificationResult) {
	reference := memory.ObjectRef{Kind: memory.ObjectKindLink, ID: string(link.ID)}
	metadata := memoryapi.Resource(reference, link.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
	}
	s.writeMemoryJSON(w, status, memoryapi.LinkResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), ResourceMetadata: metadata,
		Link: link, Classification: classification,
	})
}
