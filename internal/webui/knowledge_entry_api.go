package webui

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Server) handleKnowledgeEntries(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listKnowledgeEntries(w, r, requestID, ctx, service)
	case http.MethodPost:
		s.createKnowledgeEntry(w, r, requestID, ctx, service)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
	}
}

func (s *Server) listKnowledgeEntries(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service) {
	request, err := parseEntryListQuery(r)
	if err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	page, err := service.ListEntries(ctx, request)
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.EntryListResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Entries: page.Entries,
		Page: knowledgeapi.Page{Limit: limit, Returned: len(page.Entries), NextCursor: page.NextCursor},
	})
}

func (s *Server) createKnowledgeEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service) {
	var request knowledgeapi.EntryCreateRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	result, err := service.CreateEntry(ctx, knowledgeService.CreateEntryRequest{
		ChunkID: request.ChunkID, Entry: entryCandidate(request.Entry), ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	w.Header().Set("Location", knowledgeapi.EntryPath(result.Entry.ID))
	s.writeKnowledgeEntry(w, requestID, http.StatusCreated, result.Entry, nil, &result.Classification)
}

func (s *Server) handleKnowledgeEntry(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, knowledgeapi.EntryCollectionPath+"/"), "/")
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
	entryID := knowledge.EntryID(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		switch parts[1] {
		case "evidence":
			s.getKnowledgeEntryEvidence(w, r, requestID, ctx, service, entryID)
		case "history":
			s.getKnowledgeHistory(w, r, requestID, ctx, service, knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entryID)})
		case "archive", "restore":
			s.changeKnowledgeEntryLifecycle(w, r, requestID, ctx, service, entryID, parts[1])
		case "supersede":
			s.supersedeKnowledgeEntry(w, r, requestID, ctx, service, entryID)
		case "verify":
			s.verifyKnowledgeEntry(w, r, requestID, ctx, service, entryID)
		default:
			s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge endpoint was not found.")
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getKnowledgeEntry(w, r, requestID, ctx, service, entryID)
	case http.MethodPut:
		s.updateKnowledgeEntry(w, r, requestID, ctx, service, entryID)
	case http.MethodDelete:
		s.deleteKnowledgeEntry(w, r, requestID, ctx, service, entryID)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
	}
}

func (s *Server) getKnowledgeEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, entryID knowledge.EntryID) {
	reference := knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entryID)}
	record, err := service.Get(ctx, reference)
	if err != nil {
		s.writeKnowledgeReadError(w, requestID, err)
		return
	}
	if record.Entry == nil {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge object was not found.")
		return
	}
	metadata := knowledgeapi.Resource(reference, record.Entry.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
		if r.Header.Get("If-None-Match") == metadata.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	s.writeKnowledgeEntry(w, requestID, http.StatusOK, *record.Entry, nil, nil)
}

func (s *Server) updateKnowledgeEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, entryID knowledge.EntryID) {
	var request knowledgeapi.EntryUpdateRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	result, err := service.UpdateEntry(ctx, knowledgeService.UpdateEntryRequest{
		EntryID: entryID, ExpectedRevision: request.ExpectedRevision, Content: entryServiceContent(request.Entry),
		Reason: request.Reason, ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeEntry(w, requestID, http.StatusOK, result.Entry, nil, &result.Classification)
}

func (s *Server) changeKnowledgeEntryLifecycle(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, entryID knowledge.EntryID, action string) {
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
	serviceRequest := knowledgeService.EntryLifecycleRequest{EntryID: entryID, ExpectedRevision: request.ExpectedRevision, Reason: request.Reason}
	var result knowledgeService.EntryLifecycleResult
	var err error
	if action == "archive" {
		result, err = service.ArchiveEntry(ctx, serviceRequest)
	} else {
		result, err = service.RestoreEntry(ctx, serviceRequest)
	}
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeEntry(w, requestID, http.StatusOK, result.Entry, nil, nil)
}

func (s *Server) supersedeKnowledgeEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, entryID knowledge.EntryID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	var request knowledgeapi.EntrySupersedeRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	result, err := service.SupersedeEntry(ctx, knowledgeService.SupersedeEntryRequest{
		EntryID: entryID, ExpectedRevision: request.ExpectedRevision, ReplacementEntryID: request.ReplacementEntryID, Reason: request.Reason,
	})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeEntry(w, requestID, http.StatusOK, result.Entry, &result.Replacement, nil)
}

func (s *Server) verifyKnowledgeEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, entryID knowledge.EntryID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	var request knowledgeapi.EntryVerifyRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	result, err := service.VerifyEntry(ctx, knowledgeService.VerifyEntryRequest{
		EntryID: entryID, ExpectedRevision: request.ExpectedRevision, Status: request.Status,
		Method: request.Method, EvidenceIDs: request.EvidenceIDs, Reason: request.Reason,
	})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeEntry(w, requestID, http.StatusOK, result.Entry, nil, nil)
}

