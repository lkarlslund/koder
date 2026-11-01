package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

func worktreeTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func worktreeTestOwner(t *testing.T) (*Session, *store.Store, string, string) {
	t.Helper()
	ctx := context.Background()
	repo := t.TempDir()
	worktreeTestGit(t, repo, "init", "-b", "parent-feature")
	if err := os.Mkdir(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "file.txt"), []byte("committed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, repo, "add", ".")
	worktreeTestGit(t, repo, "commit", "-m", "base")
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	chats, plans := testSources(st)
	record, err := createSessionRecordWithOptions(ctx, st, chats, createSessionOptions{Title: "Worktrees", ProjectRoot: filepath.Join(repo, "pkg"), ProviderID: "test", ModelID: "test", InitialChatRole: chatrole.Orchestrator})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := Load(ctx, st, chats, plans, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	owner.UpdateConfig(RegistryConfig{WorktreeRoot: root})
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	return owner, st, repo, root
}

func TestWorktreeParallelEditsAndRestart(t *testing.T) {
	ctx := context.Background()
	owner, st, repo, root := worktreeTestOwner(t)
	parent := owner.Snapshot().Chats[0]
	base := worktreeTestGit(t, repo, "rev-parse", "HEAD")
	dirtyFile := filepath.Join(repo, "pkg", "file.txt")
	if err := os.WriteFile(dirtyFile, []byte("parent dirty"), 0o600); err != nil {
		t.Fatal(err)
	}
	var children [2]domain.Chat
	var errs [2]error
	var wg sync.WaitGroup
	for i := range children {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt, err := owner.NewChatWithSpec(ctx, &parent.ID, domain.ChatCreateSpec{WorkflowRole: chatrole.Execution, Isolation: "worktree"})
			errs[i] = err
			if err == nil {
				children[i] = rt.Snapshot().Chat
			}
		}()
	}
	wg.Wait()
	for i, child := range children {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if child.Worktree == nil || child.Worktree.BaseCommit != base {
			t.Fatalf("child = %#v", child)
		}
		cwd := child.WorkingDirectory(owner.Snapshot().Session)
		if cwd != filepath.Join(child.Worktree.Path, "pkg") {
			t.Fatalf("subdirectory lost: %s", cwd)
		}
		data, err := os.ReadFile(filepath.Join(cwd, "file.txt"))
		if err != nil || string(data) != "committed" {
			t.Fatalf("child inherited uncommitted files: %s, %v", data, err)
		}
		if err := os.WriteFile(filepath.Join(cwd, "file.txt"), []byte(child.ID), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if children[0].Worktree.Path == children[1].Worktree.Path || children[0].Worktree.Branch == children[1].Worktree.Branch {
		t.Fatal("children share isolation")
	}
	for _, child := range children {
		data, _ := os.ReadFile(filepath.Join(child.WorkingDirectory(owner.Snapshot().Session), "file.txt"))
		if string(data) != string(child.ID) {
			t.Fatal("parallel edits collided")
		}
	}
	data, _ := os.ReadFile(dirtyFile)
	if string(data) != "parent dirty" || worktreeTestGit(t, repo, "branch", "--show-current") != "parent-feature" {
		t.Fatal("parent checkout changed")
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	chats, plans := testSources(st)
	loaded, err := Load(ctx, st, chats, plans, owner.Snapshot().Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loaded.Close(ctx) })
	loaded.UpdateConfig(RegistryConfig{WorktreeRoot: root})
	for _, child := range children {
		rt, err := loaded.Chat(ctx, child.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := rt.Snapshot().Chat; got.Worktree == nil || *got.Worktree != *child.Worktree {
			t.Fatalf("lost checkout on restart: %#v", got)
		}
	}
}

func TestWorktreeCleanupAndRestoreLifecycle(t *testing.T) {
	ctx := context.Background()
	owner, _, repo, _ := worktreeTestOwner(t)
	parent := owner.Snapshot().Chats[0]
	rt, err := owner.NewChatWithSpec(ctx, &parent.ID, domain.ChatCreateSpec{Isolation: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	child := rt.Snapshot().Chat
	control := owner.ChatToolControl(parent.ID).(chattool.WorktreeControl)
	cleanup := func() error {
		_, err := control.ManageWorktrees(ctx, child.SessionID, parent.ID, "cleanup", string(child.ID))
		return err
	}
	if err := cleanup(); err == nil {
		t.Fatal("removed visible checkout")
	}
	archived := true
	if _, _, err := owner.UpdateChat(ctx, child.ID, chattool.UpdateRequest{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(child.Worktree.Path, "ignored.txt")
	if err := os.WriteFile(ignored, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err == nil {
		t.Fatal("removed ignored local file")
	}
	if err := os.Remove(ignored); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(child.Worktree.Path, "unique.txt")
	if err := os.WriteFile(file, []byte("child change"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err == nil {
		t.Fatal("removed untracked file")
	}
	worktreeTestGit(t, child.Worktree.Path, "add", ".")
	worktreeTestGit(t, child.Worktree.Path, "commit", "-m", "child change")
	if err := cleanup(); err == nil {
		t.Fatal("removed unmerged commit")
	}
	if err := owner.DeleteChat(ctx, child.ID); err == nil {
		t.Fatal("deleted worktree ownership before cleanup")
	}
	worktreeTestGit(t, repo, "merge", "--ff-only", child.Worktree.Branch)
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(child.Worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("checkout still exists: %v", err)
	}
	worktreeTestGit(t, repo, "rev-parse", "--verify", child.Worktree.Branch)
	archived = false
	if _, _, err := owner.UpdateChat(ctx, child.ID, chattool.UpdateRequest{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(child.Worktree.Path, "unique.txt")); err != nil || string(data) != "child change" {
		t.Fatalf("restore lost work: %s %v", data, err)
	}
	archived = true
	if _, _, err := owner.UpdateChat(ctx, child.ID, chattool.UpdateRequest{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup not idempotent: %v", err)
	}
	if err := owner.DeleteChat(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSharedDescendantPreventsWorktreeCleanup(t *testing.T) {
	ctx := context.Background()
	owner, _, _, _ := worktreeTestOwner(t)
	parent := owner.Snapshot().Chats[0]
	rt, err := owner.NewChatWithSpec(ctx, &parent.ID, domain.ChatCreateSpec{Isolation: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	child := rt.Snapshot().Chat
	shared, err := owner.NewChatWithSpec(ctx, &child.ID, domain.ChatCreateSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if shared.Snapshot().Chat.Worktree.Path != child.Worktree.Path {
		t.Fatal("shared child lost parent checkout")
	}
	archived := true
	if _, _, err := owner.UpdateChat(ctx, child.ID, chattool.UpdateRequest{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	_, err = owner.ChatToolControl(parent.ID).(chattool.WorktreeControl).ManageWorktrees(ctx, child.SessionID, parent.ID, "cleanup", string(child.ID))
	if err == nil || !strings.Contains(err.Error(), "still uses it") {
		t.Fatalf("shared checkout cleanup error = %v", err)
	}
}

func TestWorktreeRejectsNonGitAndInvalidIsolation(t *testing.T) {
	owner := &Session{config: RegistryConfig{WorktreeRoot: t.TempDir()}}
	for _, isolation := range []string{"worktree", "invalid"} {
		child := domain.Chat{ID: id.New()}
		if err := owner.prepareChatWorktree(context.Background(), domain.Session{ProjectRoot: t.TempDir()}, domain.Chat{}, &child, isolation); err == nil {
			t.Fatalf("accepted %s", isolation)
		}
	}
}

func TestWorktreeFailedCreationIsDiscoverableAndCleanupIsSafe(t *testing.T) {
	ctx := context.Background()
	owner, _, repo, _ := worktreeTestOwner(t)
	parent := owner.Snapshot().Chats[0]
	child := domain.Chat{ID: id.New(), SessionID: parent.SessionID}
	worktreeTestGit(t, repo, "branch", "koder/"+string(child.ID))
	err := owner.prepareChatWorktree(ctx, owner.Snapshot().Session, parent, &child, "worktree")
	owner.finishWorktreeCreation(child.ID)
	if err == nil {
		t.Fatal("expected branch collision")
	}
	control := owner.ChatToolControl(parent.ID).(chattool.WorktreeControl)
	statuses, err := control.ManageWorktrees(ctx, parent.SessionID, parent.ID, "list", "")
	if err != nil || len(statuses) != 1 {
		t.Fatalf("failed creation disappeared: %#v %v", statuses, err)
	}
	if statuses[0].Error == "" {
		t.Fatal("missing failure diagnostics")
	}
	if _, err := control.ManageWorktrees(ctx, parent.SessionID, parent.ID, "cleanup", string(child.ID)); err != nil {
		t.Fatal(err)
	}
	worktreeTestGit(t, repo, "rev-parse", "--verify", "koder/"+string(child.ID))
}

func TestWorktreeMissingCheckoutIsNotSilentlyRecreated(t *testing.T) {
	ctx := context.Background()
	owner, _, _, _ := worktreeTestOwner(t)
	parent := owner.Snapshot().Chats[0]
	rt, err := owner.NewChatWithSpec(ctx, &parent.ID, domain.ChatCreateSpec{Isolation: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	child := rt.Snapshot().Chat
	archived := true
	if _, _, err := owner.UpdateChat(ctx, child.ID, chattool.UpdateRequest{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(child.Worktree.Path, child.Worktree.Path+"-moved"); err != nil {
		t.Fatal(err)
	}
	archived = false
	if _, _, err := owner.UpdateChat(ctx, child.ID, chattool.UpdateRequest{Archived: &archived}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing checkout recovery: %v", err)
	}
	if _, err := os.Stat(child.Worktree.Path); !os.IsNotExist(err) {
		t.Fatal("silently recreated checkout")
	}
}

func TestNestedWorktreesRequireChildCleanupFirst(t *testing.T) {
	ctx := context.Background()
	owner, _, _, _ := worktreeTestOwner(t)
	parent := owner.Snapshot().Chats[0]
	first, err := owner.NewChatWithSpec(ctx, &parent.ID, domain.ChatCreateSpec{Isolation: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	child := first.Snapshot().Chat
	if _, err := owner.NewChatWithSpec(ctx, &child.ID, domain.ChatCreateSpec{Isolation: "worktree"}); err != nil {
		t.Fatal(err)
	}
	archived := true
	if _, _, err := owner.UpdateChat(ctx, child.ID, chattool.UpdateRequest{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	_, err = owner.ChatToolControl(parent.ID).(chattool.WorktreeControl).ManageWorktrees(ctx, child.SessionID, parent.ID, "cleanup", string(child.ID))
	if err == nil || !strings.Contains(err.Error(), "depends") {
		t.Fatalf("removed integration target: %v", err)
	}
}

func TestWorktreeCleanupHonorsOtherSessionUsers(t *testing.T) {
	ctx := context.Background()
	owner, _, _, root := worktreeTestOwner(t)
	parent := owner.Snapshot().Chats[0]
	rt, err := owner.NewChatWithSpec(ctx, &parent.ID, domain.ChatCreateSpec{Isolation: "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	child := rt.Snapshot().Chat
	archived := true
	if _, _, err := owner.UpdateChat(ctx, child.ID, chattool.UpdateRequest{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	owner.UpdateConfig(RegistryConfig{WorktreeRoot: root, WorktreeInUse: func(string) error { return fmt.Errorf("another session uses this checkout") }})
	_, err = owner.ChatToolControl(parent.ID).(chattool.WorktreeControl).ManageWorktrees(ctx, child.SessionID, parent.ID, "cleanup", string(child.ID))
	if err == nil || !strings.Contains(err.Error(), "another session") {
		t.Fatalf("cross-session usage ignored: %v", err)
	}
}
