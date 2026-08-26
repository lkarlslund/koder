package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lkarlslund/koder/internal/deviceauth"
	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestMemoryChunkReadAndListRequireAuthentication(t *testing.T) {
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
	global := createAPIChunk(t, service, "Global reference", memory.Scope{Kind: memory.ScopeKindGlobal})
	createAPIChunk(t, service, "Koder project", memory.Scope{Kind: memory.ScopeKindProject, Selector: "koder"})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindMemoryTestDevice(t, srv)

	response := memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.RoutePrefix+"/chunks", "")
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") == "" {
		closeMemoryHTTPResponse(t, response)
		t.Fatalf("unauthenticated status=%d authenticate=%q", response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}
	var unauthorized memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &unauthorized)
	if unauthorized.APIVersion != memoryapi.Version || unauthorized.Error == nil || unauthorized.Error.Code != memoryService.ErrorCodeForbidden {
		t.Fatalf("unauthorized response = %#v", unauthorized)
	}

	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.RoutePrefix+"/chunks?scope=global&limit=1", srv.memoryBrowserToken)
	if response.StatusCode != http.StatusOK {
		defer closeMemoryHTTPResponse(t, response)
		t.Fatalf("browser-authenticated list status = %d", response.StatusCode)
	}
	var browserListed memoryapi.ChunkListResponse
	decodeMemoryResponse(t, response, &browserListed)
	if len(browserListed.Chunks) != 1 || browserListed.Chunks[0].ID != global.ID {
		t.Fatalf("browser-authenticated list response = %#v", browserListed)
	}

	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.RoutePrefix+"/chunks?scope=global&limit=1", token)
	if response.StatusCode != http.StatusOK {
		defer closeMemoryHTTPResponse(t, response)
		t.Fatalf("list status = %d", response.StatusCode)
	}
	var listed memoryapi.ChunkListResponse
	decodeMemoryResponse(t, response, &listed)
	if listed.APIVersion != memoryapi.Version || listed.Page.Limit != 1 || listed.Page.Returned != 1 ||
		len(listed.Chunks) != 1 || listed.Chunks[0].ID != global.ID {
		t.Fatalf("list response = %#v", listed)
	}

	chunkURL := srv.URL() + memoryapi.RoutePrefix + "/chunks/" + string(global.ID)
	response = memoryAPIRequest(t, http.MethodGet, chunkURL, token)
	if response.StatusCode != http.StatusOK {
		defer closeMemoryHTTPResponse(t, response)
		t.Fatalf("get status = %d", response.StatusCode)
	}
	etag := response.Header.Get("ETag")
	var got memoryapi.ChunkResponse
	decodeMemoryResponse(t, response, &got)
	if got.Chunk.ID != global.ID || got.ETag == "" || got.ETag != etag || got.ExplorerURL == "" {
		t.Fatalf("get response = %#v header etag=%q", got, etag)
	}

	request, err := http.NewRequest(http.MethodGet, chunkURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("If-None-Match", etag)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("conditional get: %v", err)
	}
	defer closeMemoryHTTPResponse(t, response)
	if response.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional get status = %d", response.StatusCode)
	}
}

func TestMemoryChunkAPIHidesPolicyDeniedRecords(t *testing.T) {
	ctrl := newTestController(t)
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
		ChunkPolicy: memoryService.ChunkPolicyFunc(func(_ context.Context, actor memory.Actor, action memoryService.ChunkPolicyAction, chunk memory.Chunk) error {
			if actor.Kind != memory.ActorKindUser || !strings.HasPrefix(actor.ID, "device:") || action != memoryService.ChunkPolicyRead {
				return nil
			}
			if chunk.Title == "Hidden" {
				return errors.New("private policy detail")
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("new Memory service: %v", err)
	}
	ctrl.SetMemoryService(service)
	hidden := createAPIChunk(t, service, "Hidden", memory.Scope{Kind: memory.ScopeKindGlobal})
	visible := createAPIChunk(t, service, "Visible", memory.Scope{Kind: memory.ScopeKindGlobal})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindMemoryTestDevice(t, srv)
	response := memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.RoutePrefix+"/chunks?sort=title", token)
	var listed memoryapi.ChunkListResponse
	decodeMemoryResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || len(listed.Chunks) != 1 || listed.Chunks[0].ID != visible.ID {
		t.Fatalf("policy-filtered list status=%d response=%#v", response.StatusCode, listed)
	}

	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.RoutePrefix+"/chunks/"+string(hidden.ID), token)
	var denied memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &denied)
	if response.StatusCode != http.StatusNotFound || denied.Error == nil || denied.Error.Code != memoryService.ErrorCodeNotFound {
		t.Fatalf("denied get status=%d response=%#v", response.StatusCode, denied)
	}
}

