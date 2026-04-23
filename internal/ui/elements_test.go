package ui

import (
	"strings"
	"testing"
)

func TestRowRenderPlacesChildrenHorizontally(t *testing.T) {
	got := RenderElement(nil, Row{
		Children: []Child{
			Fixed(Static{Content: "A"}),
			Fixed(Static{Content: "B"}),
		},
		Spacing: 1,
	}, 4, 1)

	if got != "A B " {
		t.Fatalf("unexpected row render: %q", got)
	}
}

func TestColumnRenderPlacesChildrenVertically(t *testing.T) {
	got := RenderElement(nil, Column{
		Children: []Child{
			Fixed(Static{Content: "A"}),
			Fixed(Static{Content: "B"}),
		},
		Spacing: 1,
	}, 1, 3)

	if got != "A\n \nB" {
		t.Fatalf("unexpected column render: %q", got)
	}
}

func TestAlignCentersChildWithinBounds(t *testing.T) {
	got := RenderElement(nil, Align{
		Horizontal: AlignCenter,
		Vertical:   AlignCenter,
		Child:      Static{Content: "X"},
	}, 3, 3)

	lines := strings.Split(got, "\n")
	if len(lines) != 3 || lines[1] != " X " {
		t.Fatalf("unexpected centered render: %q", got)
	}
}

func TestInsetAddsPadding(t *testing.T) {
	got := RenderElement(nil, Inset{
		Padding: UniformInsets(1),
		Child:   Static{Content: "X"},
	}, 3, 3)

	lines := strings.Split(got, "\n")
	if len(lines) != 3 || lines[1] != " X " {
		t.Fatalf("unexpected inset render: %q", got)
	}
}

func TestStackOverlaysLaterChildren(t *testing.T) {
	got := RenderElement(nil, Stack{
		Children: []Element{
			Static{Content: "AAAA"},
			Static{Content: " BB "},
		},
	}, 4, 1)

	if got != " BB " {
		t.Fatalf("unexpected stack render: %q", got)
	}
}

func TestConstrainedClampsChildSize(t *testing.T) {
	got := RenderElement(nil, Constrained{
		Constraints: Constraints{MaxW: 2, MaxH: 1},
		Child:       Static{Content: "WIDE"},
	}, 4, 1)

	if got != "WI  " {
		t.Fatalf("unexpected constrained render: %q", got)
	}
}
