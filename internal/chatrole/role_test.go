package chatrole

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
)

func TestDefaultRegistryRoleSpecs(t *testing.T) {
	tests := []struct {
		name        string
		role        domain.WorkflowRole
		displayName string
		prompt      string
	}{
		{name: "orchestrator", role: Orchestrator, displayName: "Orchestrate", prompt: "main orchestration thread"},
		{name: "execution", role: Execution, displayName: "Execute", prompt: "execution worker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok := DefaultRegistry().Lookup(tt.role)
			if !ok {
				t.Fatalf("role %q not registered", tt.role)
			}
			if spec.DisplayName != tt.displayName {
				t.Fatalf("display name = %q, want %q", spec.DisplayName, tt.displayName)
			}
			if !strings.Contains(spec.SystemPrompt, tt.prompt) {
				t.Fatalf("system prompt %q does not contain %q", spec.SystemPrompt, tt.prompt)
			}
		})
	}
}

func TestRoleAllowsTool(t *testing.T) {
	tests := []struct {
		name string
		role domain.WorkflowRole
		tool domain.ToolKind
		want bool
	}{
		{"legacy decomposition allows orchestrator tools", domain.WorkflowRole("decomposition"), domain.ToolKindChatPoll, true},
		{"execution allows edit", Execution, domain.ToolKindEdit, true},
		{"execution rejects chat start", Execution, domain.ToolKindChatStart, false},
		{"execution rejects milestone add", Execution, domain.ToolKindMilestoneAdd, false},
		{"execution allows milestone update", Execution, domain.ToolKindMilestoneUpdate, true},
		{"orchestrator allows chat poll", Orchestrator, domain.ToolKindChatPoll, true},
		{"unknown rejects read", domain.WorkflowRole("unknown"), domain.ToolKindRead, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AllowsTool(tt.role, tt.tool); got != tt.want {
				t.Fatalf("AllowsTool(%q, %q) = %v, want %v", tt.role, tt.tool, got, tt.want)
			}
		})
	}
}