func TestMemoryChunkMutationLifecycleUsesOptimisticRevisions(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindMemoryTestDevice(t, srv)
	baseURL := srv.URL() + memoryapi.RoutePrefix + "/chunks"
	content := memoryapi.ChunkContent{
		Title: "API lifecycle", Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Visibility: memory.VisibilityPrivate,
	}

	response := memoryJSONRequest(t, http.MethodPost, baseURL, token, memoryapi.ChunkCreateRequest{Chunk: content})
	var created memoryapi.ChunkResponse
	decodeMemoryResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.Chunk.ID == "" || created.Chunk.Revision.Number != 1 ||
		created.Chunk.Revision.Actor.Kind != memory.ActorKindUser || response.Header.Get("Location") == "" {
		t.Fatalf("create status=%d response=%#v", response.StatusCode, created)
	}
	chunkURL := baseURL + "/" + string(created.Chunk.ID)

	content.Title = "API lifecycle updated"
	update := memoryapi.ChunkUpdateRequest{Chunk: content, ExpectedRevision: 1, Reason: "test update"}
	response = memoryJSONRequest(t, http.MethodPut, chunkURL, token, update)
	var updated memoryapi.ChunkResponse
	decodeMemoryResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated.Chunk.Title != content.Title || updated.Chunk.Revision.Number != 2 {
		t.Fatalf("update status=%d response=%#v", response.StatusCode, updated)
	}

	response = memoryJSONRequest(t, http.MethodPut, chunkURL, token, update)
	var conflict memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &conflict)
	if response.StatusCode != http.StatusConflict || conflict.Error == nil || conflict.Error.Code != memoryService.ErrorCodeConflict {
		t.Fatalf("stale update status=%d response=%#v", response.StatusCode, conflict)
	}

	response = memoryJSONRequest(t, http.MethodPost, chunkURL+"/archive", token, memoryapi.LifecycleRequest{ExpectedRevision: 2})
	var archived memoryapi.ChunkResponse
	decodeMemoryResponse(t, response, &archived)
	if response.StatusCode != http.StatusOK || archived.Chunk.State != memory.ChunkStateArchived || archived.Chunk.Revision.Number != 3 {
		t.Fatalf("archive status=%d response=%#v", response.StatusCode, archived)
	}

	response = memoryJSONRequest(t, http.MethodDelete, chunkURL, token, memoryapi.DeleteRequest{ExpectedRevision: 3})
	var confirmation memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &confirmation)
	if response.StatusCode != http.StatusBadRequest || confirmation.Error == nil || confirmation.Error.Code != memoryService.ErrorCodeInvalid {
		t.Fatalf("unconfirmed delete status=%d response=%#v", response.StatusCode, confirmation)
	}

	response = memoryJSONRequest(t, http.MethodPost, chunkURL+"/restore", token, memoryapi.LifecycleRequest{ExpectedRevision: 3})
	var restored memoryapi.ChunkResponse
	decodeMemoryResponse(t, response, &restored)
	if response.StatusCode != http.StatusOK || restored.Chunk.State != memory.ChunkStateActive || restored.Chunk.Revision.Number != 4 {
		t.Fatalf("restore status=%d response=%#v", response.StatusCode, restored)
	}
	response = memoryJSONRequest(t, http.MethodPost, chunkURL+"/archive", token, memoryapi.LifecycleRequest{ExpectedRevision: 4})
	decodeMemoryResponse(t, response, &archived)
	if response.StatusCode != http.StatusOK || archived.Chunk.Revision.Number != 5 {
		t.Fatalf("second archive status=%d response=%#v", response.StatusCode, archived)
	}

	response = memoryJSONRequest(t, http.MethodDelete, chunkURL, token, memoryapi.DeleteRequest{ExpectedRevision: 5, Confirmed: true})
	var deleted memoryapi.DeleteResponse
	decodeMemoryResponse(t, response, &deleted)
	if response.StatusCode != http.StatusOK || !deleted.Deleted || deleted.Object.ID != string(created.Chunk.ID) {
		t.Fatalf("delete status=%d response=%#v", response.StatusCode, deleted)
	}
	response = memoryAPIRequest(t, http.MethodGet, chunkURL, token)
	defer closeMemoryHTTPResponse(t, response)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted status=%d", response.StatusCode)
	}
}

