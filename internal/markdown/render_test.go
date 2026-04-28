package markdown

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

func findSpanWithText(spans []ui.StyledSpan, needle string) (ui.StyledSpan, bool) {
	for _, span := range spans {
		if strings.Contains(span.Text, needle) {
			return span, true
		}
	}
	return ui.StyledSpan{}, false
}

func TestRenderFormatsHeadingsAndLists(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := ansi.Strip(renderer.Render("# Title\n\n## Subtitle\n\n- first\n1. second"))

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
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := ansi.Strip(renderer.Render("- first\n- second\n- third"))

	if strings.Contains(got, "\n\n") {
		t.Fatalf("expected no blank lines between bullet items, got %q", got)
	}
	if !strings.Contains(got, "• first\n") || !strings.Contains(got, "\n• second\n") || !strings.Contains(got, "\n• third") {
		t.Fatalf("expected consecutive bullet items, got %q", got)
	}
}

func TestRenderKeepsConsecutiveOrderedItemsTight(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := ansi.Strip(renderer.Render("1. first\n2. second\n3. third"))

	if strings.Contains(got, "\n\n") {
		t.Fatalf("expected no blank lines between ordered items, got %q", got)
	}
	if !strings.Contains(got, "1. first\n") || !strings.Contains(got, "\n2. second\n") || !strings.Contains(got, "\n3. third") {
		t.Fatalf("expected consecutive ordered items, got %q", got)
	}
}

func TestRenderFormatsFencedCodeBlock(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := ansi.Strip(renderer.Render("```go\nfmt.Println(\"hi\")\n```"))

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

func TestRenderHighlightsCodeWithConfiguredChromaStyle(t *testing.T) {
	githubRenderer, err := New(theme.Default().Palette, "github")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	monokaiRenderer, err := New(theme.Default().Palette, "monokai")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	input := "```go\nconst answer = 42\n```"

	githubSpan, ok := findSpanWithText(githubRenderer.RenderStyled(input), "const")
	if !ok || !githubSpan.Style.FG.Valid() {
		t.Fatalf("expected highlighted const span, got %#v", githubRenderer.RenderStyled(input))
	}
	monokaiSpan, ok := findSpanWithText(monokaiRenderer.RenderStyled(input), "const")
	if !ok || !monokaiSpan.Style.FG.Valid() {
		t.Fatalf("expected highlighted const span, got %#v", monokaiRenderer.RenderStyled(input))
	}
	if githubSpan.Style.FG == monokaiSpan.Style.FG {
		t.Fatalf("expected different chroma styles to produce different colors, got %#v and %#v", githubSpan.Style, monokaiSpan.Style)
	}

	invalidRenderer, err := New(theme.Default().Palette, "not-a-style")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if invalidRenderer.codeStyle != "github" {
		t.Fatalf("expected invalid style to normalize to github, got %q", invalidRenderer.codeStyle)
	}
}

func TestRenderFormatsAnnotatedCodeBlock(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	input := strings.Join([]string{
		"```go {linenums highlight=2 focus=2-3}",
		"const answer = 42 // [!1]",
		"fmt.Println(answer)",
		"```",
		"[!1]: shared answer value",
	}, "\n")

	got := ansi.Strip(renderer.Render(input))
	for _, want := range []string{"┌─ go", "1 ", "2 ", "fmt.Println(answer)", "Notes", "shared answer value"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in rendered output, got %q", want, got)
		}
	}
	if strings.Contains(got, "[!1]") {
		t.Fatalf("expected inline note marker to be normalized, got %q", got)
	}
}

func TestRenderFormatsInlineMarkdown(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := ansi.Strip(renderer.Render("plain `code` **bold** _italic_"))

	for _, want := range []string{"plain", "code", "bold", "italic"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in rendered output, got %q", want, got)
		}
	}
}

func TestRenderRestoresBaseColorAfterStrongText(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
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
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := ansi.Strip(renderer.Render("- parent\n    - child\n    - second child"))

	if !strings.Contains(got, "• parent") {
		t.Fatalf("expected top-level bullet, got %q", got)
	}
	if !strings.Contains(got, "\n     • child") || !strings.Contains(got, "\n     • second child") {
		t.Fatalf("expected indented nested bullets, got %q", got)
	}
}

func TestRenderFormatsBlockquoteAndLink(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := ansi.Strip(renderer.Render("> quoted text with [link](https://example.com)"))

	if !strings.Contains(got, "│") || !strings.Contains(got, "quoted text") {
		t.Fatalf("expected blockquote rendering, got %q", got)
	}
	if !strings.Contains(got, "link (https://example.com)") {
		t.Fatalf("expected rendered markdown link, got %q", got)
	}
}

func TestRenderFormatsTableAndTaskList(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := ansi.Strip(renderer.Render("| Name | Value |\n| --- | --- |\n| one | two |\n\n- [x] done\n- [ ] todo"))

	for _, want := range []string{"| Name", "| one", "[x] done", "[ ] todo"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in rendered output, got %q", want, got)
		}
	}
}

