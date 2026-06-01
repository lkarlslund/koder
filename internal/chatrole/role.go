package chatrole

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/toolkind"
)

const (
	General      domain.WorkflowRole = "general"
	Orchestrator domain.WorkflowRole = "orchestrator"
	Planning     domain.WorkflowRole = "planning"
	Execution    domain.WorkflowRole = "execution"
)

const legacyDecomposition domain.WorkflowRole = "decomposition"

// Spec describes a chat role's behavior contract.
type Spec struct {
	Registered   bool // Registered is false for unknown roles.
	Name         domain.WorkflowRole
	DisplayName  string
	SystemPrompt string
	AllowTools   map[toolkind.Kind]bool
	DenyTools    map[toolkind.Kind]bool
}

// AllowsTool reports whether this role may expose or execute a tool.
func (s Spec) AllowsTool(kind toolkind.Kind) bool {
	if !s.Registered {
		return false
	}
	if len(s.AllowTools) > 0 {
		return s.AllowTools[kind]
	}
	if len(s.DenyTools) > 0 && s.DenyTools[kind] {
		return false
	}
	return true
}

// Registry stores the available chat roles by name.
type Registry struct {
	roles map[domain.WorkflowRole]Spec
}

// DefaultRegistry returns the built-in chat role registry.
func DefaultRegistry() Registry {
	return Registry{roles: map[domain.WorkflowRole]Spec{
		General:      orchestrationSpec(General, "Chat"),
		Orchestrator: orchestrationSpec(Orchestrator, "Orchestrate"),
		Planning:     orchestrationSpec(Planning, "Plan"),
		Execution: {
			Registered:  true,
			Name:        Execution,
			DisplayName: "Execute",
			SystemPrompt: strings.TrimSpace(`This chat is an execution worker.

Focus only on the assigned milestone and todo bucket.
- Implement the work using available coding tools.
- Keep todo item status updated as you progress.
			- Do not rewrite unrelated milestones or todo buckets.`),
			DenyTools: toolSet(
				toolkind.ToolKindChatStart,
				toolkind.ToolKindChatPoll,
				toolkind.ToolKindMilestoneAdd,
				toolkind.ToolKindMilestonePlan,
				toolkind.ToolKindMilestoneWrite,
			),
		},
	}}
}

// Lookup returns the role spec for name.
func (r Registry) Lookup(name domain.WorkflowRole) (Spec, bool) {
	if strings.TrimSpace(string(name)) == "" {
		name = General
	}
	spec, ok := r.roles[name]
	return spec, ok
}

// SpecFor returns the registered role spec.
func SpecFor(role domain.WorkflowRole) Spec {
	if role == legacyDecomposition {
		return orchestrationSpec(Orchestrator, "Orchestrate")
	}
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
func AllowsTool(role domain.WorkflowRole, kind toolkind.Kind) bool {
	return SpecFor(role).AllowsTool(kind)
}

// CheckToolAllowed returns an error when role cannot execute kind.
func CheckToolAllowed(role domain.WorkflowRole, kind toolkind.Kind) error {
	if AllowsTool(role, kind) {
		return nil
	}
	return fmt.Errorf("%s is not available to %s chats", kind, role)
}

// SystemPrompt returns the role-specific instruction text.
func SystemPrompt(role domain.WorkflowRole) string {
	return SpecFor(role).SystemPrompt
}

// DisplayName returns a short UI label for role.
func DisplayName(role domain.WorkflowRole) string {
	spec := SpecFor(role)
	if strings.TrimSpace(spec.DisplayName) != "" {
		return spec.DisplayName
	}
	if strings.TrimSpace(string(role)) == "" {
		return "Chat"
	}
	return string(role)
}

func orchestrationSpec(name domain.WorkflowRole, display string) Spec {
	return Spec{
		Registered:  true,
		Name:        name,
		DisplayName: display,
		SystemPrompt: strings.TrimSpace(`This chat is the main orchestration thread.

You may discuss, ask clarifying questions, manage milestones, decompose work inline, and start background execution chats when helpful.
- Use milestones for longer-horizon work.
- Use todos for concrete execution steps.
- Decompose work inline before starting execution chats.`),
	}
}

func toolSet(kinds ...toolkind.Kind) map[toolkind.Kind]bool {
	out := make(map[toolkind.Kind]bool, len(kinds))
	for _, kind := range kinds {
		out[kind] = true
	}
	return out
}
