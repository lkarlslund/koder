package tools

import (
	"fmt"

	"github.com/lkarlslund/koder/internal/domain"
)

// RoleAllowsTool reports whether a chat role may expose or execute a tool.
func RoleAllowsTool(role domain.WorkflowRole, kind domain.ToolKind) bool {
	switch role {
	case "", domain.WorkflowRoleGeneral, domain.WorkflowRoleOrchestrator, domain.WorkflowRolePlanning:
		return true
	case domain.WorkflowRoleDecomposition:
		switch kind {
		case domain.ToolKindRead,
			domain.ToolKindGlob,
			domain.ToolKindGrep,
			domain.ToolKindCodeSearch,
			domain.ToolKindMilestoneList,
			domain.ToolKindMilestoneUpdate,
			domain.ToolKindTodoList,
			domain.ToolKindTodoAddItems,
			domain.ToolKindTodoUpdateItem:
			return true
		default:
			return false
		}
	case domain.WorkflowRoleExecution:
		switch kind {
		case domain.ToolKindChatStartDecomp,
			domain.ToolKindChatStartExec,
			domain.ToolKindChatPoll,
			domain.ToolKindMilestoneAdd,
			domain.ToolKindMilestonePlan,
			domain.ToolKindMilestoneWrite:
			return false
		default:
			return true
		}
	default:
		return true
	}
}

// CheckRoleToolAllowed returns an error when a chat role cannot execute a tool.
func CheckRoleToolAllowed(role domain.WorkflowRole, kind domain.ToolKind) error {
	if RoleAllowsTool(role, kind) {
		return nil
	}
	return fmt.Errorf("%s is not available to %s chats", kind, role)
}