func TestMemoryEntryEvidenceAndHistoryReadsRespectChunkPolicy(t *testing.T) {
	ctrl := newTestController(t)
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	var denyReads atomic.Bool
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
		ChunkPolicy: memoryService.ChunkPolicyFunc(func(_ context.Context, actor memory.Actor, action memoryService.ChunkPolicyAction, _ memory.Chunk) error {
			if denyReads.Load() && actor.Kind == memory.ActorKindUser && action == memoryService.ChunkPolicyRead {
				return errors.New("hidden by test policy")
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("new Memory service: %v", err)
	}
	ctrl.SetMemoryService(service)
	chunk := createAPIChunk(t, service, "Entry owner", memory.Scope{Kind: memory.ScopeKindGlobal})
	evidence, err := service.CreateEvidence(context.Background(), memoryService.CreateEvidenceRequest{Evidence: memory.Evidence{
		Type: memory.EvidenceTypeObservation, Quality: memory.EvidenceQualityPrimary,
		Source: memory.Source{ID: "test:api", ContentHash: "sha256:api"},
	}})
	if err != nil {
		t.Fatalf("create evidence: %v", err)
	}
	entry, err := service.CreateEntry(context.Background(), memoryService.CreateEntryRequest{
		ChunkID: chunk.ID,
		Entry: memory.Entry{
			Kind: memory.EntryKindFact, Title: "API entry", Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
			Verification: memory.Verification{Status: memory.VerificationStatusUnverified},
			EvidenceIDs:  []memory.EvidenceID{evidence.Evidence.ID},
		},
	})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindMemoryTestDevice(t, srv)
	entriesURL := srv.URL() + memoryapi.EntryCollectionPath
	entryURL := srv.URL() + memoryapi.EntryPath(entry.Entry.ID)

	response := memoryAPIRequest(t, http.MethodGet, entriesURL+"?chunk_id="+string(chunk.ID)+"&scope=global", token)
	var entries memoryapi.EntryListResponse
	decodeMemoryResponse(t, response, &entries)
	if response.StatusCode != http.StatusOK || len(entries.Entries) != 1 || entries.Entries[0].ID != entry.Entry.ID {
		t.Fatalf("entry list status=%d response=%#v", response.StatusCode, entries)
	}
	response = memoryAPIRequest(t, http.MethodGet, entryURL, token)
	var got memoryapi.EntryResponse
	decodeMemoryResponse(t, response, &got)
	if response.StatusCode != http.StatusOK || got.Entry.ID != entry.Entry.ID || got.ETag == "" {
		t.Fatalf("entry get status=%d response=%#v", response.StatusCode, got)
	}
	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.EntryEvidencePath(entry.Entry.ID), token)
	var evidencePage memoryapi.EvidenceListResponse
	decodeMemoryResponse(t, response, &evidencePage)
	if response.StatusCode != http.StatusOK || len(evidencePage.Evidence) != 1 || evidencePage.Evidence[0].ID != evidence.Evidence.ID {
		t.Fatalf("evidence status=%d response=%#v", response.StatusCode, evidencePage)
	}
	for _, historyURL := range []string{
		srv.URL() + memoryapi.EntryHistoryPath(entry.Entry.ID),
		srv.URL() + memoryapi.ChunkHistoryPath(chunk.ID),
	} {
		response = memoryAPIRequest(t, http.MethodGet, historyURL, token)
		var history memoryapi.HistoryResponse
		decodeMemoryResponse(t, response, &history)
		if response.StatusCode != http.StatusOK || len(history.Revisions) != 1 {
			t.Fatalf("history %s status=%d response=%#v", historyURL, response.StatusCode, history)
		}
	}

	denyReads.Store(true)
	response = memoryAPIRequest(t, http.MethodGet, entriesURL, token)
	decodeMemoryResponse(t, response, &entries)
	if response.StatusCode != http.StatusOK || len(entries.Entries) != 0 {
		t.Fatalf("denied list status=%d response=%#v", response.StatusCode, entries)
	}
	for _, deniedURL := range []string{entryURL, srv.URL() + memoryapi.EntryEvidencePath(entry.Entry.ID), srv.URL() + memoryapi.EntryHistoryPath(entry.Entry.ID)} {
		response = memoryAPIRequest(t, http.MethodGet, deniedURL, token)
		var denied memoryapi.ErrorResponse
		decodeMemoryResponse(t, response, &denied)
		if response.StatusCode != http.StatusNotFound || denied.Error == nil || denied.Error.Code != memoryService.ErrorCodeNotFound {
			t.Fatalf("denied %s status=%d response=%#v", deniedURL, response.StatusCode, denied)
		}
	}
}

func TestMemoryEntryLifecycleWritesUseOptimisticRevisions(t *testing.T) {
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
	chunk := createAPIChunk(t, service, "Writable entries", memory.Scope{Kind: memory.ScopeKindGlobal})
	evidence, err := service.CreateEvidence(context.Background(), memoryService.CreateEvidenceRequest{Evidence: memory.Evidence{
		Type: memory.EvidenceTypeObservation, Quality: memory.EvidenceQualityPrimary,
		Source: memory.Source{ID: "test:lifecycle", ContentHash: "sha256:lifecycle"},
	}})
	if err != nil {
		t.Fatalf("create evidence: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindMemoryTestDevice(t, srv)
	content := testAPIEntryContent("Original entry")
	content.EvidenceIDs = []memory.EvidenceID{evidence.Evidence.ID}

	response := memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.EntryCollectionPath, token, memoryapi.EntryCreateRequest{
		ChunkID: chunk.ID, Entry: content,
	})
	var created memoryapi.EntryResponse
	decodeMemoryResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.Entry.Revision.Number != 1 || created.Entry.Revision.Actor.Kind != memory.ActorKindUser ||
		!strings.HasPrefix(created.Entry.Revision.Actor.ID, "device:") || response.Header.Get("Location") != memoryapi.EntryPath(created.Entry.ID) {
		t.Fatalf("create status=%d location=%q response=%#v", response.StatusCode, response.Header.Get("Location"), created)
	}
	entryURL := srv.URL() + memoryapi.EntryPath(created.Entry.ID)
	content.Title = "Updated entry"
	response = memoryJSONRequest(t, http.MethodPut, entryURL, token, memoryapi.EntryUpdateRequest{
		Entry: content, ExpectedRevision: 1, Reason: "improve wording",
	})
	var updated memoryapi.EntryResponse
	decodeMemoryResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated.Entry.Title != content.Title || updated.Entry.Revision.Number != 2 || response.Header.Get("ETag") == "" {
		t.Fatalf("update status=%d response=%#v", response.StatusCode, updated)
	}
	response = memoryJSONRequest(t, http.MethodPut, entryURL, token, memoryapi.EntryUpdateRequest{Entry: content, ExpectedRevision: 1})
	var conflict memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &conflict)
	if response.StatusCode != http.StatusConflict || conflict.Error == nil || conflict.Error.Code != memoryService.ErrorCodeConflict {
		t.Fatalf("stale update status=%d response=%#v", response.StatusCode, conflict)
	}

	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.EntryLifecyclePath(created.Entry.ID, "verify"), token, memoryapi.EntryVerifyRequest{
		ExpectedRevision: 2, Status: memory.VerificationStatusVerified,
		Method: "Reviewed source", EvidenceIDs: []memory.EvidenceID{evidence.Evidence.ID},
	})
	var verified memoryapi.EntryResponse
	decodeMemoryResponse(t, response, &verified)
	if response.StatusCode != http.StatusOK || verified.Entry.Revision.Number != 3 || verified.Entry.Verification.Status != memory.VerificationStatusVerified {
		t.Fatalf("verify status=%d response=%#v", response.StatusCode, verified)
	}

	replacementContent := testAPIEntryContent("Replacement entry")
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.EntryCollectionPath, token, memoryapi.EntryCreateRequest{
		ChunkID: chunk.ID, Entry: replacementContent,
	})
	var replacement memoryapi.EntryResponse
	decodeMemoryResponse(t, response, &replacement)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("replacement create status=%d response=%#v", response.StatusCode, replacement)
	}
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.EntryLifecyclePath(created.Entry.ID, "supersede"), token, memoryapi.EntrySupersedeRequest{
		ReplacementEntryID: replacement.Entry.ID, ExpectedRevision: 3, Reason: "replace old guidance",
	})
	var superseded memoryapi.EntryResponse
	decodeMemoryResponse(t, response, &superseded)
	if response.StatusCode != http.StatusOK || superseded.Entry.State != memory.EntryStateSuperseded || superseded.Entry.Revision.Number != 4 ||
		superseded.Replacement == nil || superseded.Replacement.ID != replacement.Entry.ID {
		t.Fatalf("supersede status=%d response=%#v", response.StatusCode, superseded)
	}

	disposableContent := testAPIEntryContent("Disposable entry")
	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.EntryCollectionPath, token, memoryapi.EntryCreateRequest{
		ChunkID: chunk.ID, Entry: disposableContent,
	})
	var disposable memoryapi.EntryResponse
	decodeMemoryResponse(t, response, &disposable)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("disposable create status=%d response=%#v", response.StatusCode, disposable)
	}
	disposableURL := srv.URL() + memoryapi.EntryPath(disposable.Entry.ID)
	for _, lifecycle := range []struct {
		action   string
		revision uint64
		state    memory.EntryState
	}{
		{action: "archive", revision: 1, state: memory.EntryStateArchived},
		{action: "restore", revision: 2, state: memory.EntryStateActive},
		{action: "archive", revision: 3, state: memory.EntryStateArchived},
	} {
		response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.EntryLifecyclePath(disposable.Entry.ID, lifecycle.action), token, memoryapi.LifecycleRequest{
			ExpectedRevision: lifecycle.revision,
		})
		var changed memoryapi.EntryResponse
		decodeMemoryResponse(t, response, &changed)
		if response.StatusCode != http.StatusOK || changed.Entry.State != lifecycle.state || changed.Entry.Revision.Number != lifecycle.revision+1 {
			t.Fatalf("%s status=%d response=%#v", lifecycle.action, response.StatusCode, changed)
		}
	}
	response = memoryJSONRequest(t, http.MethodDelete, disposableURL, token, memoryapi.EntryDeleteRequest{ExpectedRevision: 4})
	var confirmation memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &confirmation)
	if response.StatusCode != http.StatusBadRequest || confirmation.Error == nil || confirmation.Error.Code != memoryService.ErrorCodeInvalid {
		t.Fatalf("unconfirmed delete status=%d response=%#v", response.StatusCode, confirmation)
	}
	response = memoryJSONRequest(t, http.MethodDelete, disposableURL, token, memoryapi.EntryDeleteRequest{ExpectedRevision: 4, Confirmed: true})
	var deleted memoryapi.DeleteResponse
	decodeMemoryResponse(t, response, &deleted)
	if response.StatusCode != http.StatusOK || !deleted.Deleted || deleted.Object.ID != string(disposable.Entry.ID) {
		t.Fatalf("delete status=%d response=%#v", response.StatusCode, deleted)
	}
	response = memoryAPIRequest(t, http.MethodGet, disposableURL, token)
	defer closeMemoryHTTPResponse(t, response)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted status=%d", response.StatusCode)
	}
}