func (s *Server) deleteKnowledgeEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, entryID knowledge.EntryID) {
	var request knowledgeapi.EntryDeleteRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	if err := service.DeleteEntry(ctx, knowledgeService.DeleteEntryRequest{
		EntryID: entryID, ExpectedRevision: request.ExpectedRevision, Confirmed: request.Confirmed,
	}); err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.DeleteResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID),
		Object:           knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entryID)}, Deleted: true,
	})
}

func (s *Server) getKnowledgeEntryEvidence(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, entryID knowledge.EntryID) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	limit, cursor, err := parseLimitCursor(r)
	if err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	page, err := service.EntryEvidence(ctx, knowledgeService.EntryEvidenceRequest{EntryID: entryID, Limit: limit, Cursor: cursor})
	if err != nil {
		s.writeKnowledgeReadError(w, requestID, err)
		return
	}
	if limit <= 0 {
		limit = 50
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.EvidenceListResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Evidence: page.Evidence,
		Page: knowledgeapi.Page{Limit: limit, Returned: len(page.Evidence), NextCursor: page.NextCursor},
	})
}

func (s *Server) getKnowledgeHistory(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, object knowledge.ObjectRef) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
		return
	}
	limit, cursor, err := parseLimitCursor(r)
	if err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	page, err := service.History(ctx, knowledgeStore.RevisionListRequest{Object: object, Limit: limit, Cursor: cursor})
	if err != nil {
		s.writeKnowledgeReadError(w, requestID, err)
		return
	}
	revisions := make([]knowledgeapi.Record, 0, len(page.Revisions))
	for _, record := range page.Revisions {
		revisions = append(revisions, knowledgeAPIRecord(record))
	}
	if limit <= 0 {
		limit = 50
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.HistoryResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), Object: object, Revisions: revisions,
		Page: knowledgeapi.Page{Limit: limit, Returned: len(revisions), NextCursor: page.NextCursor},
	})
}

