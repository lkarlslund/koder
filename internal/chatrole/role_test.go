package chatrole

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/toolkind"
)

func TestDefaultRegistryRoleSpecs(t *testing.T) {
	tests := []struct {
		name        string
		role        Role
		displayName string
		prompt      string
	}{
		{name: "orchestrator", role: Orchestrator, displayName: "Orchestrate", prompt: "main orchestration thread"},
		{name: "execution", role: Execution, displayName: "Execute", prompt: "execution worker"},
		{name: "compaction", role: Compaction, displayName: "Compact", prompt: "summarizes conversation history"},
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
		role Role
		tool toolkind.Kind
		want bool
	}{
		{"legacy decomposition allows orchestrator tools", Role("decomposition"), toolkind.ToolKindChatSend, true},
		{"execution allows edit", Execution, toolkind.ToolKindFileEdit, true},
		{"execution rejects chat start", Execution, toolkind.ToolKindChatStart, false},
		{"execution rejects chat send", Execution, toolkind.ToolKindChatSend, false},
		{"execution rejects milestone add", Execution, toolkind.ToolKindMilestoneAdd, false},
		{"execution allows milestone update", Execution, toolkind.ToolKindMilestoneUpdate, true},
		{"orchestrator allows chat send", Orchestrator, toolkind.ToolKindChatSend, true},
		{"compaction rejects read", Compaction, toolkind.ToolKindFileRead, false},
		{"compaction rejects chat send", Compaction, toolkind.ToolKindChatSend, false},
		{"unknown rejects read", Role("unknown"), toolkind.ToolKindFileRead, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AllowsTool(tt.role, tt.tool); got != tt.want {
				t.Fatalf("AllowsTool(%q, %q) = %v, want %v", tt.role, tt.tool, got, tt.want)
			}
		})
	}
}
