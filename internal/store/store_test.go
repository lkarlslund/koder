package store

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

func TestSessionMessageRoundTrip(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddPart(context.Background(), msg.ID, domain.TextPayload{Text: "hello"}); err != nil {
				t.Fatal(err)
			}

			messages, parts, err := st.PartsForSession(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 {
				t.Fatalf("unexpected message count: %d", len(messages))
			}
			if got := parts[msg.ID][0].Text(); got != "hello" {
				t.Fatalf("unexpected part body: %q", got)
			}
		})
	}
}

func TestGenericCollectionRoundTripAndIndex(t *testing.T) {
	type note struct {
		ID     int64
		ChatID int64
		Body   string
	}
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			st := openTestStoreAt(t, backend, dir)
			notes := NewCollection(st, CollectionSpec[note]{
				Namespace: "test-notes",
				NextID:    "test-note",
				GetID:     func(v note) int64 { return v.ID },
				SetID:     func(v *note, id int64) { v.ID = id },
				Indexes: []IndexSpec[note]{
					{Name: "chat", Value: func(v note) string { return strconv.FormatInt(v.ChatID, 10) }},
				},
			})
			first, err := notes.Insert(context.Background(), note{ChatID: 7, Body: "first"})
			if err != nil {
				t.Fatal(err)
			}
			second, err := notes.Insert(context.Background(), note{ChatID: 8, Body: "second"})
			if err != nil {
				t.Fatal(err)
			}
			first.Body = "updated"
			if err := notes.Put(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			got, err := notes.Get(context.Background(), first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Body != "updated" {
				t.Fatalf("body = %q", got.Body)
			}
			indexed, err := notes.List(context.Background(), ByIndex[note]("chat", "7"))
			if err != nil {
				t.Fatal(err)
			}
			if len(indexed) != 1 || indexed[0].ID != first.ID {
				t.Fatalf("indexed = %#v", indexed)
			}
			if err := notes.Delete(context.Background(), second.ID); err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}

			reopened := openTestStoreAt(t, backend, dir)
			notes = NewCollection(reopened, notes.spec)
			reloaded, err := notes.List(context.Background(), All[note]())
			if err != nil {
				t.Fatal(err)
			}
			if len(reloaded) != 1 || reloaded[0].Body != "updated" {
				t.Fatalf("reloaded = %#v", reloaded)
			}
		})
	}
}

func TestApprovalAndTask(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			approval, err := st.CreateApproval(context.Background(), session.ID, domain.ToolKindBash, "echo hi")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionPermissionProfile(context.Background(), session.ID, "readonly"); err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionToolStates(context.Background(), session.ID, map[domain.ToolKind]bool{
				domain.ToolKindRead: true,
				domain.ToolKindBash: false,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.UpdateApproval(context.Background(), approval.ID, domain.ApprovalStatusApproved); err != nil {
				t.Fatal(err)
			}
			task, err := st.AddTask(context.Background(), session.ID, "ship v1", domain.TaskStatusPending)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.UpdateTask(context.Background(), task.ID, domain.TaskStatusCompleted); err != nil {
				t.Fatal(err)
			}
			tasks, err := st.ListTasks(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(tasks) != 1 || tasks[0].Status != domain.TaskStatusCompleted {
				t.Fatalf("unexpected tasks: %#v", tasks)
			}
			sessions, err := st.ListSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].PermissionProfile != "readonly" {
				t.Fatalf("unexpected session profile: %#v", sessions)
			}
			if sessions[0].ToolStates[domain.ToolKindBash] {
				t.Fatalf("expected bash tool disabled in stored session, got %#v", sessions[0].ToolStates)
			}
		})
	}
}

func TestUpdateSessionTitleAndCountMessagesByRole(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleAssistant, "hi"); err != nil {
				t.Fatal(err)
			}
			count, err := st.CountMessagesByRole(context.Background(), session.ID, domain.MessageRoleUser)
			if err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("unexpected user message count: %d", count)
			}
			generatedAt := time.Now().UTC().Truncate(time.Second)
			if err := st.UpdateSessionTitle(context.Background(), session.ID, "Short Helpful Session Title", generatedAt, 1); err != nil {
				t.Fatal(err)
			}
			sessions, err := st.ListSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := sessions[0].Title; got != "Short Helpful Session Title" {
				t.Fatalf("unexpected title: %q", got)
			}
			if got := sessions[0].TitleRefreshCount; got != 1 {
				t.Fatalf("unexpected title refresh count: %d", got)
			}
			if got := sessions[0].TitleGeneratedAt; !got.Equal(generatedAt) {
				t.Fatalf("unexpected title generated at: %v", got)
			}
		})
	}
}

