package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

func newQuickTestEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	cfg, err := config.LoadWithOptions(config.LoadOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(cfg, st, nil, nil), st
}

func TestCreateQuickSessionOwnsOneStandaloneChat(t *testing.T) {
	engine, _ := newQuickTestEngine(t)
	owner, err := engine.CreateQuickSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := owner.Snapshot()
	if snapshot.Session.Kind != domain.SessionKindQuick || !snapshot.Session.ProjectRootManaged {
		t.Fatalf("unexpected quick session: %#v", snapshot.Session)
	}
	if len(snapshot.Chats) != 1 || snapshot.Chats[0].WorkflowRole != chatrole.Standalone {
		t.Fatalf("unexpected quick chats: %#v", snapshot.Chats)
	}
	if info, err := os.Stat(snapshot.Session.ProjectRoot); err != nil || !info.IsDir() {
		t.Fatalf("managed project root is missing: %v", err)
	}
	if _, err := owner.NewChat(context.Background(), snapshot.Chats[0].ID, "second"); err == nil || !strings.Contains(err.Error(), "quick sessions") {
		t.Fatalf("expected new chat rejection, got %v", err)
	}
	_, err = owner.ChatToolControl(snapshot.Chats[0].ID).StartChat(context.Background(), snapshot.Session.ID, snapshot.Chats[0].ID, chattool.StartRequest{
		Profile: chatrole.Execution, Objective: "work",
	})
	if err == nil || !strings.Contains(err.Error(), "quick sessions") {
		t.Fatalf("expected child chat rejection, got %v", err)
	}
}

func TestCreateQuickVoiceSessionOwnsOneVoiceChatAndScratchWorkspace(t *testing.T) {
	engine, _ := newQuickTestEngine(t)
	owner, err := engine.CreateQuickVoiceSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := owner.Snapshot()
	if snapshot.Session.Kind != domain.SessionKindQuick || !snapshot.Session.ProjectRootManaged {
		t.Fatalf("unexpected quick voice session: %#v", snapshot.Session)
	}
	if len(snapshot.Chats) != 1 || snapshot.Chats[0].WorkflowRole != chatrole.Voice {
		t.Fatalf("unexpected quick voice chat: %#v", snapshot.Chats)
	}
	if info, err := os.Stat(snapshot.Session.ProjectRoot); err != nil || !info.IsDir() {
		t.Fatalf("managed scratch workspace is missing: %v", err)
	}
}

func TestCreateVoiceSessionOwnsOneVoiceChat(t *testing.T) {
	engine, _ := newQuickTestEngine(t)
	owner, err := engine.CreateVoiceSession(context.Background(), "Morning voice")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := owner.Snapshot()
	if snapshot.Session.Kind != domain.SessionKindVoice || snapshot.Session.ProjectRoot != "" || snapshot.Session.ProjectRootManaged {
		t.Fatalf("unexpected voice session: %#v", snapshot.Session)
	}
	if snapshot.Session.Title != "Morning voice" || len(snapshot.Chats) != 1 || snapshot.Chats[0].WorkflowRole != chatrole.Voice {
		t.Fatalf("unexpected voice chat: %#v %#v", snapshot.Session, snapshot.Chats)
	}
	second, err := owner.NewRootChat(context.Background(), "second", chatrole.Voice)
	if err != nil {
		t.Fatalf("create another voice chat: %v", err)
	}
	if second.Snapshot().Chat.WorkflowRole != chatrole.Voice || len(owner.Snapshot().Chats) != 2 {
		t.Fatalf("unexpected second voice chat: %#v", owner.Snapshot().Chats)
	}
}

func TestDeleteQuickSessionRemovesManagedArtifacts(t *testing.T) {
	engine, _ := newQuickTestEngine(t)
	owner, err := engine.CreateQuickSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session := owner.Snapshot().Session
	if err := os.WriteFile(filepath.Join(session.ProjectRoot, "answer.txt"), []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.files.ImportSessionData(session.ID, []byte("image"), "image.txt", "text/plain", "test"); err != nil {
		t.Fatal(err)
	}
	attachmentDir := engine.files.SessionDir(session.ID)
	if err := engine.DeleteSession(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{session.ProjectRoot, attachmentDir} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected %s removed, got %v", path, err)
		}
	}
}

func TestDeleteQuickSessionRefusesUnexpectedManagedRoot(t *testing.T) {
	engine, _ := newQuickTestEngine(t)
	owner, err := engine.CreateQuickSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	session, err := owner.UpdateSession(context.Background(), func(session *domain.Session) { session.ProjectRoot = outside })
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.DeleteSession(context.Background(), session.ID); err == nil || !strings.Contains(err.Error(), "refusing to delete") {
		t.Fatalf("expected managed root rejection, got %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside root was altered: %v", err)
	}
}

func TestPromoteQuickSessionMovesWorkspaceAndRole(t *testing.T) {
	engine, _ := newQuickTestEngine(t)
	owner, err := engine.CreateQuickSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := owner.Snapshot()
	source := snapshot.Session.ProjectRoot
	if err := os.WriteFile(filepath.Join(source, "answer.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "promoted")
	updated, err := engine.PromoteQuickSession(context.Background(), snapshot.Session.ID, PromoteQuickRequest{
		Mode: QuickPromotionMoveToNew, ProjectRoot: destination,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Kind != domain.SessionKindRegular || updated.ProjectRootManaged || updated.ProjectRoot != destination {
		t.Fatalf("unexpected promoted session: %#v", updated)
	}
	if got := owner.Snapshot().Chats[0].WorkflowRole; got != chatrole.Orchestrator {
		t.Fatalf("promoted role = %q", got)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "answer.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("promoted file = %q, %v", data, err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected source removed, got %v", err)
	}
}

func TestPromoteQuickSessionAssignRequiresDiscardConfirmation(t *testing.T) {
	engine, _ := newQuickTestEngine(t)
	owner, err := engine.CreateQuickSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session := owner.Snapshot().Session
	if err := os.WriteFile(filepath.Join(session.ProjectRoot, "discard.txt"), []byte("discard"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	req := PromoteQuickRequest{Mode: QuickPromotionAssign, ProjectRoot: destination}
	if _, err := engine.PromoteQuickSession(context.Background(), session.ID, req); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("expected discard confirmation error, got %v", err)
	}
	req.DiscardGeneratedFiles = true
	updated, err := engine.PromoteQuickSession(context.Background(), session.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProjectRoot != destination || updated.Kind != domain.SessionKindRegular {
		t.Fatalf("unexpected promoted session: %#v", updated)
	}
	if _, err := os.Stat(session.ProjectRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected generated root removed, got %v", err)
	}
}
