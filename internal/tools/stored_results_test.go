package tools_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestDisplayTextForPartUsesWriteContent(t *testing.T) {
	meta, err := json.Marshal(tools.MetaWithStoredResult(map[string]string{
		"tool": "write",
		"path": "note.txt",
	}, domain.PartKindToolOutput, domain.ToolKindWrite, tools.StoredResultStatusOK, tools.WriteStoredResult{
		Path:    "note.txt",
		Action:  "created",
		Summary: "Created note.txt",
		Content: "first line\nsecond line",
	}))
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	text, ok := tools.DisplayTextForPart(domain.Part{
		Kind:     domain.PartKindToolOutput,
		Body:     "Created note.txt",
		MetaJSON: string(meta),
	})
	if !ok {
		t.Fatal("expected display text")
	}
	if text != "first line\nsecond line" {
		t.Fatalf("unexpected display text: %q", text)
	}
}

func TestDisplayTextForPartIncludesWriteTruncationNotice(t *testing.T) {
	meta, err := json.Marshal(tools.MetaWithStoredResult(map[string]string{
		"tool": "write",
		"path": "note.txt",
	}, domain.PartKindToolOutput, domain.ToolKindWrite, tools.StoredResultStatusOK, tools.WriteStoredResult{
		Path:      "note.txt",
		Action:    "created",
		Summary:   "Created note.txt",
		Content:   "first line",
		Truncated: true,
	}))
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	text, ok := tools.DisplayTextForPart(domain.Part{
		Kind:     domain.PartKindToolOutput,
		Body:     "Created note.txt",
		MetaJSON: string(meta),
	})
	if !ok {
		t.Fatal("expected display text")
	}
	if !strings.Contains(text, "truncated") {
		t.Fatalf("expected truncation notice, got %q", text)
	}
}
