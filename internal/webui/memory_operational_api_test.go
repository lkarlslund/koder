package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryapi "github.com/lkarlslund/koder/internal/memory/api"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

type apiBlockingMaintenanceStore struct {
	*memoryBackend.Store
	started chan struct{}
	done    chan struct{}
}

type apiOperationalDetailsStore struct{ *memoryBackend.Store }

func (s *apiOperationalDetailsStore) OperationalDetails(context.Context) (memoryStoreAPI.OperationalDetails, error) {
	return memoryStoreAPI.OperationalDetails{
		Storage:    memoryStoreAPI.StorageDetails{PhysicalBytes: 2048, LiveBytes: 1024},
		Compaction: memoryStoreAPI.CompactionDetails{State: "idle", ReadAmplification: 1},
	}, nil
}

func (s *apiBlockingMaintenanceStore) RebuildIndexes(ctx context.Context) error {
	close(s.started)
	defer close(s.done)
	<-ctx.Done()
	return ctx.Err()
}

func TestMemoryOperationalStatusAndIndexRebuildAPI(t *testing.T) {
	ctrl := newTestController(t)
	store := &apiOperationalDetailsStore{Store: memoryBackend.New()}
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

	response := memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.OperationalStatusPath, "")
	var unauthorized memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &unauthorized)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d response=%#v", response.StatusCode, unauthorized)
	}

	token := bindMemoryTestDevice(t, srv)
	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.OperationalStatusPath, token)
	body, err := io.ReadAll(response.Body)
	closeMemoryHTTPResponse(t, response)
	if err != nil {
		t.Fatal(err)
	}
	var status memoryapi.OperationalStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if response.StatusCode != http.StatusOK || status.Status.Store.Backend != "memory" || status.Status.Store.IndexGeneration != 1 ||
		status.Status.Store.SchemaState != "current" || status.Status.Store.IndexState != "ready" || status.Status.Store.StorageState != "current" || status.Status.Store.Details == nil ||
		status.Status.Store.Details.Storage.PhysicalBytes != 2048 || status.Status.Store.Details.Compaction.State != "idle" ||
		!status.Status.MaintenanceAvailable || status.Status.LexicalIndex == nil || status.Status.MutationCheckpoint.StreamID == "" {
		t.Fatalf("status=%d response=%#v", response.StatusCode, status)
	}
	if strings.Contains(string(body), `"path"`) {
		t.Fatalf("operational response leaked backend path: %s", body)
	}

	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.IndexRebuildPath, token, memoryapi.IndexRebuildRequest{Index: "semantic"})
	var invalid memoryapi.ErrorResponse
	decodeMemoryResponse(t, response, &invalid)
	if response.StatusCode != http.StatusBadRequest || invalid.Error == nil || invalid.Error.Code != memoryService.ErrorCodeInvalid {
		t.Fatalf("invalid rebuild status=%d response=%#v", response.StatusCode, invalid)
	}

	response = memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.IndexRebuildPath, token, memoryapi.IndexRebuildRequest{Index: "lexical"})
	var accepted memoryapi.IndexRebuildResponse
	decodeMemoryResponse(t, response, &accepted)
	if response.StatusCode != http.StatusAccepted || !accepted.Result.Accepted || !accepted.Result.Status.Running || accepted.Result.Status.TargetGeneration != 2 {
		t.Fatalf("rebuild status=%d response=%#v", response.StatusCode, accepted)
	}

	deadline := time.Now().Add(time.Second)
	for {
		response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.OperationalStatusPath, token)
		decodeMemoryResponse(t, response, &status)
		if response.StatusCode == http.StatusOK && status.Status.Store.IndexGeneration == 2 && status.Status.LexicalIndex != nil && !status.Status.LexicalIndex.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rebuild did not complete: status=%d response=%#v", response.StatusCode, status)
		}
	}
}

func TestMemoryIndexRebuildCancelAPI(t *testing.T) {
	ctrl := newTestController(t)
	store := &apiBlockingMaintenanceStore{Store: memoryBackend.New(), started: make(chan struct{}), done: make(chan struct{})}
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
	response := memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.IndexRebuildPath, token, memoryapi.IndexRebuildRequest{Index: "lexical"})
	closeMemoryHTTPResponse(t, response)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("start rebuild status = %d", response.StatusCode)
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("rebuild did not start")
	}
	response = memoryAPIRequest(t, http.MethodDelete, srv.URL()+memoryapi.IndexRebuildPath, token)
	var canceled memoryapi.IndexRebuildCancelResponse
	decodeMemoryResponse(t, response, &canceled)
	if response.StatusCode != http.StatusAccepted || !canceled.Result.Accepted {
		t.Fatalf("cancel rebuild status=%d response=%#v", response.StatusCode, canceled)
	}
	select {
	case <-store.done:
	case <-time.After(time.Second):
		t.Fatal("canceled rebuild did not exit")
	}
}
