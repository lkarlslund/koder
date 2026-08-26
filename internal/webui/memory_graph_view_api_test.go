package webui

import (
	"context"
	"net/http"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestMemoryGraphViewAPILifecycleAndOwnerIsolation(t *testing.T) {
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store, Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := newTestController(t)
	ctrl.SetMemoryService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	deviceToken := bindMemoryTestDevice(t, srv)
	collectionURL := srv.URL() + memoryapi.GraphViewCollectionPath
	state := memoryStoreAPI.GraphViewState{Browser: memoryStoreAPI.GraphViewBrowserState{ScopeKind: "personal"}, MobilePane: "graph", Layout: "force_atlas2"}

	response := memoryJSONRequest(t, http.MethodPost, collectionURL, srv.memoryBrowserToken, memoryapi.GraphViewSaveRequest{Name: "Personal map", State: state})
	if response.StatusCode != http.StatusCreated || response.Header.Get("Location") == "" {
		closeMemoryHTTPResponse(t, response)
		t.Fatalf("create graph view status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	var created memoryapi.GraphViewResponse
	decodeMemoryResponse(t, response, &created)
	if created.View.Name != "Personal map" || created.View.Revision != 1 || created.View.Owner.ID != "browser:webui" {
		t.Fatalf("created graph view = %#v", created.View)
	}

	response = memoryAPIRequest(t, http.MethodGet, collectionURL, srv.memoryBrowserToken)
	var listed memoryapi.GraphViewListResponse
	decodeMemoryResponse(t, response, &listed)
	if len(listed.Views) != 1 || listed.Views[0].ID != created.View.ID {
		t.Fatalf("listed graph views = %#v", listed.Views)
	}
	response = memoryAPIRequest(t, http.MethodGet, collectionURL, deviceToken)
	var deviceListed memoryapi.GraphViewListResponse
	decodeMemoryResponse(t, response, &deviceListed)
	if len(deviceListed.Views) != 0 {
		t.Fatalf("device saw browser-owned views = %#v", deviceListed.Views)
	}

	itemURL := srv.URL() + memoryapi.GraphViewPath(created.View.ID)
	response = memoryJSONRequest(t, http.MethodPut, itemURL, srv.memoryBrowserToken, memoryapi.GraphViewSaveRequest{Name: "Renamed map", State: state, ExpectedRevision: 1})
	var updated memoryapi.GraphViewResponse
	decodeMemoryResponse(t, response, &updated)
	if updated.View.Name != "Renamed map" || updated.View.Revision != 2 {
		t.Fatalf("updated graph view = %#v", updated.View)
	}
	response = memoryJSONRequest(t, http.MethodPut, itemURL, srv.memoryBrowserToken, memoryapi.GraphViewSaveRequest{Name: "Stale", State: state, ExpectedRevision: 1})
	if response.StatusCode != http.StatusConflict {
		closeMemoryHTTPResponse(t, response)
		t.Fatalf("stale update status = %d", response.StatusCode)
	}
	closeMemoryHTTPResponse(t, response)

	response = memoryJSONRequest(t, http.MethodDelete, itemURL, srv.memoryBrowserToken, memoryapi.GraphViewDeleteRequest{ExpectedRevision: 2})
	var deleted memoryapi.GraphViewDeleteResponse
	decodeMemoryResponse(t, response, &deleted)
	if !deleted.Deleted || deleted.ID != created.View.ID {
		t.Fatalf("deleted graph view = %#v", deleted)
	}
}
