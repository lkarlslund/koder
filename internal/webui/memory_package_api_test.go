package webui

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestMemoryPackageAPIRequiresAuthenticationAndSeparatesPreviewStageActivation(t *testing.T) {
	archive, chunkID := exportMemoryPackageFixture(t)
	ctrl := newTestController(t)
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store, Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetMemoryService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	token := bindMemoryTestDevice(t, srv)

	response := memoryPackageRequest(t, http.MethodPost, srv.URL()+memoryapi.PackagePreviewPath, "", memoryapi.PackageMediaType, archive)
	if response.StatusCode != http.StatusUnauthorized {
		closeMemoryHTTPResponse(t, response)
		t.Fatalf("unauthenticated preview status = %d", response.StatusCode)
	}
	closeMemoryHTTPResponse(t, response)

	response = memoryPackageRequest(t, http.MethodPost, srv.URL()+memoryapi.PackagePreviewPath, token, memoryapi.PackageMediaType, archive)
	var preview memoryapi.PackagePreviewResponse
	decodeMemoryResponse(t, response, &preview)
	if response.StatusCode != http.StatusOK || preview.Preview.ChunkID != chunkID || !preview.Preview.ReadyToStage {
		t.Fatalf("preview status=%d response=%#v", response.StatusCode, preview)
	}
	if page, err := service.ListChunks(context.Background(), memoryStoreAPI.ChunkListRequest{}); err != nil || len(page.Chunks) != 0 {
		t.Fatalf("preview wrote chunks: page=%#v err=%v", page, err)
	}

	response = memoryPackageRequest(t, http.MethodPost, srv.URL()+memoryapi.PackageStagePath, token, memoryapi.PackageMediaType, archive)
	var staged memoryapi.PackageStageResponse
	decodeMemoryResponse(t, response, &staged)
	if response.StatusCode != http.StatusCreated || staged.Stage == nil || staged.Stage.ID == "" || staged.Stage.ChunkID != chunkID {
		t.Fatalf("stage status=%d response=%#v", response.StatusCode, staged)
	}
	if page, err := service.ListChunks(context.Background(), memoryStoreAPI.ChunkListRequest{}); err != nil || len(page.Chunks) != 0 {
		t.Fatalf("stage wrote chunks: page=%#v err=%v", page, err)
	}

	activateURL := srv.URL() + memoryapi.PackageActivatePath(staged.Stage.ID)
	response = memoryPackageRequest(t, http.MethodPost, activateURL, srv.memoryBrowserToken, "", nil)
	var wrongOwner memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &wrongOwner)
	if response.StatusCode != http.StatusNotFound || wrongOwner.Error == nil || wrongOwner.Error.Code != memoryService.ErrorCodeNotFound {
		t.Fatalf("wrong-owner activation status=%d response=%#v", response.StatusCode, wrongOwner)
	}

	response = memoryPackageRequest(t, http.MethodPost, activateURL, token, "", nil)
	var activated memoryapi.PackageActivationResponse
	decodeMemoryResponse(t, response, &activated)
	if response.StatusCode != http.StatusOK || activated.Result.ChunkID != chunkID || activated.Result.Added.Additions == 0 {
		t.Fatalf("activation status=%d response=%#v", response.StatusCode, activated)
	}
	record, err := service.Get(context.Background(), memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(chunkID)})
	if err != nil || record.Chunk == nil {
		t.Fatalf("activated chunk = %#v err=%v", record, err)
	}
}

func TestMemoryPackageAPIExportsPortableArchiveAndRejectsInvalidTransport(t *testing.T) {
	ctrl := newTestController(t)
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store, Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	chunk := createAPIChunk(t, service, "Export through API", memory.Scope{Kind: memory.ScopeKindGlobal})
	ctrl.SetMemoryService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	token := bindMemoryTestDevice(t, srv)

	response := memoryPackageRequest(t, http.MethodGet, srv.URL()+memoryapi.PackageExportPath(chunk.ID), token, "", nil)
	archive, err := io.ReadAll(response.Body)
	closeMemoryHTTPResponse(t, response)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != memoryapi.PackageMediaType ||
		!strings.Contains(response.Header.Get("Content-Disposition"), ".kmemory") || response.Header.Get("ETag") == "" {
		t.Fatalf("export status=%d headers=%v", response.StatusCode, response.Header)
	}
	validated, err := service.ValidateImportArchive(context.Background(), archive)
	if err != nil || validated.Manifest.Chunk.ID != string(chunk.ID) {
		t.Fatalf("exported archive chunk=%q err=%v", validated.Manifest.Chunk.ID, err)
	}

	response = memoryPackageRequest(t, http.MethodPost, srv.URL()+memoryapi.PackagePreviewPath, token, "text/plain", []byte("not a package"))
	var invalid memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &invalid)
	if response.StatusCode != http.StatusUnsupportedMediaType || invalid.Error == nil || invalid.Error.Code != memoryService.ErrorCodeInvalid {
		t.Fatalf("unsupported media status=%d response=%#v", response.StatusCode, invalid)
	}
	response = memoryPackageRequest(t, http.MethodGet, srv.URL()+memoryapi.PackagePreviewPath, token, "", nil)
	defer closeMemoryHTTPResponse(t, response)
	if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Allow") != http.MethodPost {
		t.Fatalf("preview method status=%d allow=%q", response.StatusCode, response.Header.Get("Allow"))
	}
}

func TestMemoryPackageAPIRequiresPersonalExportOptIn(t *testing.T) {
	ctrl := newTestController(t)
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store, Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnsurePersonalChunk(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctrl.SetMemoryService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	token := bindMemoryTestDevice(t, srv)
	exportURL := srv.URL() + memoryapi.PackageExportPath(memoryService.PersonalMeChunkID)

	response := memoryPackageRequest(t, http.MethodGet, exportURL, token, "", nil)
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)

	response = memoryPackageRequest(t, http.MethodGet, exportURL+"?include_personal=true", token, "", nil)
	defer closeMemoryHTTPResponse(t, response)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != memoryapi.PackageMediaType {
		t.Fatalf("personal export with opt-in status=%d headers=%v", response.StatusCode, response.Header)
	}

	response = memoryPackageRequest(t, http.MethodGet, exportURL+"?unexpected=true", token, "", nil)
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)
}

func exportMemoryPackageFixture(t *testing.T) ([]byte, memory.ChunkID) {
	t.Helper()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store, Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:source"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	chunk := createAPIChunk(t, service, "Portable API package", memory.Scope{Kind: memory.ScopeKindGlobal})
	var archive bytes.Buffer
	if _, err := service.ExportPackage(context.Background(), &archive, memoryService.ExportPackageRequest{ChunkID: chunk.ID}); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes(), chunk.ID
}

func memoryPackageRequest(t *testing.T, method, url, token, mediaType string, body []byte) *http.Response {
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
