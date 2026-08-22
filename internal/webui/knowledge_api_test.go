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
	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestKnowledgeChunkReadAndListRequireAuthentication(t *testing.T) {
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
	global := createAPIChunk(t, service, "Global reference", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	createAPIChunk(t, service, "Koder project", knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: "koder"})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindKnowledgeTestDevice(t, srv)

	response := knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.RoutePrefix+"/chunks", "")
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") == "" {
		closeKnowledgeHTTPResponse(t, response)
		t.Fatalf("unauthenticated status=%d authenticate=%q", response.StatusCode, response.Header.Get("WWW-Authenticate"))
	}
	var unauthorized knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &unauthorized)
	if unauthorized.APIVersion != knowledgeapi.Version || unauthorized.Error == nil || unauthorized.Error.Code != knowledgeService.ErrorCodeForbidden {
		t.Fatalf("unauthorized response = %#v", unauthorized)
	}

	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.RoutePrefix+"/chunks?scope=global&limit=1", srv.knowledgeBrowserToken)
	if response.StatusCode != http.StatusOK {
		defer closeKnowledgeHTTPResponse(t, response)
		t.Fatalf("browser-authenticated list status = %d", response.StatusCode)
	}
	var browserListed knowledgeapi.ChunkListResponse
	decodeKnowledgeResponse(t, response, &browserListed)
	if len(browserListed.Chunks) != 1 || browserListed.Chunks[0].ID != global.ID {
		t.Fatalf("browser-authenticated list response = %#v", browserListed)
	}

	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.RoutePrefix+"/chunks?scope=global&limit=1", token)
	if response.StatusCode != http.StatusOK {
		defer closeKnowledgeHTTPResponse(t, response)
		t.Fatalf("list status = %d", response.StatusCode)
	}
	var listed knowledgeapi.ChunkListResponse
	decodeKnowledgeResponse(t, response, &listed)
	if listed.APIVersion != knowledgeapi.Version || listed.Page.Limit != 1 || listed.Page.Returned != 1 ||
		len(listed.Chunks) != 1 || listed.Chunks[0].ID != global.ID {
		t.Fatalf("list response = %#v", listed)
	}

	chunkURL := srv.URL() + knowledgeapi.RoutePrefix + "/chunks/" + string(global.ID)
	response = knowledgeAPIRequest(t, http.MethodGet, chunkURL, token)
	if response.StatusCode != http.StatusOK {
		defer closeKnowledgeHTTPResponse(t, response)
		t.Fatalf("get status = %d", response.StatusCode)
	}
	etag := response.Header.Get("ETag")
	var got knowledgeapi.ChunkResponse
	decodeKnowledgeResponse(t, response, &got)
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
	defer closeKnowledgeHTTPResponse(t, response)
	if response.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional get status = %d", response.StatusCode)
	}
}

