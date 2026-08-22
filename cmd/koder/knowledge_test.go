package main

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
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

func TestConfiguredKnowledgeAvailabilityPolicy(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("store unavailable")
	failingOpen := func(string) (knowledgeStore.Store, error) { return nil, wantErr }

	tests := []struct {
		name     string
		config   config.Knowledge
		wantOpen bool
		wantErr  bool
	}{
		{name: "disabled", config: config.Knowledge{}},
		{name: "optional failure", config: config.Knowledge{Enabled: true}, wantOpen: true},
		{name: "required failure", config: config.Knowledge{Enabled: true, Required: true}, wantOpen: true, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			called := false
			subsystem, err := openConfiguredKnowledgeStore("unused", test.config, func(path string) (knowledgeStore.Store, error) {
				called = true
				return failingOpen(path)
			})
			if called != test.wantOpen {
				t.Fatalf("opener called = %v, want %v", called, test.wantOpen)
			}
			if (err != nil) != test.wantErr {
				t.Fatalf("openConfiguredKnowledgeStore() error = %v, want error %v", err, test.wantErr)
			}
			if test.wantOpen && !errors.Is(subsystem.OpenError, wantErr) {
				t.Fatalf("subsystem open error = %v, want %v", subsystem.OpenError, wantErr)
			}
		})
	}
}

func TestConfiguredRequiredKnowledgeOpensAndCloses(t *testing.T) {
	t.Parallel()
	memoryStore := memory.New()
	subsystem, err := openConfiguredKnowledgeStore("unused", config.Knowledge{Required: true}, func(string) (knowledgeStore.Store, error) {
		return memoryStore, nil
	})
	if err != nil {
		t.Fatalf("openConfiguredKnowledgeStore() error = %v", err)
	}
	if !subsystem.Enabled || !subsystem.Required || subsystem.Store != memoryStore {
		t.Fatalf("openConfiguredKnowledgeStore() = %#v", subsystem)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
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
