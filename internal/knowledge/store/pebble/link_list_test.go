package pebble

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestListAdjacentLinksReadsEndpointIndexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	first := txLink()
	second := txLink()
	second.ID = "01a020a6-84d5-7b03-a995-bb2cfb4528b1"
	second.Source, second.Target = first.Target, first.Source
	second.Kind = knowledge.LinkKindRequires
	if err := s.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutLink(ctx, first, 0); err != nil {
			return err
		}
		return tx.PutLink(ctx, second, 0)
	}); err != nil {
		t.Fatalf("put links: %v", err)
	}
	root := first.Source
	page, err := s.ListAdjacentLinks(ctx, knowledgeStore.AdjacentLinkListRequest{
		Filter: knowledgeStore.AdjacentLinkFilter{Endpoint: root, Direction: knowledgeStore.LinkDirectionBoth}, Limit: 1,
	})
	if err != nil || len(page.Links) != 1 || page.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	page, err = s.ListAdjacentLinks(ctx, knowledgeStore.AdjacentLinkListRequest{
		Filter: knowledgeStore.AdjacentLinkFilter{Endpoint: root, Direction: knowledgeStore.LinkDirectionBoth},
		Limit:  1, Cursor: page.NextCursor,
	})
	if err != nil || len(page.Links) != 1 {
		t.Fatalf("second page = %#v, %v", page, err)
	}
	incoming, err := s.ListAdjacentLinks(ctx, knowledgeStore.AdjacentLinkListRequest{Filter: knowledgeStore.AdjacentLinkFilter{
		Endpoint: root, Direction: knowledgeStore.LinkDirectionIncoming, Kinds: []knowledge.LinkKind{knowledge.LinkKindRequires},
	}})
	if err != nil || len(incoming.Links) != 1 || incoming.Links[0].ID != second.ID {
		t.Fatalf("incoming page = %#v, %v", incoming, err)
	}
}
