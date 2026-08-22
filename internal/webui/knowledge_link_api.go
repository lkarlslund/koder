package webui

import (
	"context"
	"net/http"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

func (s *Server) handleKnowledgeLinks(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	var request knowledgeapi.LinkCreateRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	result, err := service.CreateLink(ctx, knowledgeService.CreateLinkRequest{
		Link: linkCandidate(request.Link), ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	w.Header().Set("Location", knowledgeapi.LinkPath(result.Link.ID))
	s.writeKnowledgeLink(w, requestID, http.StatusCreated, result.Link, &result.Classification)
}

func (s *Server) handleKnowledgeLink(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, knowledgeapi.LinkCollectionPath+"/"), "/")
	parts := strings.Split(path, "/")
	if path == "" || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge object was not found.")
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}
	linkID := knowledge.LinkID(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		switch parts[1] {
		case "history":
			s.getKnowledgeHistory(w, r, requestID, ctx, service, knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: string(linkID)})
		case "unlink", "restore":
			s.changeKnowledgeLinkLifecycle(w, r, requestID, ctx, service, linkID, parts[1])
		default:
			s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge endpoint was not found.")
		}
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	s.getKnowledgeLink(w, r, requestID, ctx, service, linkID)
}

func (s *Server) getKnowledgeLink(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, linkID knowledge.LinkID) {
	if !s.requireNoKnowledgeQuery(w, r, requestID) {
		return
	}
	reference := knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: string(linkID)}
	record, err := service.Get(ctx, reference)
	if err != nil {
		s.writeKnowledgeReadError(w, requestID, err)
		return
	}
	if record.Link == nil {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge object was not found.")
		return
	}
	metadata := knowledgeapi.Resource(reference, record.Link.Revision)
	if metadata.ETag != "" && r.Header.Get("If-None-Match") == metadata.ETag {
		w.Header().Set("ETag", metadata.ETag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	s.writeKnowledgeLink(w, requestID, http.StatusOK, *record.Link, nil)
}

func (s *Server) changeKnowledgeLinkLifecycle(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, linkID knowledge.LinkID, action string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	var request knowledgeapi.LifecycleRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	serviceRequest := knowledgeService.LinkLifecycleRequest{LinkID: linkID, ExpectedRevision: request.ExpectedRevision, Reason: request.Reason}
	var result knowledgeService.LinkLifecycleResult
	var err error
	if action == "unlink" {
		result, err = service.Unlink(ctx, serviceRequest)
	} else {
		result, err = service.RestoreLink(ctx, serviceRequest)
	}
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeLink(w, requestID, http.StatusOK, result.Link, nil)
}

func linkCandidate(content knowledgeapi.LinkContent) knowledge.Link {
	return knowledge.Link{
		Source: content.Source, Target: content.Target, Kind: content.Kind,
		Label: content.Label, Notes: content.Notes, EvidenceIDs: content.EvidenceIDs,
	}
}

func (s *Server) writeKnowledgeLink(w http.ResponseWriter, requestID string, status int, link knowledge.Link, classification *knowledge.ClassificationResult) {
	reference := knowledge.ObjectRef{Kind: knowledge.ObjectKindLink, ID: string(link.ID)}
	metadata := knowledgeapi.Resource(reference, link.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
	}
	s.writeKnowledgeJSON(w, status, knowledgeapi.LinkResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), ResourceMetadata: metadata,
		Link: link, Classification: classification,
	})
}
