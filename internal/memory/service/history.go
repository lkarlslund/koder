package service

import (
	"context"
	"fmt"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Service) History(ctx context.Context, request memoryStoreAPI.RevisionListRequest) (memoryStoreAPI.RevisionPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.RevisionPage{}, err
	}
	if _, err := s.Get(ctx, request.Object); err != nil {
		return memoryStoreAPI.RevisionPage{}, fmt.Errorf("authorize memory object history: %w", err)
	}
	page, err := s.store.ListRevisions(ctx, request)
	if err != nil {
		return memoryStoreAPI.RevisionPage{}, fmt.Errorf("list memory object revisions: %w", err)
	}
	return page, nil
}
