package pebble

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const evidenceSourceIndex = "evidence-source"

func defaultEvidenceIndexDefinitions() []indexDefinition {
	return []indexDefinition{{
		name: evidenceSourceIndex,
		build: func(ctx context.Context, record knowledgeStore.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != knowledgeStore.RecordKindEvidence {
				return nil, nil
			}
			sourceID, contentHash := knowledge.NormalizeEvidenceIdentity(record.Evidence.Source.ID, record.Evidence.Source.ContentHash)
			return []indexEntry{{
				Suffix: encodeIndexTuple(sourceID, contentHash, string(record.Evidence.ID)), Value: []byte(record.Evidence.ID),
			}}, nil
		},
	}}
}

func buildEvidenceIndexEntries(ctx context.Context, definitions []indexDefinition, evidence knowledge.Evidence) (map[string][]indexEntry, error) {
	record := knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEvidence, Evidence: &evidence}
	result := make(map[string][]indexEntry, len(definitions))
	for _, definition := range definitions {
		entries, err := definition.build(ctx, record)
		if err != nil {
			return nil, fmt.Errorf("build knowledge index %s for evidence %s: %w", definition.name, evidence.ID, err)
		}
		for _, item := range entries {
			if len(item.Suffix) == 0 {
				return nil, fmt.Errorf("knowledge index %s produced an empty key suffix", definition.name)
			}
		}
		result[definition.name] = entries
	}
	return result, nil
}
