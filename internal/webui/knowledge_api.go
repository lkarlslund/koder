package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/deviceauth"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Server) registerKnowledgeAPI(mux *http.ServeMux) {
	mux.HandleFunc(knowledgeapi.ChunkCollectionPath, s.handleKnowledgeChunks)
	mux.HandleFunc(knowledgeapi.ChunkCollectionPath+"/", s.handleKnowledgeChunk)
}

func (s *Server) handleKnowledgeChunks(w http.ResponseWriter, r *http.Request) {
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
		s.listKnowledgeChunks(w, r, requestID, ctx, service)
	case http.MethodPost:
		s.createKnowledgeChunk(w, r, requestID, ctx, service)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
	}
}

func (s *Server) listKnowledgeChunks(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service) {
	request, err := parseChunkListQuery(r)
	if err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	page, err := service.ListChunks(ctx, request)
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	s.writeKnowledgeJSON(w, http.StatusOK, knowledgeapi.ChunkListResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID),
		Chunks:           page.Chunks,
		Page: knowledgeapi.Page{
			Limit: limit, Returned: len(page.Chunks), NextCursor: page.NextCursor,
		},
	})
}

func (s *Server) createKnowledgeChunk(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service) {
	var request knowledgeapi.ChunkCreateRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	result, err := service.CreateChunk(ctx, knowledgeService.CreateChunkRequest{
		Chunk:          chunkCandidate(request.Chunk),
		ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	w.Header().Set("Location", knowledgeapi.ChunkPath(result.Chunk.ID))
	s.writeKnowledgeChunk(w, requestID, http.StatusCreated, result.Chunk, &result.Classification)
}

func (s *Server) handleKnowledgeChunk(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateKnowledgeRequest(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, knowledgeapi.ChunkCollectionPath+"/"), "/")
	parts := strings.Split(path, "/")
	if path == "" || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge object was not found.")
		return
	}
	chunkID := strings.TrimSpace(parts[0])
	service := s.controller.KnowledgeService()
	if service == nil {
		s.writeKnowledgeError(w, requestID, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable, "Knowledge is temporarily unavailable.")
		return
	}
	if len(parts) == 2 {
		s.changeKnowledgeChunkLifecycle(w, r, requestID, ctx, service, knowledge.ChunkID(chunkID), parts[1])
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getKnowledgeChunk(w, r, requestID, ctx, service, chunkID)
	case http.MethodPut:
		s.updateKnowledgeChunk(w, r, requestID, ctx, service, knowledge.ChunkID(chunkID))
	case http.MethodDelete:
		s.deleteKnowledgeChunk(w, r, requestID, ctx, service, knowledge.ChunkID(chunkID))
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		s.writeKnowledgeError(w, requestID, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid, "This Knowledge endpoint does not support that method.")
	}
}

func (s *Server) getKnowledgeChunk(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, chunkID string) {
	reference := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: chunkID}
	record, err := service.Get(ctx, reference)
	if err != nil {
		// A denied object is deliberately indistinguishable from an absent one.
		if errors.Is(err, knowledgeService.ErrChunkPolicyDenied) {
			s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge object was not found.")
			return
		}
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	if record.Chunk == nil {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge object was not found.")
		return
	}
	metadata := knowledgeapi.Resource(reference, record.Chunk.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
		if r.Header.Get("If-None-Match") == metadata.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	s.writeKnowledgeChunk(w, requestID, http.StatusOK, *record.Chunk, nil)
}

func (s *Server) updateKnowledgeChunk(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, chunkID knowledge.ChunkID) {
	var request knowledgeapi.ChunkUpdateRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	result, err := service.UpdateChunk(ctx, knowledgeService.UpdateChunkRequest{
		ChunkID: chunkID, ExpectedRevision: request.ExpectedRevision,
		Content: chunkServiceContent(request.Chunk), Reason: request.Reason, ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeChunk(w, requestID, http.StatusOK, result.Chunk, &result.Classification)
}

func (s *Server) changeKnowledgeChunkLifecycle(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, chunkID knowledge.ChunkID, action string) {
	if action != "archive" && action != "restore" {
		s.writeKnowledgeError(w, requestID, http.StatusNotFound, knowledgeService.ErrorCodeNotFound, "The Knowledge endpoint was not found.")
		return
	}
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
	serviceRequest := knowledgeService.ChunkLifecycleRequest{
		ChunkID: chunkID, ExpectedRevision: request.ExpectedRevision, Reason: request.Reason,
	}
	var result knowledgeService.ChunkLifecycleResult
	var err error
	if action == "archive" {
		result, err = service.ArchiveChunk(ctx, serviceRequest)
	} else {
		result, err = service.RestoreChunk(ctx, serviceRequest)
	}
	if err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeChunk(w, requestID, http.StatusOK, result.Chunk, nil)
}

func (s *Server) deleteKnowledgeChunk(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *knowledgeService.Service, chunkID knowledge.ChunkID) {
	var request knowledgeapi.DeleteRequest
	if err := decodeKnowledgeJSON(w, r, &request); err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid, "The Knowledge request is invalid.")
		return
	}
	serviceRequest := knowledgeService.DeleteChunkRequest{
		ChunkID: chunkID, ExpectedRevision: request.ExpectedRevision, Confirmed: request.Confirmed,
	}
	response := knowledgeapi.DeleteResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID),
		Object:           knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(chunkID)},
		Deleted:          true,
		Cascade:          request.Cascade,
	}
	if request.Cascade {
		result, err := service.CascadeDeleteChunk(ctx, serviceRequest)
		if err != nil {
			s.writeKnowledgeServiceError(w, requestID, err)
			return
		}
		response.DeletedEntryIDs = result.DeletedEntryIDs
		response.DeletedLinkIDs = result.DeletedLinkIDs
		response.DeletedEvidence = result.DeletedEvidenceIDs
		response.UpdatedChunkIDs = result.UpdatedDependentChunkIDs
	} else if err := service.DeleteChunk(ctx, serviceRequest); err != nil {
		s.writeKnowledgeServiceError(w, requestID, err)
		return
	}
	s.writeKnowledgeJSON(w, http.StatusOK, response)
}

