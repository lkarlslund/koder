package service

import (
	"context"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// ListEntries hides drafts, superseded entries, and archives unless callers explicitly
// request lifecycle states.
func (s *Service) ListEntries(ctx context.Context, request knowledgeStore.EntryListRequest) (knowledgeStore.EntryPage, error) {
	if len(request.Filter.States) == 0 {
		request.Filter.States = []knowledge.EntryState{knowledge.EntryStateActive}
	}
	return s.store.ListEntries(ctx, request)
}
