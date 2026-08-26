package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/deviceauth"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	"github.com/lkarlslund/koder/internal/memory/kpackage"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const (
	defaultMemoryRequestTimeout = 15 * time.Second
	defaultMemoryPackageTimeout = 90 * time.Second
	maxMemoryRequestBody        = 1 << 20
	maxMemoryRequestPath        = 8 << 10
	maxMemoryRequestQuery       = 16 << 10
	maxMemoryAuthorization      = 8 << 10
	maxMemoryDeviceHeader       = 1 << 10
)

func (s *Server) registerMemoryAPI(mux *http.ServeMux) {
	mux.Handle(memoryapi.ChunkCollectionPath, s.memoryEndpoint(s.handleMemoryChunks))
	mux.Handle(memoryapi.ChunkCollectionPath+"/", s.memoryEndpoint(s.handleMemoryChunk))
	mux.Handle(memoryapi.EntryCollectionPath, s.memoryEndpoint(s.handleMemoryEntries))
	mux.Handle(memoryapi.EntryCollectionPath+"/", s.memoryEndpoint(s.handleMemoryEntry))
	mux.Handle(memoryapi.LinkCollectionPath, s.memoryEndpoint(s.handleMemoryLinks))
	mux.Handle(memoryapi.LinkCollectionPath+"/", s.memoryEndpoint(s.handleMemoryLink))
	mux.Handle(memoryapi.SearchPath, s.memoryEndpoint(s.handleMemorySearch))
	mux.Handle(memoryapi.GraphSnapshotPath, s.memoryEndpoint(s.handleMemoryGraphSnapshot))
	mux.Handle(memoryapi.NeighborPath, s.memoryEndpoint(s.handleMemoryNeighbors))
	mux.Handle(memoryapi.OperationalStatusPath, s.memoryEndpoint(s.handleMemoryOperationalStatus))
	mux.Handle(memoryapi.IndexRebuildPath, s.memoryEndpoint(s.handleMemoryIndexRebuild))
	mux.Handle(memoryapi.ChatContextPath, s.memoryEndpoint(s.handleMemoryChatContext))
	mux.Handle(memoryapi.GraphViewCollectionPath, s.memoryEndpoint(s.handleMemoryGraphViews))
	mux.Handle(memoryapi.GraphViewCollectionPath+"/", s.memoryEndpoint(s.handleMemoryGraphView))
	mux.Handle(memoryapi.PackageCollectionPath+"/", s.memoryEndpoint(s.handleMemoryPackages))
	mux.Handle(memoryapi.CurationCandidatePath, s.memoryEndpoint(s.handleMemoryCurationCandidates))
	mux.Handle(memoryapi.CurationCandidatePath+"/", s.memoryEndpoint(s.handleMemoryCurationCandidate))
}

