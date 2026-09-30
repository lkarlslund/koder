package main

import (
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
)

func openDefaultMemoryStore(stateDir string) optionalMemoryStore {
	return openOptionalMemoryStore(stateDir, func(stateDir string) (memoryStoreAPI.Store, error) {
		return memoryPebble.Open(stateDir)
	})
}
