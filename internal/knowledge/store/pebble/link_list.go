package pebble

import (
	"context"
	"fmt"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Store) ListAdjacentLinks(ctx context.Context, request knowledgeStore.AdjacentLinkListRequest) (knowledgeStore.LinkPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.LinkPage{}, err
	}
	request, err := knowledgeStore.NormalizeAdjacentLinkListRequest(request)
	if err != nil {
		return knowledgeStore.LinkPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.LinkPage{}, knowledgeStore.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	indexNames := []string{linkOutgoingIndex, linkIncomingIndex}
	switch request.Filter.Direction {
	case knowledgeStore.LinkDirectionOutgoing:
		indexNames = indexNames[:1]
	case knowledgeStore.LinkDirectionIncoming:
		indexNames = indexNames[1:]
	}
	seen := make(map[knowledge.LinkID]struct{})
	links := make([]knowledge.Link, 0)
	for _, indexName := range indexNames {
		prefix := indexKey(s.meta.IndexGeneration, indexName, encodeIndexTuple(request.Filter.Endpoint.Kind.String(), request.Filter.Endpoint.ID))
		lower, upper := prefixBounds(prefix)
		iter, err := snapshot.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
		if err != nil {
			return knowledgeStore.LinkPage{}, fmt.Errorf("list link adjacency: %w", err)
		}
		for iter.First(); iter.Valid(); iter.Next() {
			if err := ctx.Err(); err != nil {
				_ = iter.Close()
				return knowledgeStore.LinkPage{}, err
			}
			id := knowledge.LinkID(string(iter.Value()))
			if _, exists := seen[id]; exists {
				continue
			}
			link, err := readRecord[knowledge.Link](snapshot, linkKey(string(id)), "link", string(id))
			if err != nil {
				_ = iter.Close()
				return knowledgeStore.LinkPage{}, fmt.Errorf("resolve indexed link adjacency: %w", err)
			}
			if (indexName == linkOutgoingIndex && link.Source != request.Filter.Endpoint) ||
				(indexName == linkIncomingIndex && link.Target != request.Filter.Endpoint) {
				_ = iter.Close()
				return knowledgeStore.LinkPage{}, fmt.Errorf("link adjacency index does not match canonical endpoint")
			}
			seen[id] = struct{}{}
			links = append(links, link)
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return knowledgeStore.LinkPage{}, fmt.Errorf("list link adjacency: %w", err)
		}
		if err := iter.Close(); err != nil {
			return knowledgeStore.LinkPage{}, fmt.Errorf("close link adjacency iterator: %w", err)
		}
	}
	return knowledgeStore.PaginateAdjacentLinks(links, request, s.meta.IndexGeneration)
}