func (s *Server) memoryEndpoint(handler http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auditID := string(id.New())
		ctx, err := memoryService.WithAuditID(r.Context(), auditID)
		if err != nil {
			s.writeMemoryError(w, auditID, http.StatusInternalServerError, memoryService.ErrorCodeInternal, "Memory could not complete the operation.")
			return
		}
		timeout := s.memoryRequestTimeout
		if timeout <= 0 {
			timeout = defaultMemoryRequestTimeout
			if strings.HasPrefix(r.URL.Path, memoryapi.PackageCollectionPath+"/") {
				timeout = defaultMemoryPackageTimeout
			}
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		r = r.WithContext(ctx)
		if deadline, ok := ctx.Deadline(); ok {
			responseController := http.NewResponseController(w)
			_ = responseController.SetReadDeadline(deadline)
			_ = responseController.SetWriteDeadline(deadline.Add(2 * time.Second))
			defer func() {
				_ = responseController.SetReadDeadline(time.Time{})
				_ = responseController.SetWriteDeadline(time.Time{})
			}()
		}
		w.Header().Set("X-Koder-Request-ID", auditID)
		w.Header().Set("X-Koder-Audit-ID", auditID)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		recorder := &memoryResponseRecorder{ResponseWriter: w}
		started := time.Now()
		defer func() {
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			log := slog.Debug
			if r.Method != http.MethodGet || status >= http.StatusBadRequest {
				log = slog.Info
			}
			log("memory api request", "audit_id", auditID, "method", r.Method, "path", boundedMemoryAuditPath(r.URL.Path),
				"status", status, "elapsed_ms", time.Since(started).Milliseconds())
		}()
		if !validMemoryTransportRequest(r) {
			s.writeMemoryError(recorder, auditID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
			return
		}
		bodyLimit := int64(maxMemoryRequestBody)
		if strings.HasPrefix(r.URL.Path, memoryapi.PackageCollectionPath+"/") {
			bodyLimit = kpackage.HardMaxArchiveBytes
		}
		if r.ContentLength > bodyLimit {
			s.writeMemoryError(recorder, auditID, http.StatusRequestEntityTooLarge, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
			return
		}
		handler(recorder, r)
	})
}

func boundedMemoryAuditPath(value string) string {
	const limit = 256
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

type memoryResponseRecorder struct {
	http.ResponseWriter
	status int
}

func (w *memoryResponseRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *memoryResponseRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func validMemoryTransportRequest(r *http.Request) bool {
	if r == nil || len(r.URL.Path) > maxMemoryRequestPath || len(r.URL.RawQuery) > maxMemoryRequestQuery ||
		len(r.Header.Get("Authorization")) > maxMemoryAuthorization {
		return false
	}
	for _, name := range []string{
		"X-Koder-Device-ID", "X-Koder-Device-Name", "X-Koder-Device-Manufacturer", "X-Koder-Device-Model",
		"X-Koder-Android-Version", "X-Koder-App-Version", "X-Koder-App-ID",
	} {
		if len(r.Header.Get(name)) > maxMemoryDeviceHeader {
			return false
		}
	}
	return true
}

func (s *Server) handleMemoryChunks(w http.ResponseWriter, r *http.Request) {
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
		s.listMemoryChunks(w, r, requestID, ctx, service)
	case http.MethodPost:
		s.createMemoryChunk(w, r, requestID, ctx, service)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
	}
}

func (s *Server) listMemoryChunks(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service) {
	request, err := parseChunkListQuery(r)
	if err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	page, err := service.ListChunks(ctx, request)
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	s.writeMemoryJSON(w, http.StatusOK, memoryapi.ChunkListResponse{
		ResponseMetadata: memoryapi.Metadata(requestID),
		Chunks:           page.Chunks,
		Page: memoryapi.Page{
			Limit: limit, Returned: len(page.Chunks), NextCursor: page.NextCursor,
		},
	})
}

func (s *Server) createMemoryChunk(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service) {
	var request memoryapi.ChunkCreateRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	result, err := service.CreateChunk(ctx, memoryService.CreateChunkRequest{
		Chunk:          chunkCandidate(request.Chunk),
		ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	w.Header().Set("Location", memoryapi.ChunkPath(result.Chunk.ID))
	s.writeMemoryChunk(w, requestID, http.StatusCreated, result.Chunk, &result.Classification)
}

func (s *Server) handleMemoryChunk(w http.ResponseWriter, r *http.Request) {
	requestID, ctx, ok := s.authenticateMemoryRequest(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, memoryapi.ChunkCollectionPath+"/"), "/")
	parts := strings.Split(path, "/")
	if path == "" || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory object was not found.")
		return
	}
	chunkID := strings.TrimSpace(parts[0])
	service := s.controller.MemoryService()
	if service == nil {
		s.writeMemoryError(w, requestID, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable, "Memory is temporarily unavailable.")
		return
	}
	if len(parts) == 2 {
		if parts[1] == "history" {
			s.getMemoryHistory(w, r, requestID, ctx, service, memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: chunkID})
			return
		}
		s.changeMemoryChunkLifecycle(w, r, requestID, ctx, service, memory.ChunkID(chunkID), parts[1])
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.getMemoryChunk(w, r, requestID, ctx, service, chunkID)
	case http.MethodPut:
		s.updateMemoryChunk(w, r, requestID, ctx, service, memory.ChunkID(chunkID))
	case http.MethodDelete:
		s.deleteMemoryChunk(w, r, requestID, ctx, service, memory.ChunkID(chunkID))
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		s.writeMemoryError(w, requestID, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid, "This Memory endpoint does not support that method.")
	}
}

func (s *Server) getMemoryChunk(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, chunkID string) {
	if !s.requireNoMemoryQuery(w, r, requestID) {
		return
	}
	reference := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: chunkID}
	record, err := service.Get(ctx, reference)
	if err != nil {
		s.writeMemoryReadError(w, requestID, err)
		return
	}
	if record.Chunk == nil {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory object was not found.")
		return
	}
	metadata := memoryapi.Resource(reference, record.Chunk.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
		if r.Header.Get("If-None-Match") == metadata.ETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	s.writeMemoryChunk(w, requestID, http.StatusOK, *record.Chunk, nil)
}

func (s *Server) updateMemoryChunk(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, chunkID memory.ChunkID) {
	var request memoryapi.ChunkUpdateRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	result, err := service.UpdateChunk(ctx, memoryService.UpdateChunkRequest{
		ChunkID: chunkID, ExpectedRevision: request.ExpectedRevision,
		Content: chunkServiceContent(request.Chunk), Reason: request.Reason, ReviewApproved: request.ReviewApproved,
	})
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryChunk(w, requestID, http.StatusOK, result.Chunk, &result.Classification)
}

func (s *Server) changeMemoryChunkLifecycle(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, chunkID memory.ChunkID, action string) {
	if action != "archive" && action != "restore" {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory endpoint was not found.")
		return
	}
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
	serviceRequest := memoryService.ChunkLifecycleRequest{
		ChunkID: chunkID, ExpectedRevision: request.ExpectedRevision, Reason: request.Reason,
	}
	var result memoryService.ChunkLifecycleResult
	var err error
	if action == "archive" {
		result, err = service.ArchiveChunk(ctx, serviceRequest)
	} else {
		result, err = service.RestoreChunk(ctx, serviceRequest)
	}
	if err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryChunk(w, requestID, http.StatusOK, result.Chunk, nil)
}

func (s *Server) deleteMemoryChunk(w http.ResponseWriter, r *http.Request, requestID string, ctx context.Context, service *memoryService.Service, chunkID memory.ChunkID) {
	var request memoryapi.DeleteRequest
	if err := decodeMemoryJSON(w, r, &request); err != nil {
		s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
		return
	}
	serviceRequest := memoryService.DeleteChunkRequest{
		ChunkID: chunkID, ExpectedRevision: request.ExpectedRevision, Confirmed: request.Confirmed,
	}
	response := memoryapi.DeleteResponse{
		ResponseMetadata: memoryapi.Metadata(requestID),
		Object:           memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunkID)},
		Deleted:          true,
		Cascade:          request.Cascade,
	}
	if request.Cascade {
		result, err := service.CascadeDeleteChunk(ctx, serviceRequest)
		if err != nil {
			s.writeMemoryServiceError(w, requestID, err)
			return
		}
		response.DeletedEntryIDs = result.DeletedEntryIDs
		response.DeletedLinkIDs = result.DeletedLinkIDs
		response.DeletedEvidence = result.DeletedEvidenceIDs
		response.UpdatedChunkIDs = result.UpdatedDependentChunkIDs
	} else if err := service.DeleteChunk(ctx, serviceRequest); err != nil {
		s.writeMemoryServiceError(w, requestID, err)
		return
	}
	s.writeMemoryJSON(w, http.StatusOK, response)
}

