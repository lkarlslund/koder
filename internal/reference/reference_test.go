package reference

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeDecodeMetaRoundTrip(t *testing.T) {
	raw, err := EncodeMeta(Metadata{
		Kind:  KindFile,
		Path:  "docs/readme.md",
		Start: 3,
		End:   8,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMeta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindFile || got.Path != "docs/readme.md" || got.Display != "@docs/readme.md" || got.Start != 3 || got.End != 8 {
		t.Fatalf("unexpected decoded metadata: %#v", got)
	}
}

func TestDecodeMetaRejectsEmptyAndInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: "reference metadata is empty"},
		{name: "invalid json", raw: "{", want: "decode reference metadata"},
		{name: "missing fields", raw: `{"kind":"file"}`, want: "reference metadata is incomplete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeMeta(tt.raw)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestNormalizePathAndDisplayToken(t *testing.T) {
	if got := NormalizePath(" ./docs/../README.md "); got != "README.md" {
		t.Fatalf("unexpected normalized path: %q", got)
	}
	if got := NormalizePath("."); got != "" {
		t.Fatalf("expected dot path to become empty, got %q", got)
	}
	if got := DisplayToken(""); got != "@" {
		t.Fatalf("expected empty display token, got %q", got)
	}
	if got := DisplayToken("docs/guide.md"); got != "@docs/guide.md" {
		t.Fatalf("unexpected display token: %q", got)
	}
	if got := DisplayToken("docs/my guide.md"); got != `@"docs/my guide.md"` {
		t.Fatalf("unexpected quoted display token: %q", got)
	}
}

func TestEntriesSkipsGitAndSortsDirectoriesFirst(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, ".git"),
		filepath.Join(root, "docs"),
		filepath.Join(root, "internal"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("guide"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := Entries(root)
	if err != nil {
		t.Fatal(err)
	}
	gotPaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		gotPaths = append(gotPaths, entry.Display)
	}
	want := []string{"docs/", "internal/", "README.md", "docs/guide.md"}
	if strings.Join(gotPaths, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected entries order:\nwant %v\ngot  %v", want, gotPaths)
	}
}

func TestSearchRanksAndLimitsResults(t *testing.T) {
	entries := []Entry{
		{Kind: KindDirectory, Path: "docs", Display: "docs/"},
		{Kind: KindFile, Path: "docs/guide.md", Display: "docs/guide.md"},
		{Kind: KindFile, Path: "docs/getting-started.md", Display: "docs/getting-started.md"},
		{Kind: KindFile, Path: "internal/guide.txt", Display: "internal/guide.txt"},
	}

	got := Search(entries, "guide", 2)
	if len(got) != 2 {
		t.Fatalf("expected two results, got %d", len(got))
	}
	if got[0].Path != "docs/guide.md" {
		t.Fatalf("expected exact basename match first, got %#v", got)
	}

	got = Search(entries, "", 10)
	if len(got) == 0 || got[0].Kind != KindDirectory {
		t.Fatalf("expected empty query to prefer directories, got %#v", got)
	}
}

func TestPathCompletionsReturnsWorkspaceMatches(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("guide"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "glossary.md"), []byte("glossary"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := PathCompletions(root, "@/docs/g", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two completions, got %#v", got)
	}
	if got[0].Path != "docs/glossary.md" && got[0].Path != "docs/guide.md" {
		t.Fatalf("unexpected completions: %#v", got)
	}

	got, err = PathCompletions(root, "../../etc", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected outside-workspace query to return no completions, got %#v", got)
	}
}

func TestResolveFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fileBody, err := ResolveFile(root, Metadata{Kind: KindFile, Path: "docs/guide.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fileBody, "Referenced file docs/guide.md:\n1: line one") {
		t.Fatalf("unexpected resolved file body: %q", fileBody)
	}

	dirBody, err := ResolveDirectory(root, Metadata{Kind: KindDirectory, Path: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dirBody, "Referenced directory docs:\nguide.md") {
		t.Fatalf("unexpected resolved directory body: %q", dirBody)
	}
}

func TestResolveFileTruncatesLongContent(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 0, 2005)
	for i := 0; i < 2005; i++ {
		lines = append(lines, "line")
	}
	path := filepath.Join(root, "big.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	body, err := ResolveFile(root, Metadata{Kind: KindFile, Path: "big.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "2000: line") {
		t.Fatalf("expected truncated output to include line 2000, got %q", body)
	}
	if strings.Contains(body, "2001: line") {
		t.Fatalf("expected output to be truncated before line 2001, got %q", body)
	}
}

func TestResolveDirectoryTruncatesLongListing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "many")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4000; i++ {
		name := fmt.Sprintf("file-%04d-%s.txt", i, strings.Repeat("x", 20))
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body, err := ResolveDirectory(root, Metadata{Kind: KindDirectory, Path: "many"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "... truncated to ") {
		t.Fatalf("expected truncated directory output, got %q", body)
	}
}

func TestResolveHelpersReturnErrorsForMissingPaths(t *testing.T) {
	root := t.TempDir()
	if _, err := ResolveFile(root, Metadata{Kind: KindFile, Path: "missing.txt"}); err == nil {
		t.Fatal("expected missing file error")
	}
	if _, err := ResolveDirectory(root, Metadata{Kind: KindDirectory, Path: "missing"}); err == nil {
		t.Fatal("expected missing directory error")
	}
}
