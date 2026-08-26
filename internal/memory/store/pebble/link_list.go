package pebble

import (
	"context"
	"fmt"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Store) ListAdjacentLinks(ctx context.Context, request memoryStoreAPI.AdjacentLinkListRequest) (memoryStoreAPI.LinkPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.LinkPage{}, err
	}
	request, err := memoryStoreAPI.NormalizeAdjacentLinkListRequest(request)
	if err != nil {
		return memoryStoreAPI.LinkPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.LinkPage{}, memoryStoreAPI.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	indexNames := []string{linkOutgoingIndex, linkIncomingIndex}
	switch request.Filter.Direction {
	case memoryStoreAPI.LinkDirectionOutgoing:
		indexNames = indexNames[:1]
	case memoryStoreAPI.LinkDirectionIncoming:
		indexNames = indexNames[1:]
	}
	seen := make(map[memory.LinkID]struct{})
	links := make([]memory.Link, 0)
	for _, indexName := range indexNames {
		prefix := indexKey(s.meta.IndexGeneration, indexName, encodeIndexTuple(request.Filter.Endpoint.Kind.String(), request.Filter.Endpoint.ID))
		lower, upper := prefixBounds(prefix)
		iter, err := snapshot.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
		if err != nil {
			return memoryStoreAPI.LinkPage{}, fmt.Errorf("list link adjacency: %w", err)
		}
		for iter.First(); iter.Valid(); iter.Next() {
			if err := ctx.Err(); err != nil {
				_ = iter.Close()
				return memoryStoreAPI.LinkPage{}, err
			}
			id := memory.LinkID(string(iter.Value()))
			if _, exists := seen[id]; exists {
				continue
			}
			link, err := readRecord[memory.Link](snapshot, linkKey(string(id)), "link", string(id))
			if err != nil {
				_ = iter.Close()
				return memoryStoreAPI.LinkPage{}, fmt.Errorf("resolve indexed link adjacency: %w", err)
			}
			if (indexName == linkOutgoingIndex && link.Source != request.Filter.Endpoint) ||
				(indexName == linkIncomingIndex && link.Target != request.Filter.Endpoint) {
				_ = iter.Close()
				return memoryStoreAPI.LinkPage{}, fmt.Errorf("link adjacency index does not match canonical endpoint")
			}
			seen[id] = struct{}{}
			links = append(links, link)
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return memoryStoreAPI.LinkPage{}, fmt.Errorf("list link adjacency: %w", err)
		}
		if err := iter.Close(); err != nil {
			return memoryStoreAPI.LinkPage{}, fmt.Errorf("close link adjacency iterator: %w", err)
		}
	}
	return memoryStoreAPI.PaginateAdjacentLinks(links, request, s.meta.IndexGeneration)
}