func (s *Server) authenticateMemoryRequest(w http.ResponseWriter, r *http.Request) (string, context.Context, bool) {
	requestID := memoryService.AuditIDFromContext(r.Context())
	if requestID == "" {
		requestID = string(id.New())
	}
	w.Header().Set("X-Koder-Request-ID", requestID)
	w.Header().Set("X-Koder-Audit-ID", requestID)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	token, ok := bearerCredential(r.Header.Get("Authorization"))
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Koder Memory"`)
		s.writeMemoryError(w, requestID, http.StatusUnauthorized, memoryService.ErrorCodeForbidden, "Memory authentication is required.")
		return requestID, nil, false
	}
	if memoryBrowserTokenMatches(s.memoryBrowserToken, token) {
		ctx, err := memoryService.WithActor(r.Context(), memory.Actor{Kind: memory.ActorKindUser, ID: "browser:webui"})
		if err != nil {
			s.writeMemoryError(w, requestID, http.StatusInternalServerError, memoryService.ErrorCodeInternal, "Memory could not complete the operation.")
			return requestID, nil, false
		}
		return requestID, ctx, true
	}
	if s.devices == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Koder Memory"`)
		s.writeMemoryError(w, requestID, http.StatusUnauthorized, memoryService.ErrorCodeForbidden, "Memory authentication is required.")
		return requestID, nil, false
	}
	device, ok := s.devices.Authenticate(token, memoryDeviceInfo(r.Header))
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Koder Memory"`)
		s.writeMemoryError(w, requestID, http.StatusUnauthorized, memoryService.ErrorCodeForbidden, "Memory authentication is required.")
		return requestID, nil, false
	}
	ctx, err := memoryService.WithActor(r.Context(), memory.Actor{Kind: memory.ActorKindUser, ID: "device:" + device.ID})
	if err != nil {
		s.writeMemoryError(w, requestID, http.StatusInternalServerError, memoryService.ErrorCodeInternal, "Memory could not complete the operation.")
		return requestID, nil, false
	}
	return requestID, ctx, true
}