func TestKnowledgeChunkAPIHidesPolicyDeniedRecords(t *testing.T) {
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		ChunkPolicy: knowledgeService.ChunkPolicyFunc(func(_ context.Context, actor knowledge.Actor, action knowledgeService.ChunkPolicyAction, chunk knowledge.Chunk) error {
			if actor.Kind != knowledge.ActorKindUser || !strings.HasPrefix(actor.ID, "device:") || action != knowledgeService.ChunkPolicyRead {
				return nil
			}
			if chunk.Title == "Hidden" {
				return errors.New("private policy detail")
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("new Knowledge service: %v", err)
	}
	ctrl.SetKnowledgeService(service)
	hidden := createAPIChunk(t, service, "Hidden", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	visible := createAPIChunk(t, service, "Visible", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindKnowledgeTestDevice(t, srv)
	response := knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.RoutePrefix+"/chunks?sort=title", token)
	var listed knowledgeapi.ChunkListResponse
	decodeKnowledgeResponse(t, response, &listed)
	if response.StatusCode != http.StatusOK || len(listed.Chunks) != 1 || listed.Chunks[0].ID != visible.ID {
		t.Fatalf("policy-filtered list status=%d response=%#v", response.StatusCode, listed)
	}

	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.RoutePrefix+"/chunks/"+string(hidden.ID), token)
	var denied knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &denied)
	if response.StatusCode != http.StatusNotFound || denied.Error == nil || denied.Error.Code != knowledgeService.ErrorCodeNotFound {
		t.Fatalf("denied get status=%d response=%#v", response.StatusCode, denied)
	}
}

func TestKnowledgeChunkMutationLifecycleUsesOptimisticRevisions(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	token := bindKnowledgeTestDevice(t, srv)
	baseURL := srv.URL() + knowledgeapi.RoutePrefix + "/chunks"
	content := knowledgeapi.ChunkContent{
		Title: "API lifecycle", Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Visibility: knowledge.VisibilityPrivate,
	}

	response := knowledgeJSONRequest(t, http.MethodPost, baseURL, token, knowledgeapi.ChunkCreateRequest{Chunk: content})
	var created knowledgeapi.ChunkResponse
	decodeKnowledgeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.Chunk.ID == "" || created.Chunk.Revision.Number != 1 ||
		created.Chunk.Revision.Actor.Kind != knowledge.ActorKindUser || response.Header.Get("Location") == "" {
		t.Fatalf("create status=%d response=%#v", response.StatusCode, created)
	}
	chunkURL := baseURL + "/" + string(created.Chunk.ID)

	content.Title = "API lifecycle updated"
	update := knowledgeapi.ChunkUpdateRequest{Chunk: content, ExpectedRevision: 1, Reason: "test update"}
	response = knowledgeJSONRequest(t, http.MethodPut, chunkURL, token, update)
	var updated knowledgeapi.ChunkResponse
	decodeKnowledgeResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated.Chunk.Title != content.Title || updated.Chunk.Revision.Number != 2 {
		t.Fatalf("update status=%d response=%#v", response.StatusCode, updated)
	}

	response = knowledgeJSONRequest(t, http.MethodPut, chunkURL, token, update)
	var conflict knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &conflict)
	if response.StatusCode != http.StatusConflict || conflict.Error == nil || conflict.Error.Code != knowledgeService.ErrorCodeConflict {
		t.Fatalf("stale update status=%d response=%#v", response.StatusCode, conflict)
	}

	response = knowledgeJSONRequest(t, http.MethodPost, chunkURL+"/archive", token, knowledgeapi.LifecycleRequest{ExpectedRevision: 2})
	var archived knowledgeapi.ChunkResponse
	decodeKnowledgeResponse(t, response, &archived)
	if response.StatusCode != http.StatusOK || archived.Chunk.State != knowledge.ChunkStateArchived || archived.Chunk.Revision.Number != 3 {
		t.Fatalf("archive status=%d response=%#v", response.StatusCode, archived)
	}

	response = knowledgeJSONRequest(t, http.MethodDelete, chunkURL, token, knowledgeapi.DeleteRequest{ExpectedRevision: 3})
	var confirmation knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &confirmation)
	if response.StatusCode != http.StatusBadRequest || confirmation.Error == nil || confirmation.Error.Code != knowledgeService.ErrorCodeInvalid {
		t.Fatalf("unconfirmed delete status=%d response=%#v", response.StatusCode, confirmation)
	}

	response = knowledgeJSONRequest(t, http.MethodPost, chunkURL+"/restore", token, knowledgeapi.LifecycleRequest{ExpectedRevision: 3})
	var restored knowledgeapi.ChunkResponse
	decodeKnowledgeResponse(t, response, &restored)
	if response.StatusCode != http.StatusOK || restored.Chunk.State != knowledge.ChunkStateActive || restored.Chunk.Revision.Number != 4 {
		t.Fatalf("restore status=%d response=%#v", response.StatusCode, restored)
	}
	response = knowledgeJSONRequest(t, http.MethodPost, chunkURL+"/archive", token, knowledgeapi.LifecycleRequest{ExpectedRevision: 4})
	decodeKnowledgeResponse(t, response, &archived)
	if response.StatusCode != http.StatusOK || archived.Chunk.Revision.Number != 5 {
		t.Fatalf("second archive status=%d response=%#v", response.StatusCode, archived)
	}

	response = knowledgeJSONRequest(t, http.MethodDelete, chunkURL, token, knowledgeapi.DeleteRequest{ExpectedRevision: 5, Confirmed: true})
	var deleted knowledgeapi.DeleteResponse
	decodeKnowledgeResponse(t, response, &deleted)
	if response.StatusCode != http.StatusOK || !deleted.Deleted || deleted.Object.ID != string(created.Chunk.ID) {
		t.Fatalf("delete status=%d response=%#v", response.StatusCode, deleted)
	}
	response = knowledgeAPIRequest(t, http.MethodGet, chunkURL, token)
	defer closeKnowledgeHTTPResponse(t, response)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted status=%d", response.StatusCode)
	}
}

