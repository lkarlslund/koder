package pebble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	cockroachpebble "github.com/cockroachdb/pebble"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const entryLexicalIndex = "entry-lexical"

func entryLexicalIndexDefinition() indexDefinition {
	return indexDefinition{
		name: entryLexicalIndex,
		build: func(ctx context.Context, record memoryStoreAPI.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != memoryStoreAPI.RecordKindEntry {
				return nil, nil
			}
			postings := memoryStoreAPI.EntryLexicalPostings(*record.Entry)
			entries := make([]indexEntry, 0, len(postings))
			for _, posting := range postings {
				encoded, err := json.Marshal(posting)
				if err != nil {
					return nil, fmt.Errorf("encode lexical posting: %w", err)
				}
				entries = append(entries, indexEntry{
					Suffix: encodeIndexTuple(posting.Term, string(posting.EntryID)),
					Value:  encoded,
				})
			}
			return entries, nil
		},
	}
}

func (s *Store) LookupLexicalPostings(ctx context.Context, request memoryStoreAPI.LexicalPostingRequest) (memoryStoreAPI.LexicalPostingPage, error) {
	request, err := memoryStoreAPI.NormalizeLexicalPostingRequest(request)
	if err != nil {
		return memoryStoreAPI.LexicalPostingPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.LexicalPostingPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.LexicalPostingPage{}, memoryStoreAPI.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	postings := make([]memoryStoreAPI.LexicalPosting, 0, len(request.Terms)*4)
	for _, term := range request.Terms {
		prefix := indexKey(s.meta.IndexGeneration, entryLexicalIndex, encodeIndexTuple(term))
		lower, upper := prefixBounds(prefix)
		iter, err := snapshot.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
		if err != nil {
			return memoryStoreAPI.LexicalPostingPage{}, fmt.Errorf("lookup lexical posting %q: %w", term, err)
		}
		for iter.First(); iter.Valid(); iter.Next() {
			if err := ctx.Err(); err != nil {
				_ = iter.Close()
				return memoryStoreAPI.LexicalPostingPage{}, err
			}
			posting, err := decodeLexicalPosting(iter.Value())
			if err != nil {
				_ = iter.Close()
				return memoryStoreAPI.LexicalPostingPage{}, err
			}
			if posting.Term != term {
				_ = iter.Close()
				return memoryStoreAPI.LexicalPostingPage{}, fmt.Errorf("%w: lexical posting term does not match index", memoryStoreAPI.ErrIncompatible)
			}
			postings = append(postings, posting)
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return memoryStoreAPI.LexicalPostingPage{}, fmt.Errorf("lookup lexical posting %q: %w", term, err)
		}
		if err := iter.Close(); err != nil {
			return memoryStoreAPI.LexicalPostingPage{}, fmt.Errorf("close lexical posting iterator: %w", err)
		}
	}
	documentCount, err := countLexicalDocuments(snapshot, s.meta.IndexGeneration)
	if err != nil {
		return memoryStoreAPI.LexicalPostingPage{}, err
	}
	return memoryStoreAPI.PaginateLexicalPostings(postings, request, s.meta.IndexGeneration, documentCount)
}

func countLexicalDocuments(reader iteratorReader, generation uint64) (uint64, error) {
	prefix := indexKey(generation, entryStateIndex, nil)
	lower, upper := prefixBounds(prefix)
	iter, err := reader.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return 0, fmt.Errorf("count lexical documents: %w", err)
	}
	defer func() { _ = iter.Close() }()
	var count uint64
	for iter.First(); iter.Valid(); iter.Next() {
		count++
	}
	if err := iter.Error(); err != nil {
		return 0, fmt.Errorf("count lexical documents: %w", err)
	}
	return count, nil
}

func decodeLexicalPosting(data []byte) (memoryStoreAPI.LexicalPosting, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var posting memoryStoreAPI.LexicalPosting
	if err := decoder.Decode(&posting); err != nil {
		return memoryStoreAPI.LexicalPosting{}, fmt.Errorf("%w: decode lexical posting: %v", memoryStoreAPI.ErrIncompatible, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return memoryStoreAPI.LexicalPosting{}, fmt.Errorf("%w: lexical posting contains trailing data", memoryStoreAPI.ErrIncompatible)
	}
	return posting, nil
}
