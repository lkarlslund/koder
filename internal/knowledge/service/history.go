package service

import (
	"context"
	"fmt"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Service) History(ctx context.Context, request knowledgeStore.RevisionListRequest) (knowledgeStore.RevisionPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.RevisionPage{}, err
	}
	if _, err := s.Get(ctx, request.Object); err != nil {
		return knowledgeStore.RevisionPage{}, fmt.Errorf("authorize knowledge object history: %w", err)
	}
	page, err := s.store.ListRevisions(ctx, request)
	if err != nil {
		return knowledgeStore.RevisionPage{}, fmt.Errorf("list knowledge object revisions: %w", err)
	}
	return page, nil
}
