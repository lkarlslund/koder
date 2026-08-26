package toolruntime

import (
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestRuntimeIncludesConfiguredMemoryService(t *testing.T) {
	service, err := memoryService.New(memoryService.Config{
		Store: memoryBackend.New(),
		Actor: memoryService.ContextActorSource(memory.Actor{
			Kind: memory.ActorKindSystem,
			ID:   "system:test",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := New(Config{})
	runtime.SetMemoryService(service)

	got := runtime.Runtime(domain.Session{}, domain.Chat{})
	if got.Services["memory"] != service {
		t.Fatalf("memory runtime service = %T %v, want configured service", got.Services["memory"], got.Services["memory"])
	}

	runtime.SetMemoryService(nil)
	if _, exists := runtime.Runtime(domain.Session{}, domain.Chat{}).Services["memory"]; exists {
		t.Fatal("nil Memory service must remove the runtime capability")
	}
}

func TestRuntimePropagatesSessionKind(t *testing.T) {
	runtime := New(Config{})
	got := runtime.Runtime(domain.Session{Kind: domain.SessionKindQuick}, domain.Chat{})
	if got.SessionKind != domain.SessionKindQuick {
		t.Fatalf("session kind = %v, want quick", got.SessionKind)
	}
}
