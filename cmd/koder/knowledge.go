package main

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	knowledgePebble "github.com/lkarlslund/koder/internal/knowledge/store/pebble"
)

type knowledgeStoreOpener func(string) (knowledgeStore.Store, error)

// optionalKnowledgeStore keeps Knowledge's lifecycle independent from Koder's main
// store. OpenError is retained for later health reporting; an unavailable optional store
// does not prevent the rest of Koder from starting.
type optionalKnowledgeStore struct {
	Store     knowledgeStore.Store
	OpenError error
	Enabled   bool
	Required  bool
	Backend   string
}

func openOptionalKnowledgeStore(stateDir string, open knowledgeStoreOpener) optionalKnowledgeStore {
	if open == nil {
		return optionalKnowledgeStore{OpenError: fmt.Errorf("knowledge store opener is required")}
	}
	st, err := open(stateDir)
	if err != nil {
		return optionalKnowledgeStore{OpenError: err}
	}
	if st == nil {
		return optionalKnowledgeStore{OpenError: fmt.Errorf("knowledge store opener returned a nil store")}
	}
	return optionalKnowledgeStore{Store: st}
}

func openDefaultKnowledgeStore(stateDir string) optionalKnowledgeStore {
	return openOptionalKnowledgeStore(stateDir, func(stateDir string) (knowledgeStore.Store, error) {
		return knowledgePebble.Open(stateDir)
	})
}

func openConfiguredKnowledgeStore(stateDir string, cfg config.Knowledge, open knowledgeStoreOpener) (optionalKnowledgeStore, error) {
	if !cfg.Enabled && !cfg.Required {
		return optionalKnowledgeStore{}, nil
	}
	subsystem := openOptionalKnowledgeStore(stateDir, open)
	subsystem.Enabled = true
	subsystem.Required = cfg.Required
	if subsystem.OpenError != nil && cfg.Required {
		return subsystem, fmt.Errorf("required knowledge store unavailable: %w", subsystem.OpenError)
	}
	return subsystem, nil
}

func openConfiguredDefaultKnowledgeStore(stateDir string, cfg config.Knowledge) (optionalKnowledgeStore, error) {
	subsystem, err := openConfiguredKnowledgeStore(stateDir, cfg, func(stateDir string) (knowledgeStore.Store, error) {
		return knowledgePebble.Open(stateDir)
	})
	subsystem.Backend = "pebble"
	return subsystem, err
}

func (s optionalKnowledgeStore) OperationalHealth(ctx context.Context) debugsrv.SubsystemHealth {
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
	if s.OpenError != nil || s.Store == nil {
		health.Status = "unavailable"
		health.LastError = "knowledge store failed to open"
		return health
	}
	storeHealth, err := s.Store.Health(ctx)
	if err != nil {
		health.Status = "unavailable"
		health.LastError = "knowledge store health check failed"
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
		health.LastError = "knowledge store reported an operational error"
		if health.Status == "ready" {
			health.Status = "degraded"
		}
	}
	return health
}

func (s optionalKnowledgeStore) Close() error {
	if s.Store == nil {
		return nil
	}
	return s.Store.Close()
}
