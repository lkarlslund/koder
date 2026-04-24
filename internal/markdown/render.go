package markdown

import (
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/mattn/go-runewidth"
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
	spans := r.RenderStyled(input)
	base := ui.CellStyle{}
	if r != nil {
		base.FG = ui.CellColorFromLipgloss(r.palette.MarkdownText)
	}
	merged := make([]ui.StyledSpan, 0, len(spans))
	for _, span := range spans {
		span.Style = base.Merge(span.Style)
		merged = append(merged, span)
	}
	return ui.RenderStyledTextANSI(merged)
}

func (r *Renderer) RenderPlain(input string) string {
	if r == nil {
		return strings.TrimSpace(input)
	}
	return strings.TrimSpace(ui.PlainStyledText(r.RenderStyled(input)))
}

func (r *Renderer) RenderStyled(input string) []ui.StyledSpan {
	if r == nil {
		return []ui.StyledSpan{{Text: strings.TrimSpace(input)}}
	}
	source := []byte(strings.TrimSpace(input))
	if len(source) == 0 {
		return nil
	}
	doc := r.md.Parser().Parse(text.NewReader(source))
	return r.renderStyledBlockChildren(doc, source)
}

func (r *Renderer) renderStyledBlockChildren(parent ast.Node, source []byte) []ui.StyledSpan {
	var out []ui.StyledSpan
	first := true
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		block := r.renderStyledBlock(child, source)
		if len(block) == 0 {
			continue
		}
		if !first {
			out = ui.AppendStyledSpan(out, "\n\n", ui.CellStyle{})
		}
		out = append(out, block...)
		first = false
	}
	return out
}

func (r *Renderer) renderStyledBlock(node ast.Node, source []byte) []ui.StyledSpan {
	switch typed := node.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return r.renderStyledInlineChildren(node, source, ui.CellStyle{})
	case *ast.Heading:
		style := ui.CellStyle{Bold: true}
		switch typed.Level {
		case 1:
			style.FG = ui.CellColorFromLipgloss(r.palette.MarkdownHeadingPrimary)
		case 2:
			style.FG = ui.CellColorFromLipgloss(r.palette.MarkdownHeadingSecondary)
		default:
			style.FG = ui.CellColorFromLipgloss(r.palette.MarkdownHeadingTertiary)
		}
		return r.renderStyledInlineChildren(node, source, style)
	case *ast.Blockquote:
		inner := ui.SplitStyledLines(r.renderStyledBlockChildren(node, source))
		return prefixStyledLines(
			inner,
			[]ui.StyledSpan{{Text: "│ ", Style: ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownQuoteBorder)}}},
			[]ui.StyledSpan{{Text: "│ ", Style: ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownQuoteBorder)}}},
		)
	case *ast.FencedCodeBlock:
		return r.renderStyledCodeBlock(string(typed.Language(source)), typed.Lines(), source)
	case *ast.CodeBlock:
		return r.renderStyledCodeBlock("", typed.Lines(), source)
	case *ast.List:
		return r.renderStyledList(typed, source)
	case *ast.ThematicBreak:
		return []ui.StyledSpan{{
			Text:  strings.Repeat("─", 32),
			Style: ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownRule)},
		}}
	case *extensionast.Table:
		return r.renderStyledTable(typed, source)
	case *ast.HTMLBlock:
		return nil
	default:
		if node.HasChildren() {
			return r.renderStyledBlockChildren(node, source)
		}
		return nil
	}
}

func (r *Renderer) renderStyledInlineChildren(parent ast.Node, source []byte, style ui.CellStyle) []ui.StyledSpan {
	var output []ui.StyledSpan
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		output = append(output, r.renderStyledInline(child, source, style)...)
	}
	return output
}

