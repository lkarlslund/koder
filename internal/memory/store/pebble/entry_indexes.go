package pebble

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const (
	entryChunkIndex       = "entry-chunk"
	entryKindIndex        = "entry-kind"
	entryScopeIndex       = "entry-scope"
	entryTagIndex         = "entry-tag"
	entryAliasIndex       = "entry-alias"
	entryLocaleIndex      = "entry-locale"
	entryStateIndex       = "entry-state"
	entryTitleIndex       = "entry-title"
	entryCreatedAtIndex   = "entry-created-at"
	entryUpdatedAtIndex   = "entry-updated-at"
	entryLastUsedAtIndex  = "entry-last-used-at"
	entryReviewAfterIndex = "entry-review-after"
	entryValidFromIndex   = "entry-valid-from"
	entryValidUntilIndex  = "entry-valid-until"
)

func defaultEntryIndexDefinitions() []indexDefinition {
	definitions := []indexDefinition{
		entrySingleIndex(entryChunkIndex, func(entry memory.Entry) []string {
			return []string{string(entry.ChunkID)}
		}),
		entrySingleIndex(entryKindIndex, func(entry memory.Entry) []string {
			return []string{entry.Kind.String()}
		}),
		entrySingleIndex(entryScopeIndex, func(entry memory.Entry) []string {
			return []string{entry.Scope.Kind.String(), entry.Scope.Selector}
		}),
		entryMultiIndex(entryTagIndex, func(entry memory.Entry) []string { return entry.Tags }),
		entryMultiIndex(entryAliasIndex, func(entry memory.Entry) []string {
			aliases := make([]string, 0, len(entry.Aliases))
			for _, alias := range entry.Aliases {
				aliases = append(aliases, memory.NormalizeComparisonKey(alias))
			}
			return aliases
		}),
		entryMultiIndex(entryLocaleIndex, func(entry memory.Entry) []string { return entry.Applicability.Locales }),
		entrySingleIndex(entryStateIndex, func(entry memory.Entry) []string {
			return []string{entry.State.String()}
		}),
		entrySingleIndex(entryTitleIndex, func(entry memory.Entry) []string {
			return []string{memory.NormalizeComparisonKey(entry.Title)}
		}),
		entrySingleIndex(entryCreatedAtIndex, func(entry memory.Entry) []string {
			return []string{indexTime(entry.CreatedAt)}
		}),
		entrySingleIndex(entryUpdatedAtIndex, func(entry memory.Entry) []string {
			return []string{indexTime(entry.UpdatedAt)}
		}),
		entrySingleIndex(entryLastUsedAtIndex, func(entry memory.Entry) []string {
			return []string{indexTime(entry.LastUsedAt)}
		}),
		entrySingleIndex(entryReviewAfterIndex, func(entry memory.Entry) []string {
			return []string{indexTime(entry.ReviewAfter)}
		}),
		entrySingleIndex(entryValidFromIndex, func(entry memory.Entry) []string {
			return []string{indexTime(entry.ValidFrom)}
		}),
		entrySingleIndex(entryValidUntilIndex, func(entry memory.Entry) []string {
			return []string{indexTime(entry.ValidUntil)}
		}),
	}
	return append(definitions, entryLexicalIndexDefinition())
}

func entrySingleIndex(name string, components func(memory.Entry) []string) indexDefinition {
	return indexDefinition{
		name: name,
		build: func(ctx context.Context, record memoryStoreAPI.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != memoryStoreAPI.RecordKindEntry {
				return nil, nil
			}
			return []indexEntry{entryIndexEntry(record.Entry.ID, components(*record.Entry)...)}, nil
		},
	}
}

func entryMultiIndex(name string, values func(memory.Entry) []string) indexDefinition {
	return indexDefinition{
		name: name,
		build: func(ctx context.Context, record memoryStoreAPI.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != memoryStoreAPI.RecordKindEntry {
				return nil, nil
			}
			items := values(*record.Entry)
			entries := make([]indexEntry, 0, len(items))
			for _, item := range items {
				entries = append(entries, entryIndexEntry(record.Entry.ID, item))
			}
			return entries, nil
		},
	}
}

func entryIndexEntry(id memory.EntryID, components ...string) indexEntry {
	components = append(components, string(id))
	return indexEntry{Suffix: encodeIndexTuple(components...), Value: []byte(id)}
}

func buildEntryIndexEntries(ctx context.Context, definitions []indexDefinition, entry memory.Entry) (map[string][]indexEntry, error) {
	record := memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEntry, Entry: &entry}
	result := make(map[string][]indexEntry, len(definitions))
	for _, definition := range definitions {
		entries, err := definition.build(ctx, record)
		if err != nil {
			return nil, fmt.Errorf("build memory index %s for entry %s: %w", definition.name, entry.ID, err)
		}
		for _, item := range entries {
			if len(item.Suffix) == 0 {
				return nil, fmt.Errorf("memory index %s produced an empty key suffix", definition.name)
			}
		}
		result[definition.name] = entries
	}
	return result, nil
}
