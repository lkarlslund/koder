package webui

import (
	"context"
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

type deadlineChunkListStore struct {
	*memoryBackend.Store
	canceled chan struct{}
}

func (s *deadlineChunkListStore) ListChunks(ctx context.Context, _ memoryStoreAPI.ChunkListRequest) (memoryStoreAPI.ChunkPage, error) {
	<-ctx.Done()
	close(s.canceled)
	return memoryStoreAPI.ChunkPage{}, ctx.Err()
}

func TestMemoryAPIEnforcesAuditLimitsAndDeadlines(t *testing.T) {
	ctrl := newTestController(t)
	store := &deadlineChunkListStore{Store: memoryBackend.New(), canceled: make(chan struct{})}
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
	if srv.server.ReadHeaderTimeout <= 0 || srv.server.IdleTimeout <= 0 || srv.server.MaxHeaderBytes != 64<<10 {
		t.Fatalf("HTTP server limits = header %s idle %s max %d", srv.server.ReadHeaderTimeout, srv.server.IdleTimeout, srv.server.MaxHeaderBytes)
	}
	token := bindMemoryTestDevice(t, srv)

	mutations, unsubscribe := service.SubscribeMutations(1)
	defer unsubscribe()
	response := memoryJSONRequest(t, http.MethodPost, srv.URL()+memoryapi.ChunkCollectionPath, token, memoryapi.ChunkCreateRequest{
		Chunk: memoryapi.ChunkContent{
			Title: "Audited request", Kind: memory.ChunkKindReference,
			Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
		},
	})
	var created memoryapi.ChunkResponse
	decodeMemoryResponse(t, response, &created)
	auditID := response.Header.Get("X-Koder-Audit-ID")
	if response.StatusCode != http.StatusCreated || auditID == "" || response.Header.Get("X-Koder-Request-ID") != auditID || created.RequestID != auditID {
		t.Fatalf("create status=%d headers=%v response=%#v", response.StatusCode, response.Header, created)
	}
	select {
	case mutation := <-mutations:
		if mutation.AuditID != auditID || mutation.Object.ID != string(created.Chunk.ID) {
			t.Fatalf("mutation audit correlation = %#v, want %q", mutation, auditID)
		}
	default:
		t.Fatal("audited create did not publish a mutation")
	}

	srv.memoryRequestTimeout = 20 * time.Millisecond
	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.ChunkCollectionPath, token)
	assertMemoryAPIError(t, response, http.StatusServiceUnavailable, memoryService.ErrorCodeUnavailable)
	select {
	case <-store.canceled:
	default:
		t.Fatal("request deadline did not cancel the store operation")
	}

	response = memoryAPIRequest(t, http.MethodGet, srv.URL()+memoryapi.OperationalStatusPath+"?q="+strings.Repeat("x", maxMemoryRequestQuery), token)
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)

	request, err := http.NewRequest(http.MethodPost, srv.URL()+memoryapi.IndexRebuildPath, strings.NewReader(strings.Repeat("x", maxMemoryRequestBody+1)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("oversized Memory request: %v", err)
	}
	assertMemoryAPIError(t, response, http.StatusRequestEntityTooLarge, memoryService.ErrorCodeInvalid)

	request, err = http.NewRequest(http.MethodGet, srv.URL()+memoryapi.OperationalStatusPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("x", maxMemoryAuthorization))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("oversized Memory authorization: %v", err)
	}
	assertMemoryAPIError(t, response, http.StatusBadRequest, memoryService.ErrorCodeInvalid)
}