func (r *Renderer) renderStyledInline(node ast.Node, source []byte, style ui.CellStyle) []ui.StyledSpan {
	switch typed := node.(type) {
	case *ast.Text:
		value := string(typed.Segment.Value(source))
		if typed.HardLineBreak() {
			value += "\n"
		} else if typed.SoftLineBreak() {
			value += " "
		}
		return []ui.StyledSpan{{Text: value, Style: style}}
	case *ast.String:
		return []ui.StyledSpan{{Text: string(typed.Value), Style: style}}
	case *ast.CodeSpan:
		codeStyle := style.Merge(ui.CellStyle{
			FG: ui.CellColorFromLipgloss(r.palette.MarkdownInlineCodeText),
			BG: ui.CellColorFromLipgloss(r.palette.MarkdownInlineCodeBackground),
		})
		return r.renderStyledInlineChildren(node, source, codeStyle)
	case *ast.Emphasis:
		emphasisStyle := style
		if typed.Level >= 2 {
			emphasisStyle = emphasisStyle.Merge(ui.CellStyle{
				FG:   ui.CellColorFromLipgloss(r.palette.MarkdownStrongText),
				Bold: true,
			})
		} else {
			emphasisStyle = emphasisStyle.Merge(ui.CellStyle{
				FG:     ui.CellColorFromLipgloss(r.palette.MarkdownEmphasisText),
				Italic: true,
			})
		}
		return r.renderStyledInlineChildren(node, source, emphasisStyle)
	case *extensionast.Strikethrough:
		return r.renderStyledInlineChildren(node, source, style.Merge(ui.CellStyle{Strikethrough: true}))
	case *ast.Link:
		labelStyle := style.Merge(ui.CellStyle{
			FG:        ui.CellColorFromLipgloss(r.palette.MarkdownLinkText),
			Underline: true,
		})
		targetStyle := style.Merge(ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownLinkTargetText)})
		out := r.renderStyledInlineChildren(node, source, labelStyle)
		target := strings.TrimSpace(string(typed.Destination))
		if target != "" {
			out = ui.AppendStyledSpan(out, " ("+target+")", targetStyle)
		}
		return out
	case *ast.AutoLink:
		return []ui.StyledSpan{{
			Text:  string(typed.URL(source)),
			Style: style.Merge(ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownLinkTargetText), Underline: true}),
		}}
	case *ast.RawHTML:
		return nil
	case *extensionast.TaskCheckBox:
		label := "[ ] "
		if typed.IsChecked {
			label = "[x] "
		}
		return []ui.StyledSpan{{Text: label, Style: style}}
	default:
		if node.HasChildren() {
			return r.renderStyledInlineChildren(node, source, style)
		}
		return nil
	}
}

func (r *Renderer) renderStyledCodeBlock(lang string, lines *text.Segments, source []byte) []ui.StyledSpan {
	label := strings.TrimSpace(lang)
	if label == "" {
		label = "code"
	}
	borderStyle := ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownCodeBlockBorder)}
	bodyStyle := ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownCodeBlockText)}
	out := []ui.StyledSpan{{Text: "┌─ " + label, Style: borderStyle}}
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		line := strings.TrimRight(string(segment.Value(source)), "\n")
		out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		out = ui.AppendStyledSpan(out, "  "+line, bodyStyle)
	}
	out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
	out = ui.AppendStyledSpan(out, "└"+strings.Repeat("─", max(2, runewidth.StringWidth(label)+2)), borderStyle)
	return out
}

func (r *Renderer) renderStyledList(node *ast.List, source []byte) []ui.StyledSpan {
	var out []ui.StyledSpan
	itemNumber := node.Start
	first := true
	for item := node.FirstChild(); item != nil; item = item.NextSibling() {
		listItem, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		marker := "•"
		markerStyle := ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownListMarker)}
		if node.IsOrdered() {
			marker = strconv.Itoa(itemNumber) + "."
			markerStyle = ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownListEnumeration)}
			itemNumber++
		}
		lines := ui.SplitStyledLines(r.renderStyledListItem(listItem, source))
		block := prefixStyledLines(
			lines,
			[]ui.StyledSpan{{Text: marker + " ", Style: markerStyle}},
			[]ui.StyledSpan{{Text: strings.Repeat(" ", len(marker)+2), Style: ui.CellStyle{}}},
		)
		if !first {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		out = append(out, block...)
		first = false
	}
	return out
}