func TestKnowledgeEntryEvidenceAndHistoryReadsRespectChunkPolicy(t *testing.T) {
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	var denyReads atomic.Bool
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		ChunkPolicy: knowledgeService.ChunkPolicyFunc(func(_ context.Context, actor knowledge.Actor, action knowledgeService.ChunkPolicyAction, _ knowledge.Chunk) error {
			if denyReads.Load() && actor.Kind == knowledge.ActorKindUser && action == knowledgeService.ChunkPolicyRead {
				return errors.New("hidden by test policy")
			}
			return nil
		}),
	})
	if err != nil {
		t.Fatalf("new Knowledge service: %v", err)
	}
	ctrl.SetKnowledgeService(service)
	chunk := createAPIChunk(t, service, "Entry owner", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	evidence, err := service.CreateEvidence(context.Background(), knowledgeService.CreateEvidenceRequest{Evidence: knowledge.Evidence{
		Type: knowledge.EvidenceTypeObservation, Quality: knowledge.EvidenceQualityPrimary,
		Source: knowledge.Source{ID: "test:api", ContentHash: "sha256:api"},
	}})
	if err != nil {
		t.Fatalf("create evidence: %v", err)
	}
	entry, err := service.CreateEntry(context.Background(), knowledgeService.CreateEntryRequest{
		ChunkID: chunk.ID,
		Entry: knowledge.Entry{
			Kind: knowledge.EntryKindFact, Title: "API entry", Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
			Verification: knowledge.Verification{Status: knowledge.VerificationStatusUnverified},
			EvidenceIDs:  []knowledge.EvidenceID{evidence.Evidence.ID},
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
	token := bindKnowledgeTestDevice(t, srv)
	entriesURL := srv.URL() + knowledgeapi.EntryCollectionPath
	entryURL := srv.URL() + knowledgeapi.EntryPath(entry.Entry.ID)

	response := knowledgeAPIRequest(t, http.MethodGet, entriesURL+"?chunk_id="+string(chunk.ID)+"&scope=global", token)
	var entries knowledgeapi.EntryListResponse
	decodeKnowledgeResponse(t, response, &entries)
	if response.StatusCode != http.StatusOK || len(entries.Entries) != 1 || entries.Entries[0].ID != entry.Entry.ID {
		t.Fatalf("entry list status=%d response=%#v", response.StatusCode, entries)
	}
	response = knowledgeAPIRequest(t, http.MethodGet, entryURL, token)
	var got knowledgeapi.EntryResponse
	decodeKnowledgeResponse(t, response, &got)
	if response.StatusCode != http.StatusOK || got.Entry.ID != entry.Entry.ID || got.ETag == "" {
		t.Fatalf("entry get status=%d response=%#v", response.StatusCode, got)
	}
	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.EntryEvidencePath(entry.Entry.ID), token)
	var evidencePage knowledgeapi.EvidenceListResponse
	decodeKnowledgeResponse(t, response, &evidencePage)
	if response.StatusCode != http.StatusOK || len(evidencePage.Evidence) != 1 || evidencePage.Evidence[0].ID != evidence.Evidence.ID {
		t.Fatalf("evidence status=%d response=%#v", response.StatusCode, evidencePage)
	}
	for _, historyURL := range []string{
		srv.URL() + knowledgeapi.EntryHistoryPath(entry.Entry.ID),
		srv.URL() + knowledgeapi.ChunkHistoryPath(chunk.ID),
	} {
		response = knowledgeAPIRequest(t, http.MethodGet, historyURL, token)
		var history knowledgeapi.HistoryResponse
		decodeKnowledgeResponse(t, response, &history)
		if response.StatusCode != http.StatusOK || len(history.Revisions) != 1 {
			t.Fatalf("history %s status=%d response=%#v", historyURL, response.StatusCode, history)
		}
	}

	denyReads.Store(true)
	response = knowledgeAPIRequest(t, http.MethodGet, entriesURL, token)
	decodeKnowledgeResponse(t, response, &entries)
	if response.StatusCode != http.StatusOK || len(entries.Entries) != 0 {
		t.Fatalf("denied list status=%d response=%#v", response.StatusCode, entries)
	}
	for _, deniedURL := range []string{entryURL, srv.URL() + knowledgeapi.EntryEvidencePath(entry.Entry.ID), srv.URL() + knowledgeapi.EntryHistoryPath(entry.Entry.ID)} {
		response = knowledgeAPIRequest(t, http.MethodGet, deniedURL, token)
		var denied knowledgeapi.ErrorResponse
		decodeKnowledgeResponse(t, response, &denied)
		if response.StatusCode != http.StatusNotFound || denied.Error == nil || denied.Error.Code != knowledgeService.ErrorCodeNotFound {
			t.Fatalf("denied %s status=%d response=%#v", deniedURL, response.StatusCode, denied)
		}
	}
}

func TestKnowledgeEntryLifecycleWritesUseOptimisticRevisions(t *testing.T) {
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
	chunk := createAPIChunk(t, service, "Writable entries", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	evidence, err := service.CreateEvidence(context.Background(), knowledgeService.CreateEvidenceRequest{Evidence: knowledge.Evidence{
		Type: knowledge.EvidenceTypeObservation, Quality: knowledge.EvidenceQualityPrimary,
		Source: knowledge.Source{ID: "test:lifecycle", ContentHash: "sha256:lifecycle"},
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
	token := bindKnowledgeTestDevice(t, srv)
	content := testAPIEntryContent("Original entry")
	content.EvidenceIDs = []knowledge.EvidenceID{evidence.Evidence.ID}

	response := knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.EntryCollectionPath, token, knowledgeapi.EntryCreateRequest{
		ChunkID: chunk.ID, Entry: content,
	})
	var created knowledgeapi.EntryResponse
	decodeKnowledgeResponse(t, response, &created)
	if response.StatusCode != http.StatusCreated || created.Entry.Revision.Number != 1 || created.Entry.Revision.Actor.Kind != knowledge.ActorKindUser ||
		!strings.HasPrefix(created.Entry.Revision.Actor.ID, "device:") || response.Header.Get("Location") != knowledgeapi.EntryPath(created.Entry.ID) {
		t.Fatalf("create status=%d location=%q response=%#v", response.StatusCode, response.Header.Get("Location"), created)
	}
	entryURL := srv.URL() + knowledgeapi.EntryPath(created.Entry.ID)
	content.Title = "Updated entry"
	response = knowledgeJSONRequest(t, http.MethodPut, entryURL, token, knowledgeapi.EntryUpdateRequest{
		Entry: content, ExpectedRevision: 1, Reason: "improve wording",
	})
	var updated knowledgeapi.EntryResponse
	decodeKnowledgeResponse(t, response, &updated)
	if response.StatusCode != http.StatusOK || updated.Entry.Title != content.Title || updated.Entry.Revision.Number != 2 || response.Header.Get("ETag") == "" {
		t.Fatalf("update status=%d response=%#v", response.StatusCode, updated)
	}
	response = knowledgeJSONRequest(t, http.MethodPut, entryURL, token, knowledgeapi.EntryUpdateRequest{Entry: content, ExpectedRevision: 1})
	var conflict knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &conflict)
	if response.StatusCode != http.StatusConflict || conflict.Error == nil || conflict.Error.Code != knowledgeService.ErrorCodeConflict {
		t.Fatalf("stale update status=%d response=%#v", response.StatusCode, conflict)
	}

	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.EntryLifecyclePath(created.Entry.ID, "verify"), token, knowledgeapi.EntryVerifyRequest{
		ExpectedRevision: 2, Status: knowledge.VerificationStatusVerified,
		Method: "Reviewed source", EvidenceIDs: []knowledge.EvidenceID{evidence.Evidence.ID},
	})
	var verified knowledgeapi.EntryResponse
	decodeKnowledgeResponse(t, response, &verified)
	if response.StatusCode != http.StatusOK || verified.Entry.Revision.Number != 3 || verified.Entry.Verification.Status != knowledge.VerificationStatusVerified {
		t.Fatalf("verify status=%d response=%#v", response.StatusCode, verified)
	}

	replacementContent := testAPIEntryContent("Replacement entry")
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.EntryCollectionPath, token, knowledgeapi.EntryCreateRequest{
		ChunkID: chunk.ID, Entry: replacementContent,
	})
	var replacement knowledgeapi.EntryResponse
	decodeKnowledgeResponse(t, response, &replacement)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("replacement create status=%d response=%#v", response.StatusCode, replacement)
	}
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.EntryLifecyclePath(created.Entry.ID, "supersede"), token, knowledgeapi.EntrySupersedeRequest{
		ReplacementEntryID: replacement.Entry.ID, ExpectedRevision: 3, Reason: "replace old guidance",
	})
	var superseded knowledgeapi.EntryResponse
	decodeKnowledgeResponse(t, response, &superseded)
	if response.StatusCode != http.StatusOK || superseded.Entry.State != knowledge.EntryStateSuperseded || superseded.Entry.Revision.Number != 4 ||
		superseded.Replacement == nil || superseded.Replacement.ID != replacement.Entry.ID {
		t.Fatalf("supersede status=%d response=%#v", response.StatusCode, superseded)
	}

	disposableContent := testAPIEntryContent("Disposable entry")
	response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.EntryCollectionPath, token, knowledgeapi.EntryCreateRequest{
		ChunkID: chunk.ID, Entry: disposableContent,
	})
	var disposable knowledgeapi.EntryResponse
	decodeKnowledgeResponse(t, response, &disposable)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("disposable create status=%d response=%#v", response.StatusCode, disposable)
	}
	disposableURL := srv.URL() + knowledgeapi.EntryPath(disposable.Entry.ID)
	for _, lifecycle := range []struct {
		action   string
		revision uint64
		state    knowledge.EntryState
	}{
		{action: "archive", revision: 1, state: knowledge.EntryStateArchived},
		{action: "restore", revision: 2, state: knowledge.EntryStateActive},
		{action: "archive", revision: 3, state: knowledge.EntryStateArchived},
	} {
		response = knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.EntryLifecyclePath(disposable.Entry.ID, lifecycle.action), token, knowledgeapi.LifecycleRequest{
			ExpectedRevision: lifecycle.revision,
		})
		var changed knowledgeapi.EntryResponse
		decodeKnowledgeResponse(t, response, &changed)
		if response.StatusCode != http.StatusOK || changed.Entry.State != lifecycle.state || changed.Entry.Revision.Number != lifecycle.revision+1 {
			t.Fatalf("%s status=%d response=%#v", lifecycle.action, response.StatusCode, changed)
		}
	}
	response = knowledgeJSONRequest(t, http.MethodDelete, disposableURL, token, knowledgeapi.EntryDeleteRequest{ExpectedRevision: 4})
	var confirmation knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &confirmation)
	if response.StatusCode != http.StatusBadRequest || confirmation.Error == nil || confirmation.Error.Code != knowledgeService.ErrorCodeInvalid {
		t.Fatalf("unconfirmed delete status=%d response=%#v", response.StatusCode, confirmation)
	}
	response = knowledgeJSONRequest(t, http.MethodDelete, disposableURL, token, knowledgeapi.EntryDeleteRequest{ExpectedRevision: 4, Confirmed: true})
	var deleted knowledgeapi.DeleteResponse
	decodeKnowledgeResponse(t, response, &deleted)
	if response.StatusCode != http.StatusOK || !deleted.Deleted || deleted.Object.ID != string(disposable.Entry.ID) {
		t.Fatalf("delete status=%d response=%#v", response.StatusCode, deleted)
	}
	response = knowledgeAPIRequest(t, http.MethodGet, disposableURL, token)
	defer closeKnowledgeHTTPResponse(t, response)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted status=%d", response.StatusCode)
	}
}

