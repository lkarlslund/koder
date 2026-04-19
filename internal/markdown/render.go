package markdown

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

type Renderer struct {
	md      goldmark.Markdown
	palette theme.Palette
}

func New(palette theme.Palette) (*Renderer, error) {
	return &Renderer{
		md:      goldmark.New(goldmark.WithExtensions(extension.GFM)),
		palette: palette,
	}, nil
}

func (r *Renderer) Render(input string) string {
	if r == nil {
		return strings.TrimSpace(input)
	}
	source := []byte(strings.TrimSpace(input))
	if len(source) == 0 {
		return ""
	}
	doc := r.md.Parser().Parse(text.NewReader(source))
	blocks := r.renderBlockChildren(doc, source)
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func (r *Renderer) renderBlockChildren(parent ast.Node, source []byte) []string {
	var blocks []string
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		if rendered := r.renderBlock(child, source); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	return blocks
}

func (r *Renderer) renderBlock(node ast.Node, source []byte) string {
	switch typed := node.(type) {
	case *ast.Paragraph:
		return lipgloss.NewStyle().Foreground(r.palette.MarkdownText).Render(r.renderInlineChildren(node, source))
	case *ast.TextBlock:
		return lipgloss.NewStyle().Foreground(r.palette.MarkdownText).Render(r.renderInlineChildren(node, source))
	case *ast.Heading:
		return r.renderHeading(typed, source)
	case *ast.Blockquote:
		return r.renderBlockquote(typed, source)
	case *ast.FencedCodeBlock:
		return r.renderFencedCodeBlock(typed, source)
	case *ast.CodeBlock:
		return r.renderCodeBlock("", typed.Lines(), source)
	case *ast.List:
		return r.renderList(typed, source)
	case *ast.ThematicBreak:
		return lipgloss.NewStyle().Foreground(r.palette.MarkdownRule).Render(strings.Repeat("─", 32))
	case *extensionast.Table:
		return r.renderTable(typed, source)
	case *ast.HTMLBlock:
		return ""
	default:
		if node.HasChildren() {
			return strings.Join(r.renderBlockChildren(node, source), "\n\n")
		}
		return ""
	}
}

func (r *Renderer) renderHeading(node *ast.Heading, source []byte) string {
	style := lipgloss.NewStyle().Bold(true)
	switch node.Level {
	case 1:
		style = style.Foreground(r.palette.MarkdownHeadingPrimary)
	case 2:
		style = style.Foreground(r.palette.MarkdownHeadingSecondary)
	default:
		style = style.Foreground(r.palette.MarkdownHeadingTertiary)
	}
	return style.Render(r.renderInlineChildren(node, source))
}

func (r *Renderer) renderBlockquote(node *ast.Blockquote, source []byte) string {
	inner := strings.Join(r.renderBlockChildren(node, source), "\n")
	if strings.TrimSpace(inner) == "" {
		return ""
	}
	prefix := lipgloss.NewStyle().Foreground(r.palette.MarkdownQuoteBorder).Render("│")
	body := lipgloss.NewStyle().Foreground(r.palette.MarkdownQuoteText).Italic(true)
	lines := strings.Split(inner, "\n")
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			rendered = append(rendered, prefix)
			continue
		}
		rendered = append(rendered, prefix+" "+body.Render(line))
	}
	return strings.Join(rendered, "\n")
}

func (r *Renderer) renderFencedCodeBlock(node *ast.FencedCodeBlock, source []byte) string {
	return r.renderCodeBlock(string(node.Language(source)), node.Lines(), source)
}

func (r *Renderer) renderCodeBlock(lang string, lines *text.Segments, source []byte) string {
	border := lipgloss.NewStyle().Foreground(r.palette.MarkdownCodeBlockBorder)
	body := lipgloss.NewStyle().Foreground(r.palette.MarkdownCodeBlockText).Padding(0, 1)

	label := strings.TrimSpace(lang)
	if label == "" {
		label = "code"
	}
	var contentLines []string
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		line := strings.TrimRight(string(segment.Value(source)), "\n")
		contentLines = append(contentLines, line)
	}
	content := strings.Join(contentLines, "\n")
	if strings.TrimSpace(content) == "" {
		content = " "
	}
	header := border.Render("┌─ " + label)
	footer := border.Render("└" + strings.Repeat("─", max(2, len(label)+2)))
	return strings.Join([]string{header, body.Render(content), footer}, "\n")
}

func (r *Renderer) renderList(node *ast.List, source []byte) string {
	var lines []string
	itemNumber := node.Start
	for item := node.FirstChild(); item != nil; item = item.NextSibling() {
		listItem, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		markerText := "•"
		if node.IsOrdered() {
			markerText = strconv.Itoa(itemNumber) + "."
			itemNumber++
		}
		markerColor := r.palette.MarkdownListMarker
		if node.IsOrdered() {
			markerColor = r.palette.MarkdownListEnumeration
		}
		marker := lipgloss.NewStyle().Foreground(markerColor).Render(markerText)
		lines = append(lines, r.renderListItem(listItem, source, marker, len(markerText)+1)...)
	}
	return strings.Join(lines, "\n")
}

