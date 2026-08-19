package chatrole

import (
	"strings"
	"testing"
)

type testTool string

func (t testTool) String() string { return string(t) }

func TestDefaultRegistryRoleSpecs(t *testing.T) {
	tests := []struct {
		name        string
		role        Role
		displayName string
		prompt      string
	}{
		{name: "orchestrator", role: Orchestrator, displayName: "Orchestrate", prompt: "main orchestration thread"},
		{name: "execution", role: Execution, displayName: "Execute", prompt: "execution worker"},
		{name: "standalone", role: Standalone, displayName: "Standalone", prompt: "standalone chat"},
		{name: "compaction", role: Compaction, displayName: "Compact", prompt: "summarizes conversation history"},
		{name: "voice", role: Voice, displayName: "Voice", prompt: "voice assistant"},
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
		tool testTool
		want bool
	}{
		{"execution allows edit", Execution, testTool("file_edit"), true},
		{"execution rejects user input request", Execution, testTool("request_user_input"), false},
		{"execution rejects chat start", Execution, testTool("chat_start"), false},
		{"execution rejects chat send", Execution, testTool("chat_send"), false},
		{"execution rejects chat cleanup", Execution, testTool("chat_cleanup"), false},
		{"execution rejects milestone add", Execution, testTool("milestone_add"), false},
		{"execution allows milestone update", Execution, testTool("milestone_update"), true},
		{"orchestrator allows chat send", Orchestrator, testTool("chat_send"), true},
		{"standalone rejects chat start", Standalone, testTool("chat_start"), false},
		{"standalone allows edit", Standalone, testTool("file_edit"), true},
		{"compaction rejects read", Compaction, testTool("file_read"), false},
		{"compaction rejects chat send", Compaction, testTool("chat_send"), false},
		{"voice allows chat status", Voice, testTool("chat_status"), true},
		{"voice allows session delegation", Voice, testTool("session_delegate"), true},
		{"voice rejects file read", Voice, testTool("file_read"), false},
		{"unknown rejects read", Role("unknown"), testTool("file_read"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AllowsTool(tt.role, tt.tool); got != tt.want {
				t.Fatalf("AllowsTool(%q, %q) = %v, want %v", tt.role, tt.tool, got, tt.want)
			}
		})
	}
}
