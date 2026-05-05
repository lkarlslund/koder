package tools_test

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestDisplayTextForPartUsesWriteContent(t *testing.T) {
	text, ok := tools.DisplayTextForPart(toolOutputPart(domain.ToolKindWrite, tools.StoredResultStatusOK, "Created note.txt", tools.WriteStoredResult{
		Path:    "note.txt",
		Action:  "created",
		Summary: "Created note.txt",
		Content: "first line\nsecond line",
	}))
	if !ok {
		t.Fatal("expected display text")
	}
	if text != "first line\nsecond line" {
		t.Fatalf("unexpected display text: %q", text)
	}
}

func TestDisplayTextForPartIncludesWriteTruncationNotice(t *testing.T) {
	text, ok := tools.DisplayTextForPart(toolOutputPart(domain.ToolKindWrite, tools.StoredResultStatusOK, "Created note.txt", tools.WriteStoredResult{
		Path:      "note.txt",
		Action:    "created",
		Summary:   "Created note.txt",
		Content:   "first line",
		Truncated: true,
	}))
	if !ok {
		t.Fatal("expected display text")
	}
	if !strings.Contains(text, "truncated") {
		t.Fatalf("expected truncation notice, got %q", text)
	}
}

func TestDisplayTextForPartFormatsEditHunks(t *testing.T) {
	text, ok := tools.DisplayTextForPart(toolOutputPart(domain.ToolKindEdit, tools.StoredResultStatusOK, "Edited game/map.go", tools.EditStoredResult{
		Path:    "game/map.go",
		Summary: "Edited game/map.go (replaced 1 occurrence)",
		Diff:    "--- game/map.go\n+++ game/map.go\n@@ -12,1 +12,1 @@\n-if oldCondition {\n+if newCondition {",
	}))
	if !ok {
		t.Fatal("expected display text")
	}
	for _, want := range []string{"--- game/map.go", "+++ game/map.go", "@@ -12,1 +12,1 @@", "-if oldCondition {", "+if newCondition {"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in %q", want, text)
		}
	}
}

func TestModelTextForPartUsesEditSummaryWithoutDiff(t *testing.T) {
	text, ok := tools.ModelTextForPart(toolOutputPart(domain.ToolKindEdit, tools.StoredResultStatusOK, "Edited game/map.go", tools.EditStoredResult{
		Path:    "game/map.go",
		Summary: "Edited game/map.go (replaced 1 occurrence)",
		Diff:    "--- game/map.go\n+++ game/map.go\n@@ -12,1 +12,1 @@\n-if oldCondition {\n+if newCondition {",
	}), "--- ignored diff")
	if !ok {
		t.Fatal("expected model text")
	}
	if text != "Edited game/map.go (replaced 1 occurrence)" {
		t.Fatalf("unexpected model text: %q", text)
	}
}

func TestModelTextForPartUsesApplyPatchSummaryWithoutDiff(t *testing.T) {
	text, ok := tools.ModelTextForPart(toolOutputPart(domain.ToolKindApplyPatch, tools.StoredResultStatusOK, "Applied patch", tools.ApplyPatchStoredResult{
		Summary:      "Applied patch to game/map.go",
		ChangedFiles: []string{"game/map.go"},
		FileCount:    1,
	}), "--- ignored diff")
	if !ok {
		t.Fatal("expected model text")
	}
	if text != "Applied patch to game/map.go" {
		t.Fatalf("unexpected model text: %q", text)
	}
}

func TestDisplayTextForPartStripsRedundantToolFailurePrefix(t *testing.T) {
	text, ok := tools.DisplayTextForPart(toolOutputPart(domain.ToolKindTodoUpdateItem, tools.StoredResultStatusError, "todo_update_item failed: id must be a non-negative integer", tools.ErrorStoredResult{
		Message: "todo_update_item failed: id must be a non-negative integer",
	}))
	if !ok {
		t.Fatal("expected display text")
	}
	if text != "id must be a non-negative integer" {
		t.Fatalf("unexpected display text: %q", text)
	}
}

func toolOutputPart(tool domain.ToolKind, status tools.StoredResultStatus, text string, result any) domain.Part {
	return domain.Part{
		Kind: domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:   tool,
			Status: domain.ToolResultStatus(status),
			Text:   text,
			Result: result,
		},
	}
}
