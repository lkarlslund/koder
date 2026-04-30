package ui

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
)

func TestToolRunCardViewPlacesSubtitleOnSeparateLine(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	run := ToolRun{
		Tool:     domain.ToolKindRead,
		Title:    "Read file",
		Subtitle: "README.md",
		Output:   "line 1\nline 2",
	}

	got := SurfaceText(run.CardSurface(palette, 80, false, false))
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multi-line card, got %q", got)
	}
	if strings.Contains(lines[0], "Read file  README.md") {
		t.Fatalf("expected subtitle on separate line, got %q", got)
	}
	if !strings.Contains(lines[0], "Read file") {
		t.Fatalf("expected title on first line, got %q", got)
	}
	if !strings.Contains(lines[1], "README.md") {
		t.Fatalf("expected subtitle on second line, got %q", got)
	}
}

func TestToolRunCardViewPlacesGrepSubtitleOnSeparateLine(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	run := ToolRun{
		Tool:     domain.ToolKindGrep,
		Title:    "Search text",
		Subtitle: "needle in internal (*.go)",
		Output:   "internal/a.go:1:needle",
	}

	got := SurfaceText(run.CardSurface(palette, 80, false, false))
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multi-line card, got %q", got)
	}
	if strings.Contains(lines[0], "Search text  needle in internal (*.go)") {
		t.Fatalf("expected subtitle on separate line, got %q", got)
	}
	if !strings.Contains(lines[1], "needle in internal (*.go)") {
		t.Fatalf("expected grep subtitle on second line, got %q", got)
	}
}

