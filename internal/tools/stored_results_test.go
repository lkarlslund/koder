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

func TestDisplayTextForPartFormatsEditHunks(t *testing.T) {
	meta, err := json.Marshal(tools.MetaWithStoredResult(map[string]string{
		"tool": "edit",
		"path": "game/map.go",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/map.go",
		Summary: "Edited game/map.go (replaced 1 occurrence)",
		Hunks: []tools.EditStoredHunk{{
			OldStart: 12,
			NewStart: 12,
			OldLines: []string{"if oldCondition {"},
			NewLines: []string{"if newCondition {"},
		}},
	}))
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	text, ok := tools.DisplayTextForPart(domain.Part{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/map.go (replaced 1 occurrence)",
		MetaJSON: string(meta),
	})
	if !ok {
		t.Fatal("expected display text")
	}
	for _, want := range []string{
		"Edited game/map.go (replaced 1 occurrence)",
		"@@ -12,1 +12,1 @@",
		"-12 if oldCondition {",
		"+12 if newCondition {",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in %q", want, text)
		}
	}
}
