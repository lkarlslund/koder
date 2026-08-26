package pebble

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const (
	linkOutgoingIndex = "link-outgoing"
	linkIncomingIndex = "link-incoming"
)

func defaultLinkIndexDefinitions() []indexDefinition {
	return []indexDefinition{
		linkEndpointIndex(linkOutgoingIndex, func(link memory.Link) memory.ObjectRef { return link.Source }),
		linkEndpointIndex(linkIncomingIndex, func(link memory.Link) memory.ObjectRef { return link.Target }),
	}
}

func linkEndpointIndex(name string, endpoint func(memory.Link) memory.ObjectRef) indexDefinition {
	return indexDefinition{
		name: name,
		build: func(ctx context.Context, record memoryStoreAPI.CanonicalRecord) ([]indexEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if record.Kind != memoryStoreAPI.RecordKindLink {
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

func buildLinkIndexEntries(ctx context.Context, definitions []indexDefinition, link memory.Link) (map[string][]indexEntry, error) {
	record := memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindLink, Link: &link}
	result := make(map[string][]indexEntry, len(definitions))
	for _, definition := range definitions {
		entries, err := definition.build(ctx, record)
		if err != nil {
			return nil, fmt.Errorf("build memory index %s for link %s: %w", definition.name, link.ID, err)
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