func testAPIEntryContent(title string) memoryapi.EntryContent {
	return memoryapi.EntryContent{
		Kind: memory.EntryKindFact, Title: title, Summary: "API lifecycle test",
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Confidence: 0.8,
	}
}

func createAPIChunk(t *testing.T, service *memoryService.Service, title string, scope memory.Scope) memory.Chunk {
	t.Helper()
	created, err := service.CreateChunk(context.Background(), memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: title, Kind: memory.ChunkKindReference, Scope: scope,
	}})
	if err != nil {
		t.Fatalf("create chunk %q: %v", title, err)
	}
	return created.Chunk
}

func bindMemoryTestDevice(t *testing.T, server *Server) string {
	t.Helper()
	invitation, err := server.devices.CreateInvitation()
	if err != nil {
		t.Fatalf("create device invitation: %v", err)
	}
	binding, err := server.devices.Bind(invitation.Code, deviceauth.DeviceInfo{Name: "Memory API test"})
	if err != nil {
		t.Fatalf("bind device: %v", err)
	}
	return binding.Token
}

func memoryAPIRequest(t *testing.T, method, url, token string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Memory API request: %v", err)
	}
	return response
}

func memoryJSONRequest(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode Memory request: %v", err)
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Memory API request: %v", err)
	}
	return response
}

func decodeMemoryResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer closeMemoryHTTPResponse(t, response)
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode Memory response: %v", err)
	}
}