func (s *Server) authenticateKnowledgeRequest(w http.ResponseWriter, r *http.Request) (string, context.Context, bool) {
	requestID := string(id.New())
	w.Header().Set("X-Koder-Request-ID", requestID)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	token, ok := bearerCredential(r.Header.Get("Authorization"))
	if !ok || s.devices == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Koder Knowledge"`)
		s.writeKnowledgeError(w, requestID, http.StatusUnauthorized, knowledgeService.ErrorCodeForbidden, "Knowledge authentication is required.")
		return requestID, nil, false
	}
	device, ok := s.devices.Authenticate(token, knowledgeDeviceInfo(r.Header))
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Koder Knowledge"`)
		s.writeKnowledgeError(w, requestID, http.StatusUnauthorized, knowledgeService.ErrorCodeForbidden, "Knowledge authentication is required.")
		return requestID, nil, false
	}
	ctx, err := knowledgeService.WithActor(r.Context(), knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "device:" + device.ID})
	if err != nil {
		s.writeKnowledgeError(w, requestID, http.StatusInternalServerError, knowledgeService.ErrorCodeInternal, "Knowledge could not complete the operation.")
		return requestID, nil, false
	}
	return requestID, ctx, true
}

func bearerCredential(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	token = strings.TrimSpace(token)
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != "" && !strings.ContainsAny(token, " \t\r\n")
}

func knowledgeDeviceInfo(header http.Header) deviceauth.DeviceInfo {
	return deviceauth.DeviceInfo{
		InstallationID: header.Get("X-Koder-Device-ID"),
		Name:           header.Get("X-Koder-Device-Name"),
		Manufacturer:   header.Get("X-Koder-Device-Manufacturer"),
		Model:          header.Get("X-Koder-Device-Model"),
		AndroidVersion: header.Get("X-Koder-Android-Version"),
		AppVersion:     header.Get("X-Koder-App-Version"),
		AppID:          header.Get("X-Koder-App-ID"),
	}
}

func parseChunkListQuery(r *http.Request) (knowledgeStore.ChunkListRequest, error) {
	query := r.URL.Query()
	allowed := map[string]bool{
		"kind": true, "state": true, "scope": true, "scope_kind": true, "tag": true,
		"locale": true, "sort": true, "descending": true, "limit": true, "cursor": true,
	}
	for name := range query {
		if !allowed[name] {
			return knowledgeStore.ChunkListRequest{}, fmt.Errorf("unsupported query parameter %q", name)
		}
	}
	request := knowledgeStore.ChunkListRequest{
		Sort:   knowledgeStore.ChunkSort(strings.TrimSpace(query.Get("sort"))),
		Cursor: strings.TrimSpace(query.Get("cursor")),
		Filter: knowledgeStore.ChunkFilter{Locale: strings.TrimSpace(query.Get("locale"))},
	}
	var err error
	if value := strings.TrimSpace(query.Get("descending")); value != "" {
		request.Descending, err = strconv.ParseBool(value)
		if err != nil {
			return knowledgeStore.ChunkListRequest{}, fmt.Errorf("parse descending: %w", err)
		}
	}
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		request.Limit, err = strconv.Atoi(value)
		if err != nil || request.Limit <= 0 {
			return knowledgeStore.ChunkListRequest{}, fmt.Errorf("invalid limit")
		}
	}
	for _, value := range queryValues(query["kind"]) {
		kind, err := knowledge.ChunkKindString(value)
		if err != nil || kind == knowledge.ChunkKindUnspecified {
			return knowledgeStore.ChunkListRequest{}, fmt.Errorf("invalid chunk kind %q", value)
		}
		request.Filter.Kinds = append(request.Filter.Kinds, kind)
	}
	for _, value := range queryValues(query["state"]) {
		state, err := knowledge.ChunkStateString(value)
		if err != nil || state == knowledge.ChunkStateUnspecified {
			return knowledgeStore.ChunkListRequest{}, fmt.Errorf("invalid chunk state %q", value)
		}
		request.Filter.States = append(request.Filter.States, state)
	}
	for _, value := range queryValues(query["scope_kind"]) {
		kind, err := knowledge.ScopeKindString(value)
		if err != nil || kind == knowledge.ScopeKindUnspecified {
			return knowledgeStore.ChunkListRequest{}, fmt.Errorf("invalid scope kind %q", value)
		}
		request.Filter.ScopeKinds = append(request.Filter.ScopeKinds, kind)
	}
	for _, value := range queryValues(query["scope"]) {
		scope, err := parseKnowledgeScope(value)
		if err != nil {
			return knowledgeStore.ChunkListRequest{}, err
		}
		request.Filter.Scopes = append(request.Filter.Scopes, scope)
	}
	request.Filter.Tags = queryValues(query["tag"])
	return request, nil
}

