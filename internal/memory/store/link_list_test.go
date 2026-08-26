package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

func TestPaginateAdjacentLinksFiltersDirectionTypeStateAndUsesCursor(t *testing.T) {
	t.Parallel()
	root := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: "019f132e-4f3a-739a-9ab2-5198dcd19e67"}
	other := memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33"}
	links := []memory.Link{
		listTestLink(1, root, other, memory.LinkKindRelatedTo, memory.LinkStateActive),
		listTestLink(2, other, root, memory.LinkKindRequires, memory.LinkStateActive),
		listTestLink(3, root, other, memory.LinkKindRelatedTo, memory.LinkStateArchived),
	}
	request := AdjacentLinkListRequest{Filter: AdjacentLinkFilter{
		Endpoint: root, Direction: LinkDirectionBoth, States: []memory.LinkState{memory.LinkStateActive},
	}, Limit: 1}
	first, err := PaginateAdjacentLinks(links, request, 2)
	if err != nil || len(first.Links) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	request.Cursor = first.NextCursor
	second, err := PaginateAdjacentLinks(links, request, 2)
	if err != nil || len(second.Links) != 1 || second.Links[0].ID == first.Links[0].ID {
		t.Fatalf("second page = %#v, %v", second, err)
	}
	outgoing, err := PaginateAdjacentLinks(links, AdjacentLinkListRequest{Filter: AdjacentLinkFilter{
		Endpoint: root, Direction: LinkDirectionOutgoing, Kinds: []memory.LinkKind{memory.LinkKindRelatedTo},
		States: []memory.LinkState{memory.LinkStateActive},
	}}, 2)
	if err != nil || len(outgoing.Links) != 1 || outgoing.Links[0].ID != links[0].ID {
		t.Fatalf("outgoing filtered links = %#v, %v", outgoing, err)
	}
}

func TestAdjacentLinkCursorBindsEndpointAndDirection(t *testing.T) {
	t.Parallel()
	root := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: "019f132e-4f3a-739a-9ab2-5198dcd19e67"}
	other := memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33"}
	links := []memory.Link{
		listTestLink(1, root, other, memory.LinkKindRelatedTo, memory.LinkStateActive),
		listTestLink(2, root, other, memory.LinkKindRequires, memory.LinkStateActive),
	}
	request := AdjacentLinkListRequest{Filter: AdjacentLinkFilter{Endpoint: root}, Limit: 1}
	page, err := PaginateAdjacentLinks(links, request, 1)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	request.Cursor = page.NextCursor
	request.Filter.Direction = LinkDirectionOutgoing
	if _, err := PaginateAdjacentLinks(links, request, 1); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("changed direction error = %v, want ErrInvalidCursor", err)
	}
}

func TestPaginateAdjacentLinksRejectsInvalidBounds(t *testing.T) {
	t.Parallel()
	root := memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: "019f132e-4f3a-739a-9ab2-5198dcd19e67"}
	for _, request := range []AdjacentLinkListRequest{
		{}, {Filter: AdjacentLinkFilter{Endpoint: root, Direction: "sideways"}},
		{Filter: AdjacentLinkFilter{Endpoint: root}, Limit: 101},
		{Filter: AdjacentLinkFilter{Endpoint: root, Kinds: []memory.LinkKind{memory.LinkKindUnspecified}}},
	} {
		if _, err := PaginateAdjacentLinks(nil, request, 1); err == nil {
			t.Errorf("PaginateAdjacentLinks(%#v) unexpectedly succeeded", request)
		}
	}
}

func listTestLink(number uint64, source, target memory.ObjectRef, kind memory.LinkKind, state memory.LinkState) memory.Link {
	at := time.Date(2026, 8, 22, 12, 0, int(number), 0, time.UTC)
	return memory.Link{
		ID: memory.LinkID(fmt.Sprintf("01a020a6-84d5-7b03-a995-%012x", number)), Source: source, Target: target,
		Kind: kind, State: state,
		Revision: memory.Revision{
			Number: 1, ID: memory.RevisionID(fmt.Sprintf("01a01688-fc5d-7f7d-8bb8-%012x", number)),
			Actor: memory.Actor{Kind: memory.ActorKindSystem, ID: "test"}, CreatedAt: at,
		},
		CreatedAt: at, UpdatedAt: at,
	}
}
