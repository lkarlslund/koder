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
		Diff:    "--- game/map.go\n+++ game/map.go\n@@ -12,1 +12,1 @@\n-if oldCondition {\n+if newCondition {",
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
		"--- game/map.go",
		"+++ game/map.go",
		"@@ -12,1 +12,1 @@",
		"-if oldCondition {",
		"+if newCondition {",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in %q", want, text)
		}
	}
}

func TestModelTextForPartUsesEditSummaryWithoutDiff(t *testing.T) {
	meta, err := json.Marshal(tools.MetaWithStoredResult(map[string]string{
		"tool": "edit",
		"path": "game/map.go",
	}, domain.PartKindToolOutput, domain.ToolKindEdit, tools.StoredResultStatusOK, tools.EditStoredResult{
		Path:    "game/map.go",
		Summary: "Edited game/map.go (replaced 1 occurrence)",
		Diff:    "--- game/map.go\n+++ game/map.go\n@@ -12,1 +12,1 @@\n-if oldCondition {\n+if newCondition {",
	}))
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	text, ok := tools.ModelTextForPart(domain.Part{
		Kind:     domain.PartKindToolOutput,
		Body:     "Edited game/map.go (replaced 1 occurrence)",
		MetaJSON: string(meta),
	}, "--- ignored diff")
	if !ok {
		t.Fatal("expected model text")
	}
	if text != "Edited game/map.go (replaced 1 occurrence)" {
		t.Fatalf("unexpected model text: %q", text)
	}
}

func TestModelTextForPartUsesApplyPatchSummaryWithoutDiff(t *testing.T) {
	meta, err := json.Marshal(tools.MetaWithStoredResult(map[string]string{
		"tool": "apply_patch",
	}, domain.PartKindToolOutput, domain.ToolKindApplyPatch, tools.StoredResultStatusOK, tools.ApplyPatchStoredResult{
		Summary:      "Applied patch to game/map.go",
		ChangedFiles: []string{"game/map.go"},
		FileCount:    1,
	}))
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}

	text, ok := tools.ModelTextForPart(domain.Part{
		Kind:     domain.PartKindToolOutput,
		Body:     "Applied patch to game/map.go",
		MetaJSON: string(meta),
	}, "--- ignored diff")
	if !ok {
		t.Fatal("expected model text")
	}
	if text != "Applied patch to game/map.go" {
		t.Fatalf("unexpected model text: %q", text)
	}
}
