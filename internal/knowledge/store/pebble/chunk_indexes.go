package pebble

import (
	"context"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	chunkKindIndex        = "chunk-kind"
	chunkScopeIndex       = "chunk-scope"
	chunkTagIndex         = "chunk-tag"
	chunkAliasIndex       = "chunk-alias"
	chunkLocaleIndex      = "chunk-locale"
	chunkStateIndex       = "chunk-state"
	chunkTitleIndex       = "chunk-title"
	chunkCreatedAtIndex   = "chunk-created-at"
	chunkUpdatedAtIndex   = "chunk-updated-at"
	chunkLastUsedAtIndex  = "chunk-last-used-at"
	chunkReviewAfterIndex = "chunk-review-after"
)

func defaultIndexDefinitions() []indexDefinition {
	definitions := []indexDefinition{
		chunkSingleIndex(chunkKindIndex, func(chunk knowledge.Chunk) []string {
			return []string{chunk.Kind.String()}
		}),
		chunkSingleIndex(chunkScopeIndex, func(chunk knowledge.Chunk) []string {
			return []string{chunk.Scope.Kind.String(), chunk.Scope.Selector}
		}),
		{
			name: chunkTagIndex,
			build: func(ctx context.Context, record knowledgeStore.CanonicalRecord) ([]indexEntry, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if record.Kind != knowledgeStore.RecordKindChunk {
					return nil, nil
				}
				entries := make([]indexEntry, 0, len(record.Chunk.Tags))
				for _, tag := range record.Chunk.Tags {
					entries = append(entries, chunkIndexEntry(record.Chunk.ID, tag))
				}
				return entries, nil
			},
		},
		chunkMultiIndex(chunkAliasIndex, func(chunk knowledge.Chunk) []string {
			aliases := make([]string, 0, len(chunk.Aliases))
			for _, alias := range chunk.Aliases {
				aliases = append(aliases, knowledge.NormalizeComparisonKey(alias))
			}
			return aliases
		}),
		chunkSingleIndex(chunkLocaleIndex, func(chunk knowledge.Chunk) []string {
			return []string{chunk.Locale}
		}),
		chunkSingleIndex(chunkStateIndex, func(chunk knowledge.Chunk) []string {
			return []string{chunk.State.String()}
		}),
		chunkSingleIndex(chunkTitleIndex, func(chunk knowledge.Chunk) []string {
			return []string{knowledge.NormalizeComparisonKey(chunk.Title)}
		}),
		chunkSingleIndex(chunkCreatedAtIndex, func(chunk knowledge.Chunk) []string {
			return []string{indexTime(chunk.CreatedAt)}
		}),
		chunkSingleIndex(chunkUpdatedAtIndex, func(chunk knowledge.Chunk) []string {
			return []string{indexTime(chunk.UpdatedAt)}
		}),
		chunkSingleIndex(chunkLastUsedAtIndex, func(chunk knowledge.Chunk) []string {
			return []string{indexTime(chunk.LastUsedAt)}
		}),
		chunkSingleIndex(chunkReviewAfterIndex, func(chunk knowledge.Chunk) []string {
			return []string{indexTime(chunk.ReviewAfter)}
		}),
	}
	definitions = append(definitions, defaultEntryIndexDefinitions()...)
	definitions = append(definitions, defaultLinkIndexDefinitions()...)
	return append(definitions, defaultEvidenceIndexDefinitions()...)
}

func chunkMultiIndex(name string, values func(knowledge.Chunk) []string) indexDefinition {
	return indexDefinition{
		name: name,
		build: func(ctx context.Context, record knowledgeStore.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != knowledgeStore.RecordKindChunk {
				return nil, nil
			}
			items := values(*record.Chunk)
			entries := make([]indexEntry, 0, len(items))
			for _, item := range items {
				entries = append(entries, chunkIndexEntry(record.Chunk.ID, item))
			}
			return entries, nil
		},
	}
}

func chunkSingleIndex(name string, components func(knowledge.Chunk) []string) indexDefinition {
	return indexDefinition{
		name: name,
		build: func(ctx context.Context, record knowledgeStore.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != knowledgeStore.RecordKindChunk {
				return nil, nil
			}
			return []indexEntry{chunkIndexEntry(record.Chunk.ID, components(*record.Chunk)...)}, nil
		},
	}
}

func chunkIndexEntry(id knowledge.ChunkID, components ...string) indexEntry {
	components = append(components, string(id))
	return indexEntry{Suffix: encodeIndexTuple(components...), Value: []byte(id)}
}

// encodeIndexTuple preserves bytewise component ordering while preventing separators in
// user-controlled values from changing tuple boundaries. Zero bytes are escaped as 00 ff;
// components end in 00 00.
func encodeIndexTuple(components ...string) []byte {
	encoded := make([]byte, 0, len(components)*12)
	for _, component := range components {
		for _, value := range []byte(component) {
			if value == 0 {
				encoded = append(encoded, 0, 0xff)
				continue
			}
			encoded = append(encoded, value)
		}
		encoded = append(encoded, 0, 0)
	}
	return encoded
}

func indexTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("20060102T150405.000000000Z")
}

func buildChunkIndexEntries(ctx context.Context, definitions []indexDefinition, chunk knowledge.Chunk) (map[string][]indexEntry, error) {
	record := knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindChunk, Chunk: &chunk}
	result := make(map[string][]indexEntry, len(definitions))
	for _, definition := range definitions {
		entries, err := definition.build(ctx, record)
		if err != nil {
			return nil, fmt.Errorf("build knowledge index %s for chunk %s: %w", definition.name, chunk.ID, err)
		}
		for _, entry := range entries {
			if len(entry.Suffix) == 0 {
				return nil, fmt.Errorf("knowledge index %s produced an empty key suffix", definition.name)
			}
		}
		result[definition.name] = entries
	}
	return result, nil
}