func bearerCredential(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	token = strings.TrimSpace(token)
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != "" && !strings.ContainsAny(token, " \t\r\n")
}

func memoryDeviceInfo(header http.Header) deviceauth.DeviceInfo {
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

func parseChunkListQuery(r *http.Request) (memoryStoreAPI.ChunkListRequest, error) {
	query := r.URL.Query()
	allowed := map[string]bool{
		"kind": true, "state": true, "scope": true, "scope_kind": true, "tag": true,
		"locale": true, "sort": true, "descending": true, "limit": true, "cursor": true,
	}
	for name := range query {
		if !allowed[name] {
			return memoryStoreAPI.ChunkListRequest{}, fmt.Errorf("unsupported query parameter %q", name)
		}
	}
	request := memoryStoreAPI.ChunkListRequest{
		Sort:   memoryStoreAPI.ChunkSort(strings.TrimSpace(query.Get("sort"))),
		Cursor: strings.TrimSpace(query.Get("cursor")),
		Filter: memoryStoreAPI.ChunkFilter{Locale: strings.TrimSpace(query.Get("locale"))},
	}
	var err error
	if value := strings.TrimSpace(query.Get("descending")); value != "" {
		request.Descending, err = strconv.ParseBool(value)
		if err != nil {
			return memoryStoreAPI.ChunkListRequest{}, fmt.Errorf("parse descending: %w", err)
		}
	}
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		request.Limit, err = strconv.Atoi(value)
		if err != nil || request.Limit <= 0 {
			return memoryStoreAPI.ChunkListRequest{}, fmt.Errorf("invalid limit")
		}
	}
	for _, value := range queryValues(query["kind"]) {
		kind, err := memory.ChunkKindString(value)
		if err != nil || kind == memory.ChunkKindUnspecified {
			return memoryStoreAPI.ChunkListRequest{}, fmt.Errorf("invalid chunk kind %q", value)
		}
		request.Filter.Kinds = append(request.Filter.Kinds, kind)
	}
	for _, value := range queryValues(query["state"]) {
		state, err := memory.ChunkStateString(value)
		if err != nil || state == memory.ChunkStateUnspecified {
			return memoryStoreAPI.ChunkListRequest{}, fmt.Errorf("invalid chunk state %q", value)
		}
		request.Filter.States = append(request.Filter.States, state)
	}
	for _, value := range queryValues(query["scope_kind"]) {
		kind, err := memory.ScopeKindString(value)
		if err != nil || kind == memory.ScopeKindUnspecified {
			return memoryStoreAPI.ChunkListRequest{}, fmt.Errorf("invalid scope kind %q", value)
		}
		request.Filter.ScopeKinds = append(request.Filter.ScopeKinds, kind)
	}
	for _, value := range queryValues(query["scope"]) {
		scope, err := parseMemoryScope(value)
		if err != nil {
			return memoryStoreAPI.ChunkListRequest{}, err
		}
		request.Filter.Scopes = append(request.Filter.Scopes, scope)
	}
	request.Filter.Tags = queryValues(query["tag"])
	return request, nil
}

