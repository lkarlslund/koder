package webui

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestMemoryAPIErrorsAreConsistentStructuredAndSanitized(t *testing.T) {
	ctrl := newTestController(t)
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatalf("new Memory service: %v", err)
	}
	ctrl.SetMemoryService(service)
	chunk := createAPIChunk(t, service, "Error contract", memory.Scope{Kind: memory.ScopeKindGlobal})
	entry := createAPIGraphEntry(t, service, chunk.ID, "Error contract entry")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindMemoryTestDevice(t, srv)

	response := memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.ChunkCollectionPath, "")
	assertMemoryAPIError(t, response, http.StatusUnauthorized, memoryService.ErrorCodeForbidden)
	if response.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("unauthorized response omitted WWW-Authenticate")
	}

	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.ChunkCollectionPath+"?sort=private-invalid-sort", token)
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)
	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.EntryPath(entry.ID)+"?private=secret", token)
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)
	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.EntryEvidencePath(entry.ID)+"?limit=201", token)
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)
	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.EntryHistoryPath(entry.ID)+"?limit=201", token)
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.SearchPath, token, memoryapi.SearchRequest{})
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.EntryCollectionPath, token, map[string]any{"private_unknown_field": true})
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.EntryCollectionPath+"?private=secret", token, memoryapi.EntryCreateRequest{})
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)
	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.SearchPath, token)
	assertMemoryAPIError(t, response, http.StatusMethodNotAllowed, memoryService.ErrorCodeInvalid)
	if response.Header.Get("Allow") != http.MethodPost {
		t.Fatalf("method response Allow = %q", response.Header.Get("Allow"))
	}
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.EntryLifecyclePath(entry.ID, "private-action"), token, memoryapi.LifecycleRequest{})
	assertMemoryAPIError(t, response, http.StatusNotFound, memoryService.ErrorCodeNotFound)
}

func assertMemoryAPIError(t *testing.T, response *http.Response, status int, code memoryService.ErrorCode) {
	t.Helper()
	var body memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &body)
	requestID := response.Header.Get("X-Koder-Request-ID")
	if response.StatusCode != status || body.APIVersion != memoryapi.Version || body.RequestID == "" || body.RequestID != requestID ||
		response.Header.Get("X-Koder-Audit-ID") != requestID ||
		body.Error == nil || body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error status=%d headers=%v response=%#v", response.StatusCode, response.Header, body)
	}
	if response.Header.Get("Cache-Control") != "private, no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("inconsistent Memory error headers: %v", response.Header)
	}
	encoded := strings.ToLower(body.Error.Message)
	for _, forbidden := range []string{"private", "secret", "invalid-sort", "unknown_field"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("Memory error leaked request detail %q: %#v", forbidden, body)
		}
	}
}
