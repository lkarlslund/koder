package greptool

import (
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestPresentationForPreviewOmitsQueryPrefix(t *testing.T) {
	got := tool{}.PresentationForPreview("needle")
	if got.Title != "Search text" {
		t.Fatalf("unexpected title: %#v", got)
	}
	if got.Subtitle != "needle" {
		t.Fatalf("expected plain subtitle without query prefix, got %#v", got)
	}
}

func TestPresentationIncludesPathScope(t *testing.T) {
	got := tool{}.Presentation(tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
			"path":    "internal",
		},
	})
	if got.Subtitle != "needle in internal" {
		t.Fatalf("unexpected subtitle: %#v", got)
	}
}

func TestPresentationIncludesIncludeScope(t *testing.T) {
	got := tool{}.Presentation(tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
			"include": "*.go",
		},
	})
	if got.Subtitle != "needle in *.go" {
		t.Fatalf("unexpected subtitle: %#v", got)
	}
}

func TestPresentationIncludesPathAndIncludeScope(t *testing.T) {
	got := tool{}.Presentation(tools.Request{
		Tool: domain.ToolKindGrep,
		Args: map[string]string{
			"pattern": "needle",
			"path":    "internal",
			"include": "*.go",
		},
	})
	if got.Subtitle != "needle in internal (*.go)" {
		t.Fatalf("unexpected subtitle: %#v", got)
	}
}
