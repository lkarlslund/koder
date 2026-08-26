package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
	memoryMigration "github.com/lkarlslund/koder/internal/memory/store/migration"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
)

func TestOptionalMemoryStoreOpenAndClose(t *testing.T) {
	t.Parallel()
	backendStore := memoryBackend.New()
	subsystem := openOptionalMemoryStore("unused", func(string) (memoryStoreAPI.Store, error) {
		return backendStore, nil
	})
	if subsystem.OpenError != nil || subsystem.Store != backendStore {
		t.Fatalf("openOptionalMemoryStore() = %#v", subsystem)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if health, err := backendStore.Health(context.Background()); err != nil || health.Open {
		t.Fatalf("Health() after Close = %#v, %v", health, err)
	}
}

func TestConfiguredMemoryAvailabilityPolicy(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("store unavailable")
	failingOpen := func(string) (memoryStoreAPI.Store, error) { return nil, wantErr }

	tests := []struct {
		name     string
		config   config.Memory
		wantOpen bool
		wantErr  bool
	}{
		{name: "disabled", config: config.Memory{}},
		{name: "optional failure", config: config.Memory{Enabled: true}, wantOpen: true},
		{name: "required failure", config: config.Memory{Enabled: true, Required: true}, wantOpen: true, wantErr: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			called := false
			subsystem, err := openConfiguredMemoryStore("unused", test.config, func(path string) (memoryStoreAPI.Store, error) {
				called = true
				return failingOpen(path)
			})
			if called != test.wantOpen {
				t.Fatalf("opener called = %v, want %v", called, test.wantOpen)
			}
			if (err != nil) != test.wantErr {
				t.Fatalf("openConfiguredMemoryStore() error = %v, want error %v", err, test.wantErr)
			}
			if test.wantOpen && !errors.Is(subsystem.OpenError, wantErr) {
				t.Fatalf("subsystem open error = %v, want %v", subsystem.OpenError, wantErr)
			}
		})
	}
}

func TestConfiguredOptionalMemoryDegradesAcrossStartupFaults(t *testing.T) {
	t.Parallel()

	faults := map[string]error{
		"read-only":     os.ErrPermission,
		"mid-migration": memoryMigration.ErrMigrationActive,
	}
	for name, fault := range faults {
		name, fault := name, fault
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			subsystem, err := openConfiguredMemoryStore("unused", config.Memory{Enabled: true}, func(string) (memoryStoreAPI.Store, error) {
				return nil, fault
			})
			if err != nil {
				t.Fatalf("optional Memory prevented startup: %v", err)
			}
			assertUnavailableMemorySubsystem(t, subsystem, fault)
		})
	}
}

func TestConfiguredOptionalMemoryDegradesWhenPebbleIsLocked(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	locked, err := memoryPebble.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locked.Close() })

	subsystem, err := openConfiguredDefaultMemoryStore(stateDir, config.Memory{Enabled: true})
	if err != nil {
		t.Fatalf("locked optional Memory prevented startup: %v", err)
	}
	assertUnavailableMemorySubsystem(t, subsystem, nil)
}

