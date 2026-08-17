package offeredfile

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/store"
)

func TestManagerCreatesResolvesAndDeletesSessionCapabilities(t *testing.T) {
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	manager := NewManager(st)
	record, err := manager.Create(context.Background(), Record{
		SessionID: "session-1", ChatID: "chat-1", Path: "/tmp/report.zip", Name: "report.zip", MIME: "application/zip", Size: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Token) != tokenBytes*2 {
		t.Fatalf("token length = %d, want %d", len(record.Token), tokenBytes*2)
	}
	got, err := manager.Resolve(context.Background(), record.Token)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != record.Path || got.Name != record.Name || got.Size != record.Size {
		t.Fatalf("resolved record = %#v, want %#v", got, record)
	}
	if err := manager.DeleteSession(context.Background(), "session-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Resolve(context.Background(), record.Token); err == nil {
		t.Fatal("expected deleted capability to be unavailable")
	}
}
