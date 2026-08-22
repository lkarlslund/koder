package webui

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestKnowledgeAPIErrorsAreConsistentStructuredAndSanitized(t *testing.T) {
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatalf("new Knowledge service: %v", err)
	}
	ctrl.SetKnowledgeService(service)
	chunk := createAPIChunk(t, service, "Error contract", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	entry := createAPIGraphEntry(t, service, chunk.ID, "Error contract entry")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindKnowledgeTestDevice(t, srv)

	response := knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.ChunkCollectionPath, "")
	assertKnowledgeAPIError(t, response, http.StatusUnauthorized, knowledgeService.ErrorCodeForbidden)
	if response.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("unauthorized response omitted WWW-Authenticate")
	}

	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.ChunkCollectionPath+"?sort=private-invalid-sort", token)
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)
	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.EntryPath(entry.ID)+"?private=secret", token)
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)
	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.EntryEvidencePath(entry.ID)+"?limit=201", token)
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)
	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.EntryHistoryPath(entry.ID)+"?limit=201", token)
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.SearchPath, token, knowledgeapi.SearchRequest{})
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.EntryCollectionPath, token, map[string]any{"private_unknown_field": true})
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.EntryCollectionPath+"?private=secret", token, knowledgeapi.EntryCreateRequest{})
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)
	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.SearchPath, token)
	assertKnowledgeAPIError(t, response, http.StatusMethodNotAllowed, knowledgeService.ErrorCodeInvalid)
	if response.Header.Get("Allow") != http.MethodPost {
		t.Fatalf("method response Allow = %q", response.Header.Get("Allow"))
	}
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.EntryLifecyclePath(entry.ID, "private-action"), token, knowledgeapi.LifecycleRequest{})
	assertKnowledgeAPIError(t, response, http.StatusNotFound, knowledgeService.ErrorCodeNotFound)
}

func assertKnowledgeAPIError(t *testing.T, response *http.Response, status int, code knowledgeService.ErrorCode) {
	t.Helper()
	var body knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &body)
	requestID := response.Header.Get("X-Koder-Request-ID")
	if response.StatusCode != status || body.APIVersion != knowledgeapi.Version || body.RequestID == "" || body.RequestID != requestID ||
		response.Header.Get("X-Koder-Audit-ID") != requestID ||
		body.Error == nil || body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error status=%d headers=%v response=%#v", response.StatusCode, response.Header, body)
	}
	if response.Header.Get("Cache-Control") != "private, no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("inconsistent Knowledge error headers: %v", response.Header)
	}
	encoded := strings.ToLower(body.Error.Message)
	for _, forbidden := range []string{"private", "secret", "invalid-sort", "unknown_field"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("Knowledge error leaked request detail %q: %#v", forbidden, body)
		}
	}
}
