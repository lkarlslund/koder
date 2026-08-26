package pebble

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const evidenceSourceIndex = "evidence-source"

func defaultEvidenceIndexDefinitions() []indexDefinition {
	return []indexDefinition{{
		name: evidenceSourceIndex,
		build: func(ctx context.Context, record memoryStoreAPI.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != memoryStoreAPI.RecordKindEvidence {
				return nil, nil
			}
			sourceID, contentHash := memory.NormalizeEvidenceIdentity(record.Evidence.Source.ID, record.Evidence.Source.ContentHash)
			return []indexEntry{{
				Suffix: encodeIndexTuple(sourceID, contentHash, string(record.Evidence.ID)), Value: []byte(record.Evidence.ID),
			}}, nil
		},
	}}
}

func buildEvidenceIndexEntries(ctx context.Context, definitions []indexDefinition, evidence memory.Evidence) (map[string][]indexEntry, error) {
	record := memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEvidence, Evidence: &evidence}
	result := make(map[string][]indexEntry, len(definitions))
	for _, definition := range definitions {
		entries, err := definition.build(ctx, record)
		if err != nil {
			return nil, fmt.Errorf("build memory index %s for evidence %s: %w", definition.name, evidence.ID, err)
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
