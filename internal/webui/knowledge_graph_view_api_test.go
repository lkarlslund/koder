package webui

import (
	"context"
	"net/http"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestKnowledgeGraphViewAPILifecycleAndOwnerIsolation(t *testing.T) {
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store, Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := newTestController(t)
	ctrl.SetKnowledgeService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	deviceToken := bindKnowledgeTestDevice(t, srv)
	collectionURL := srv.URL() + knowledgeapi.GraphViewCollectionPath
	state := knowledgeStore.GraphViewState{Browser: knowledgeStore.GraphViewBrowserState{ScopeKind: "personal"}, MobilePane: "graph", Layout: "force_atlas2"}

	response := knowledgeJSONRequest(t, http.MethodPost, collectionURL, srv.knowledgeBrowserToken, knowledgeapi.GraphViewSaveRequest{Name: "Personal map", State: state})
	if response.StatusCode != http.StatusCreated || response.Header.Get("Location") == "" {
		response.Body.Close()
		t.Fatalf("create graph view status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	var created knowledgeapi.GraphViewResponse
	decodeKnowledgeResponse(t, response, &created)
	if created.View.Name != "Personal map" || created.View.Revision != 1 || created.View.Owner.ID != "browser:webui" {
		t.Fatalf("created graph view = %#v", created.View)
	}

	response = knowledgeAPIRequest(t, http.MethodGet, collectionURL, srv.knowledgeBrowserToken)
	var listed knowledgeapi.GraphViewListResponse
	decodeKnowledgeResponse(t, response, &listed)
	if len(listed.Views) != 1 || listed.Views[0].ID != created.View.ID {
		t.Fatalf("listed graph views = %#v", listed.Views)
	}
	response = knowledgeAPIRequest(t, http.MethodGet, collectionURL, deviceToken)
	var deviceListed knowledgeapi.GraphViewListResponse
	decodeKnowledgeResponse(t, response, &deviceListed)
	if len(deviceListed.Views) != 0 {
		t.Fatalf("device saw browser-owned views = %#v", deviceListed.Views)
	}

	itemURL := srv.URL() + knowledgeapi.GraphViewPath(created.View.ID)
	response = knowledgeJSONRequest(t, http.MethodPut, itemURL, srv.knowledgeBrowserToken, knowledgeapi.GraphViewSaveRequest{Name: "Renamed map", State: state, ExpectedRevision: 1})
	var updated knowledgeapi.GraphViewResponse
	decodeKnowledgeResponse(t, response, &updated)
	if updated.View.Name != "Renamed map" || updated.View.Revision != 2 {
		t.Fatalf("updated graph view = %#v", updated.View)
	}
	response = knowledgeJSONRequest(t, http.MethodPut, itemURL, srv.knowledgeBrowserToken, knowledgeapi.GraphViewSaveRequest{Name: "Stale", State: state, ExpectedRevision: 1})
	if response.StatusCode != http.StatusConflict {
		response.Body.Close()
		t.Fatalf("stale update status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = knowledgeJSONRequest(t, http.MethodDelete, itemURL, srv.knowledgeBrowserToken, knowledgeapi.GraphViewDeleteRequest{ExpectedRevision: 2})
	var deleted knowledgeapi.GraphViewDeleteResponse
	decodeKnowledgeResponse(t, response, &deleted)
	if !deleted.Deleted || deleted.ID != created.View.ID {
		t.Fatalf("deleted graph view = %#v", deleted)
	}
}
