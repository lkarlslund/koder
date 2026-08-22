package knowledgetool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestKnowledgeToolRegisteredAsSingleBuiltin(t *testing.T) {
	registered, ok := tools.Lookup(tools.Knowledge)
	if !ok {
		t.Fatal("knowledge tool is not registered")
	}
	if registered.ID() != domain.ToolKindKnowledge {
		t.Fatalf("registered ID = %q, want %q", registered.ID(), domain.ToolKindKnowledge)
	}
	if !tools.IsBuiltinID(tools.Knowledge) {
		t.Fatal("knowledge tool is not a built-in tool")
	}
	count := 0
	for _, toolID := range tools.RegisteredIDs() {
		if toolID == tools.Knowledge {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("knowledge registration count = %d, want 1", count)
	}
}

func TestKnowledgeToolShellIsNotOfferedToModels(t *testing.T) {
	if _, enabled := tools.DefinitionFor(tools.Knowledge, tools.Runtime{}); enabled {
		t.Fatal("unfinished knowledge actions must not be model-facing")
	}
	if _, enabled := tools.DefinitionFor(tools.Knowledge, tools.Runtime{Services: RuntimeService(newService(t))}); enabled {
		t.Fatal("unfinished knowledge actions must remain model-facing disabled")
	}
}

func TestKnowledgeToolRequiresRuntimeService(t *testing.T) {
	_, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{},
		Request: tools.Request{Tool: tools.Knowledge, Args: map[string]string{"action": "search"}},
	})
	if err == nil || !strings.Contains(err.Error(), "knowledge service is not configured") {
		t.Fatalf("Call() error = %v, want missing service", err)
	}
}

func newService(t *testing.T) *knowledgeService.Service {
	t.Helper()
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
	return service
}
