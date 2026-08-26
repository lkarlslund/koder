package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
)

type memoryStoreOpener func(string) (memoryStoreAPI.Store, error)

// optionalMemoryStore keeps Memory's lifecycle independent from Koder's main
// store. OpenError is retained for later health reporting; an unavailable optional store
// does not prevent the rest of Koder from starting.
type optionalMemoryStore struct {
	Store     memoryStoreAPI.Store
	Service   *memoryService.Service
	OpenError error
	Enabled   bool
	Required  bool
	Backend   string
}

func openOptionalMemoryStore(stateDir string, open memoryStoreOpener) optionalMemoryStore {
	if open == nil {
		return optionalMemoryStore{OpenError: fmt.Errorf("memory store opener is required")}
	}
	st, err := open(stateDir)
	if err != nil {
		return optionalMemoryStore{OpenError: err}
	}
	if st == nil {
		return optionalMemoryStore{OpenError: fmt.Errorf("memory store opener returned a nil store")}
	}
	return optionalMemoryStore{Store: st}
}

func openDefaultMemoryStore(stateDir string) optionalMemoryStore {
	return openOptionalMemoryStore(stateDir, func(stateDir string) (memoryStoreAPI.Store, error) {
		return memoryPebble.Open(stateDir)
	})
}

func openConfiguredMemoryStore(stateDir string, cfg config.Memory, open memoryStoreOpener) (optionalMemoryStore, error) {
	if !cfg.Enabled && !cfg.Required {
		return optionalMemoryStore{}, nil
	}
	subsystem := openOptionalMemoryStore(stateDir, open)
	subsystem.Enabled = true
	subsystem.Required = cfg.Required
	if subsystem.OpenError == nil && subsystem.Store != nil {
		publishers, registryErr := configuredPublisherRegistry(cfg.TrustedPublishers)
		if registryErr != nil {
			_ = subsystem.Store.Close()
			subsystem.Store = nil
			subsystem.OpenError = registryErr
		}
		if subsystem.OpenError != nil {
			if cfg.Required {
				return subsystem, fmt.Errorf("required memory store unavailable: %w", subsystem.OpenError)
			}
			return subsystem, nil
		}
		service, err := memoryService.New(memoryService.Config{
			Store:             subsystem.Store,
			PublisherRegistry: publishers,
			Actor: memoryService.ContextActorSource(memory.Actor{
				Kind: memory.ActorKindSystem, ID: "system:koder",
			}),
		})
		if err == nil {
			_, err = service.EnsurePersonalChunk(context.Background())
		}
		if err == nil {
			_, err = service.EnsureCuratedLearningChunk(context.Background())
		}
		if err != nil {
			_ = subsystem.Store.Close()
			subsystem.Store = nil
			subsystem.OpenError = fmt.Errorf("seed built-in memory: %w", err)
		} else {
			subsystem.Service = service
		}
	}
	if subsystem.OpenError != nil && cfg.Required {
		return subsystem, fmt.Errorf("required memory store unavailable: %w", subsystem.OpenError)
	}
	return subsystem, nil
}

func configuredPublisherRegistry(values []config.MemoryTrustedPublisher) (*memoryService.PublisherRegistry, error) {
	publishers := make([]memoryService.TrustedPublisher, 0, len(values))
	for _, value := range values {
		keys := make(map[string]ed25519.PublicKey, len(value.Keys))
		for keyID, encoded := range value.Keys {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
			if err != nil || len(decoded) != ed25519.PublicKeySize {
				return nil, fmt.Errorf("decode trusted Memory publisher %q key %q: invalid Ed25519 public key", value.ID, keyID)
			}
			keys[keyID] = ed25519.PublicKey(decoded)
		}
		publishers = append(publishers, memoryService.TrustedPublisher{ID: value.ID, Name: value.Name, Keys: keys})
	}
	registry, err := memoryService.NewPublisherRegistry(publishers)
	if err != nil {
		return nil, fmt.Errorf("configure trusted Memory publishers: %w", err)
	}
	return registry, nil
}

func openConfiguredDefaultMemoryStore(stateDir string, cfg config.Memory) (optionalMemoryStore, error) {
	subsystem, err := openConfiguredMemoryStore(stateDir, cfg, func(stateDir string) (memoryStoreAPI.Store, error) {
		return memoryPebble.Open(stateDir)
	})
	subsystem.Backend = "pebble"
	return subsystem, err
}

func (s optionalMemoryStore) OperationalHealth(ctx context.Context) debugsrv.SubsystemHealth {
	health := debugsrv.SubsystemHealth{
		Status:    "disabled",
		Enabled:   s.Enabled,
		Required:  s.Required,
		Backend:   s.Backend,
		Available: false,
	}
	if !s.Enabled {
		return health
	}
	if s.OpenError != nil || s.Store == nil || s.Service == nil {
		health.Status = "unavailable"
		health.LastError = "memory store failed to open"
		return health
	}
	storeHealth, err := s.Store.Health(ctx)
	if err != nil {
		health.Status = "unavailable"
		health.LastError = "memory store health check failed"
		return health
	}
	health.Backend = storeHealth.Backend
	health.Available = storeHealth.Open
	health.ReadOnly = storeHealth.ReadOnly
	health.SchemaVersion = storeHealth.SchemaVersion
	health.IndexGeneration = storeHealth.IndexGeneration
	switch {
	case !storeHealth.Open:
		health.Status = "unavailable"
	case storeHealth.ReadOnly:
		health.Status = "read_only"
	default:
		health.Status = "ready"
	}
	if storeHealth.LastError != "" {
		health.LastError = "memory store reported an operational error"
		if health.Status == "ready" {
			health.Status = "degraded"
		}
	}
	return health
}

func (s optionalMemoryStore) Close() error {
	if s.Service != nil {
		if err := s.Service.ShutdownOperations(context.Background()); err != nil {
			return err
		}
	}
	if s.Store == nil {
		return nil
	}
	return s.Store.Close()
}
