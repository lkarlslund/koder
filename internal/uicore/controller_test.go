package uicore

import (
	"context"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
)

func TestControllerStartCreatesSessionAndPublishesState(t *testing.T) {
	ctrl, _ := newTestController(t)
	events, unsub := ctrl.Subscribe()
	defer unsub()

	state := ctrl.State()
	if state.Session.ID == 0 {
		t.Fatal("expected active session")
	}
	if state.ActiveChatID == 0 {
		t.Fatal("expected active chat")
	}
	select {
	case event := <-events:
		if event.Type != "snapshot" {
			t.Fatalf("expected initial snapshot event, got %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("expected initial snapshot event")
	}
}

func TestControllerNewChatAndSwitchChat(t *testing.T) {
	ctrl, _ := newTestController(t)
	first := ctrl.State().ActiveChatID
	if first == 0 {
		t.Fatal("expected first chat")
	}
	if err := ctrl.NewChat(context.Background(), "side chat"); err != nil {
		t.Fatalf("new chat: %v", err)
	}
	second := ctrl.State().ActiveChatID
	if second == 0 || second == first {
		t.Fatalf("expected new active chat, first=%d second=%d", first, second)
	}
	if err := ctrl.SwitchChat(context.Background(), first); err != nil {
		t.Fatalf("switch chat: %v", err)
	}
	if got := ctrl.State().ActiveChatID; got != first {
		t.Fatalf("expected active chat %d, got %d", first, got)
	}
}

func TestControllerSetPermissionProfileUpdatesActiveChat(t *testing.T) {
	ctrl, st := newTestController(t)
	chatID := ctrl.State().ActiveChatID
	if err := ctrl.SetPermissionProfile(context.Background(), "write-ask"); err != nil {
		t.Fatalf("set permission profile: %v", err)
	}
	chat, err := st.GetChat(context.Background(), chatID)
	if err != nil {
		t.Fatalf("get chat: %v", err)
	}
	if chat.PermissionProfile != "write-ask" {
		t.Fatalf("expected chat permission profile write-ask, got %q", chat.PermissionProfile)
	}
	if got := ctrl.State().Permissions.Active; got != "write-ask" {
		t.Fatalf("expected active permission profile write-ask, got %q", got)
	}
}

func TestControllerSetPermissionProfileRejectsUnknownProfile(t *testing.T) {
	ctrl, _ := newTestController(t)
	if err := ctrl.SetPermissionProfile(context.Background(), "nope"); err == nil {
		t.Fatal("expected unknown permission profile error")
	}
}

func newTestController(t *testing.T) (*Controller, *store.Store) {
	t.Helper()
	cfg := config.Default().WithStateDir(t.TempDir())
	cfg.DefaultProvider = "test"
	cfg.DefaultModel = "model"
	st, err := store.OpenWithOptions(cfg.StateDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	engine := agent.New(cfg, st, nil, nil, t.TempDir())
	ctrl := New(cfg, st, engine, t.TempDir())
	if err := ctrl.Start(context.Background(), StartupModeNew); err != nil {
		t.Fatalf("start controller: %v", err)
	}
	return ctrl, st
}

func TestNewestSessionUsesUpdatedAtThenID(t *testing.T) {
	now := time.Now()
	got := newestSession([]domain.Session{
		{ID: 1, UpdatedAt: now},
		{ID: 2, UpdatedAt: now},
		{ID: 3, UpdatedAt: now.Add(-time.Second)},
	})
	if got.ID != 2 {
		t.Fatalf("expected newest session 2, got %d", got.ID)
	}
}
