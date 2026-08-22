package pebble

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	cockroachpebble "github.com/cockroachdb/pebble"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const entryLexicalIndex = "entry-lexical"

func entryLexicalIndexDefinition() indexDefinition {
	return indexDefinition{
		name: entryLexicalIndex,
		build: func(ctx context.Context, record knowledgeStore.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != knowledgeStore.RecordKindEntry {
				return nil, nil
			}
			postings := knowledgeStore.EntryLexicalPostings(*record.Entry)
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

func (s *Store) LookupLexicalPostings(ctx context.Context, request knowledgeStore.LexicalPostingRequest) (knowledgeStore.LexicalPostingPage, error) {
	request, err := knowledgeStore.NormalizeLexicalPostingRequest(request)
	if err != nil {
		return knowledgeStore.LexicalPostingPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return knowledgeStore.LexicalPostingPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.LexicalPostingPage{}, knowledgeStore.ErrClosed
	}
	snapshot := s.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()
	postings := make([]knowledgeStore.LexicalPosting, 0, len(request.Terms)*4)
	for _, term := range request.Terms {
		prefix := indexKey(s.meta.IndexGeneration, entryLexicalIndex, encodeIndexTuple(term))
		lower, upper := prefixBounds(prefix)
		iter, err := snapshot.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
		if err != nil {
			return knowledgeStore.LexicalPostingPage{}, fmt.Errorf("lookup lexical posting %q: %w", term, err)
		}
		for iter.First(); iter.Valid(); iter.Next() {
			if err := ctx.Err(); err != nil {
				_ = iter.Close()
				return knowledgeStore.LexicalPostingPage{}, err
			}
			posting, err := decodeLexicalPosting(iter.Value())
			if err != nil {
				_ = iter.Close()
				return knowledgeStore.LexicalPostingPage{}, err
			}
			if posting.Term != term {
				_ = iter.Close()
				return knowledgeStore.LexicalPostingPage{}, fmt.Errorf("%w: lexical posting term does not match index", knowledgeStore.ErrIncompatible)
			}
			postings = append(postings, posting)
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return knowledgeStore.LexicalPostingPage{}, fmt.Errorf("lookup lexical posting %q: %w", term, err)
		}
		if err := iter.Close(); err != nil {
			return knowledgeStore.LexicalPostingPage{}, fmt.Errorf("close lexical posting iterator: %w", err)
		}
	}
	documentCount, err := countLexicalDocuments(snapshot, s.meta.IndexGeneration)
	if err != nil {
		return knowledgeStore.LexicalPostingPage{}, err
	}
	return knowledgeStore.PaginateLexicalPostings(postings, request, s.meta.IndexGeneration, documentCount)
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

func decodeLexicalPosting(data []byte) (knowledgeStore.LexicalPosting, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var posting knowledgeStore.LexicalPosting
	if err := decoder.Decode(&posting); err != nil {
		return knowledgeStore.LexicalPosting{}, fmt.Errorf("%w: decode lexical posting: %v", knowledgeStore.ErrIncompatible, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return knowledgeStore.LexicalPosting{}, fmt.Errorf("%w: lexical posting contains trailing data", knowledgeStore.ErrIncompatible)
	}
	return posting, nil
}
