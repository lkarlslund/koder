package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
			if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindText, "hello", ""); err != nil {
				t.Fatal(err)
			}

			messages, parts, err := st.PartsForSession(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 {
				t.Fatalf("unexpected message count: %d", len(messages))
			}
			if got := parts[msg.ID][0].Body; got != "hello" {
				t.Fatalf("unexpected part body: %q", got)
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
			if err := st.UpdateSessionTitle(context.Background(), session.ID, "Short Helpful Session Title"); err != nil {
				t.Fatal(err)
			}
			sessions, err := st.ListSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := sessions[0].Title; got != "Short Helpful Session Title" {
				t.Fatalf("unexpected title: %q", got)
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
			msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindText, "hello", ""); err != nil {
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
			if forked.ToolStates[domain.ToolKindBash] {
				t.Fatalf("expected tool states copied, got %#v", forked.ToolStates)
			}

			messages, parts, err := st.PartsForSession(context.Background(), forked.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0].Summary != "hello" {
				t.Fatalf("unexpected forked messages: %#v", messages)
			}
			if got := parts[messages[0].ID][0].Body; got != "hello" {
				t.Fatalf("unexpected forked part body: %q", got)
			}
		})
	}
}

func TestUpdatePartMetaJSON(t *testing.T) {
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
			part, err := st.AddPart(context.Background(), msg.ID, domain.PartKindAttachment, "note.txt", `{"path":"old"}`)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.UpdatePartMetaJSON(context.Background(), part.ID, `{"path":"new"}`); err != nil {
				t.Fatal(err)
			}
			_, parts, err := st.PartsForSession(context.Background(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := parts[msg.ID][0].MetaJSON; got != `{"path":"new"}` {
				t.Fatalf("unexpected updated part metadata: %q", got)
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
	if _, err := os.Stat(filepath.Join(root, "store-jsonfs", "sessions", formatID(session.ID)+".json")); err != nil {
		t.Fatalf("expected inspectable session JSON file: %v", err)
	}
}

func openTestStore(t *testing.T, backend string) *Store {
	t.Helper()
	st, err := OpenWithOptions(t.TempDir(), Options{Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}
