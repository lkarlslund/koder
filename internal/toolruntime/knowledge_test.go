package toolruntime

import (
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestRuntimeIncludesConfiguredKnowledgeService(t *testing.T) {
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: memory.New(),
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{
			Kind: knowledge.ActorKindSystem,
			ID:   "system:test",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := New(Config{})
	runtime.SetKnowledgeService(service)

	got := runtime.Runtime(domain.Session{}, domain.Chat{})
	if got.Services["knowledge"] != service {
		t.Fatalf("knowledge runtime service = %T %v, want configured service", got.Services["knowledge"], got.Services["knowledge"])
	}

	runtime.SetKnowledgeService(nil)
	if _, exists := runtime.Runtime(domain.Session{}, domain.Chat{}).Services["knowledge"]; exists {
		t.Fatal("nil Knowledge service must remove the runtime capability")
	}
}

func TestRuntimePropagatesSessionKind(t *testing.T) {
	runtime := New(Config{})
	got := runtime.Runtime(domain.Session{Kind: domain.SessionKindQuick}, domain.Chat{})
	if got.SessionKind != domain.SessionKindQuick {
		t.Fatalf("session kind = %v, want quick", got.SessionKind)
	}
}
