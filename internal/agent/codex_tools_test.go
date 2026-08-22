package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
	"github.com/lkarlslund/koder/internal/provider"
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

func TestKoderAndCodexKnowledgeToolInteropFixtures(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "protocol", "knowledge", "v1", "testdata", "tool_interop.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion int    `json:"schema_version"`
		Tool          string `json:"tool"`
		Cases         []struct {
			Name        string            `json:"name"`
			CallID      string            `json:"call_id"`
			Arguments   json.RawMessage   `json:"arguments"`
			Normalized  map[string]string `json:"normalized"`
			ResultArray string            `json:"result_array"`
			ResultCount int               `json:"result_count"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	var canonical bytes.Buffer
	if err := json.Indent(&canonical, bytes.TrimSpace(data), "", "  "); err != nil {
		t.Fatal(err)
	}
	canonical.WriteByte('\n')
	if !bytes.Equal(data, canonical.Bytes()) {
		t.Fatal("tool interoperability fixture is not canonical pretty-printed JSON")
	}
	if fixture.SchemaVersion != 1 || fixture.Tool != tools.Knowledge.String() || len(fixture.Cases) == 0 {
		t.Fatalf("invalid interoperability fixture header: %#v", fixture)
	}

	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{
			Kind: knowledge.ActorKindSystem, ID: "system:interop",
		}),
		Now: func() time.Time { return time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{ChatID: "01a01688-fc5d-7f7d-8bb8-de244977fee1", Services: knowledgetool.RuntimeService(service)}
	koderDefinition, ok := tools.DefinitionFor(tools.Knowledge, runtime)
	if !ok {
		t.Fatal("Koder Knowledge definition is unavailable")
	}
	codexDefinition, ok := codexAdditionalToolDefinition(tools.Knowledge, runtime)
	if !ok || codexDefinition.Name != koderDefinition.Function.Name || !bytes.Equal(codexDefinition.InputSchema, koderDefinition.Function.Parameters) {
		t.Fatalf("Codex definition diverged from Koder: %#v", codexDefinition)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(codexDefinition.InputSchema))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("knowledge-tool.json", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("knowledge-tool.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			value, err := jsonschema.UnmarshalJSON(bytes.NewReader(test.Arguments))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(value); err != nil {
				t.Fatalf("fixture arguments violate shared tool schema: %v", err)
			}
			request, err := tools.ParseProviderCall(provider.ToolCall{
				ID: test.CallID, Type: "function", Function: provider.FunctionCall{
					Name: fixture.Tool, Arguments: string(test.Arguments),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if request.Tool != tools.Knowledge || request.ToolCallID != test.CallID || !maps.Equal(request.Args, test.Normalized) {
				t.Fatalf("normalized request = %#v, want %#v", request, test.Normalized)
			}
			result, err := tools.Call(context.Background(), tools.Options{Runtime: runtime, Request: request})
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal([]byte(result.Output), &payload); err != nil {
				t.Fatalf("decode shared result: %v", err)
			}
			var values []json.RawMessage
			if err := json.Unmarshal(payload[test.ResultArray], &values); err != nil || len(values) != test.ResultCount {
				t.Fatalf("result %q = %s, %v; want %d items", test.ResultArray, payload[test.ResultArray], err, test.ResultCount)
			}
		})
	}
}
