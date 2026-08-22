package webui

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestKnowledgePackageAPIRequiresAuthenticationAndSeparatesPreviewStageActivation(t *testing.T) {
	archive, chunkID := exportKnowledgePackageFixture(t)
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store, Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetKnowledgeService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	token := bindKnowledgeTestDevice(t, srv)

	response := knowledgePackageRequest(t, http.MethodPost, srv.URL()+knowledgeapi.PackagePreviewPath, "", knowledgeapi.PackageMediaType, archive)
	if response.StatusCode != http.StatusUnauthorized {
		response.Body.Close()
		t.Fatalf("unauthenticated preview status = %d", response.StatusCode)
	}
	response.Body.Close()

	response = knowledgePackageRequest(t, http.MethodPost, srv.URL()+knowledgeapi.PackagePreviewPath, token, knowledgeapi.PackageMediaType, archive)
	var preview knowledgeapi.PackagePreviewResponse
	decodeKnowledgeResponse(t, response, &preview)
	if response.StatusCode != http.StatusOK || preview.Preview.ChunkID != chunkID || !preview.Preview.ReadyToStage {
		t.Fatalf("preview status=%d response=%#v", response.StatusCode, preview)
	}
	if page, err := service.ListChunks(context.Background(), knowledgeStore.ChunkListRequest{}); err != nil || len(page.Chunks) != 0 {
		t.Fatalf("preview wrote chunks: page=%#v err=%v", page, err)
	}

	response = knowledgePackageRequest(t, http.MethodPost, srv.URL()+knowledgeapi.PackageStagePath, token, knowledgeapi.PackageMediaType, archive)
	var staged knowledgeapi.PackageStageResponse
	decodeKnowledgeResponse(t, response, &staged)
	if response.StatusCode != http.StatusCreated || staged.Stage == nil || staged.Stage.ID == "" || staged.Stage.ChunkID != chunkID {
		t.Fatalf("stage status=%d response=%#v", response.StatusCode, staged)
	}
	if page, err := service.ListChunks(context.Background(), knowledgeStore.ChunkListRequest{}); err != nil || len(page.Chunks) != 0 {
		t.Fatalf("stage wrote chunks: page=%#v err=%v", page, err)
	}

	activateURL := srv.URL() + knowledgeapi.PackageActivatePath(staged.Stage.ID)
	response = knowledgePackageRequest(t, http.MethodPost, activateURL, srv.knowledgeBrowserToken, "", nil)
	var wrongOwner knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &wrongOwner)
	if response.StatusCode != http.StatusNotFound || wrongOwner.Error == nil || wrongOwner.Error.Code != knowledgeService.ErrorCodeNotFound {
		t.Fatalf("wrong-owner activation status=%d response=%#v", response.StatusCode, wrongOwner)
	}

	response = knowledgePackageRequest(t, http.MethodPost, activateURL, token, "", nil)
	var activated knowledgeapi.PackageActivationResponse
	decodeKnowledgeResponse(t, response, &activated)
	if response.StatusCode != http.StatusOK || activated.Result.ChunkID != chunkID || activated.Result.Added.Additions == 0 {
		t.Fatalf("activation status=%d response=%#v", response.StatusCode, activated)
	}
	record, err := service.Get(context.Background(), knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(chunkID)})
	if err != nil || record.Chunk == nil {
		t.Fatalf("activated chunk = %#v err=%v", record, err)
	}
}

func TestKnowledgePackageAPIExportsPortableArchiveAndRejectsInvalidTransport(t *testing.T) {
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store, Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	chunk := createAPIChunk(t, service, "Export through API", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	ctrl.SetKnowledgeService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	token := bindKnowledgeTestDevice(t, srv)

	response := knowledgePackageRequest(t, http.MethodGet, srv.URL()+knowledgeapi.PackageExportPath(chunk.ID), token, "", nil)
	archive, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != knowledgeapi.PackageMediaType ||
		!strings.Contains(response.Header.Get("Content-Disposition"), ".kknowledge") || response.Header.Get("ETag") == "" {
		t.Fatalf("export status=%d headers=%v", response.StatusCode, response.Header)
	}
	validated, err := service.ValidateImportArchive(context.Background(), archive)
	if err != nil || validated.Manifest.Chunk.ID != string(chunk.ID) {
		t.Fatalf("exported archive chunk=%q err=%v", validated.Manifest.Chunk.ID, err)
	}

	response = knowledgePackageRequest(t, http.MethodPost, srv.URL()+knowledgeapi.PackagePreviewPath, token, "text/plain", []byte("not a package"))
	var invalid knowledgeapi.ErrorResponse
	decodeKnowledgeResponse(t, response, &invalid)
	if response.StatusCode != http.StatusUnsupportedMediaType || invalid.Error == nil || invalid.Error.Code != knowledgeService.ErrorCodeInvalid {
		t.Fatalf("unsupported media status=%d response=%#v", response.StatusCode, invalid)
	}
	response = knowledgePackageRequest(t, http.MethodGet, srv.URL()+knowledgeapi.PackagePreviewPath, token, "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Allow") != http.MethodPost {
		t.Fatalf("preview method status=%d allow=%q", response.StatusCode, response.Header.Get("Allow"))
	}
}

func TestKnowledgePackageAPIRequiresPersonalExportOptIn(t *testing.T) {
	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store, Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnsurePersonalChunk(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctrl.SetKnowledgeService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	token := bindKnowledgeTestDevice(t, srv)
	exportURL := srv.URL() + knowledgeapi.PackageExportPath(knowledgeService.PersonalMeChunkID)

	response := knowledgePackageRequest(t, http.MethodGet, exportURL, token, "", nil)
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)

	response = knowledgePackageRequest(t, http.MethodGet, exportURL+"?include_personal=true", token, "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != knowledgeapi.PackageMediaType {
		t.Fatalf("personal export with opt-in status=%d headers=%v", response.StatusCode, response.Header)
	}

	response = knowledgePackageRequest(t, http.MethodGet, exportURL+"?unexpected=true", token, "", nil)
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)
}

func exportKnowledgePackageFixture(t *testing.T) ([]byte, knowledge.ChunkID) {
	t.Helper()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store, Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:source"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	chunk := createAPIChunk(t, service, "Portable API package", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})
	var archive bytes.Buffer
	if _, err := service.ExportPackage(context.Background(), &archive, knowledgeService.ExportPackageRequest{ChunkID: chunk.ID}); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes(), chunk.ID
}

func knowledgePackageRequest(t *testing.T, method, url, token, mediaType string, body []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if mediaType != "" {
		request.Header.Set("Content-Type", mediaType)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