func parseKnowledgeScope(value string) (knowledge.Scope, error) {
	kindValue, selector, hasSelector := strings.Cut(strings.TrimSpace(value), ":")
	kind, err := knowledge.ScopeKindString(kindValue)
	if err != nil || kind == knowledge.ScopeKindUnspecified {
		return knowledge.Scope{}, fmt.Errorf("invalid scope %q", value)
	}
	scope := knowledge.Scope{Kind: kind, Selector: strings.TrimSpace(selector)}
	if !hasSelector {
		scope.Selector = ""
	}
	if err := scope.Validate(); err != nil {
		return knowledge.Scope{}, err
	}
	return scope, nil
}

func queryValues(values []string) []string {
	var result []string
	for _, value := range values {
		for item := range strings.SplitSeq(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				result = append(result, item)
			}
		}
	}
	return result
}

func chunkCandidate(content knowledgeapi.ChunkContent) knowledge.Chunk {
	return knowledge.Chunk{
		Title: content.Title, Description: content.Description, Aliases: content.Aliases, Tags: content.Tags,
		Kind: content.Kind, Scope: content.Scope, Visibility: content.Visibility, SharedWith: content.SharedWith,
		Language: content.Language, Locale: content.Locale, Domain: content.Domain, Risk: content.Risk,
		Publisher: content.Publisher, License: content.License, SourcePolicy: content.SourcePolicy,
		DependencyIDs: content.DependencyIDs, MinKoderVersion: content.MinKoderVersion, ReviewAfter: content.ReviewAfter,
	}
}

func chunkServiceContent(content knowledgeapi.ChunkContent) knowledgeService.ChunkContent {
	return knowledgeService.ChunkContent{
		Title: content.Title, Description: content.Description, Aliases: content.Aliases, Tags: content.Tags,
		Kind: content.Kind, Scope: content.Scope, Visibility: content.Visibility, SharedWith: content.SharedWith,
		Language: content.Language, Locale: content.Locale, Domain: content.Domain, Risk: content.Risk,
		Publisher: content.Publisher, License: content.License, SourcePolicy: content.SourcePolicy,
		DependencyIDs: content.DependencyIDs, MinKoderVersion: content.MinKoderVersion, ReviewAfter: content.ReviewAfter,
	}
}

func decodeKnowledgeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("request body contains multiple JSON values")
		}
		return err
	}
	return nil
}

func (s *Server) writeKnowledgeChunk(w http.ResponseWriter, requestID string, status int, chunk knowledge.Chunk, classification *knowledge.ClassificationResult) {
	reference := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(chunk.ID)}
	metadata := knowledgeapi.Resource(reference, chunk.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
	}
	s.writeKnowledgeJSON(w, status, knowledgeapi.ChunkResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID), ResourceMetadata: metadata,
		Chunk: chunk, Classification: classification,
	})
}

func (s *Server) writeKnowledgeServiceError(w http.ResponseWriter, requestID string, err error) {
	classified := knowledgeService.ClassifyError(err)
	status := http.StatusInternalServerError
	switch classified.Code {
	case knowledgeService.ErrorCodeInvalid:
		status = http.StatusBadRequest
	case knowledgeService.ErrorCodeForbidden:
		status = http.StatusForbidden
	case knowledgeService.ErrorCodeNotFound:
		status = http.StatusNotFound
	case knowledgeService.ErrorCodeConflict, knowledgeService.ErrorCodeDependency:
		status = http.StatusConflict
	case knowledgeService.ErrorCodeStale:
		status = http.StatusGone
	case knowledgeService.ErrorCodeUnavailable:
		status = http.StatusServiceUnavailable
	}
	s.writeKnowledgeJSON(w, status, knowledgeapi.ErrorResponse{ResponseMetadata: knowledgeapi.Metadata(requestID), Error: classified})
}

func (s *Server) writeKnowledgeError(w http.ResponseWriter, requestID string, status int, code knowledgeService.ErrorCode, message string) {
	s.writeKnowledgeJSON(w, status, knowledgeapi.ErrorResponse{
		ResponseMetadata: knowledgeapi.Metadata(requestID),
		Error:            &knowledgeService.ServiceError{Code: code, Message: message},
	})
}

func (s *Server) writeKnowledgeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
