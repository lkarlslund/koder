package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

func TestDefinitionsHideRoleForbiddenTools(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{ChatRole: chatrole.Execution})
	names := map[string]bool{}
	for _, def := range defs {
		names[def.Function.Name] = true
	}
	for _, name := range []string{domain.ToolKindChatSend.String(), domain.ToolKindChatStart.String(), domain.ToolKindMilestoneAdd.String(), domain.ToolKindMilestonePlan.String()} {
		if names[name] {
			t.Fatalf("execution definitions exposed forbidden tool %q", name)
		}
	}
	for _, name := range []string{domain.ToolKindFileRead.String(), domain.ToolKindFileGrep.String(), domain.ToolKindFileEdit.String(), domain.ToolKindMilestoneUpdate.String()} {
		if !names[name] {
			t.Fatalf("execution definitions did not expose allowed tool %q", name)
		}
	}
}

func TestExecuteWithChatRejectsRoleForbiddenTool(t *testing.T) {
	_, err := tools.Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		SessionID: "session-1",
		ChatID:    "chat-1",
		ChatRole:  chatrole.Execution,
	}, Request: tools.Request{
		Tool: domain.ToolKindChatStart,
		Args: map[string]string{"profile": string(chatrole.Execution), "objective": "no"},
	}})
	if err == nil || !strings.Contains(err.Error(), "not available to execution chats") {
		t.Fatalf("expected role denial, got %v", err)
	}
	if !tools.IsDenied(err) {
		t.Fatalf("expected denied error, got %T %[1]v", err)
	}
}

func TestDefinitionsHideDisabledTools(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{
		AllowedTools: map[tools.ID]bool{domain.ToolKindFileRead: false},
	})
	for _, def := range defs {
		if def.Function.Name == domain.ToolKindFileRead.String() {
			t.Fatalf("definitions exposed disabled tool %q", def.Function.Name)
		}
	}
}

func TestCallRejectsDisabledTool(t *testing.T) {
	_, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{
			AllowedTools: map[tools.ID]bool{domain.ToolKindFileRead: false},
		},
		Request: tools.Request{
			Tool: domain.ToolKindFileRead,
			Args: map[string]string{"path": "."},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "disabled for this session") {
		t.Fatalf("expected disabled tool denial, got %v", err)
	}
	if !tools.IsDenied(err) {
		t.Fatalf("expected denied error, got %T %[1]v", err)
	}
}

func TestBypassPermissionToolStillObeysDisabledState(t *testing.T) {
	_, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{
			SessionID:    "session-1",
			ChatID:       "chat-1",
			AllowedTools: map[tools.ID]bool{domain.ToolKindChatList: false},
		},
		Request: tools.Request{Tool: domain.ToolKindChatList},
	})
	if err == nil || !strings.Contains(err.Error(), "disabled for this session") {
		t.Fatalf("expected disabled chat tool denial, got %v", err)
	}
	if !tools.IsDenied(err) {
		t.Fatalf("expected denied error, got %T %[1]v", err)
	}
}
