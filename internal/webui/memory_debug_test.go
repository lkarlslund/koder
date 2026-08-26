package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestMemoryDebugReportsAuthorizedSanitizedOperations(t *testing.T) {
	ctrl := newTestController(t)
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: backend,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetMemoryService(service)
	if _, err := service.SearchLexical(context.Background(), memoryService.LexicalSearchRequest{
		Query: "private diagnostic sentinel query",
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true, Debug: debugsrv.NewRecorder()})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(srv.URL() + "/debug/memory")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var result memoryDebugResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode response: %v: %s", err, body)
	}
	if response.StatusCode != http.StatusOK || result.Status.Store.Backend != "memory" ||
		len(result.Status.Operations.Operations) != 1 || result.Status.Operations.Operations[0].Operation != "search" {
		t.Fatalf("GET /debug/memory status=%d result=%#v", response.StatusCode, result)
	}
	for _, private := range []string{"private diagnostic sentinel query", "system:test"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("diagnostics disclosed %q: %s", private, body)
		}
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("diagnostic security headers = %#v", response.Header)
	}
}

func TestMemoryDebugEnforcesOperationalPolicyAndMethod(t *testing.T) {
	ctrl := newTestController(t)
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: backend,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}),
		Operational: memoryService.OperationalPolicyFunc(func(context.Context, memory.Actor, memoryService.OperationalAction) error {
			return errors.New("private policy reason")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl.SetMemoryService(service)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true, Debug: debugsrv.NewRecorder()})
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.Get(srv.URL() + "/debug/memory")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusForbidden || strings.Contains(string(body), "private policy reason") {
		t.Fatalf("policy response status=%d body=%s", response.StatusCode, body)
	}

	request, err := http.NewRequest(http.MethodPost, srv.URL()+"/debug/memory", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Allow") != http.MethodGet {
		t.Fatalf("POST /debug/memory status=%d headers=%#v", response.StatusCode, response.Header)
	}
}