func TestRenderPlainWidthWrapsTablesWithinHint(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	input := strings.Join([]string{
		"| File | Description |",
		"| --- | --- |",
		"| pacman.go | Compiled binary for the game |",
		"| test-project | Another compiled binary |",
		"| go.mod / go.sum | Go module files with dependencies (Ebitenengine v2.9.9, gomobile, etc.) |",
	}, "\n")

	got := renderer.RenderPlainWidth(input, 48)
	lines := strings.Split(got, "\n")
	if len(lines) < 5 {
		t.Fatalf("expected wrapped table output, got %q", got)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 48 {
			t.Fatalf("expected line width <= 48, got %d in %q", ansi.StringWidth(line), line)
		}
	}
	if strings.Contains(got, "-----------------\n") {
		t.Fatalf("expected divider row to stay on one line, got %q", got)
	}
	if !strings.Contains(got, "| File") || !strings.Contains(got, "| pacman.go") {
		t.Fatalf("expected table content to remain intact, got %q", got)
	}
}

func TestRenderStyledTracksInlineAttributes(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.RenderStyled("plain **bold** _italic_ ~~gone~~ [link](https://example.com)")
	var (
		sawBold   bool
		sawItalic bool
		sawStrike bool
		sawLink   bool
	)
	for _, span := range got {
		switch strings.TrimSpace(span.Text) {
		case "bold":
			sawBold = span.Style.Bold()
		case "italic":
			sawItalic = span.Style.Italic()
		case "gone":
			sawStrike = span.Style.Strikethrough()
		case "link":
			sawLink = span.Style.Underline()
		}
	}
	if !sawBold || !sawItalic || !sawStrike || !sawLink {
		t.Fatalf("expected inline styles in spans, got %#v", got)
	}
}

func TestRenderFormatsImagesFootnotesMathAndDefinitionLists(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	input := strings.Join([]string{
		"![Demo Image](https://example.com/demo.png \"hero\")",
		"",
		"Term",
		": Definition value",
		"",
		"Inline math $E = mc^2$ and ref[^1].",
		"",
		"$$",
		"a+b",
		"$$",
		"",
		"[^1]: Footnote body",
	}, "\n")

	got := ansi.Strip(renderer.Render(input))
	for _, want := range []string{
		"🖼 Image",
		"Alt: Demo Image",
		"Src: https://example.com/demo.png",
		"Title: hero",
		"Term",
		": Definition value",
		"⟪E = mc^2⟫",
		"[1]",
		"┌─ math",
		"a+b",
		"Footnotes",
		"[^1] Footnote body",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in rendered output, got %q", want, got)
		}
	}
}

func TestRenderFormatsCalloutsAndSafeHTML(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	input := strings.Join([]string{
		"> [!NOTE]",
		"> This is a **note**.",
		"",
		"H<sup>2</sup>O and CO<sub>2</sub> plus <abbr title=\"HyperText Markup Language\">HTML</abbr>.",
	}, "\n")

	got := ansi.Strip(renderer.Render(input))
	for _, want := range []string{"ℹ NOTE", "This is a note.", "H^(2)O", "CO_(2)", "HTML (HyperText Markup Language)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in rendered output, got %q", want, got)
		}
	}
	if strings.Contains(got, "[!NOTE]") {
		t.Fatalf("expected callout marker to be normalized, got %q", got)
	}
}

func TestRenderFormatsHighlightWithBackground(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := renderer.RenderStyled("plain ==mark== plain")
	found := false
	for _, span := range got {
		if span.Text == "mark" {
			found = true
			if !span.Style.BG.Valid() || span.Style.BG.A() == 0xff {
				t.Fatalf("expected translucent background for highlight, got %#v", span.Style)
			}
		}
	}
	if !found {
		t.Fatalf("expected highlight span in %#v", got)
	}
}

func TestRenderUltimateMarkdownDemoCoverage(t *testing.T) {
	renderer, err := New(theme.Default().Palette, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	input := strings.Join([]string{
		"# 📘 The Ultimate Markdown Demo",
		"",
		"This is **bold** and this is *italic* and this is ***bold italic***.",
		"",
		"You can use ~~strikethrough~~ for deleted text and ==highlighted== for emphasis.",
		"",
		"Superscript: H<sup>2</sup>O. Abbreviation: <abbr title=\"HyperText Markup Language\">HTML</abbr>.",
		"",
		"> [!WARNING]",
		"> This is a **warning** — something that could cause problems.",
		"",
		"![Alt Text](https://via.placeholder.com/400x200?text=Demo+Image)",
		"",
		"- [x] Create project structure",
		"",
		"Here is a sentence with a footnote[^1].",
		"",
		"[^1]: This is the first footnote.",
		"",
		"**Markdown**",
		": A lightweight markup language for creating formatted text.",
		"",
		"The equation $E = mc^2$ is one of the most famous in physics.",
		"",
		"$$",
		"\\int_{-\\infty}^{\\infty} e^{-x^2} dx = \\sqrt{\\pi}",
		"$$",
	}, "\n")

	got := ansi.Strip(renderer.Render(input))
	for _, want := range []string{
		"highlighted",
		"⚠ WARNING",
		"🖼 Image",
		"Alt: Alt Text",
		"footnote[1]",
		"Footnotes",
		"[^1] This is the first footnote.",
		": A lightweight markup language",
		"⟪E = mc^2⟫",
		"┌─ math",
		"H^(2)O",
		"HTML (HyperText Markup Language)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in rendered output, got %q", want, got)
		}
	}
	if strings.Contains(got, "[!WARNING]") {
		t.Fatalf("expected callout marker to be normalized, got %q", got)
	}
}
