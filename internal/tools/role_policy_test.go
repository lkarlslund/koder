package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

func TestDefinitionsHideRoleForbiddenTools(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{ChatRole: chatrole.Decomposition})
	names := map[string]bool{}
	for _, def := range defs {
		names[def.Function.Name] = true
	}
	for _, name := range []string{string(domain.ToolKindBash), string(domain.ToolKindEdit), string(domain.ToolKindWrite), string(domain.ToolKindChatPoll), string(domain.ToolKindChatStart)} {
		if names[name] {
			t.Fatalf("decomposition definitions exposed forbidden tool %q", name)
		}
	}
	for _, name := range []string{string(domain.ToolKindRead), string(domain.ToolKindGrep), string(domain.ToolKindTodoAddItems), string(domain.ToolKindMilestoneUpdate)} {
		if !names[name] {
			t.Fatalf("decomposition definitions did not expose allowed tool %q", name)
		}
	}
}

func TestExecuteWithChatRejectsRoleForbiddenTool(t *testing.T) {
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	registry := tools.NewRegistry(t.TempDir())
	_, err = registry.ExecuteWithChat(context.Background(), st, "session-1", domain.Chat{
		ID:           "chat-1",
		WorkflowRole: chatrole.Decomposition,
	}, tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{"command": "echo no"},
	})
	if err == nil || !strings.Contains(err.Error(), "not available to decomposition chats") {
		t.Fatalf("expected role denial, got %v", err)
	}
}