func testAPIEntryContent(title string) knowledgeapi.EntryContent {
	return knowledgeapi.EntryContent{
		Kind: knowledge.EntryKindFact, Title: title, Summary: "API lifecycle test",
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Confidence: 0.8,
	}
}

func createAPIChunk(t *testing.T, service *knowledgeService.Service, title string, scope knowledge.Scope) knowledge.Chunk {
	t.Helper()
	created, err := service.CreateChunk(context.Background(), knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: title, Kind: knowledge.ChunkKindReference, Scope: scope,
	}})
	if err != nil {
		t.Fatalf("create chunk %q: %v", title, err)
	}
	return created.Chunk
}

func bindKnowledgeTestDevice(t *testing.T, server *Server) string {
	t.Helper()
	invitation, err := server.devices.CreateInvitation()
	if err != nil {
		t.Fatalf("create device invitation: %v", err)
	}
	binding, err := server.devices.Bind(invitation.Code, deviceauth.DeviceInfo{Name: "Knowledge API test"})
	if err != nil {
		t.Fatalf("bind device: %v", err)
	}
	return binding.Token
}

func knowledgeAPIRequest(t *testing.T, method, url, token string) *http.Response {
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
		t.Fatalf("Knowledge API request: %v", err)
	}
	return response
}

func knowledgeJSONRequest(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode Knowledge request: %v", err)
	}
	request, err := http.NewRequest(method, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Knowledge API request: %v", err)
	}
	return response
}

func decodeKnowledgeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer closeKnowledgeHTTPResponse(t, response)
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode Knowledge response: %v", err)
	}
}
