package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

type lexicalSearchCursorQuery struct {
	Terms             []string               `json:"terms"`
	ChunkIDs          []memory.ChunkID       `json:"chunk_ids,omitempty"`
	Scopes            []memory.Scope         `json:"scopes,omitempty"`
	EntryStates       []memory.EntryState    `json:"entry_states"`
	ChunkStates       []memory.ChunkState    `json:"chunk_states"`
	IncludeInvalid    bool                   `json:"include_invalid"`
	IncludeSuperseded bool                   `json:"include_superseded"`
	ExplicitValidAt   string                 `json:"explicit_valid_at,omitempty"`
	Weights           LexicalFieldWeights    `json:"weights"`
	GraphExpansion    *GraphExpansionOptions `json:"graph_expansion,omitempty"`
	Actor             memory.Actor           `json:"actor"`
}

type rankedSearchCursorPosition struct {
	AsOf         time.Time      `json:"as_of"`
	Total        float64        `json:"total"`
	Verification float64        `json:"verification"`
	LexicalScore float64        `json:"lexical_score"`
	EntryID      memory.EntryID `json:"entry_id"`
}

func lexicalSearchCursorBinding(request LexicalSearchRequest, terms []string, actor memory.Actor, generation uint64, explicitValidAt bool) (memoryStoreAPI.CursorBinding, error) {
	query := lexicalSearchCursorQuery{
		Terms: terms, ChunkIDs: request.ChunkIDs, Scopes: request.Scopes,
		EntryStates: request.EntryStates, ChunkStates: request.ChunkStates,
		IncludeInvalid: request.IncludeInvalid, IncludeSuperseded: request.IncludeSuperseded,
		Weights: request.Weights, GraphExpansion: request.GraphExpansion, Actor: actor,
	}
	if explicitValidAt {
		query.ExplicitValidAt = request.ValidAt.UTC().Format(time.RFC3339Nano)
	}
	encoded, err := json.Marshal(query)
	if err != nil {
		return memoryStoreAPI.CursorBinding{}, err
	}
	digest := sha256.Sum256(encoded)
	return memoryStoreAPI.CursorBinding{
		Index: "memory-lexical-search", IndexGeneration: generation,
		QueryFingerprint: hex.EncodeToString(digest[:]),
		SortField:        "rank_verification_lexical_entry_id", Descending: true,
	}, nil
}

func encodeRankedSearchCursorPosition(asOf time.Time, match LexicalSearchMatch) (memoryStoreAPI.CursorPosition, error) {
	position := rankedSearchCursorPosition{
		AsOf: asOf.UTC().Round(0), Total: match.Rank.Total, Verification: match.Rank.Verification,
		LexicalScore: match.LexicalScore, EntryID: match.EntryID,
	}
	if err := validateRankedSearchCursorPosition(position); err != nil {
		return memoryStoreAPI.CursorPosition{}, err
	}
	encoded, err := json.Marshal(position)
	if err != nil {
		return memoryStoreAPI.CursorPosition{}, err
	}
	return memoryStoreAPI.CursorPosition{
		SortValue: base64.RawURLEncoding.EncodeToString(encoded), ObjectID: string(match.EntryID),
	}, nil
}

func decodeRankedSearchCursorPosition(position memoryStoreAPI.CursorPosition) (rankedSearchCursorPosition, error) {
	encoded, err := base64.RawURLEncoding.DecodeString(position.SortValue)
	if err != nil {
		return rankedSearchCursorPosition{}, fmt.Errorf("%w: decode lexical search rank position", memoryStoreAPI.ErrInvalidCursor)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var value rankedSearchCursorPosition
	if err := decoder.Decode(&value); err != nil {
		return rankedSearchCursorPosition{}, fmt.Errorf("%w: decode lexical search rank position", memoryStoreAPI.ErrInvalidCursor)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return rankedSearchCursorPosition{}, fmt.Errorf("%w: lexical search rank position contains trailing data", memoryStoreAPI.ErrInvalidCursor)
	}
	if string(value.EntryID) != position.ObjectID {
		return rankedSearchCursorPosition{}, fmt.Errorf("%w: lexical search rank position ID mismatch", memoryStoreAPI.ErrInvalidCursor)
	}
	if err := validateRankedSearchCursorPosition(value); err != nil {
		return rankedSearchCursorPosition{}, err
	}
	return value, nil
}

func validateRankedSearchCursorPosition(position rankedSearchCursorPosition) error {
	_, offset := position.AsOf.Zone()
	if position.AsOf.IsZero() || offset != 0 || position.EntryID == "" ||
		math.IsNaN(position.Total) || math.IsInf(position.Total, 0) ||
		math.IsNaN(position.Verification) || math.IsInf(position.Verification, 0) ||
		math.IsNaN(position.LexicalScore) || math.IsInf(position.LexicalScore, 0) {
		return fmt.Errorf("%w: invalid lexical search rank position", memoryStoreAPI.ErrInvalidCursor)
	}
	return nil
}

func rankedSearchMatchesAfter(matches []LexicalSearchMatch, position rankedSearchCursorPosition) []LexicalSearchMatch {
	cursorMatch := LexicalSearchMatch{
		EntryID: position.EntryID, LexicalScore: position.LexicalScore,
		Rank: SearchRank{Total: position.Total, Verification: position.Verification},
	}
	for index, match := range matches {
		if compareRankedSearchMatches(match, cursorMatch) > 0 {
			return matches[index:]
		}
	}
	return matches[len(matches):]
}
