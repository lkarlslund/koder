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
	for _, name := range executionForbiddenToolNames() {
		if names[name] {
			t.Fatalf("execution definitions exposed forbidden tool %q", name)
		}
	}
	for _, name := range []string{domain.ToolKindFileRead.String(), domain.ToolKindFileGrep.String(), domain.ToolKindFileEdit.String(), domain.ToolKindMilestones.String()} {
		if !names[name] {
			t.Fatalf("execution definitions did not expose allowed tool %q", name)
		}
	}
	for _, def := range defs {
		if def.Function.Name != domain.ToolKindMilestones.String() {
			continue
		}
		params := string(def.Function.Parameters)
		if strings.Contains(params, `"create"`) || !strings.Contains(params, `"update"`) {
			t.Fatalf("execution milestone actions were not role filtered: %s", params)
		}
	}
}

func TestBaseToolSurfaceRemainsCompactAndCanonical(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{SessionID: "session-1", ChatID: "chat-1", ChatRole: chatrole.Orchestrator})
	if len(defs) > 20 {
		t.Fatalf("base tool surface grew to %d definitions; group related operations or make them runtime-specific", len(defs))
	}
	names := map[string]bool{}
	for _, def := range defs {
		names[def.Function.Name] = true
	}
	for _, required := range []tools.ID{tools.ExecCommand, tools.ExecSession, tools.Milestones, tools.Tasks, tools.Chats, tools.Present} {
		if !names[required.String()] {
			t.Errorf("canonical tool %q is missing", required)
		}
	}
	for _, legacy := range []tools.ID{tools.Bash, tools.ExecStatus, tools.MilestoneList, tools.TaskList, tools.ChatList, tools.ShowMedia} {
		if names[legacy.String()] {
			t.Errorf("legacy operation %q is still model-visible", legacy)
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

func TestExecutionRoleRejectsRequestUserInput(t *testing.T) {
	_, err := tools.Call(context.Background(), tools.Options{Runtime: tools.Runtime{
		SessionID: "session-1",
		ChatID:    "chat-1",
		ChatRole:  chatrole.Execution,
	}, Request: tools.Request{
		Tool: domain.ToolKindRequestUserInput,
		Args: map[string]string{"questions": `[{"id":"choice","header":"Choice","question":"Which?","options":[{"label":"A","description":"A"},{"label":"B","description":"B"}]}]`},
	}})
	if err == nil || !strings.Contains(err.Error(), "not available to execution chats") {
		t.Fatalf("expected role denial, got %v", err)
	}
	if !tools.IsDenied(err) {
		t.Fatalf("expected denied error, got %T %[1]v", err)
	}
}

func TestExecuteWithChatRejectsAllChatToolsForExecutionRole(t *testing.T) {
	for _, kind := range executionForbiddenChatTools() {
		t.Run(kind.String(), func(t *testing.T) {
			_, err := tools.Call(context.Background(), tools.Options{Runtime: tools.Runtime{
				SessionID: "session-1",
				ChatID:    "chat-1",
				ChatRole:  chatrole.Execution,
			}, Request: tools.Request{
				Tool: kind,
				Args: map[string]string{"chat_id": "child-1", "profile": string(chatrole.Execution), "objective": "no", "message": "no", "archived": "true", "title": "no"},
			}})
			if err == nil || !strings.Contains(err.Error(), "not available to execution chats") {
				t.Fatalf("expected role denial, got %v", err)
			}
			if !tools.IsDenied(err) {
				t.Fatalf("expected denied error, got %T %[1]v", err)
			}
		})
	}
}

func TestStandaloneRoleRejectsAllChatTools(t *testing.T) {
	defs := tools.Definitions(tools.Runtime{ChatRole: chatrole.Standalone})
	for _, def := range defs {
		for _, denied := range executionForbiddenChatTools() {
			if def.Function.Name == denied.String() {
				t.Fatalf("standalone definitions exposed forbidden tool %q", def.Function.Name)
			}
		}
	}
	for _, kind := range executionForbiddenChatTools() {
		_, err := tools.Call(context.Background(), tools.Options{Runtime: tools.Runtime{
			SessionID: "session-1",
			ChatID:    "chat-1",
			ChatRole:  chatrole.Standalone,
		}, Request: tools.Request{
			Tool: kind,
			Args: map[string]string{"chat_id": "child-1", "profile": string(chatrole.Execution), "objective": "no", "message": "no", "archived": "true", "title": "no"},
		}})
		if err == nil || !tools.IsDenied(err) {
			t.Fatalf("expected %s to be denied, got %v", kind, err)
		}
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

func TestCanonicalRequestTranslatesLegacyOperations(t *testing.T) {
	tests := []struct {
		name       string
		request    tools.Request
		wantTool   tools.ID
		wantAction string
		absentArg  string
	}{
		{name: "bash", request: tools.Request{Tool: tools.Bash, Args: map[string]string{"command": "pwd"}}, wantTool: tools.ExecCommand},
		{name: "browser", request: tools.Request{Tool: tools.BrowserTabClose, Args: map[string]string{"tab_id": "tab-1"}}, wantTool: tools.BrowserTabs, wantAction: "close"},
		{name: "milestone restore", request: tools.Request{Tool: tools.MilestoneArchive, Args: map[string]string{"milestone_key": "M001", "archived": "false"}}, wantTool: tools.Milestones, wantAction: "restore", absentArg: "archived"},
		{name: "chat archive", request: tools.Request{Tool: tools.ChatArchive, Args: map[string]string{"chat_id": "child", "archived": "true"}}, wantTool: tools.Chats, wantAction: "archive", absentArg: "archived"},
		{name: "phone media", request: tools.Request{Tool: tools.Phone, Args: map[string]string{"action": "media_control", "media_action": "pause"}}, wantTool: tools.PhoneMedia, wantAction: "pause", absentArg: "media_action"},
		{name: "present default", request: tools.Request{Tool: tools.Present, Args: map[string]string{"content": "hello"}}, wantTool: tools.Present, wantAction: "content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tools.CanonicalRequest(tt.request)
			if got.Tool != tt.wantTool || got.Args["action"] != tt.wantAction {
				t.Fatalf("canonical request = %#v, want tool=%s action=%q", got, tt.wantTool, tt.wantAction)
			}
			if tt.request.Tool == tools.Bash && got.Args["cmd"] != "pwd" {
				t.Fatalf("bash command was not translated: %#v", got.Args)
			}
			if tt.absentArg != "" {
				if _, ok := got.Args[tt.absentArg]; ok {
					t.Fatalf("fixed legacy argument %q remained in %#v", tt.absentArg, got.Args)
				}
			}
		})
	}
}

func TestRequestIdentityIncludesResourceAction(t *testing.T) {
	req := tools.Request{Tool: tools.BrowserTabs, Args: map[string]string{"action": "close"}}
	if got := req.Identity(); got != "browser_tabs.close" {
		t.Fatalf("identity = %q", got)
	}
}

func executionForbiddenToolNames() []string {
	names := []string{
		domain.ToolKindRequestUserInput.String(),
		domain.ToolKindMilestoneAdd.String(),
		domain.ToolKindMilestonePlan.String(),
	}
	for _, kind := range executionForbiddenChatTools() {
		names = append(names, kind.String())
	}
	return names
}

func executionForbiddenChatTools() []tools.ID {
	return []tools.ID{
		domain.ToolKindChatList,
		domain.ToolKindChatStart,
		domain.ToolKindChatSend,
		domain.ToolKindChatCancel,
		domain.ToolKindChatArchive,
		domain.ToolKindChatRename,
		domain.ToolKindChatCleanup,
	}
}