func TestConfiguredOptionalMemoryDegradesWhenPebbleIsCorrupt(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	initialized, err := memoryPebble.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := initialized.Close(); err != nil {
		t.Fatal(err)
	}
	databasePath, err := memoryPebble.DefaultPath(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(databasePath, "CURRENT"), []byte("not-a-manifest\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	subsystem, err := openConfiguredDefaultMemoryStore(stateDir, config.Memory{Enabled: true})
	if err != nil {
		t.Fatalf("corrupt optional Memory prevented startup: %v", err)
	}
	assertUnavailableMemorySubsystem(t, subsystem, nil)
}

func TestConfiguredMemoryCreatesMissingPebbleStore(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	databasePath, err := memoryPebble.DefaultPath(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("memory store exists before startup: %v", err)
	}

	subsystem, err := openConfiguredDefaultMemoryStore(stateDir, config.Memory{Enabled: true})
	if err != nil {
		t.Fatalf("missing optional Memory prevented startup: %v", err)
	}
	t.Cleanup(func() { _ = subsystem.Close() })
	if subsystem.OpenError != nil || subsystem.Store == nil || subsystem.Service == nil {
		t.Fatalf("missing Memory was not initialized: %#v", subsystem)
	}
	if health := subsystem.OperationalHealth(context.Background()); health.Status != "ready" || !health.Available {
		t.Fatalf("OperationalHealth() = %#v, want ready", health)
	}
}

func TestConfiguredRequiredMemoryFailsAcrossStartupFaults(t *testing.T) {
	t.Parallel()
	for name, fault := range map[string]error{
		"locked":        errors.New("database is locked"),
		"corrupt":       memoryStoreAPI.ErrIncompatible,
		"read-only":     os.ErrPermission,
		"mid-migration": memoryMigration.ErrMigrationActive,
	} {
		name, fault := name, fault
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			subsystem, err := openConfiguredMemoryStore("unused", config.Memory{Required: true}, func(string) (memoryStoreAPI.Store, error) {
				return nil, fault
			})
			if err == nil || !errors.Is(err, fault) {
				t.Fatalf("required Memory startup error = %v, want %v", err, fault)
			}
			assertUnavailableMemorySubsystem(t, subsystem, fault)
		})
	}
}

func assertUnavailableMemorySubsystem(t *testing.T, subsystem optionalMemoryStore, wantError error) {
	t.Helper()
	if subsystem.Store != nil || subsystem.Service != nil || subsystem.OpenError == nil {
		t.Fatalf("memory subsystem = %#v, want unavailable", subsystem)
	}
	if wantError != nil && !errors.Is(subsystem.OpenError, wantError) {
		t.Fatalf("memory open error = %v, want %v", subsystem.OpenError, wantError)
	}
	health := subsystem.OperationalHealth(context.Background())
	if health.Status != "unavailable" || health.Available || health.LastError != "memory store failed to open" {
		t.Fatalf("OperationalHealth() = %#v, want sanitized unavailable state", health)
	}
}