func parseEntryListQuery(r *http.Request) (knowledgeStore.EntryListRequest, error) {
	query := r.URL.Query()
	allowed := map[string]bool{
		"chunk_id": true, "kind": true, "state": true, "scope": true, "scope_kind": true,
		"tag": true, "locale": true, "sort": true, "descending": true, "limit": true, "cursor": true,
	}
	for name := range query {
		if !allowed[name] {
			return knowledgeStore.EntryListRequest{}, fmt.Errorf("unsupported query parameter %q", name)
		}
	}
	request := knowledgeStore.EntryListRequest{
		Sort: knowledgeStore.EntrySort(strings.TrimSpace(query.Get("sort"))), Cursor: strings.TrimSpace(query.Get("cursor")),
	}
	var err error
	if value := strings.TrimSpace(query.Get("descending")); value != "" {
		request.Descending, err = strconv.ParseBool(value)
		if err != nil {
			return knowledgeStore.EntryListRequest{}, err
		}
	}
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		request.Limit, err = strconv.Atoi(value)
		if err != nil || request.Limit <= 0 {
			return knowledgeStore.EntryListRequest{}, fmt.Errorf("invalid limit")
		}
	}
	for _, value := range queryValues(query["chunk_id"]) {
		request.Filter.ChunkIDs = append(request.Filter.ChunkIDs, knowledge.ChunkID(value))
	}
	for _, value := range queryValues(query["kind"]) {
		kind, err := knowledge.EntryKindString(value)
		if err != nil || kind == knowledge.EntryKindUnspecified {
			return knowledgeStore.EntryListRequest{}, fmt.Errorf("invalid entry kind %q", value)
		}
		request.Filter.Kinds = append(request.Filter.Kinds, kind)
	}
	for _, value := range queryValues(query["state"]) {
		state, err := knowledge.EntryStateString(value)
		if err != nil || state == knowledge.EntryStateUnspecified {
			return knowledgeStore.EntryListRequest{}, fmt.Errorf("invalid entry state %q", value)
		}
		request.Filter.States = append(request.Filter.States, state)
	}
	for _, value := range queryValues(query["scope_kind"]) {
		kind, err := knowledge.ScopeKindString(value)
		if err != nil || kind == knowledge.ScopeKindUnspecified {
			return knowledgeStore.EntryListRequest{}, fmt.Errorf("invalid scope kind %q", value)
		}
		request.Filter.ScopeKinds = append(request.Filter.ScopeKinds, kind)
	}
	for _, value := range queryValues(query["scope"]) {
		scope, err := parseKnowledgeScope(value)
		if err != nil {
			return knowledgeStore.EntryListRequest{}, err
		}
		request.Filter.Scopes = append(request.Filter.Scopes, scope)
	}
	request.Filter.Tags = queryValues(query["tag"])
	request.Filter.Locales = queryValues(query["locale"])
	return request, nil
}

func parseLimitCursor(r *http.Request) (int, string, error) {
	query := r.URL.Query()
	for name := range query {
		if name != "limit" && name != "cursor" {
			return 0, "", fmt.Errorf("unsupported query parameter %q", name)
		}
	}
	limit := 0
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return 0, "", fmt.Errorf("invalid limit")
		}
		limit = parsed
	}
	return limit, strings.TrimSpace(query.Get("cursor")), nil
}

func knowledgeAPIRecord(record knowledgeStore.CanonicalRecord) knowledgeapi.Record {
	return knowledgeapi.Record{
		Kind: record.Kind, Chunk: record.Chunk, Entry: record.Entry, Link: record.Link, Evidence: record.Evidence,
	}
}

func entryCandidate(content knowledgeapi.EntryContent) knowledge.Entry {
	return knowledge.Entry{
		Kind: content.Kind, Title: content.Title, Summary: content.Summary, Body: content.Body,
		Aliases: content.Aliases, Tags: content.Tags, Scope: content.Scope, Applicability: content.Applicability,
		Risk: content.Risk, Confidence: content.Confidence, ValidFrom: content.ValidFrom, ValidUntil: content.ValidUntil,
		ObservedAt: content.ObservedAt, ReviewAfter: content.ReviewAfter, EvidenceIDs: content.EvidenceIDs,
		PersonalOrigin: content.PersonalOrigin,
	}
}

func entryServiceContent(content knowledgeapi.EntryContent) knowledgeService.EntryContent {
	return knowledgeService.EntryContent{
		Kind: content.Kind, Title: content.Title, Summary: content.Summary, Body: content.Body,
		Aliases: content.Aliases, Tags: content.Tags, Scope: content.Scope, Applicability: content.Applicability,
		Risk: content.Risk, Confidence: content.Confidence, ValidFrom: content.ValidFrom, ValidUntil: content.ValidUntil,
		ObservedAt: content.ObservedAt, ReviewAfter: content.ReviewAfter, EvidenceIDs: content.EvidenceIDs,
		PersonalOrigin: content.PersonalOrigin,
	}
}

func (s *Server) writeKnowledgeEntry(w http.ResponseWriter, requestID string, status int, entry knowledge.Entry, replacement *knowledge.Entry, classification *knowledge.ClassificationResult) {
	reference := knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.ID)}
	metadata := knowledgeapi.Resource(reference, entry.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
	}
	s.writeKnowledgeJSON(w, status, knowledgeapi.EntryResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), ResourceMetadata: metadata,
		Entry: entry, Replacement: replacement, Classification: classification,
	})
}
