package tools_test

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestDisplayTextForPartUsesWriteContent(t *testing.T) {
	text, ok := tools.DisplayTextForPart(toolOutputPart(domain.ToolKindFileWrite, tools.StoredResultStatusOK, "Created note.txt", tools.WriteStoredResult{
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
	text, ok := tools.DisplayTextForPart(toolOutputPart(domain.ToolKindFileWrite, tools.StoredResultStatusOK, "Created note.txt", tools.WriteStoredResult{
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
	text, ok := tools.DisplayTextForPart(toolOutputPart(domain.ToolKindFileEdit, tools.StoredResultStatusOK, "Edited game/map.go", tools.EditStoredResult{
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
	text, ok := tools.ModelTextForPart(toolOutputPart(domain.ToolKindFileEdit, tools.StoredResultStatusOK, "Edited game/map.go", tools.EditStoredResult{
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

func TestModelTextForPartUsesWriteSummaryWithoutContentOrDiff(t *testing.T) {
	text, ok := tools.ModelTextForPart(toolOutputPart(domain.ToolKindFileWrite, tools.StoredResultStatusOK, "Created note.txt", tools.WriteStoredResult{
		Path:    "note.txt",
		Action:  "created",
		Summary: "Created note.txt",
		Content: "first line\nsecond line",
	}), "--- ignored diff")
	if !ok {
		t.Fatal("expected model text")
	}
	if strings.Contains(text, "first line") || strings.Contains(text, "ignored diff") {
		t.Fatalf("expected write model text to omit content and diff, got %q", text)
	}
	for _, want := range []string{"Created note.txt", "Wrote 2 lines."} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in %q", want, text)
		}
	}
}

func TestCompactModelTextForPartTruncatesExecOutput(t *testing.T) {
	lines := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		lines = append(lines, "line "+string(rune('A'+i)))
	}
	exitCode := 7
	text, ok := tools.CompactModelTextForPart(toolOutputPart(domain.ToolKindExecCommand, tools.StoredResultStatusOK, "", tools.ExecStoredResult{
		ProcessID: "proc-1",
		Command:   "go test ./...",
		State:     "done",
		ExitCode:  &exitCode,
		Output:    strings.Join(lines, "\n"),
	}), "", tools.CompactFormatLimits{MaxBytes: 4096, ExecHeadLines: 3, ExecTailLines: 2})
	if !ok {
		t.Fatal("expected compact model text")
	}
	for _, want := range []string{"process_id: proc-1", "command: go test ./...", "exit_code: 7", "line A", "line B", "line C", "line S", "line T", "exec output truncated for compaction"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in %q", want, text)
		}
	}
	if strings.Contains(text, "line J") {
		t.Fatalf("expected middle output to be omitted, got %q", text)
	}
}

func TestCompactModelTextForPartOmitsViewImageBytes(t *testing.T) {
	text, ok := tools.CompactModelTextForPart(toolOutputPart(domain.ToolKindViewImage, tools.StoredResultStatusOK, "Viewed image", tools.ViewImageStoredResult{
		Path:       "screen.png",
		SourcePath: "/tmp/screen.png",
		MIMEType:   "image/png",
		Summary:    "Viewed image screen.png",
	}), "", tools.DefaultCompactFormatLimits())
	if !ok {
		t.Fatal("expected compact image text")
	}
	for _, want := range []string{"image bytes omitted", "summary: Viewed image screen.png", "path: screen.png", "mime: image/png"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in %q", want, text)
		}
	}
}

func TestCompactModelTextForPartBoundsReadOutput(t *testing.T) {
	var readLines []tools.ReadStoredLine
	for i := 1; i <= 8; i++ {
		readLines = append(readLines, tools.ReadStoredLine{Number: i, Text: "content"})
	}
	text, ok := tools.CompactModelTextForPart(toolOutputPart(domain.ToolKindFileRead, tools.StoredResultStatusOK, "", tools.ReadStoredResult{
		Path:  "main.go",
		Mode:  tools.ReadStoredModeFile,
		Lines: readLines,
		Start: 1,
		End:   8,
		Total: 8,
	}), "", tools.CompactFormatLimits{MaxBytes: 4096, ReadMaxLines: 4})
	if !ok {
		t.Fatal("expected compact read text")
	}
	if !strings.Contains(text, "path: main.go") || !strings.Contains(text, "range: 1-8 of 8") {
		t.Fatalf("expected read metadata, got %q", text)
	}
	if !strings.Contains(text, "read result truncated for compaction") || strings.Contains(text, "8: content") {
		t.Fatalf("expected bounded read output, got %q", text)
	}
}

func TestDisplayTextForPartStripsRedundantToolFailurePrefix(t *testing.T) {
	text, ok := tools.DisplayTextForPart(toolOutputPart(domain.ToolKindTaskUpdateItem, tools.StoredResultStatusError, "task_update_item failed: id must be a non-negative integer", tools.ErrorStoredResult{
		Message: "task_update_item failed: id must be a non-negative integer",
	}))
	if !ok {
		t.Fatal("expected display text")
	}
	if text != "id must be a non-negative integer" {
		t.Fatalf("unexpected display text: %q", text)
	}
}

func TestDisplayTextForPartIgnoresLegacyMetaJSON(t *testing.T) {
	_, ok := tools.DisplayTextForPart(domain.Part{
		Kind:     domain.PartKindToolOutput,
		MetaJSON: `{"_stored_result":"{\"version\":1}"}`,
	})
	if ok {
		t.Fatal("expected legacy MetaJSON stored result to be ignored")
	}
}

func TestDisplayTextForPartIncludesChatQueuedInputs(t *testing.T) {
	text, ok := tools.DisplayTextForPart(toolOutputPart(domain.ToolKindChatPoll, tools.StoredResultStatusOK, "", tools.ChatListStoredResult{
		Items: []tools.ChatStoredItem{{
			ID:           "chat-1",
			Title:        "Queued child",
			State:        "idle",
			QueuedInputs: 1,
			StatusText:   "Idle",
		}},
	}))
	if !ok {
		t.Fatal("expected display text")
	}
	if !strings.Contains(text, "{idle} {queued_inputs:1}") {
		t.Fatalf("expected queued input count, got %q", text)
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
