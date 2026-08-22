package main

import (
	"fmt"

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

func (s optionalKnowledgeStore) Close() error {
	if s.Store == nil {
		return nil
	}
	return s.Store.Close()
}