func TestUpdateSessionWorkspacePersistsCWD(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.UpdateSessionWorkspace(context.Background(), session.ID, "/repo/worktree", "/repo"); err != nil {
				t.Fatal(err)
			}

			sessions, err := st.ListSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := sessions[0].CWD; got != "/repo/worktree" {
				t.Fatalf("expected cwd persisted, got %q", got)
			}
			if got := sessions[0].ProjectRoot; got != "/repo" {
				t.Fatalf("expected project root persisted, got %q", got)
			}
		})
	}
}

func TestCreateChatInheritsParentPermissionProfile(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionPermissionProfile(context.Background(), session.ID, "readonly"); err != nil {
				t.Fatal(err)
			}
			mainChat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			child, err := st.CreateChat(context.Background(), session.ID, "child", domain.WorkflowRoleExecution, &mainChat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if child.PermissionProfile != "readonly" {
				t.Fatalf("expected child chat to inherit session fallback profile, got %q", child.PermissionProfile)
			}

			mainChat.PermissionProfile = "full-access"
			if err := st.UpdateChat(context.Background(), mainChat); err != nil {
				t.Fatal(err)
			}
			secondChild, err := st.CreateChat(context.Background(), session.ID, "child 2", domain.WorkflowRoleExecution, &mainChat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if secondChild.PermissionProfile != "full-access" {
				t.Fatalf("expected child chat to inherit parent chat profile, got %q", secondChild.PermissionProfile)
			}
		})
	}
}

func TestUpdateChatPersistsContextTokens(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			chat, err := st.DefaultChat(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			chat.LastKnownContextTokens = 1234
			chat.ContextTokensKnown = true
			if err := st.UpdateChat(context.Background(), chat); err != nil {
				t.Fatal(err)
			}

			got, err := st.GetChat(context.Background(), chat.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.LastKnownContextTokens != 1234 || !got.ContextTokensKnown {
				t.Fatalf("expected persisted context tokens, got %#v", got)
			}
		})
	}
}

func TestAddSessionPermissionRulePersistsAndReplacesByKey(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.AddSessionPermissionRule(context.Background(), session.ID, domain.PermissionOverride{
				Tool:    domain.ToolKindBash,
				Pattern: "git *",
				Action:  domain.PermissionModeAllow,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.AddSessionPermissionRule(context.Background(), session.ID, domain.PermissionOverride{
				Tool:    domain.ToolKindBash,
				Pattern: "git *",
				Action:  domain.PermissionModeAllow,
			}); err != nil {
				t.Fatal(err)
			}

			updated, err := st.GetSession(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(updated.PermissionRules) != 1 {
				t.Fatalf("expected one deduplicated permission rule, got %#v", updated.PermissionRules)
			}
			if updated.PermissionRules[0].Tool != domain.ToolKindBash || updated.PermissionRules[0].Pattern != "git *" {
				t.Fatalf("unexpected stored permission rule: %#v", updated.PermissionRules[0])
			}
		})
	}
}

func TestMilestonePlanAndTodosRoundTrip(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := st.SetMilestonePlan(context.Background(), session.ID, "Ship the feature", []Milestone{
				{Ref: "investigate", Title: "Investigate", Status: domain.MilestoneStatusCompleted, Position: 0},
				{Ref: "implement", Title: "Implement", Status: domain.MilestoneStatusInProgress, Position: 1},
			})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Summary != "Ship the feature" || len(plan.Milestones) != 2 {
				t.Fatalf("unexpected milestone plan: %#v", plan)
			}
			items, err := st.AddTodoItems(context.Background(), session.ID, "implement", []string{"Write tests", "Fix bug"})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 2 || items[0].Position != 0 || items[1].Position != 1 {
				t.Fatalf("unexpected todo items: %#v", items)
			}
			if _, err := st.UpdateTodoItem(context.Background(), items[0].ID, domain.TodoStatusInProgress, "Write focused tests"); err != nil {
				t.Fatal(err)
			}
			gotPlan, err := st.GetMilestonePlan(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if gotPlan.Summary != "Ship the feature" || len(gotPlan.Milestones) != 2 {
				t.Fatalf("unexpected stored plan: %#v", gotPlan)
			}
			todos, err := st.ListTodos(context.Background(), session.ID, "implement")
			if err != nil {
				t.Fatal(err)
			}
			if len(todos) != 2 || todos[0].Status != domain.TodoStatusInProgress || todos[0].Content != "Write focused tests" {
				t.Fatalf("unexpected stored todos: %#v", todos)
			}
		})
	}
}

