package agent

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/knowledgetool"
)

func TestCodexAdditionalToolsIncludeKnowledge(t *testing.T) {
	if !slices.Contains(CodexAdditionalToolIDs(), tools.Knowledge) {
		t.Fatal("Codex additional tools do not include Knowledge")
	}
}

func TestCodexKnowledgeDefinitionMatchesKoderRuntimeContract(t *testing.T) {
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		ToolPolicy: knowledgeService.ToolOfferPolicyFunc(func(context.Context, knowledge.Actor, knowledgeService.ToolOffer) (knowledgeService.ToolOffer, error) {
			return knowledgeService.ToolOffer{
				Actions:    []string{"search", "get", "history"},
				ScopeKinds: []knowledge.ScopeKind{knowledge.ScopeKindProject, knowledge.ScopeKindEnvironment},
			}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{
		ChatID:   "01a01688-fc5d-7f7d-8bb8-de244977fee1",
		Services: knowledgetool.RuntimeService(service),
	}
	koderDefinition, ok := tools.DefinitionFor(tools.Knowledge, runtime)
	if !ok {
		t.Fatal("Koder Knowledge definition is unavailable")
	}
	codexDefinition, ok := codexAdditionalToolDefinition(tools.Knowledge, runtime)
	if !ok {
		t.Fatal("Codex Knowledge definition is unavailable")
	}
	if codexDefinition.Type != "function" || codexDefinition.Name != koderDefinition.Function.Name ||
		codexDefinition.Description != koderDefinition.Function.Description ||
		!bytes.Equal(codexDefinition.InputSchema, koderDefinition.Function.Parameters) {
		t.Fatalf("Codex definition diverged from Koder: codex=%#v koder=%#v", codexDefinition, koderDefinition)
	}
}

func TestCodexKnowledgeDefinitionHonorsRuntimeAvailability(t *testing.T) {
	if _, ok := codexAdditionalToolDefinition(tools.Knowledge, tools.Runtime{}); ok {
		t.Fatal("Codex must not offer Knowledge without the runtime service")
	}
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled := tools.Runtime{
		Services:     knowledgetool.RuntimeService(service),
		AllowedTools: map[tools.ID]bool{tools.Knowledge: false},
	}
	if _, ok := codexAdditionalToolDefinition(tools.Knowledge, disabled); ok {
		t.Fatal("Codex must not offer disabled Knowledge")
	}
}
