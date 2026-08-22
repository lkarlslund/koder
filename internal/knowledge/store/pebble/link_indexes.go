package pebble

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const (
	linkOutgoingIndex = "link-outgoing"
	linkIncomingIndex = "link-incoming"
)

func defaultLinkIndexDefinitions() []indexDefinition {
	return []indexDefinition{
		linkEndpointIndex(linkOutgoingIndex, func(link knowledge.Link) knowledge.ObjectRef { return link.Source }),
		linkEndpointIndex(linkIncomingIndex, func(link knowledge.Link) knowledge.ObjectRef { return link.Target }),
	}
}

func linkEndpointIndex(name string, endpoint func(knowledge.Link) knowledge.ObjectRef) indexDefinition {
	return indexDefinition{
		name: name,
		build: func(ctx context.Context, record knowledgeStore.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != knowledgeStore.RecordKindLink {
				return nil, nil
			}
			owner := endpoint(*record.Link)
			return []indexEntry{{
				Suffix: encodeIndexTuple(owner.Kind.String(), owner.ID, record.Link.Kind.String(), string(record.Link.ID)),
				Value:  []byte(record.Link.ID),
			}}, nil
		},
	}
}

func buildLinkIndexEntries(ctx context.Context, definitions []indexDefinition, link knowledge.Link) (map[string][]indexEntry, error) {
	record := knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindLink, Link: &link}
	result := make(map[string][]indexEntry, len(definitions))
	for _, definition := range definitions {
		entries, err := definition.build(ctx, record)
		if err != nil {
			return nil, fmt.Errorf("build knowledge index %s for link %s: %w", definition.name, link.ID, err)
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
