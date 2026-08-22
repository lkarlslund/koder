package main

import (
	"context"
	"errors"
	"testing"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
	knowledgePebble "github.com/lkarlslund/koder/internal/knowledge/store/pebble"
)

func TestOptionalKnowledgeStoreOpenAndClose(t *testing.T) {
	t.Parallel()
	memoryStore := memory.New()
	subsystem := openOptionalKnowledgeStore("unused", func(string) (knowledgeStore.Store, error) {
		return memoryStore, nil
	})
	if subsystem.OpenError != nil || subsystem.Store != memoryStore {
		t.Fatalf("openOptionalKnowledgeStore() = %#v", subsystem)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if health, err := memoryStore.Health(context.Background()); err != nil || health.Open {
		t.Fatalf("Health() after Close = %#v, %v", health, err)
	}
}

func TestOptionalKnowledgeStoreDegradesOnOpenFailure(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("store unavailable")
	subsystem := openOptionalKnowledgeStore("unused", func(string) (knowledgeStore.Store, error) {
		return nil, wantErr
	})
	if subsystem.Store != nil || !errors.Is(subsystem.OpenError, wantErr) {
		t.Fatalf("openOptionalKnowledgeStore() = %#v", subsystem)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close() after failed open error = %v", err)
	}
}

func TestDefaultKnowledgeStoreUsesIndependentPebbleLifecycle(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	subsystem := openDefaultKnowledgeStore(stateDir)
	if subsystem.OpenError != nil || subsystem.Store == nil {
		t.Fatalf("openDefaultKnowledgeStore() = %#v", subsystem)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := knowledgePebble.Open(stateDir)
	if err != nil {
		t.Fatalf("Pebble Open() after process lifecycle close error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("reopened Close() error = %v", err)
	}
}

func TestOptionalKnowledgeStoreRejectsInvalidOpener(t *testing.T) {
	t.Parallel()
	for name, open := range map[string]knowledgeStoreOpener{
		"missing": nil,
		"nil store": func(string) (knowledgeStore.Store, error) {
			return nil, nil
		},
	} {
		name, open := name, open
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			subsystem := openOptionalKnowledgeStore("unused", open)
			if subsystem.Store != nil || subsystem.OpenError == nil {
				t.Fatalf("openOptionalKnowledgeStore() = %#v", subsystem)
			}
		})
	}
}
