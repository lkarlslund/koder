package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
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
	if err := memoryStore.View(context.Background(), func(tx knowledgeStore.ReadTx) error {
		personal, err := tx.Chunk(context.Background(), knowledgeService.PersonalMeChunkID)
		if err != nil {
			return err
		}
		if personal.Counts.Entries != 0 {
			return fmt.Errorf("personal seed created %d entries", personal.Counts.Entries)
		}
		return nil
	}); err != nil {
		t.Fatalf("personal seed: %v", err)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestKnowledgeOperationalHealthIsSanitized(t *testing.T) {
	t.Parallel()
	subsystem := optionalKnowledgeStore{
		Enabled:   true,
		Backend:   "pebble",
		OpenError: errors.New("open /home/private/knowledge: secret database detail"),
	}
	health := subsystem.OperationalHealth(context.Background())
	if health.Status != "unavailable" || health.Available || health.LastError != "knowledge store failed to open" {
		t.Fatalf("OperationalHealth() = %#v", health)
	}
	encoded := fmt.Sprintf("%#v", health)
	if strings.Contains(encoded, "/home/private") || strings.Contains(encoded, "secret database detail") {
		t.Fatalf("OperationalHealth() leaked raw error: %s", encoded)
	}
}

func TestKnowledgeOperationalHealthReportsStoreMetadataWithoutPath(t *testing.T) {
	t.Parallel()
	memoryStore := memory.New()
	t.Cleanup(func() { _ = memoryStore.Close() })
	subsystem := optionalKnowledgeStore{Store: memoryStore, Enabled: true, Backend: "configured"}
	health := subsystem.OperationalHealth(context.Background())
	if health.Status != "ready" || !health.Available || health.Backend != "memory" || health.SchemaVersion != 1 {
		t.Fatalf("OperationalHealth() = %#v", health)
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
