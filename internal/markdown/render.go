package markdown

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lkarlslund/koder/internal/theme"
)

var (
	boldPattern       = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	italicPattern     = regexp.MustCompile(`\*([^*\n]+)\*|_([^_\n]+)_`)
	inlineCodePattern = regexp.MustCompile("`([^`]+)`")
)

type Renderer struct {
	palette theme.Palette
}

func New(palette theme.Palette) (*Renderer, error) {
	return &Renderer{palette: palette}, nil
}

func (r *Renderer) Render(input string) string {
	if r == nil {
		return strings.TrimSpace(input)
	}
	lines := strings.Split(strings.TrimSpace(input), "\n")
	if len(lines) == 0 {
		return ""
	}

	var blocks []string
	var paragraph []string
	var listItems []string
	listKind := ""
	inCode := false
	codeLang := ""
	var codeLines []string

	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		text := strings.Join(paragraph, " ")
		blocks = append(blocks, r.renderParagraph(text))
		paragraph = nil
	}
	flushList := func() {
		if len(listItems) == 0 {
			return
		}
		blocks = append(blocks, strings.Join(listItems, "\n"))
		listItems = nil
		listKind = ""
	}
	flushCode := func() {
		blocks = append(blocks, r.renderCodeBlock(codeLang, codeLines))
		codeLang = ""
		codeLines = nil
	}

	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			flushParagraph()
			flushList()
			if inCode {
				flushCode()
			} else {
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			inCode = !inCode
			continue
		}
		if inCode {
			codeLines = append(codeLines, line)
			continue
		}
		if trimmed == "" {
			flushParagraph()
			flushList()
			continue
		}
		if isBullet(trimmed) {
			flushParagraph()
			if listKind != "" && listKind != "bullet" {
				flushList()
			}
			listItems = append(listItems, r.renderBullet(trimmed))
			listKind = "bullet"
			continue
		}
		if isOrderedItem(trimmed) {
			flushParagraph()
			if listKind != "" && listKind != "ordered" {
				flushList()
			}
			listItems = append(listItems, r.renderOrderedItem(trimmed))
			listKind = "ordered"
			continue
		}

		if rendered, ok := r.renderBlock(trimmed); ok {
			flushParagraph()
			flushList()
			blocks = append(blocks, rendered)
			continue
		}
		flushList()
		paragraph = append(paragraph, trimmed)
	}

	flushParagraph()
	flushList()
	if inCode {
		flushCode()
	}

	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func (r *Renderer) renderBlock(line string) (string, bool) {
	switch {
	case isHeading(line):
		return r.renderHeading(line), true
	case isBullet(line):
		return r.renderBullet(line), true
	case isOrderedItem(line):
		return r.renderOrderedItem(line), true
	case strings.HasPrefix(line, ">"):
		return r.renderBlockquote(line), true
	case isRule(line):
		return lipgloss.NewStyle().Foreground(r.palette.MarkdownRule).Render(strings.Repeat("─", 32)), true
	default:
		return "", false
	}
}

func isHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	level := 0
	for _, ch := range line {
		if ch != '#' {
			break
		}
		level++
	}
	return level > 0 && level <= 6 && len(line) > level && line[level] == ' '
}

func (r *Renderer) renderHeading(line string) string {
	level := 0
	for _, ch := range line {
		if ch != '#' {
			break
		}
		level++
	}
	text := strings.TrimSpace(line[level:])
	style := lipgloss.NewStyle().Bold(true)
	switch level {
	case 1:
		style = style.Foreground(r.palette.MarkdownHeadingPrimary)
	case 2:
		style = style.Foreground(r.palette.MarkdownHeadingSecondary)
	default:
		style = style.Foreground(r.palette.MarkdownHeadingTertiary)
	}
	return style.Render(r.renderInline(text))
}

func isBullet(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ")
}

func (r *Renderer) renderBullet(line string) string {
	text := strings.TrimSpace(line[2:])
	bullet := lipgloss.NewStyle().Foreground(r.palette.MarkdownListMarker).Render("•")
	return fmt.Sprintf("%s %s", bullet, r.renderInline(text))
}

func isOrderedItem(line string) bool {
	parts := strings.SplitN(line, ". ", 2)
	if len(parts) != 2 {
		return false
	}
	_, err := strconv.Atoi(parts[0])
	return err == nil
}

func (r *Renderer) renderOrderedItem(line string) string {
	parts := strings.SplitN(line, ". ", 2)
	number := lipgloss.NewStyle().Foreground(r.palette.MarkdownListMarker).Render(parts[0] + ".")
	return fmt.Sprintf("%s %s", number, r.renderInline(parts[1]))
}

func (r *Renderer) renderBlockquote(line string) string {
	text := strings.TrimSpace(strings.TrimPrefix(line, ">"))
	prefix := lipgloss.NewStyle().Foreground(r.palette.MarkdownQuoteBorder).Render("│")
	body := lipgloss.NewStyle().Foreground(r.palette.MarkdownQuoteText).Italic(true).Render(r.renderInline(text))
	return prefix + " " + body
}

func isRule(line string) bool {
	return line == "---" || line == "***"
}

func (r *Renderer) renderParagraph(text string) string {
	return r.renderInline(text)
}

func (r *Renderer) renderCodeBlock(lang string, lines []string) string {
	border := lipgloss.NewStyle().Foreground(r.palette.MarkdownCodeBlockBorder)
	body := lipgloss.NewStyle().
		Foreground(r.palette.MarkdownCodeBlockText).
		Padding(0, 1)

	label := "code"
	if strings.TrimSpace(lang) != "" {
		label = lang
	}
	header := border.Render("┌─ " + label)
	content := strings.Join(lines, "\n")
	if strings.TrimSpace(content) == "" {
		content = " "
	}
	renderedBody := body.Render(content)
	footer := border.Render("└" + strings.Repeat("─", max(2, len(label)+2)))
	return strings.Join([]string{header, renderedBody, footer}, "\n")
}

func (r *Renderer) renderInline(input string) string {
	out := inlineCodePattern.ReplaceAllStringFunc(input, func(match string) string {
		groups := inlineCodePattern.FindStringSubmatch(match)
		if len(groups) < 2 {
			return match
		}
		return lipgloss.NewStyle().
			Foreground(r.palette.MarkdownInlineCodeText).
			Background(r.palette.MarkdownInlineCodeBackground).
			Render(groups[1])
	})
	out = boldPattern.ReplaceAllStringFunc(out, func(match string) string {
		groups := boldPattern.FindStringSubmatch(match)
		text := firstNonEmpty(groups[1:]...)
		return lipgloss.NewStyle().Bold(true).Render(text)
	})
	out = italicPattern.ReplaceAllStringFunc(out, func(match string) string {
		groups := italicPattern.FindStringSubmatch(match)
		text := firstNonEmpty(groups[1:]...)
		return lipgloss.NewStyle().Italic(true).Render(text)
	})
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
