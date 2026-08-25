package chatrole

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
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
		{name: "voice", role: Voice, displayName: "Voice", prompt: "voice orchestrator"},
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
		{"voice rejects obsolete session delegation", Voice, testTool("session_delegate"), false},
		{"voice allows presentation", Voice, testTool("present"), true},
		{"voice allows file read", Voice, testTool("file_read"), true},
		{"voice allows web search", Voice, testTool("web_search"), true},
		{"voice allows chat list", Voice, testTool("chat_list"), true},
		{"voice allows chat start", Voice, testTool("chat_start"), true},
		{"voice rejects text-only input request", Voice, testTool("request_user_input"), false},
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

func TestExecutionPromptMatchesAssignment(t *testing.T) {
	tests := []struct {
		name    string
		chat    domain.Chat
		want    string
		without string
	}{
		{name: "unassigned", chat: domain.Chat{WorkflowRole: Execution}, want: "requested work directly", without: "You have been assigned"},
		{name: "milestone", chat: domain.Chat{WorkflowRole: Execution, ActiveMilestoneKey: "M002"}, want: "assigned milestone M002", without: "assigned task"},
		{name: "task", chat: domain.Chat{WorkflowRole: Execution, ActiveMilestoneKey: "M002", AssignedTaskRef: "M002T003"}, want: "assigned task M002T003", without: "assigned milestone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := SystemPromptForChat(tt.chat)
			if !strings.Contains(prompt, tt.want) || strings.Contains(prompt, tt.without) {
				t.Fatalf("prompt = %q, want %q and no %q", prompt, tt.want, tt.without)
			}
		})
	}
}

func TestChatStatusIsAvailableToEveryUserFacingRole(t *testing.T) {
	for _, role := range []Role{General, Orchestrator, Planning, Execution, Standalone, Voice} {
		if !AllowsTool(role, testTool("chat_status")) {
			t.Errorf("chat_status is not available to %q", role)
		}
	}
}

func TestVoicePromptOverridesDocumentFormatting(t *testing.T) {
	spec, ok := DefaultRegistry().Lookup(Voice)
	if !ok {
		t.Fatal("voice role not registered")
	}
	for _, requirement := range []string{
		"plain conversational sentences",
		"This overrides general formatting guidance",
		"Never put a list, table, code, or other visual material in the spoken response",
	} {
		if !strings.Contains(spec.SystemPrompt, requirement) {
			t.Fatalf("voice prompt does not contain %q: %s", requirement, spec.SystemPrompt)
		}
	}
}
