package chatrole

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
)

type Role = domain.WorkflowRole

const (
	General      Role = domain.WorkflowRoleGeneral
	Orchestrator Role = domain.WorkflowRoleOrchestrator
	Planning     Role = domain.WorkflowRolePlanning
	Execution    Role = domain.WorkflowRoleExecution
	Compaction   Role = domain.WorkflowRoleCompaction
	Standalone   Role = domain.WorkflowRoleStandalone
	Voice        Role = domain.WorkflowRoleVoice
)

// Spec describes a chat role's behavior contract.
type Spec struct {
	Registered   bool // Registered is false for unknown roles.
	Name         Role
	DisplayName  string
	SystemPrompt string
	AllowTools   map[string]bool
	DenyTools    map[string]bool
}

// AllowsTool reports whether this role may expose or execute a tool.
func (s Spec) AllowsTool(kind fmt.Stringer) bool {
	tool := strings.TrimSpace(kind.String())
	if tool == "" {
		return false
	}
	if !s.Registered {
		return false
	}
	if s.AllowTools != nil {
		return s.AllowTools[tool]
	}
	if len(s.DenyTools) > 0 && s.DenyTools[tool] {
		return false
	}
	return true
}

// Registry stores the available chat roles by name.
type Registry struct {
	roles map[Role]Spec
}

// DefaultRegistry returns the built-in chat role registry.
func DefaultRegistry() Registry {
	return Registry{roles: map[Role]Spec{
		General:      orchestrationSpec(General, "Chat"),
		Orchestrator: orchestrationSpec(Orchestrator, "Orchestrate"),
		Planning:     orchestrationSpec(Planning, "Plan"),
		Standalone: {
			Registered:  true,
			Name:        Standalone,
			DisplayName: "Standalone",
			SystemPrompt: strings.TrimSpace(`This is a standalone chat.

Answer the user's questions and complete requested work directly. Do not create, control, or coordinate other chats.`),
			DenyTools: toolSet(
				"chat_list",
				"chat_start",
				"chat_send",
				"chat_cancel",
				"chat_archive",
				"chat_rename",
				"chat_cleanup",
			),
		},
		Voice: {
			Registered:  true,
			Name:        Voice,
			DisplayName: "Voice",
			SystemPrompt: strings.TrimSpace(`You are a voice assistant. Your responses are spoken aloud, so sound like a helpful person in a phone conversation.

You are the user's persistent coordination chat and retain the conversation across voice calls.
- Answer simple conversational questions directly.
- Use the available coordination capabilities when another chat should inspect its history or perform work.
- Ask one short clarifying question only when the choice materially changes the result.
- The final response is speech, not a document. Always write it as plain conversational sentences with no Markdown or other formatting: no headings, bullets, numbered lists, tables, code blocks, link syntax, or raw source URLs. This overrides general formatting guidance from the shared system prompt.
- Use normal pacing by default: one or two short sentences. A call may provide a response-pacing instruction; follow it while remaining conversational. Never put a list, table, code, or other visual material in the spoken response, even when the user asks to see it; visual material belongs in a separate presentation.
- When work will take time, first say a brief natural acknowledgement such as "Let me check." Then do the work and give only its concise outcome.
- After tool work, preserve important uncertainty and any action the user must take, but leave supporting detail in the visual response instead of speaking it all.
- Do not mention tools, routing, delegation, prompts, Markdown, or internal implementation details.`),
			AllowTools: toolSet("chat_status", "session_list", "session_delegate", "session_start", "phone", "present"),
		},
		Compaction: {
			Registered:  true,
			Name:        Compaction,
			DisplayName: "Compact",
			SystemPrompt: strings.TrimSpace(`This chat summarizes conversation history for compaction.

Return only the compacted summary requested by the compaction prompt.`),
			AllowTools: toolSet(),
		},
		Execution: {
			Registered:  true,
			Name:        Execution,
			DisplayName: "Execute",
			SystemPrompt: strings.TrimSpace(`This chat is an execution worker.

Focus only on the assigned milestone and task list.
- Implement the work using available coding tools.
- Keep task status updated as you progress.
			- Do not rewrite unrelated milestones or task lists.`),
			DenyTools: toolSet(
				"request_user_input",
				"chat_list",
				"chat_start",
				"chat_send",
				"chat_cancel",
				"chat_archive",
				"chat_rename",
				"chat_cleanup",
				"milestone_add",
				"milestone_plan",
				"milestone_write",
			),
		},
	}}
}

// Lookup returns the role spec for name.
func (r Registry) Lookup(name Role) (Spec, bool) {
	if strings.TrimSpace(string(name)) == "" {
		name = General
	}
	spec, ok := r.roles[name]
	return spec, ok
}

// SpecFor returns the registered role spec.
func SpecFor(role Role) Spec {
	if spec, ok := DefaultRegistry().Lookup(role); ok {
		return spec
	}
	name := role
	if strings.TrimSpace(string(name)) == "" {
		name = General
	}
	return Spec{Name: name, DisplayName: strings.TrimSpace(string(name))}
}

// AllowsTool reports whether role may expose or execute kind.
func AllowsTool(role Role, kind fmt.Stringer) bool {
	return SpecFor(role).AllowsTool(kind)
}

// CheckToolAllowed returns an error when role cannot execute kind.
func CheckToolAllowed(role Role, kind fmt.Stringer) error {
	if AllowsTool(role, kind) {
		return nil
	}
	return fmt.Errorf("%s is not available to %s chats", kind, role)
}

// SystemPrompt returns the role-specific instruction text.
func SystemPrompt(role Role) string {
	return SpecFor(role).SystemPrompt
}

// DisplayName returns a short UI label for role.
func DisplayName(role Role) string {
	spec := SpecFor(role)
	if strings.TrimSpace(spec.DisplayName) != "" {
		return spec.DisplayName
	}
	if strings.TrimSpace(string(role)) == "" {
		return "Chat"
	}
	return string(role)
}

func orchestrationSpec(name Role, display string) Spec {
	return Spec{
		Registered:  true,
		Name:        name,
		DisplayName: display,
		SystemPrompt: strings.TrimSpace(`This chat is the main orchestration thread.

You may discuss, ask clarifying questions, manage milestones, decompose work inline, and start background execution chats when helpful.
- Use milestones for longer-horizon work.
- Use tasks for concrete execution steps.
- Decompose work inline before starting execution chats.`),
	}
}

func toolSet(kinds ...string) map[string]bool {
	out := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		kind = strings.TrimSpace(kind)
		if kind != "" {
			out[kind] = true
		}
	}
	return out
}
