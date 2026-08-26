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

func TestMemoryLinkCreateReadUnlinkRestoreAndHistory(t *testing.T) {
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
	first := createAPIChunk(t, service, "First endpoint", memory.Scope{Kind: memory.ScopeKindGlobal})
	second := createAPIChunk(t, service, "Second endpoint", memory.Scope{Kind: memory.ScopeKindGlobal})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindMemoryTestDevice(t, srv)
	content := memoryapi.LinkContent{
		Source: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(first.ID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(second.ID)},
		Kind:   memory.LinkKindRelatedTo, Label: "Related systems", Notes: "Created through the API",
	}
	response := memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.LinkCollectionPath, token, memoryapi.LinkCreateRequest{Link: content})
	var created memoryapi.LinkResponse
	decodeMemoryResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.Link.Revision.Number != 1 || created.Link.State != memory.LinkStateActive ||
		created.Link.Revision.Actor.Kind != memory.ActorKindUser || !strings.HasPrefix(created.Link.Revision.Actor.ID, "device:") ||
		response.Header.Get("Location") != memoryapi.LinkPath(created.Link.ID) {
		t.Fatalf("create status=%d location=%q response=%#v", response.StatusCode, response.Header.Get("Location"), created)
	}
	linkURL := srv.URL() + memoryapi.LinkPath(created.Link.ID)
	response = memoryAPIRequest(t, http.MethodGet, linkURL, token)
	var got memoryapi.LinkResponse
	decodeMemoryResponse(t, response, &got)
	if response.StatusCode != http.StatusOK || got.Link.ID != created.Link.ID || response.Header.Get("ETag") == "" {
		t.Fatalf("get status=%d response=%#v", response.StatusCode, got)
	}
	etag := response.Header.Get("ETag")
	request, err := http.NewRequest(http.MethodGet, linkURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("If-None-Match", etag)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	closeMemoryHTTPResponse(t, response)
	if response.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional get status=%d", response.StatusCode)
	}

	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.LinkLifecyclePath(created.Link.ID, "unlink"), token, memoryapi.LifecycleRequest{ExpectedRevision: 2})
	var conflict memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &conflict)
	if response.StatusCode != http.StatusConflict || conflict.Error == nil || conflict.Error.Code != memoryService.ErrorCodeConflict {
		t.Fatalf("stale unlink status=%d response=%#v", response.StatusCode, conflict)
	}
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.LinkLifecyclePath(created.Link.ID, "unlink"), token, memoryapi.LifecycleRequest{
		ExpectedRevision: 1, Reason: "relationship is temporarily inactive",
	})
	var unlinked memoryapi.LinkResponse
	decodeMemoryResponse(t, response, &unlinked)
	if response.StatusCode != http.StatusOK || unlinked.Link.State != memory.LinkStateArchived || unlinked.Link.Revision.Number != 2 {
		t.Fatalf("unlink status=%d response=%#v", response.StatusCode, unlinked)
	}
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.LinkLifecyclePath(created.Link.ID, "restore"), token, memoryapi.LifecycleRequest{
		ExpectedRevision: 2, Reason: "relationship applies again",
	})
	var restored memoryapi.LinkResponse
	decodeMemoryResponse(t, response, &restored)
	if response.StatusCode != http.StatusOK || restored.Link.State != memory.LinkStateActive || restored.Link.Revision.Number != 3 {
		t.Fatalf("restore status=%d response=%#v", response.StatusCode, restored)
	}
	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.LinkHistoryPath(created.Link.ID), token)
	var history memoryapi.HistoryResponse
	decodeMemoryResponse(t, response, &history)
	if response.StatusCode != http.StatusOK || len(history.Revisions) != 3 || history.Revisions[0].Link == nil || history.Revisions[0].Link.Revision.Number != 3 {
		t.Fatalf("history status=%d response=%#v", response.StatusCode, history)
	}

	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.LinkCollectionPath, token, memoryapi.LinkCreateRequest{Link: content})
	decodeMemoryResponse(t, response, &conflict)
	if response.StatusCode != http.StatusConflict || conflict.Error == nil || conflict.Error.Code != memoryService.ErrorCodeConflict {
		t.Fatalf("duplicate create status=%d response=%#v", response.StatusCode, conflict)
	}
}
