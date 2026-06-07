package readtool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type recordingFileTracker struct {
	path    string
	content string
}

func (r *recordingFileTracker) TouchFile(_ context.Context, path, content string) {
	r.path = path
	r.content = content
}

func TestExecuteAllowsAbsolutePathOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outsideDir := t.TempDir()
	target := filepath.Join(outsideDir, "rules.md")
	if err := os.WriteFile(target, []byte("# Rules\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{"path": target},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Fatalf("expected absolute path read output, got %q", result.Output)
	}
	if got := result.Meta["path"]; got != filepath.ToSlash(target) {
		t.Fatalf("expected absolute path label %q, got %q", filepath.ToSlash(target), got)
	}
}

func TestExecuteTouchesReadFileForCodeIntel(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracker := &recordingFileTracker{}
	_, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace, FileTracker: tracker}, Request: tools.Request{
		Args: map[string]string{"path": "main.go"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if tracker.path != "main.go" || tracker.content != "package main\n" {
		t.Fatalf("unexpected file touch path=%q content=%q", tracker.path, tracker.content)
	}
}

func TestExecuteDoesNotTouchDirectoriesForCodeIntel(t *testing.T) {
	workspace := t.TempDir()
	tracker := &recordingFileTracker{}
	_, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace, FileTracker: tracker}, Request: tools.Request{
		Args: map[string]string{"path": "."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if tracker.path != "" || tracker.content != "" {
		t.Fatalf("unexpected directory touch path=%q content=%q", tracker.path, tracker.content)
	}
}

func TestPresentationIncludesPathAndLineRange(t *testing.T) {
	got := tool{}.Presentation(tools.Request{
		Tool: domain.ToolKindFileRead,
		Args: map[string]string{
			"path":       "internal/tools/readtool/read.go",
			"start_line": "10",
			"end_line":   "14",
		},
	})
	if got.Title != "Read file internal/tools/readtool/read.go, lines 10-14" {
		t.Fatalf("unexpected title: %q", got.Title)
	}
	if got.Subtitle != "" {
		t.Fatalf("expected empty subtitle, got %q", got.Subtitle)
	}
}

func TestNormalizeRejectsLegacyAliases(t *testing.T) {
	for _, args := range []map[string]string{
		{"file": "README.md"},
		{"path": "README.md", "offset": "2"},
		{"path": "README.md", "limit": "10"},
		{"path": "README.md", "max_lines": "10"},
	} {
		if _, err := (tool{}).NormalizeArgs(args); err == nil {
			t.Fatalf("expected compatibility error for %#v", args)
		}
	}
}

func TestExecutePagesLargeFilesWithContinuationHint(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "long.txt")
	if err := os.WriteFile(target, []byte(numberedLines(2505)), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{"path": "long.txt"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "1: line1") {
		t.Fatalf("expected first line, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "1000: line1000") {
		t.Fatalf("expected page to include line 1000")
	}
	if strings.Contains(result.Output, "1001: line1001") {
		t.Fatalf("expected first page to stop before line 1001")
	}
	wantFooter := "(showing lines 1-1000 of 2505, capped at 1000 lines; use start_line=1001 end_line=2000 only if you need the next section; prefer file_grep or a narrower range for specific code)"
	if !strings.Contains(result.Output, wantFooter) {
		t.Fatalf("expected continuation footer %q, got %q", wantFooter, result.Output)
	}
	if got := result.Meta["next_start_line"]; got != "1001" {
		t.Fatalf("expected next_start_line 1001, got %q", got)
	}
	if got := result.Meta["total"]; got != "2505" {
		t.Fatalf("expected total 2505, got %q", got)
	}
}

func TestExecuteReadsSecondPageAndReportsEOF(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "long.txt")
	if err := os.WriteFile(target, []byte(numberedLines(2505)), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{
			"path":       "long.txt",
			"start_line": "2001",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "2001: line2001") {
		t.Fatalf("expected second page to start at line 2001")
	}
	if !strings.Contains(result.Output, "2505: line2505") {
		t.Fatalf("expected second page to include final line")
	}
	if !strings.Contains(result.Output, "End of file - total 2505 lines.") {
		t.Fatalf("expected eof footer, got %q", result.Output)
	}
	if got := result.Meta["truncated"]; got != "false" {
		t.Fatalf("expected truncated=false, got %q", got)
	}
}

func TestExecuteRespectsExplicitLimitAndNextOffset(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "limited.txt")
	if err := os.WriteFile(target, []byte(numberedLines(100)), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{
			"path":       "limited.txt",
			"start_line": "1",
			"end_line":   "10",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "10: line10") {
		t.Fatalf("expected tenth line in page")
	}
	if strings.Contains(result.Output, "11: line11") {
		t.Fatalf("expected output to stop before line 11")
	}
	wantFooter := "(showing lines 1-10 of 100; use start_line=11 end_line=20 only if you need the next section; prefer file_grep or a narrower range for specific code)"
	if !strings.Contains(result.Output, wantFooter) {
		t.Fatalf("expected explicit-limit footer %q, got %q", wantFooter, result.Output)
	}
}

func TestExecuteRejectsOutOfRangeStartLines(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "small.txt")
	if err := os.WriteFile(target, []byte(numberedLines(3)), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{
			"path":       "small.txt",
			"start_line": "4",
			"end_line":   "8",
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "start_line 4 is out of range for this file (3 lines)") {
		t.Fatalf("expected out-of-range file error, got %v", err)
	}
}

func TestExecuteHandlesEmptyFileStartLines(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "empty.txt")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{"path": "empty.txt"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "End of file - total 0 lines.") {
		t.Fatalf("expected empty-file eof footer, got %q", result.Output)
	}

	_, err = tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{
			"path":       "empty.txt",
			"start_line": "2",
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "start_line 2 is out of range for this file (0 lines)") {
		t.Fatalf("expected out-of-range empty-file error, got %v", err)
	}
}

func TestExecutePagesDirectories(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "entries")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, 12)
	for i := 1; i <= 12; i++ {
		name := fileName(i)
		names = append(names, name)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(names)

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{
			"path":       "entries",
			"start_line": "6",
			"end_line":   "10",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range names[5:10] {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected directory page to include %q", want)
		}
	}
	if strings.Contains(result.Output, names[4]) {
		t.Fatalf("expected directory page to exclude prior entry %q", names[4])
	}
	if strings.Contains(result.Output, names[10]) {
		t.Fatalf("expected directory page to exclude next entry %q", names[10])
	}
	wantFooter := "(showing entries 6-10 of 12; use start_line=11 end_line=15 only if you need the next section; prefer file_grep or a narrower range for specific code)"
	if !strings.Contains(result.Output, wantFooter) {
		t.Fatalf("expected directory continuation footer %q, got %q", wantFooter, result.Output)
	}

	finalPage, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{
			"path":       "entries",
			"start_line": "11",
			"end_line":   "15",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(finalPage.Output, "End of directory - total 12 entries.") {
		t.Fatalf("expected directory eof footer, got %q", finalPage.Output)
	}
	if got := finalPage.Meta["truncated"]; got != "false" {
		t.Fatalf("expected truncated=false for final directory page, got %q", got)
	}
}

func TestExecuteRejectsOutputOverCharacterLimit(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "wide.txt")
	var builder strings.Builder
	for i := 1; i <= 120; i++ {
		builder.WriteString(strings.Repeat("x", 1000))
		if i < 120 {
			builder.WriteByte('\n')
		}
	}
	if err := os.WriteFile(target, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{"path": "wide.txt"},
	}})
	if err == nil || !strings.Contains(err.Error(), "exceeds the 100000 character limit") {
		t.Fatalf("expected character-limit error, got %v", err)
	}
}

func TestExecutePagesDirectoriesInStableSortedOrder(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "sorted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"z-last.txt", "a-first.txt", "m-middle.txt", "b-dir"} {
		target := filepath.Join(dir, name)
		if strings.HasSuffix(name, "-dir") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := tool{}.Call(context.Background(), tools.Options{Runtime: tools.Runtime{Workdir: workspace}, Request: tools.Request{
		Args: map[string]string{
			"path":     "sorted",
			"end_line": "4",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"a-first.txt", "b-dir/", "m-middle.txt", "z-last.txt"}
	lastIndex := -1
	for _, want := range wantOrder {
		idx := strings.Index(result.Output, want)
		if idx == -1 {
			t.Fatalf("expected sorted directory output to include %q in %q", want, result.Output)
		}
		if idx <= lastIndex {
			t.Fatalf("expected %q to appear after prior entries in %q", want, result.Output)
		}
		lastIndex = idx
	}
}

func numberedLines(count int) string {
	lines := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		lines = append(lines, fmt.Sprintf("line%d", i))
	}
	return strings.Join(lines, "\n")
}

func fileName(i int) string {
	return fmt.Sprintf("file-%02d.txt", i)
}