func TestConfiguredRequiredMemoryOpensAndCloses(t *testing.T) {
	t.Parallel()
	backendStore := memoryBackend.New()
	subsystem, err := openConfiguredMemoryStore("unused", config.Memory{Required: true}, func(string) (memoryStoreAPI.Store, error) {
		return backendStore, nil
	})
	if err != nil {
		t.Fatalf("openConfiguredMemoryStore() error = %v", err)
	}
	if !subsystem.Enabled || !subsystem.Required || subsystem.Store != backendStore || subsystem.Service == nil {
		t.Fatalf("openConfiguredMemoryStore() = %#v", subsystem)
	}
	personal, err := subsystem.Service.Chunk(context.Background(), memoryService.PersonalMeChunkID)
	if err != nil {
		t.Fatalf("personal seed: %v", err)
	}
	if personal.Counts.Entries != 0 {
		t.Fatalf("personal seed created %d entries", personal.Counts.Entries)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestConfiguredPublisherRegistryDecodesEd25519Keys(t *testing.T) {
	t.Parallel()
	public, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := configuredPublisherRegistry([]config.MemoryTrustedPublisher{{
		ID: "publisher:example", Name: "Example", Keys: map[string]string{
			"example:key": base64.StdEncoding.EncodeToString(public),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.VerificationKeys()["example:key"]; !ed25519.PublicKey(got).Equal(public) {
		t.Fatalf("decoded public key = %x, want %x", got, public)
	}
}

func TestConfiguredPublisherRegistryFailureFollowsMemoryAvailabilityPolicy(t *testing.T) {
	t.Parallel()
	invalid := []config.MemoryTrustedPublisher{{
		ID: "publisher:example", Keys: map[string]string{"example:key": "not-base64"},
	}}
	for _, test := range []struct {
		name     string
		required bool
		wantErr  bool
	}{
		{name: "optional degrades"},
		{name: "required fails startup", required: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := memoryBackend.New()
			subsystem, err := openConfiguredMemoryStore("unused", config.Memory{
				Enabled: true, Required: test.required, TrustedPublishers: invalid,
			}, func(string) (memoryStoreAPI.Store, error) { return store, nil })
			if (err != nil) != test.wantErr {
				t.Fatalf("openConfiguredMemoryStore() error = %v, want error %v", err, test.wantErr)
			}
			if subsystem.Store != nil || subsystem.Service != nil || subsystem.OpenError == nil {
				t.Fatalf("openConfiguredMemoryStore() = %#v, want unavailable subsystem", subsystem)
			}
			if health, healthErr := store.Health(context.Background()); healthErr != nil || health.Open {
				t.Fatalf("store health after registry failure = %#v, %v; want closed", health, healthErr)
			}
		})
	}
}

func TestMemoryOperationalHealthIsSanitized(t *testing.T) {
	t.Parallel()
	subsystem := optionalMemoryStore{
		Enabled:   true,
		Backend:   "pebble",
		OpenError: errors.New("open /home/private/memory: secret database detail"),
	}
	health := subsystem.OperationalHealth(context.Background())
	if health.Status != "unavailable" || health.Available || health.LastError != "memory store failed to open" {
		t.Fatalf("OperationalHealth() = %#v", health)
	}
	encoded := fmt.Sprintf("%#v", health)
	if strings.Contains(encoded, "/home/private") || strings.Contains(encoded, "secret database detail") {
		t.Fatalf("OperationalHealth() leaked raw error: %s", encoded)
	}
}

func TestMemoryOperationalHealthReportsStoreMetadataWithoutPath(t *testing.T) {
	t.Parallel()
	backendStore := memoryBackend.New()
	t.Cleanup(func() { _ = backendStore.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: backendStore,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:koder"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	subsystem := optionalMemoryStore{Store: backendStore, Service: service, Enabled: true, Backend: "configured"}
	health := subsystem.OperationalHealth(context.Background())
	if health.Status != "ready" || !health.Available || health.Backend != "memory" || health.SchemaVersion != 1 {
		t.Fatalf("OperationalHealth() = %#v", health)
	}
}

func TestOptionalMemoryStoreDegradesOnOpenFailure(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("store unavailable")
	subsystem := openOptionalMemoryStore("unused", func(string) (memoryStoreAPI.Store, error) {
		return nil, wantErr
	})
	if subsystem.Store != nil || !errors.Is(subsystem.OpenError, wantErr) {
		t.Fatalf("openOptionalMemoryStore() = %#v", subsystem)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close() after failed open error = %v", err)
	}
}

func TestDefaultMemoryStoreUsesIndependentPebbleLifecycle(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	subsystem := openDefaultMemoryStore(stateDir)
	if subsystem.OpenError != nil || subsystem.Store == nil {
		t.Fatalf("openDefaultMemoryStore() = %#v", subsystem)
	}
	if err := subsystem.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := memoryPebble.Open(stateDir)
	if err != nil {
		t.Fatalf("Pebble Open() after process lifecycle close error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("reopened Close() error = %v", err)
	}
}

func TestOptionalMemoryStoreRejectsInvalidOpener(t *testing.T) {
	t.Parallel()
	for name, open := range map[string]memoryStoreOpener{
		"missing": nil,
		"nil store": func(string) (memoryStoreAPI.Store, error) {
			return nil, nil
		},
	} {
		name, open := name, open
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			subsystem := openOptionalMemoryStore("unused", open)
			if subsystem.Store != nil || subsystem.OpenError == nil {
				t.Fatalf("openOptionalMemoryStore() = %#v", subsystem)
			}
		})
	}
}
