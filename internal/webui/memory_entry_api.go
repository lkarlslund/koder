package webui

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Server) handleMemoryEntries(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listMemoryEntries(w, r, requestID, ctx, service)
	case http.MethodPost:
		s.createMemoryEntry(w, r, requestID, ctx, service)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
	}
}

func (s *Server) listMemoryEntries(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service) {
	request, err := parseEntryListQuery(r)
	if err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	page, err := service.ListEntries(ctx, request)
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.EntryListResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), Entries: page.Entries,
		Page: memoryapi.Page{Limit: limit, Returned: len(page.Entries), NextCursor: page.NextCursor},
	})
}

func (s *Server) createMemoryEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service) {
	var request memoryapi.EntryCreateRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	result, err := service.CreateEntry(ctx, memoryService.CreateEntryRequest{
		ChunkID: request.ChunkID, Entry: entryCandidate(request.Entry), ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	w.Header().Set("Location", memoryapi.EntryPath(result.Entry.ID))
	s.writeMemoryEntry(w, requestID, http.StatusCreated, result.Entry, nil, &result.Classification)
}

func (s *Server) handleMemoryEntry(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, memoryapi.EntryCollectionPath+"/"), "/")
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
	entryID := memory.EntryID(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		switch parts[1] {
		case "evidence":
			s.getMemoryEntryEvidence(w, r, requestID, ctx, service, entryID)
		case "history":
			s.getMemoryHistory(w, r, requestID, ctx, service, memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entryID)})
		case "archive", "restore":
			s.changeMemoryEntryLifecycle(w, r, requestID, ctx, service, entryID, parts[1])
		case "supersede":
			s.supersedeMemoryEntry(w, r, requestID, ctx, service, entryID)
		case "verify":
			s.verifyMemoryEntry(w, r, requestID, ctx, service, entryID)
		default:
			s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory endpoint was not found.")
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getMemoryEntry(w, r, requestID, ctx, service, entryID)
	case http.MethodPut:
		s.updateMemoryEntry(w, r, requestID, ctx, service, entryID)
	case http.MethodDelete:
		s.deleteMemoryEntry(w, r, requestID, ctx, service, entryID)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
	}
}

func (s *Server) getMemoryEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, entryID memory.EntryID) {
	if !s.requireNoMemoryQuery(w, r, requestID) {
		return
	}
	reference := memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entryID)}
	record, err := service.Get(ctx, reference)
	if err != nil {
		s.writeMemoryReadError(w, requestID, err)
		return
	}
	if record.Entry == nil {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory object was not found.")
		return
	}
	metadata := memoryapi.Resource(reference, record.Entry.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
		if r.Header.Get("If-None-Match") == metadata.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	s.writeMemoryEntry(w, requestID, http.StatusOK, *record.Entry, nil, nil)
}

func (s *Server) updateMemoryEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, entryID memory.EntryID) {
	var request memoryapi.EntryUpdateRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	result, err := service.UpdateEntry(ctx, memoryService.UpdateEntryRequest{
		EntryID: entryID, ExpectedRevision: request.ExpectedRevision, Content: entryServiceContent(request.Entry),
		Reason: request.Reason, ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryEntry(w, requestID, http.StatusOK, result.Entry, nil, &result.Classification)
}

func (s *Server) changeMemoryEntryLifecycle(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, entryID memory.EntryID, action string) {
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
	serviceRequest := memoryService.EntryLifecycleRequest{EntryID: entryID, ExpectedRevision: request.ExpectedRevision, Reason: request.Reason}
	var result memoryService.EntryLifecycleResult
	var err error
	if action == "archive" {
		result, err = service.ArchiveEntry(ctx, serviceRequest)
	} else {
		result, err = service.RestoreEntry(ctx, serviceRequest)
	}
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryEntry(w, requestID, http.StatusOK, result.Entry, nil, nil)
}

func (s *Server) supersedeMemoryEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, entryID memory.EntryID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	var request memoryapi.EntrySupersedeRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	result, err := service.SupersedeEntry(ctx, memoryService.SupersedeEntryRequest{
		EntryID: entryID, ExpectedRevision: request.ExpectedRevision, ReplacementEntryID: request.ReplacementEntryID, Reason: request.Reason,
	})
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryEntry(w, requestID, http.StatusOK, result.Entry, &result.Replacement, nil)
}

func (s *Server) verifyMemoryEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, entryID memory.EntryID) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	var request memoryapi.EntryVerifyRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	result, err := service.VerifyEntry(ctx, memoryService.VerifyEntryRequest{
		EntryID: entryID, ExpectedRevision: request.ExpectedRevision, Status: request.Status,
		Method: request.Method, EvidenceIDs: request.EvidenceIDs, Reason: request.Reason,
	})
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryEntry(w, requestID, http.StatusOK, result.Entry, nil, nil)
}

func (s *Server) deleteMemoryEntry(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, entryID memory.EntryID) {
	var request memoryapi.EntryDeleteRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	if err := service.DeleteEntry(ctx, memoryService.DeleteEntryRequest{
		EntryID: entryID, ExpectedRevision: request.ExpectedRevision, Confirmed: request.Confirmed,
	}); err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.DeleteResponse{
		ResponseMetadata: memoryapi.Metadata(requestID),
		Object:           memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entryID)}, Deleted: true,
	})
}

func (s *Server) getMemoryEntryEvidence(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, entryID memory.EntryID) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	limit, cursor, err := parseLimitCursor(r)
	if err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	page, err := service.EntryEvidence(ctx, memoryService.EntryEvidenceRequest{EntryID: entryID, Limit: limit, Cursor: cursor})
	if err != nil {
		s.writeMemoryReadError(w, requestID, err)
		return
	}
	if limit <= 0 {
		limit = 50
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.EvidenceListResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), Evidence: page.Evidence,
		Page: memoryapi.Page{Limit: limit, Returned: len(page.Evidence), NextCursor: page.NextCursor},
	})
}

