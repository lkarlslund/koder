package webui

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeapi "github.com/lkarlslund/koder/internal/knowledge/api"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

type deadlineChunkListStore struct {
	*memory.Store
	canceled chan struct{}
}

func (s *deadlineChunkListStore) ListChunks(ctx context.Context, _ knowledgeStore.ChunkListRequest) (knowledgeStore.ChunkPage, error) {
	<-ctx.Done()
	close(s.canceled)
	return knowledgeStore.ChunkPage{}, ctx.Err()
}

func TestKnowledgeAPIEnforcesAuditLimitsAndDeadlines(t *testing.T) {
	ctrl := newTestController(t)
	store := &deadlineChunkListStore{Store: memory.New(), canceled: make(chan struct{})}
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
	if srv.server.ReadHeaderTimeout <= 0 || srv.server.IdleTimeout <= 0 || srv.server.MaxHeaderBytes != 64<<10 {
		t.Fatalf("HTTP server limits = header %s idle %s max %d", srv.server.ReadHeaderTimeout, srv.server.IdleTimeout, srv.server.MaxHeaderBytes)
	}
	token := bindKnowledgeTestDevice(t, srv)

	mutations, unsubscribe := service.SubscribeMutations(1)
	defer unsubscribe()
	response := knowledgeJSONRequest(t, http.MethodPost, srv.URL()+knowledgeapi.ChunkCollectionPath, token, knowledgeapi.ChunkCreateRequest{
		Chunk: knowledgeapi.ChunkContent{
			Title: "Audited request", Kind: knowledge.ChunkKindReference,
			Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		},
	})
	var created knowledgeapi.ChunkResponse
	decodeKnowledgeResponse(t, response, &created)
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

	srv.knowledgeRequestTimeout = 20 * time.Millisecond
	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.ChunkCollectionPath, token)
	assertKnowledgeAPIError(t, response, http.StatusServiceUnavailable, knowledgeService.ErrorCodeUnavailable)
	select {
	case <-store.canceled:
	default:
		t.Fatal("request deadline did not cancel the store operation")
	}

	response = knowledgeAPIRequest(t, http.MethodGet, srv.URL()+knowledgeapi.OperationalStatusPath+"?q="+strings.Repeat("x", maxKnowledgeRequestQuery), token)
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)

	request, err := http.NewRequest(http.MethodPost, srv.URL()+knowledgeapi.IndexRebuildPath, strings.NewReader(strings.Repeat("x", maxKnowledgeRequestBody+1)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("oversized Knowledge request: %v", err)
	}
	assertKnowledgeAPIError(t, response, http.StatusRequestEntityTooLarge, knowledgeService.ErrorCodeInvalid)

	request, err = http.NewRequest(http.MethodGet, srv.URL()+knowledgeapi.OperationalStatusPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("x", maxKnowledgeAuthorization))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("oversized Knowledge authorization: %v", err)
	}
	assertKnowledgeAPIError(t, response, http.StatusBadRequest, knowledgeService.ErrorCodeInvalid)
}