func (r *Renderer) renderListItem(node *ast.ListItem, source []byte, marker string, markerWidth int) []string {
	blocks := r.renderBlockChildren(node, source)
	if len(blocks) == 0 {
		return []string{marker}
	}
	firstPrefix := marker + " "
	continuation := strings.Repeat(" ", markerWidth+1)
	var out []string
	for blockIndex, block := range blocks {
		lines := strings.Split(block, "\n")
		for lineIndex, line := range lines {
			prefix := continuation
			if blockIndex == 0 && lineIndex == 0 {
				prefix = firstPrefix
			}
			out = append(out, prefix+line)
		}
	}
	return out
}

func (r *Renderer) renderInlineChildren(parent ast.Node, source []byte) string {
	var output strings.Builder
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		output.WriteString(r.renderInline(child, source))
	}
	return strings.TrimSpace(output.String())
}

func (r *Renderer) renderInline(node ast.Node, source []byte) string {
	switch typed := node.(type) {
	case *ast.Text:
		value := string(typed.Segment.Value(source))
		if typed.HardLineBreak() {
			return value + "\n"
		}
		if typed.SoftLineBreak() {
			return value + " "
		}
		return value
	case *ast.String:
		return string(typed.Value)
	case *ast.CodeSpan:
		return lipgloss.NewStyle().
			Foreground(r.palette.MarkdownInlineCodeText).
			Background(r.palette.MarkdownInlineCodeBackground).
			Render(r.renderInlineChildren(node, source))
	case *ast.Emphasis:
		text := r.renderInlineChildren(node, source)
		style := lipgloss.NewStyle()
		if typed.Level == 2 {
			style = style.Bold(true).Foreground(r.palette.MarkdownStrongText)
		} else {
			style = style.Italic(true).Foreground(r.palette.MarkdownEmphasisText)
		}
		return style.Render(text)
	case *extensionast.Strikethrough:
		return lipgloss.NewStyle().Strikethrough(true).Render(r.renderInlineChildren(node, source))
	case *ast.Link:
		return r.renderLink(typed, source)
	case *ast.AutoLink:
		target := string(typed.URL(source))
		return lipgloss.NewStyle().Foreground(r.palette.MarkdownLinkTargetText).Underline(true).Render(target)
	case *ast.RawHTML:
		return ""
	case *extensionast.TaskCheckBox:
		if typed.IsChecked {
			return "[x] "
		}
		return "[ ] "
	default:
		if node.HasChildren() {
			return r.renderInlineChildren(node, source)
		}
		return ""
	}
}

func (r *Renderer) renderLink(node *ast.Link, source []byte) string {
	label := r.renderInlineChildren(node, source)
	target := string(node.Destination)
	if strings.TrimSpace(label) == "" || label == target {
		return lipgloss.NewStyle().Foreground(r.palette.MarkdownLinkTargetText).Underline(true).Render(target)
	}
	linkLabel := lipgloss.NewStyle().Foreground(r.palette.MarkdownLinkText).Underline(true).Render(label)
	targetText := lipgloss.NewStyle().Foreground(r.palette.MarkdownLinkTargetText).Render(target)
	return fmt.Sprintf("%s (%s)", linkLabel, targetText)
}

func (r *Renderer) renderTable(node *extensionast.Table, source []byte) string {
	var rows [][]string
	for row := node.FirstChild(); row != nil; row = row.NextSibling() {
		switch typed := row.(type) {
		case *extensionast.TableHeader:
			rows = append(rows, r.renderTableCells(typed, source))
		case *extensionast.TableRow:
			rows = append(rows, r.renderTableCells(typed, source))
		}
	}
	if len(rows) == 0 {
		return ""
	}
	widths := make([]int, 0, len(rows[0]))
	for _, row := range rows {
		for idx, cell := range row {
			if idx >= len(widths) {
				widths = append(widths, 0)
			}
			widths[idx] = max(widths[idx], lipgloss.Width(cell))
		}
	}

	border := lipgloss.NewStyle().Foreground(r.palette.MarkdownTableBorder)
	var lines []string
	for rowIndex, row := range rows {
		lines = append(lines, r.renderTableRow(row, widths))
		if rowIndex == 0 {
			lines = append(lines, r.renderTableDivider(widths, border))
		}
	}
	return strings.Join(lines, "\n")
}

func (r *Renderer) renderTableCells(parent ast.Node, source []byte) []string {
	var cells []string
	for cell := parent.FirstChild(); cell != nil; cell = cell.NextSibling() {
		tableCell, ok := cell.(*extensionast.TableCell)
		if !ok {
			continue
		}
		cells = append(cells, r.renderInlineChildren(tableCell, source))
	}
	return cells
}

func (r *Renderer) renderTableRow(row []string, widths []int) string {
	var cells []string
	for idx, width := range widths {
		cell := ""
		if idx < len(row) {
			cell = row[idx]
		}
		cells = append(cells, lipgloss.NewStyle().Foreground(r.palette.MarkdownText).Render(padRight(cell, width)))
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

func (r *Renderer) renderTableDivider(widths []int, border lipgloss.Style) string {
	parts := make([]string, 0, len(widths))
	for _, width := range widths {
		parts = append(parts, strings.Repeat("-", width+2))
	}
	return border.Render("|" + strings.Join(parts, "|") + "|")
}

func padRight(value string, width int) string {
	padding := width - lipgloss.Width(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