func (r *Renderer) renderStyledListItem(item *ast.ListItem, source []byte) []ui.StyledSpan {
	var out []ui.StyledSpan
	first := true
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		block := r.renderStyledBlock(child, source)
		if len(block) == 0 {
			continue
		}
		if !first {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		out = append(out, block...)
		first = false
	}
	return out
}

func (r *Renderer) renderStyledTable(node *extensionast.Table, source []byte) []ui.StyledSpan {
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
		return nil
	}
	widths := make([]int, 0, len(rows[0]))
	for _, row := range rows {
		for idx, cell := range row {
			if idx >= len(widths) {
				widths = append(widths, 0)
			}
			widths[idx] = max(widths[idx], runewidth.StringWidth(cell))
		}
	}
	var out []ui.StyledSpan
	borderStyle := ui.CellStyle{FG: ui.CellColorFromLipgloss(r.palette.MarkdownTableBorder)}
	for rowIndex, row := range rows {
		if rowIndex > 0 {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		out = append(out, r.renderStyledTableRow(row, widths, borderStyle, rowIndex == 0)...)
		if rowIndex == 0 {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
			out = append(out, r.renderStyledTableDivider(widths, borderStyle)...)
		}
	}
	return out
}

func (r *Renderer) renderStyledTableRow(row []string, widths []int, borderStyle ui.CellStyle, header bool) []ui.StyledSpan {
	var out []ui.StyledSpan
	out = ui.AppendStyledSpan(out, "| ", borderStyle)
	for idx, width := range widths {
		cell := ""
		if idx < len(row) {
			cell = padRight(row[idx], width)
		}
		cellStyle := ui.CellStyle{}
		if header {
			cellStyle.Bold = true
		}
		out = ui.AppendStyledSpan(out, cell, cellStyle)
		if idx == len(widths)-1 {
			out = ui.AppendStyledSpan(out, " |", borderStyle)
			break
		}
		out = ui.AppendStyledSpan(out, " | ", borderStyle)
	}
	return out
}

func (r *Renderer) renderStyledTableDivider(widths []int, borderStyle ui.CellStyle) []ui.StyledSpan {
	var out []ui.StyledSpan
	out = ui.AppendStyledSpan(out, "|", borderStyle)
	for idx, width := range widths {
		out = ui.AppendStyledSpan(out, strings.Repeat("-", width+2), borderStyle)
		if idx < len(widths)-1 {
			out = ui.AppendStyledSpan(out, "|", borderStyle)
		}
	}
	out = ui.AppendStyledSpan(out, "|", borderStyle)
	return out
}

func prefixStyledLines(lines [][]ui.StyledSpan, firstPrefix, continuationPrefix []ui.StyledSpan) []ui.StyledSpan {
	var out []ui.StyledSpan
	for idx, line := range lines {
		if idx > 0 {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		prefix := continuationPrefix
		if idx == 0 {
			prefix = firstPrefix
		}
		out = append(out, prefix...)
		out = append(out, line...)
	}
	return out
}

func (r *Renderer) renderTableCells(parent ast.Node, source []byte) []string {
	var cells []string
	for cell := parent.FirstChild(); cell != nil; cell = cell.NextSibling() {
		tableCell, ok := cell.(*extensionast.TableCell)
		if !ok {
			continue
		}
		cells = append(cells, strings.TrimSpace(ui.PlainStyledText(r.renderStyledInlineChildren(tableCell, source, ui.CellStyle{}))))
	}
	return cells
}

func padRight(value string, width int) string {
	padding := width - runewidth.StringWidth(value)
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