func TestToolRunCardViewHidesReadPreviewUntilExpanded(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	run := ToolRun{
		Tool:   domain.ToolKindRead,
		Title:  "Read file README.md, lines 1-2",
		Output: "1: first line\n2: second line",
		Status: ToolRunStatusCompleted,
	}

	collapsed := SurfaceText(run.CardSurface(palette, 80, false, false))
	if strings.Contains(collapsed, "first line") || strings.Contains(collapsed, "second line") {
		t.Fatalf("expected collapsed read card to hide file contents, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "Expand (1 line)") && !strings.Contains(collapsed, "Expand (2 lines)") {
		t.Fatalf("expected collapsed read card to remain expandable, got %q", collapsed)
	}

	expanded := SurfaceText(run.CardSurface(palette, 80, true, false))
	if !strings.Contains(expanded, "first line") || !strings.Contains(expanded, "second line") {
		t.Fatalf("expected expanded read card to show file contents, got %q", expanded)
	}
}

func TestToolRunToggleLabelUsesLineCountWithoutMore(t *testing.T) {
	run := ToolRun{
		Tool:   domain.ToolKindBash,
		Title:  "Ran command echo hi",
		Output: "one\ntwo",
		Status: ToolRunStatusCompleted,
	}

	if got := run.ToggleLabel(80, false); got != "Expand output (1 line)" {
		t.Fatalf("expected singular expand label, got %q", got)
	}
	if got := run.ToggleLabel(80, true); got != "Collapse output" {
		t.Fatalf("expected collapse label when expanded, got %q", got)
	}

	run.Output = "one\ntwo\nthree"
	if got := run.ToggleLabel(80, false); got != "Expand output (2 lines)" {
		t.Fatalf("expected plural expand label, got %q", got)
	}
}

func TestBashToolRunSeparatesCommandAndOutputExpansion(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	run := ToolRun{
		Tool:    domain.ToolKindBash,
		Title:   "Ran command cat > /tmp/file",
		Command: "cat > /tmp/file\nline two\nline three",
		Output:  "first output\nsecond output\nthird output",
		Status:  ToolRunStatusCompleted,
	}

	collapsed := SurfaceText(run.CardSurface(palette, 80, false, false))
	if !strings.Contains(collapsed, "Expand command (2 lines)") {
		t.Fatalf("expected command expand label, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "Expand output (2 lines)") {
		t.Fatalf("expected output expand label, got %q", collapsed)
	}
	if strings.Contains(collapsed, "line two") || strings.Contains(collapsed, "second output") {
		t.Fatalf("expected collapsed bash card to hide extra command/output lines, got %q", collapsed)
	}

	commandExpanded := SurfaceText(run.CardSurface(palette, 80, false, true))
	if !strings.Contains(commandExpanded, "line two") || !strings.Contains(commandExpanded, "line three") {
		t.Fatalf("expected expanded command to show hidden command lines, got %q", commandExpanded)
	}
	if strings.Contains(commandExpanded, "second output") {
		t.Fatalf("expected command expansion to leave output collapsed, got %q", commandExpanded)
	}

	outputExpanded := SurfaceText(run.CardSurface(palette, 80, true, false))
	if !strings.Contains(outputExpanded, "second output") || !strings.Contains(outputExpanded, "third output") {
		t.Fatalf("expected expanded output to show hidden output lines, got %q", outputExpanded)
	}
	if strings.Contains(outputExpanded, "line two") {
		t.Fatalf("expected output expansion to leave command collapsed, got %q", outputExpanded)
	}
}

func TestExecToolRunShowsProcessMetadataAndRunningState(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	run := ToolRun{
		Tool:      domain.ToolKindExecCommand,
		Title:     "Exec session npm run dev",
		Command:   "npm run dev",
		ProcessID: "exec_42",
		TTY:       true,
		Output:    "ready",
		Status:    ToolRunStatusRunning,
	}

	got := SurfaceText(run.CardSurface(palette, 80, false, false))
	if !strings.Contains(got, "Running") {
		t.Fatalf("expected running status label, got %q", got)
	}
	if !strings.Contains(got, "id exec_42  tty") {
		t.Fatalf("expected exec metadata line, got %q", got)
	}
}

func TestExecToolRunStatusLabelsDistinguishTerminalStates(t *testing.T) {
	tests := []struct {
		status ToolRunStatus
		want   string
	}{
		{status: ToolRunStatusRunning, want: "Running"},
		{status: ToolRunStatusTerminated, want: "Terminated"},
		{status: ToolRunStatusLost, want: "Lost"},
	}

	for _, tt := range tests {
		run := ToolRun{Status: tt.status}
		if got := run.StatusLabel(); got != tt.want {
			t.Fatalf("status %q: expected %q, got %q", tt.status, tt.want, got)
		}
	}
}

func TestToolRunCardViewShowsShortEditDiffInline(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	run := ToolRun{
		Tool:   domain.ToolKindEdit,
		Title:  "Edited game/map.go",
		Output: EditDiffSummary("--- game/map.go\n+++ game/map.go\n@@ -12,1 +12,1 @@\n-if oldCondition {\n+if newCondition {"),
		Diff:   "--- game/map.go\n+++ game/map.go\n@@ -12,1 +12,1 @@\n-if oldCondition {\n+if newCondition {",
		Status: ToolRunStatusCompleted,
	}

	collapsed := SurfaceText(run.CardSurface(palette, 80, false, false))
	if !strings.Contains(collapsed, "Edited game/map.go  -1 / +1") {
		t.Fatalf("expected compact edit header, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "-1 / +1") {
		t.Fatalf("expected collapsed edit card to show diff summary, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "@@ -12,1 +12,1 @@") || !strings.Contains(collapsed, "+if newCondition {") {
		t.Fatalf("expected collapsed short edit card to show inline diff, got %q", collapsed)
	}
	if strings.Contains(collapsed, "Expand") {
		t.Fatalf("expected collapsed short edit card to avoid expansion controls, got %q", collapsed)
	}
}

func TestToolRunCardViewKeepsLargeEditDiffExpandable(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	diff := strings.Join([]string{
		"--- game/map.go",
		"+++ game/map.go",
		"@@ -12,4 +12,5 @@",
		"-if oldCondition {",
		"-\tfirst()",
		"-\tsecond()",
		"+if newCondition {",
		"+\tfirst()",
		"+\tsecond()",
		"+\tthird()",
	}, "\n")
	run := ToolRun{
		Tool:   domain.ToolKindEdit,
		Title:  "Edited game/map.go",
		Output: EditDiffSummary(diff),
		Diff:   diff,
		Status: ToolRunStatusCompleted,
	}

	collapsed := SurfaceText(run.CardSurface(palette, 80, false, false))
	if !strings.Contains(collapsed, "Edited game/map.go  -3 / +4  Expand (10 lines)") {
		t.Fatalf("expected compact edit header with expand control, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "-3 / +4") {
		t.Fatalf("expected collapsed edit card to show diff summary, got %q", collapsed)
	}
	if strings.Contains(collapsed, "@@ -12,4 +12,5 @@") || strings.Contains(collapsed, "+\tthird()") {
		t.Fatalf("expected collapsed large edit card to hide diff details, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "Expand (10 lines)") {
		t.Fatalf("expected collapsed large edit card to stay expandable, got %q", collapsed)
	}

	expanded := SurfaceText(run.CardSurface(palette, 80, true, false))
	if !strings.Contains(expanded, "@@ -12,4 +12,5 @@") || !strings.Contains(expanded, "+third()") {
		t.Fatalf("expected expanded edit card to show full diff, got %q", expanded)
	}
}

func TestToolRunDockRenderMatchesPaint(t *testing.T) {
	palette := theme.Resolve("tokyonight").Palette
	element := ToolRunDock{
		Palette: palette,
		Run: ToolRun{
			ID:       "run-1",
			Title:    "Read file",
			Subtitle: "README.md",
			Preview:  "line 1\nline 2",
			Status:   ToolRunStatusCompleted,
		},
		Buttons: ButtonRow{
			Buttons: []Button{{ID: "expand", Label: "Expand"}},
			Width:   32,
		},
		Hints: "Enter to expand",
	}
	assertRenderMatchesPaint(t, &Context{Palette: palette, Runtime: &Runtime{}}, element, Rect{W: 32, H: 6})
}
