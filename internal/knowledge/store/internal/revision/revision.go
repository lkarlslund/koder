// Package revision implements optimistic-revision checks shared by Knowledge stores.
package revision

import (
	"fmt"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// CheckPut verifies the create-or-update precondition defined by store.WriteTx.
func CheckPut(kind, id string, expected, supplied, current uint64, exists bool) error {
	if expected == 0 {
		if exists || supplied != 1 {
			return fmt.Errorf("%w: create %s %s expected absent revision 1", knowledgeStore.ErrConflict, kind, id)
		}
		return nil
	}
	if !exists || current != expected || supplied != expected+1 {
		return fmt.Errorf("%w: update %s %s expected revision %d, current %d, supplied %d", knowledgeStore.ErrConflict, kind, id, expected, current, supplied)
	}
	return nil
}

// CheckDelete verifies the delete precondition defined by store.WriteTx.
func CheckDelete(kind, id string, expected, current uint64, exists bool) error {
	if !exists {
		return fmt.Errorf("%w: %s %s", knowledgeStore.ErrNotFound, kind, id)
	}
	if expected == 0 || current != expected {
		return fmt.Errorf("%w: delete %s %s expected revision %d, current %d", knowledgeStore.ErrConflict, kind, id, expected, current)
	}
	return nil
}
