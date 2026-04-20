package markdown

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

func TestRenderFormatsHeadingsAndLists(t *testing.T) {
	renderer, err := New(theme.Default().Palette)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.Render("# Title\n\n## Subtitle\n\n- first\n1. second")

	if !strings.Contains(got, "Title") {
		t.Fatalf("expected heading text, got %q", got)
	}
	if !strings.Contains(got, "Subtitle") {
		t.Fatalf("expected h2 text, got %q", got)
	}
	if !strings.Contains(got, "• first") {
		t.Fatalf("expected bullet item, got %q", got)
	}
	if !strings.Contains(got, "1. second") {
		t.Fatalf("expected ordered item, got %q", got)
	}
}

func TestRenderKeepsConsecutiveBulletItemsTight(t *testing.T) {
	renderer, err := New(theme.Default().Palette)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.Render("- first\n- second\n- third")

	if strings.Contains(got, "\n\n") {
		t.Fatalf("expected no blank lines between bullet items, got %q", got)
	}
	if !strings.Contains(got, "• first\n") || !strings.Contains(got, "\n• second\n") || !strings.Contains(got, "\n• third") {
		t.Fatalf("expected consecutive bullet items, got %q", got)
	}
}

func TestRenderKeepsConsecutiveOrderedItemsTight(t *testing.T) {
	renderer, err := New(theme.Default().Palette)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.Render("1. first\n2. second\n3. third")

	if strings.Contains(got, "\n\n") {
		t.Fatalf("expected no blank lines between ordered items, got %q", got)
	}
	if !strings.Contains(got, "1. first\n") || !strings.Contains(got, "\n2. second\n") || !strings.Contains(got, "\n3. third") {
		t.Fatalf("expected consecutive ordered items, got %q", got)
	}
}

func TestRenderFormatsFencedCodeBlock(t *testing.T) {
	renderer, err := New(theme.Default().Palette)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.Render("```go\nfmt.Println(\"hi\")\n```")

	if !strings.Contains(got, "┌─ go") {
		t.Fatalf("expected code block header, got %q", got)
	}
	if !strings.Contains(got, "fmt.Println(\"hi\")") {
		t.Fatalf("expected code body, got %q", got)
	}
	if !strings.Contains(got, "└") {
		t.Fatalf("expected code block footer, got %q", got)
	}
}

func TestRenderFormatsInlineMarkdown(t *testing.T) {
	renderer, err := New(theme.Default().Palette)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.Render("plain `code` **bold** _italic_")

	for _, want := range []string{"plain", "code", "bold", "italic"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in rendered output, got %q", want, got)
		}
	}
}

func TestRenderRestoresBaseColorAfterStrongText(t *testing.T) {
	renderer, err := New(theme.Default().Palette)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.Render("plain **bold** plain")
	wantReset := "\x1b[38;2;200;211;245m"
	if !strings.Contains(got, wantReset) {
		t.Fatalf("expected base markdown foreground to be restored after strong text, got %q", got)
	}
}

func TestRenderFormatsNestedLists(t *testing.T) {
	renderer, err := New(theme.Default().Palette)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.Render("- parent\n    - child\n    - second child")

	if !strings.Contains(got, "• parent") {
		t.Fatalf("expected top-level bullet, got %q", got)
	}
	if !strings.Contains(got, "\n     • child") || !strings.Contains(got, "\n     • second child") {
		t.Fatalf("expected indented nested bullets, got %q", got)
	}
}

func TestRenderFormatsBlockquoteAndLink(t *testing.T) {
	renderer, err := New(theme.Default().Palette)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.Render("> quoted text with [link](https://example.com)")

	if !strings.Contains(got, "│") || !strings.Contains(got, "quoted text") {
		t.Fatalf("expected blockquote rendering, got %q", got)
	}
	if !strings.Contains(got, "link (https://example.com)") {
		t.Fatalf("expected rendered markdown link, got %q", got)
	}
}

func TestRenderFormatsTableAndTaskList(t *testing.T) {
	renderer, err := New(theme.Default().Palette)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.Render("| Name | Value |\n| --- | --- |\n| one | two |\n\n- [x] done\n- [ ] todo")

	for _, want := range []string{"| Name", "| one", "[x] done", "[ ] todo"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in rendered output, got %q", want, got)
		}
	}
}