func TestForkSessionCopiesTranscriptAndParent(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionPermissionProfile(context.Background(), session.ID, "readonly"); err != nil {
				t.Fatal(err)
			}
			if err := st.SetSessionToolStates(context.Background(), session.ID, map[domain.ToolKind]bool{
				domain.ToolKindRead: true,
				domain.ToolKindBash: false,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.UpdateSessionWorkspace(context.Background(), session.ID, "/repo/a", "/repo"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetMilestonePlan(context.Background(), session.ID, "Ship feature", []Milestone{
				{Ref: "implement", Title: "Implement", Status: domain.MilestoneStatusInProgress, Position: 0},
			}); err != nil {
				t.Fatal(err)
			}
			todos, err := st.AddTodoItems(context.Background(), session.ID, "implement", []string{"Write tests"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpdateTodoItem(context.Background(), todos[0].ID, domain.TodoStatusCompleted, "Write tests"); err != nil {
				t.Fatal(err)
			}
			msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddPart(context.Background(), msg.ID, domain.TextPayload{Text: "hello"}); err != nil {
				t.Fatal(err)
			}

			forked, err := st.ForkSession(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if forked.ID == session.ID {
				t.Fatal("expected forked session to have distinct id")
			}
			if forked.ParentID == nil || *forked.ParentID != session.ID {
				t.Fatalf("expected parent id %d, got %#v", session.ID, forked.ParentID)
			}
			if forked.PermissionProfile != "readonly" {
				t.Fatalf("expected permission profile copied, got %q", forked.PermissionProfile)
			}
			if forked.CWD != "/repo/a" || forked.ProjectRoot != "/repo" {
				t.Fatalf("expected workspace copied, got %#v", forked)
			}
			if forked.ToolStates[domain.ToolKindBash] {
				t.Fatalf("expected tool states copied, got %#v", forked.ToolStates)
			}
			plan, err := st.GetMilestonePlan(context.Background(), forked.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Milestones) != 1 || plan.Milestones[0].Ref != "implement" {
				t.Fatalf("expected milestone plan copied, got %#v", plan)
			}
			forkTodos, err := st.ListTodos(context.Background(), forked.ID, "implement")
			if err != nil {
				t.Fatal(err)
			}
			if len(forkTodos) != 1 || forkTodos[0].Status != domain.TodoStatusCompleted {
				t.Fatalf("expected todos copied, got %#v", forkTodos)
			}

			messages, parts, err := st.PartsForSession(context.Background(), forked.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0].Summary != "hello" {
				t.Fatalf("unexpected forked messages: %#v", messages)
			}
			if got := parts[messages[0].ID][0].Text(); got != "hello" {
				t.Fatalf("unexpected forked part body: %q", got)
			}
		})
	}
}

func TestUpdatePartPayload(t *testing.T) {
	for _, backend := range []string{BackendPebble, BackendJSONFS} {
		t.Run(backend, func(t *testing.T) {
			st := openTestStore(t, backend)

			session, err := st.CreateSession(context.Background(), "test", "provider", "model", nil)
			if err != nil {
				t.Fatal(err)
			}
			msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello")
			if err != nil {
				t.Fatal(err)
			}
			part, err := st.AddPart(context.Background(), msg.ID, domain.AttachmentPayload{Name: "note.txt", Path: "old"})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.UpdatePartPayload(context.Background(), part.ID, domain.AttachmentPayload{Name: "note.txt", Path: "new"}); err != nil {
				t.Fatal(err)
			}
			_, parts, err := st.PartsForSession(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			payload, ok := parts[msg.ID][0].Payload.(domain.AttachmentPayload)
			if !ok || payload.Path != "new" {
				t.Fatalf("unexpected updated part payload: %#v", parts[msg.ID][0].Payload)
			}
		})
	}
}

func TestJSONFSWritesInspectableFiles(t *testing.T) {
	root := t.TempDir()
	st, err := OpenWithOptions(root, Options{Backend: BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "inspect", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "store-jsonfs-v2", "sessions", formatID(session.ID)+".json")); err != nil {
		t.Fatalf("expected inspectable session JSON file: %v", err)
	}
}

func openTestStore(t *testing.T, backend string) *Store {
	t.Helper()
	return openTestStoreAt(t, backend, t.TempDir())
}

func openTestStoreAt(t *testing.T, backend, dir string) *Store {
	t.Helper()
	st, err := OpenWithOptions(dir, Options{Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}
