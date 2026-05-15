package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

func TestRoleAllowsTool(t *testing.T) {
	tests := []struct {
		name string
		role domain.WorkflowRole
		tool domain.ToolKind
		want bool
	}{
		{"decomposition read", domain.WorkflowRoleDecomposition, domain.ToolKindRead, true},
		{"decomposition add todos", domain.WorkflowRoleDecomposition, domain.ToolKindTodoAddItems, true},
		{"decomposition rejects bash", domain.WorkflowRoleDecomposition, domain.ToolKindBash, false},
		{"decomposition rejects chat poll", domain.WorkflowRoleDecomposition, domain.ToolKindChatPoll, false},
		{"execution allows edit", domain.WorkflowRoleExecution, domain.ToolKindEdit, true},
		{"execution rejects chat start", domain.WorkflowRoleExecution, domain.ToolKindChatStartExec, false},
		{"execution rejects milestone add", domain.WorkflowRoleExecution, domain.ToolKindMilestoneAdd, false},
		{"execution allows milestone update", domain.WorkflowRoleExecution, domain.ToolKindMilestoneUpdate, true},
		{"orchestrator allows chat poll", domain.WorkflowRoleOrchestrator, domain.ToolKindChatPoll, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tools.RoleAllowsTool(tt.role, tt.tool); got != tt.want {
				t.Fatalf("RoleAllowsTool(%q, %q) = %v, want %v", tt.role, tt.tool, got, tt.want)
			}
		})
	}
}

func TestDefinitionsHideRoleForbiddenTools(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{ChatRole: domain.WorkflowRoleDecomposition})
	names := map[string]bool{}
	for _, def := range defs {
		names[def.Function.Name] = true
	}
	for _, name := range []string{string(domain.ToolKindBash), string(domain.ToolKindEdit), string(domain.ToolKindWrite), string(domain.ToolKindApplyPatch), string(domain.ToolKindChatPoll), string(domain.ToolKindChatStartExec)} {
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
		WorkflowRole: domain.WorkflowRoleDecomposition,
	}, tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{"command": "echo no"},
	})
	if err == nil || !strings.Contains(err.Error(), "not available to decomposition chats") {
		t.Fatalf("expected role denial, got %v", err)
	}
}