func (s *Server) getMemoryHistory(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, object memory.ObjectRef) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
		return
	}
	limit, cursor, err := parseLimitCursor(r)
	if err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	page, err := service.History(ctx, memoryStoreAPI.RevisionListRequest{Object: object, Limit: limit, Cursor: cursor})
	if err != nil {
		s.writeMemoryReadError(w, requestID, err)
		return
	}
	revisions := make([]memoryapi.Record, 0, len(page.Revisions))
	for _, record := range page.Revisions {
		revisions = append(revisions, memoryAPIRecord(record))
	}
	if limit <= 0 {
		limit = 50
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.HistoryResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), Object: object, Revisions: revisions,
		Page: memoryapi.Page{Limit: limit, Returned: len(revisions), NextCursor: page.NextCursor},
	})
}

func parseEntryListQuery(r *http.Request) (memoryStoreAPI.EntryListRequest, error) {
	query := r.URL.Query()
	allowed := map[string]bool{
		"chunk_id": true, "kind": true, "state": true, "scope": true, "scope_kind": true,
		"tag": true, "locale": true, "sort": true, "descending": true, "limit": true, "cursor": true,
	}
	for name := range query {
		if !allowed[name] {
			return memoryStoreAPI.EntryListRequest{}, fmt.Errorf("unsupported query parameter %q", name)
		}
	}
	request := memoryStoreAPI.EntryListRequest{
		Sort: memoryStoreAPI.EntrySort(strings.TrimSpace(query.Get("sort"))), Cursor: strings.TrimSpace(query.Get("cursor")),
	}
	var err error
	if value := strings.TrimSpace(query.Get("descending")); value != "" {
		request.Descending, err = strconv.ParseBool(value)
		if err != nil {
			return memoryStoreAPI.EntryListRequest{}, err
		}
	}
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		request.Limit, err = strconv.Atoi(value)
		if err != nil || request.Limit <= 0 {
			return memoryStoreAPI.EntryListRequest{}, fmt.Errorf("invalid limit")
		}
	}
	for _, value := range queryValues(query["chunk_id"]) {
		request.Filter.ChunkIDs = append(request.Filter.ChunkIDs, memory.ChunkID(value))
	}
	for _, value := range queryValues(query["kind"]) {
		kind, err := memory.EntryKindString(value)
		if err != nil || kind == memory.EntryKindUnspecified {
			return memoryStoreAPI.EntryListRequest{}, fmt.Errorf("invalid entry kind %q", value)
		}
		request.Filter.Kinds = append(request.Filter.Kinds, kind)
	}
	for _, value := range queryValues(query["state"]) {
		state, err := memory.EntryStateString(value)
		if err != nil || state == memory.EntryStateUnspecified {
			return memoryStoreAPI.EntryListRequest{}, fmt.Errorf("invalid entry state %q", value)
		}
		request.Filter.States = append(request.Filter.States, state)
	}
	for _, value := range queryValues(query["scope_kind"]) {
		kind, err := memory.ScopeKindString(value)
		if err != nil || kind == memory.ScopeKindUnspecified {
			return memoryStoreAPI.EntryListRequest{}, fmt.Errorf("invalid scope kind %q", value)
		}
		request.Filter.ScopeKinds = append(request.Filter.ScopeKinds, kind)
	}
	for _, value := range queryValues(query["scope"]) {
		scope, err := parseMemoryScope(value)
		if err != nil {
			return memoryStoreAPI.EntryListRequest{}, err
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

func memoryAPIRecord(record memoryStoreAPI.CanonicalRecord) memoryapi.Record {
	return memoryapi.Record{
		Kind: record.Kind, Chunk: record.Chunk, Entry: record.Entry, Link: record.Link, Evidence: record.Evidence,
	}
}

func entryCandidate(content memoryapi.EntryContent) memory.Entry {
	return memory.Entry{
		Kind: content.Kind, Title: content.Title, Summary: content.Summary, Body: content.Body,
		Aliases: content.Aliases, Tags: content.Tags, Scope: content.Scope, Applicability: content.Applicability,
		Risk: content.Risk, Confidence: content.Confidence, ValidFrom: content.ValidFrom, ValidUntil: content.ValidUntil,
		ObservedAt: content.ObservedAt, ReviewAfter: content.ReviewAfter, EvidenceIDs: content.EvidenceIDs,
		PersonalOrigin: content.PersonalOrigin,
	}
}

func entryServiceContent(content memoryapi.EntryContent) memoryService.EntryContent {
	return memoryService.EntryContent{
		Kind: content.Kind, Title: content.Title, Summary: content.Summary, Body: content.Body,
		Aliases: content.Aliases, Tags: content.Tags, Scope: content.Scope, Applicability: content.Applicability,
		Risk: content.Risk, Confidence: content.Confidence, ValidFrom: content.ValidFrom, ValidUntil: content.ValidUntil,
		ObservedAt: content.ObservedAt, ReviewAfter: content.ReviewAfter, EvidenceIDs: content.EvidenceIDs,
		PersonalOrigin: content.PersonalOrigin,
	}
}

func (s *Server) writeMemoryEntry(w http.ResponseWriter, requestID string, status int, entry memory.Entry, replacement *memory.Entry, classification *memory.ClassificationResult) {
	reference := memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entry.ID)}
	metadata := memoryapi.Resource(reference, entry.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
	}
	s.writeMemoryJSON(w, status, memoryapi.EntryResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), ResourceMetadata: metadata,
		Entry: entry, Replacement: replacement, Classification: classification,
	})
}
