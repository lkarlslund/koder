package app

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
)

func TestControllerEditsGlobalAndProjectMemory(t *testing.T) {
	ctrl, _, _ := newTestControllerWithExec(t)
	ctx := context.Background()
	sessionID := activateTestSession(t, ctrl, t.TempDir()).ID
	if _, err := ctrl.SaveMemory(ctx, MemoryEdit{SessionID: sessionID, Scope: memory.ScopeGlobal, Name: "terse-answers", Description: "User wants terse answers", Content: "No summaries.", Create: true}); err != nil {
		t.Fatal(err)
	}
	state, err := ctrl.SaveMemory(ctx, MemoryEdit{SessionID: sessionID, Scope: memory.ScopeProject, Name: "deploy", Description: "How to deploy", Content: "Restart koder.service.", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if state.ProjectRoot == "" || len(state.Global) != 1 || len(state.Project) != 1 || !strings.Contains(state.ProjectDir, "memory") {
		t.Fatalf("state = %+v", state)
	}
	if state, err = ctrl.SaveMemory(ctx, MemoryEdit{SessionID: sessionID, Scope: memory.ScopeProject, Name: "deploy", Description: "Deploy steps", Content: "Check /debug/chats first."}); err != nil || state.Project[0].Description != "Deploy steps" {
		t.Fatalf("update = %+v, %v", state.Project, err)
	}
	if state, err = ctrl.DeleteMemory(ctx, sessionID, memory.ScopeProject, "deploy"); err != nil || len(state.Project) != 0 || len(state.Global) != 1 {
		t.Fatalf("delete = %+v, %v", state, err)
	}
	if _, err := ctrl.SaveMemory(ctx, MemoryEdit{SessionID: sessionID, Scope: memory.ScopeGlobal, Name: "terse-answers", Description: "dup", Content: "dup", Create: true}); err == nil {
		t.Fatal("duplicate create succeeded")
	}
}