func parseMemoryScope(value string) (memory.Scope, error) {
	kindValue, selector, hasSelector := strings.Cut(strings.TrimSpace(value), ":")
	kind, err := memory.ScopeKindString(kindValue)
	if err != nil || kind == memory.ScopeKindUnspecified {
		return memory.Scope{}, fmt.Errorf("invalid scope %q", value)
	}
	scope := memory.Scope{Kind: kind, Selector: strings.TrimSpace(selector)}
	if !hasSelector {
		scope.Selector = ""
	}
	if err := scope.Validate(); err != nil {
		return memory.Scope{}, err
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

func chunkCandidate(content memoryapi.ChunkContent) memory.Chunk {
	return memory.Chunk{
		Title: content.Title, Description: content.Description, Aliases: content.Aliases, Tags: content.Tags,
		Kind: content.Kind, Scope: content.Scope, Visibility: content.Visibility, SharedWith: content.SharedWith,
		Language: content.Language, Locale: content.Locale, Domain: content.Domain, Risk: content.Risk,
		Publisher: content.Publisher, License: content.License, SourcePolicy: content.SourcePolicy,
		DependencyIDs: content.DependencyIDs, MinKoderVersion: content.MinKoderVersion, ReviewAfter: content.ReviewAfter,
	}
}

func chunkServiceContent(content memoryapi.ChunkContent) memoryService.ChunkContent {
	return memoryService.ChunkContent{
		Title: content.Title, Description: content.Description, Aliases: content.Aliases, Tags: content.Tags,
		Kind: content.Kind, Scope: content.Scope, Visibility: content.Visibility, SharedWith: content.SharedWith,
		Language: content.Language, Locale: content.Locale, Domain: content.Domain, Risk: content.Risk,
		Publisher: content.Publisher, License: content.License, SourcePolicy: content.SourcePolicy,
		DependencyIDs: content.DependencyIDs, MinKoderVersion: content.MinKoderVersion, ReviewAfter: content.ReviewAfter,
	}
}

func decodeMemoryJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if r.URL.RawQuery != "" {
		return fmt.Errorf("request query parameters are not supported")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMemoryRequestBody)
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

func (s *Server) requireNoMemoryQuery(w http.ResponseWriter, r *http.Request, requestID string) bool {
	if r.URL.RawQuery == "" {
		return true
	}
	s.writeMemoryError(w, requestID, http.StatusBadRequest, memoryService.ErrorCodeInvalid, "The Memory request is invalid.")
	return false
}

func (s *Server) writeMemoryChunk(w http.ResponseWriter, requestID string, status int, chunk memory.Chunk, classification *memory.ClassificationResult) {
	reference := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunk.ID)}
	metadata := memoryapi.Resource(reference, chunk.Revision)
	if metadata.ETag != "" {
		w.Header().Set("ETag", metadata.ETag)
	}
	s.writeMemoryJSON(w, status, memoryapi.ChunkResponse{
		ResponseMetadata: memoryapi.Metadata(requestID), ResourceMetadata: metadata,
		Chunk: chunk, Classification: classification,
	})
}

func (s *Server) writeMemoryServiceError(w http.ResponseWriter, requestID string, err error) {
	classified := memoryService.ClassifyError(err)
	status := memoryServiceHTTPStatus(classified.Code)
	s.writeMemoryJSON(w, status, memoryapi.ErrorResponse{ResponseMetadata: memoryapi.Metadata(requestID), Error: classified})
}

func (s *Server) writeMemoryReadError(w http.ResponseWriter, requestID string, err error) {
	// A policy-denied object is deliberately indistinguishable from an absent one.
	if errors.Is(err, memoryService.ErrChunkPolicyDenied) {
		s.writeMemoryError(w, requestID, http.StatusNotFound, memoryService.ErrorCodeNotFound, "The Memory object was not found.")
		return
	}
	s.writeMemoryServiceError(w, requestID, err)
}

func (s *Server) writeMemoryError(w http.ResponseWriter, requestID string, status int, code memoryService.ErrorCode, message string) {
	s.writeMemoryJSON(w, status, memoryapi.ErrorResponse{
		ResponseMetadata: memoryapi.Metadata(requestID),
		Error:            &memoryService.ServiceError{Code: code, Message: message},
	})
}

func (s *Server) writeMemoryJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
