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
