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

func TestKnowledgeLinkCreateReadUnlinkRestoreAndHistory(t *testing.T) {
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
	first := createAPIChunk(t, service, "First endpoint", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	second := createAPIChunk(t, service, "Second endpoint", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindKnowledgeTestDevice(t, srv)
	content := knowledgeapi.LinkContent{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(first.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(second.ID)},
		Kind:   knowledge.LinkKindRelatedTo, Label: "Related systems", Notes: "Created through the API",
	}
	response := knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.LinkCollectionPath, token, knowledgeapi.LinkCreateRequest{Link: content})
	var created knowledgeapi.LinkResponse
	decodeKnowledgeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.Link.Revision.Number != 1 || created.Link.State != knowledge.LinkStateActive ||
		created.Link.Revision.Actor.Kind != knowledge.ActorKindUser || !strings.HasPrefix(created.Link.Revision.Actor.ID, "device:") ||
		response.Header.Get("Location") != knowledgeapi.LinkPath(created.Link.ID) {
		t.Fatalf("create status=%d location=%q response=%#v", response.StatusCode, response.Header.Get("Location"), created)
	}
	linkURL := srv.URL() + knowledgeapi.LinkPath(created.Link.ID)
	response = knowledgeAPIRequest(t, http.MethodGet, linkURL, token)
	var got knowledgeapi.LinkResponse
	decodeKnowledgeResponse(t, response, &got)
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
	response.Body.Close()
	if response.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional get status=%d", response.StatusCode)
	}

	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.LinkLifecyclePath(created.Link.ID, "unlink"), token, knowledgeapi.LifecycleRequest{ExpectedRevision: 2})
	var conflict knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &conflict)
	if response.StatusCode != http.StatusConflict || conflict.Error == nil || conflict.Error.Code != knowledgeService.ErrorCodeConflict {
		t.Fatalf("stale unlink status=%d response=%#v", response.StatusCode, conflict)
	}
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.LinkLifecyclePath(created.Link.ID, "unlink"), token, knowledgeapi.LifecycleRequest{
		ExpectedRevision: 1, Reason: "relationship is temporarily inactive",
	})
	var unlinked knowledgeapi.LinkResponse
	decodeKnowledgeResponse(t, response, &unlinked)
	if response.StatusCode != http.StatusOK || unlinked.Link.State != knowledge.LinkStateArchived || unlinked.Link.Revision.Number != 2 {
		t.Fatalf("unlink status=%d response=%#v", response.StatusCode, unlinked)
	}
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.LinkLifecyclePath(created.Link.ID, "restore"), token, knowledgeapi.LifecycleRequest{
		ExpectedRevision: 2, Reason: "relationship applies again",
	})
	var restored knowledgeapi.LinkResponse
	decodeKnowledgeResponse(t, response, &restored)
	if response.StatusCode != http.StatusOK || restored.Link.State != knowledge.LinkStateActive || restored.Link.Revision.Number != 3 {
		t.Fatalf("restore status=%d response=%#v", response.StatusCode, restored)
	}
	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.LinkHistoryPath(created.Link.ID), token)
	var history knowledgeapi.HistoryResponse
	decodeKnowledgeResponse(t, response, &history)
	if response.StatusCode != http.StatusOK || len(history.Revisions) != 3 || history.Revisions[0].Link == nil || history.Revisions[0].Link.Revision.Number != 3 {
		t.Fatalf("history status=%d response=%#v", response.StatusCode, history)
	}

	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.LinkCollectionPath, token, knowledgeapi.LinkCreateRequest{Link: content})
	decodeKnowledgeResponse(t, response, &conflict)
	if response.StatusCode != http.StatusConflict || conflict.Error == nil || conflict.Error.Code != knowledgeService.ErrorCodeConflict {
		t.Fatalf("duplicate create status=%d response=%#v", response.StatusCode, conflict)
	}
}
