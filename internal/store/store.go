package store

import (
	"fmt"

	"github.com/lkarlslund/koder/internal/store/driver"
	"github.com/lkarlslund/koder/internal/store/driver/jsonfsdriver"
	"github.com/lkarlslund/koder/internal/store/driver/pebbledriver"
)

const (
	BackendPebble = "pebble"
	BackendJSONFS = "jsonfs"
)

type Options struct {
	Backend string
}

type Store struct {
	backend driver.Backend
}

func Open(stateDir string) (*Store, error) {
	return OpenWithOptions(stateDir, Options{Backend: BackendPebble})
}

func OpenWithOptions(stateDir string, opts Options) (*Store, error) {
	backendName := opts.Backend
	if backendName == "" {
		backendName = BackendPebble
	}

	var impl driver.Backend
	var err error
	switch backendName {
	case BackendPebble:
		impl, err = pebbledriver.Open(stateDir)
	case BackendJSONFS:
		impl, err = jsonfsdriver.Open(stateDir)
	default:
		return nil, fmt.Errorf("unsupported store backend %q", backendName)
	}
	if err != nil {
		return nil, err
	}
	return &Store{backend: impl}, nil
}

func (s *Store) Close() error {
	return s.backend.Close()
}
