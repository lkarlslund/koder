package markdown

import (
	"regexp"
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
	md        goldmark.Markdown
	palette   theme.Palette
	graphics  ui.GraphicsCapability
	imageMode ImageRenderMode
	codeStyle string
}

func New(palette theme.Palette, codeStyle string) (*Renderer, error) {
	codeStyle, _ = resolveCodeStyle(codeStyle)
	return &Renderer{
		md: goldmark.New(goldmark.WithExtensions(
			extension.GFM,
			extension.Footnote,
			extension.DefinitionList,
		)),
		palette:   palette,
		graphics:  ui.GraphicsNone,
		imageMode: ImageRenderTextOnly,
		codeStyle: codeStyle,
	}, nil
}

func (r *Renderer) Render(input string) string {
	spans := r.RenderStyled(input)
	base := ui.CellStyle{}
	if r != nil {
		base.FG = r.palette.MarkdownText
	}
	merged := make([]ui.StyledSpan, 0, len(spans))
	for _, span := range spans {
		span.Style = base.Merge(span.Style)
		merged = append(merged, span)
	}
	return ui.RenderStyledTextANSI(merged)
}

func (r *Renderer) RenderPlain(input string) string {
	return r.RenderPlainWidth(input, 0)
}

func (r *Renderer) RenderPlainWidth(input string, width int) string {
	if r == nil {
		return strings.TrimSpace(input)
	}
	return strings.TrimSpace(ui.PlainStyledText(r.RenderStyledWidth(input, width)))
}

func (r *Renderer) RenderStyled(input string) []ui.StyledSpan {
	return r.RenderStyledWidth(input, 0)
}

func (r *Renderer) RenderStyledWidth(input string, width int) []ui.StyledSpan {
	if r == nil {
		return []ui.StyledSpan{{Text: strings.TrimSpace(input)}}
	}
	source := []byte(strings.TrimSpace(input))
	if len(source) == 0 {
		return nil
	}
	doc := r.md.Parser().Parse(text.NewReader(source))
	return r.renderStyledBlockChildren(doc, source, width)
}

func (r *Renderer) renderStyledBlockChildren(parent ast.Node, source []byte, width int) []ui.StyledSpan {
	var out []ui.StyledSpan
	first := true
	for child := parent.FirstChild(); child != nil; {
		next := child.NextSibling()
		block, consumedNext, handled := r.renderStyledCodeBlockBundle(child, source)
		if handled {
			next = consumedNext
		} else {
			block = r.renderStyledBlock(child, source, width)
		}
		if len(block) == 0 {
			child = next
			continue
		}
		if !first {
			out = ui.AppendStyledSpan(out, "\n\n", ui.CellStyle{})
		}
		out = append(out, block...)
		first = false
		child = next
	}
	return out
}

func (r *Renderer) renderStyledBlock(node ast.Node, source []byte, width int) []ui.StyledSpan {
	switch typed := node.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		if desc, ok := r.standaloneImageDescriptor(node, source); ok {
			return r.renderImage(desc)
		}
		if mathBody, ok := extractDisplayMath(node, source); ok {
			return r.renderDisplayMath(mathBody)
		}
		return r.renderStyledInlineChildren(node, source, ui.CellStyle{})
	case *ast.Heading:
		return r.renderStyledHeading(typed, source)
	case *ast.Blockquote:
		if kind, body, ok := r.renderCalloutBlock(typed, source, width); ok {
			_ = kind
			return body
		}
		inner := ui.SplitStyledLines(r.renderStyledBlockChildren(node, source, width))
		return prefixStyledLines(
			inner,
			[]ui.StyledSpan{{Text: "│ ", Style: ui.CellStyle{FG: r.palette.MarkdownQuoteBorder}}},
			[]ui.StyledSpan{{Text: "│ ", Style: ui.CellStyle{FG: r.palette.MarkdownQuoteBorder}}},
		)
	case *ast.FencedCodeBlock:
		return r.renderStyledFencedCodeBlock(typed, source, nil)
	case *ast.CodeBlock:
		return r.renderStyledCodeBlock("", extractCodeLines(typed.Lines(), source))
	case *ast.List:
		return r.renderStyledList(typed, source, width)
	case *ast.ThematicBreak:
		return []ui.StyledSpan{{
			Text:  strings.Repeat("─", 32),
			Style: ui.CellStyle{FG: r.palette.MarkdownRule},
		}}
	case *extensionast.Table:
		return r.renderStyledTable(typed, source, width)
	case *extensionast.DefinitionList:
		return r.renderStyledDefinitionList(typed, source, width)
	case *extensionast.FootnoteList:
		return r.renderStyledFootnoteList(typed, source, width)
	case *ast.HTMLBlock:
		return r.renderHTMLBlock(node, source)
	default:
		if node.HasChildren() {
			return r.renderStyledBlockChildren(node, source, width)
		}
		return nil
	}
}

func (r *Renderer) renderStyledHeading(node *ast.Heading, source []byte) []ui.StyledSpan {
	style := ui.CellStyle{}
	switch node.Level {
	case 1:
		style = style.Merge(ui.CellStyle{FG: r.palette.MarkdownHeadingPrimary}.WithBold(true))
	case 2:
		style = style.Merge(ui.CellStyle{FG: r.palette.MarkdownHeadingSecondary}.WithBold(true))
	case 3:
		style = style.Merge(ui.CellStyle{FG: r.palette.MarkdownHeadingTertiary}.WithBold(true))
	case 4:
		style = style.Merge(ui.CellStyle{FG: r.palette.MarkdownHeadingTertiary}.WithBold(true).WithUnderline(true))
	case 5:
		style = style.Merge(ui.CellStyle{FG: r.palette.MarkdownQuoteText}.WithBold(true).WithItalic(true))
	default:
		style = style.Merge(ui.CellStyle{FG: r.palette.MarkdownQuoteText}.WithItalic(true))
	}
	return r.renderStyledInlineChildren(node, source, style)
}

type inlineHTMLState struct {
	abbrTitles []string
}

func (r *Renderer) renderStyledInlineChildren(parent ast.Node, source []byte, style ui.CellStyle) []ui.StyledSpan {
	var output []ui.StyledSpan
	state := &inlineHTMLState{}
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
		switch typed := child.(type) {
		case *ast.Text:
			value := string(typed.Segment.Value(source))
			if typed.HardLineBreak() {
				value += "\n"
			} else if typed.SoftLineBreak() {
				value += " "
			}
			output = append(output, r.renderStyledTextFragments(value, style)...)
		case *ast.String:
			output = append(output, r.renderStyledTextFragments(string(typed.Value), style)...)
		case *ast.RawHTML:
			output = append(output, r.renderRawHTML(typed, source, style, state)...)
		default:
			output = append(output, r.renderStyledInline(child, source, style)...)
		}
	}
	return output
}

func (r *Renderer) renderStyledInline(node ast.Node, source []byte, style ui.CellStyle) []ui.StyledSpan {
	switch typed := node.(type) {
	case *ast.CodeSpan:
		codeStyle := style.Merge(ui.CellStyle{
			FG: r.palette.MarkdownInlineCodeText,
			BG: r.palette.MarkdownInlineCodeBackground,
		})
		return r.renderStyledInlineChildren(node, source, codeStyle)
	case *ast.Emphasis:
		emphasisStyle := style
		if typed.Level >= 2 {
			emphasisStyle = emphasisStyle.Merge(ui.CellStyle{
				FG: r.palette.MarkdownStrongText,
			}.WithBold(true))
		} else {
			emphasisStyle = emphasisStyle.Merge(ui.CellStyle{
				FG: r.palette.MarkdownEmphasisText,
			}.WithItalic(true))
		}
		return r.renderStyledInlineChildren(node, source, emphasisStyle)
	case *extensionast.Strikethrough:
		return r.renderStyledInlineChildren(node, source, style.Merge(ui.CellStyle{}.WithStrikethrough(true)))
	case *ast.Link:
		labelStyle := style.Merge(ui.CellStyle{
			FG: r.palette.MarkdownLinkText,
		}.WithUnderline(true))
		targetStyle := style.Merge(ui.CellStyle{FG: r.palette.MarkdownLinkTargetText})
		out := r.renderStyledInlineChildren(node, source, labelStyle)
		target := strings.TrimSpace(string(typed.Destination))
		if target != "" {
			out = ui.AppendStyledSpan(out, " ("+target+")", targetStyle)
		}
		return out
	case *ast.Image:
		return r.renderInlineImage(imageDescriptorFromNode(typed, source, true, ""))
	case *ast.AutoLink:
		return []ui.StyledSpan{{
			Text:  string(typed.URL(source)),
			Style: style.Merge(ui.CellStyle{FG: r.palette.MarkdownLinkTargetText}.WithUnderline(true)),
		}}
	case *extensionast.TaskCheckBox:
		label := "☐ "
		checkboxStyle := style.Merge(ui.CellStyle{FG: r.palette.MarkdownListMarker})
		if typed.IsChecked {
			label = "✓ "
			checkboxStyle = checkboxStyle.Merge(ui.CellStyle{FG: firstColor(r.palette.DiffAddedText, r.palette.MarkdownListMarker)}.WithBold(true))
		}
		return []ui.StyledSpan{{Text: label, Style: checkboxStyle}}
	case *extensionast.FootnoteLink:
		return []ui.StyledSpan{{
			Text:  "[" + strconv.Itoa(typed.Index) + "]",
			Style: style.Merge(ui.CellStyle{FG: r.palette.MarkdownLinkTargetText}.WithUnderline(true)),
		}}
	case *extensionast.FootnoteBacklink:
		return nil
	default:
		if node.HasChildren() {
			return r.renderStyledInlineChildren(node, source, style)
		}
		return nil
	}
}

func (r *Renderer) renderStyledTextFragments(value string, style ui.CellStyle) []ui.StyledSpan {
	var out []ui.StyledSpan
	for len(value) > 0 {
		idx, token := nextInlineToken(value)
		if idx < 0 {
			out = appendStyled(out, value, style)
			break
		}
		if idx > 0 {
			out = appendStyled(out, value[:idx], style)
			value = value[idx:]
		}
		switch token {
		case "mark":
			end := strings.Index(value[2:], "==")
			if end <= 0 || strings.Contains(value[2:2+end], "\n") {
				out = appendStyled(out, value[:2], style)
				value = value[2:]
				continue
			}
			markStyle := style.Merge(ui.CellStyle{
				BG: firstColor(r.palette.MarkdownMarkBackground, r.palette.MarkdownLinkText.WithAlpha(72)),
			})
			out = appendStyled(out, value[2:2+end], markStyle)
			value = value[2+end+2:]
		case "math":
			end := findInlineMathEnd(value[1:])
			if end <= 0 || strings.Contains(value[1:1+end], "\n") {
				out = appendStyled(out, value[:1], style)
				value = value[1:]
				continue
			}
			out = append(out, r.renderInlineMath(value[1:1+end], style)...)
			value = value[1+end+1:]
		default:
			out = appendStyled(out, value[:1], style)
			value = value[1:]
		}
	}
	return out
}

func nextInlineToken(value string) (int, string) {
	bestIdx := -1
	bestToken := ""
	if idx := strings.Index(value, "=="); idx >= 0 {
		bestIdx, bestToken = idx, "mark"
	}
	for i := 0; i < len(value); i++ {
		if value[i] != '$' {
			continue
		}
		if (i > 0 && value[i-1] == '\\') || (i+1 < len(value) && value[i+1] == '$') {
			continue
		}
		if bestIdx == -1 || i < bestIdx {
			bestIdx, bestToken = i, "math"
		}
		break
	}
	return bestIdx, bestToken
}

func findInlineMathEnd(value string) int {
	for i := 0; i < len(value); i++ {
		if value[i] == '$' && (i == 0 || value[i-1] != '\\') {
			return i
		}
	}
	return -1
}

func (r *Renderer) renderRawHTML(node *ast.RawHTML, source []byte, style ui.CellStyle, state *inlineHTMLState) []ui.StyledSpan {
	raw := strings.TrimSpace(string(node.Segments.Value(source)))
	lower := strings.ToLower(raw)
	switch {
	case lower == "<sup>":
		return []ui.StyledSpan{{Text: "^(", Style: style.Merge(ui.CellStyle{FG: r.palette.MarkdownEmphasisText}.WithBold(true))}}
	case lower == "</sup>":
		return []ui.StyledSpan{{Text: ")", Style: style.Merge(ui.CellStyle{FG: r.palette.MarkdownEmphasisText}.WithBold(true))}}
	case lower == "<sub>":
		return []ui.StyledSpan{{Text: "_(", Style: style.Merge(ui.CellStyle{FG: r.palette.MarkdownEmphasisText}.WithBold(true))}}
	case lower == "</sub>":
		return []ui.StyledSpan{{Text: ")", Style: style.Merge(ui.CellStyle{FG: r.palette.MarkdownEmphasisText}.WithBold(true))}}
	case strings.HasPrefix(lower, "<abbr"):
		if title := parseHTMLAttr(raw, "title"); title != "" {
			state.abbrTitles = append(state.abbrTitles, title)
		} else {
			state.abbrTitles = append(state.abbrTitles, "")
		}
		return nil
	case lower == "</abbr>":
		if len(state.abbrTitles) == 0 {
			return nil
		}
		title := state.abbrTitles[len(state.abbrTitles)-1]
		state.abbrTitles = state.abbrTitles[:len(state.abbrTitles)-1]
		if strings.TrimSpace(title) == "" {
			return nil
		}
		return []ui.StyledSpan{{
			Text:  " (" + title + ")",
			Style: style.Merge(ui.CellStyle{FG: r.palette.MarkdownQuoteText}.WithItalic(true)),
		}}
	case lower == "<br>" || lower == "<br/>" || lower == "<br />":
		return []ui.StyledSpan{{Text: "\n", Style: style}}
	default:
		return nil
	}
}

func (r *Renderer) renderStyledCodeBlock(lang string, lines []string) []ui.StyledSpan {
	label := strings.TrimSpace(lang)
	if label == "" {
		label = "code"
	}
	borderStyle := ui.CellStyle{FG: r.palette.MarkdownCodeBlockBorder}
	bodyStyle := ui.CellStyle{
		FG: r.palette.MarkdownCodeBlockText,
		BG: r.palette.MarkdownInlineCodeBackground.WithAlpha(72),
	}
	out := []ui.StyledSpan{{Text: "┌─ " + label, Style: borderStyle}}
	for _, line := range lines {
		out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		out = ui.AppendStyledSpan(out, "  "+line, bodyStyle)
	}
	out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
	out = ui.AppendStyledSpan(out, "└"+strings.Repeat("─", max(2, runewidth.StringWidth(label)+2)), borderStyle)
	return out
}

func (r *Renderer) renderStyledList(node *ast.List, source []byte, width int) []ui.StyledSpan {
	var out []ui.StyledSpan
	itemNumber := node.Start
	first := true
	for item := node.FirstChild(); item != nil; item = item.NextSibling() {
		listItem, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		lines := ui.SplitStyledLines(r.renderStyledListItem(listItem, source, width))
		if !node.IsOrdered() && renderedTaskListItem(lines) {
			if !first {
				out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
			}
			out = append(out, linesToSpans(lines)...)
			first = false
			continue
		}
		marker := "•"
		markerStyle := ui.CellStyle{FG: r.palette.MarkdownListMarker}
		if node.IsOrdered() {
			marker = strconv.Itoa(itemNumber) + "."
			markerStyle = ui.CellStyle{FG: r.palette.MarkdownListEnumeration}
			itemNumber++
		}
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

func renderedTaskListItem(lines [][]ui.StyledSpan) bool {
	if len(lines) == 0 {
		return false
	}
	firstLine := strings.TrimSpace(ui.PlainStyledText(lines[0]))
	return strings.HasPrefix(firstLine, "✓ ") || strings.HasPrefix(firstLine, "☐ ")
}

func linesToSpans(lines [][]ui.StyledSpan) []ui.StyledSpan {
	var out []ui.StyledSpan
	for idx, line := range lines {
		if idx > 0 {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		out = append(out, line...)
	}
	return out
}

func (r *Renderer) renderStyledListItem(item *ast.ListItem, source []byte, width int) []ui.StyledSpan {
	var out []ui.StyledSpan
	first := true
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		block := r.renderStyledBlock(child, source, width)
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

func (r *Renderer) renderStyledTable(node *extensionast.Table, source []byte, widthHint int) []ui.StyledSpan {
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
	widths = fitTableWidths(widths, widthHint)
	var out []ui.StyledSpan
	borderStyle := ui.CellStyle{FG: r.palette.MarkdownTableBorder}
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
	cellLines := make([][]string, len(widths))
	rowHeight := 1
	for idx, width := range widths {
		cell := ""
		if idx < len(row) {
			cell = row[idx]
		}
		cellLines[idx] = wrapTableCell(cell, width)
		rowHeight = max(rowHeight, len(cellLines[idx]))
	}
	cellStyle := ui.CellStyle{}
	if header {
		cellStyle = cellStyle.WithBold(true)
	}
	for lineIndex := 0; lineIndex < rowHeight; lineIndex++ {
		if lineIndex > 0 {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		out = ui.AppendStyledSpan(out, "| ", borderStyle)
		for idx, width := range widths {
			cellLine := ""
			if idx < len(cellLines) && lineIndex < len(cellLines[idx]) {
				cellLine = padRight(cellLines[idx][lineIndex], width)
			} else {
				cellLine = strings.Repeat(" ", width)
			}
			out = ui.AppendStyledSpan(out, cellLine, cellStyle)
			if idx == len(widths)-1 {
				out = ui.AppendStyledSpan(out, " |", borderStyle)
				break
			}
			out = ui.AppendStyledSpan(out, " | ", borderStyle)
		}
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

func (r *Renderer) renderStyledDefinitionList(node *extensionast.DefinitionList, source []byte, width int) []ui.StyledSpan {
	var out []ui.StyledSpan
	first := true
	for child := node.FirstChild(); child != nil; {
		term, ok := child.(*extensionast.DefinitionTerm)
		if !ok {
			child = child.NextSibling()
			continue
		}
		if !first {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		out = append(out, r.renderStyledInlineChildren(term, source, ui.CellStyle{FG: r.palette.MarkdownHeadingSecondary}.WithBold(true))...)
		descNode := child.NextSibling()
		for descNode != nil {
			desc, ok := descNode.(*extensionast.DefinitionDescription)
			if !ok {
				break
			}
			lines := ui.SplitStyledLines(r.renderStyledBlockChildren(desc, source, width))
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
			out = append(out, prefixStyledLines(
				lines,
				[]ui.StyledSpan{{Text: "  : ", Style: ui.CellStyle{FG: r.palette.MarkdownListEnumeration}}},
				[]ui.StyledSpan{{Text: "    ", Style: ui.CellStyle{}}},
			)...)
			descNode = descNode.NextSibling()
			if descNode != nil {
				out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
			}
		}
		child = descNode
		first = false
	}
	return out
}

func (r *Renderer) renderStyledFootnoteList(node *extensionast.FootnoteList, source []byte, width int) []ui.StyledSpan {
	var out []ui.StyledSpan
	out = ui.AppendStyledSpan(out, "Footnotes", ui.CellStyle{FG: r.palette.MarkdownHeadingSecondary}.WithBold(true))
	first := true
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		footnote, ok := child.(*extensionast.Footnote)
		if !ok {
			continue
		}
		if first {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
			first = false
		} else {
			out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		}
		lines := ui.SplitStyledLines(r.renderStyledBlockChildren(footnote, source, width))
		out = append(out, prefixStyledLines(
			lines,
			[]ui.StyledSpan{{Text: "[^" + string(footnote.Ref) + "] ", Style: ui.CellStyle{FG: r.palette.MarkdownLinkTargetText}.WithBold(true)}},
			[]ui.StyledSpan{{Text: "     ", Style: ui.CellStyle{}}},
		)...)
	}
	return out
}

func (r *Renderer) renderHTMLBlock(node ast.Node, source []byte) []ui.StyledSpan {
	block, ok := node.(interface{ Lines() *text.Segments })
	if !ok {
		return nil
	}
	text := strings.TrimSpace(string(block.Lines().Value(source)))
	if text == "" {
		return nil
	}
	text = safeHTMLPlainText(text)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []ui.StyledSpan{{Text: text, Style: ui.CellStyle{FG: r.palette.MarkdownQuoteText}.WithItalic(true)}}
}

type calloutKind string

const (
	calloutNote      calloutKind = "NOTE"
	calloutTip       calloutKind = "TIP"
	calloutImportant calloutKind = "IMPORTANT"
	calloutWarning   calloutKind = "WARNING"
	calloutCaution   calloutKind = "CAUTION"
)

var calloutPattern = regexp.MustCompile(`^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*`)

func (r *Renderer) renderCalloutBlock(node *ast.Blockquote, source []byte, width int) (calloutKind, []ui.StyledSpan, bool) {
	firstParagraph, ok := node.FirstChild().(*ast.Paragraph)
	if !ok {
		return "", nil, false
	}
	firstLine := strings.TrimSpace(strings.SplitN(inlinePlainText(firstParagraph, source), "\n", 2)[0])
	match := calloutPattern.FindStringSubmatch(firstLine)
	if len(match) != 2 {
		return "", nil, false
	}
	kind := calloutKind(match[1])
	inner := ui.SplitStyledLines(r.renderStyledBlockChildren(node, source, width))
	if len(inner) == 0 {
		return kind, nil, true
	}
	inner[0] = trimStyledLinePrefix(inner[0], calloutPattern.FindString(firstLine))
	headerGlyph, borderColor, wash := r.calloutStyle(kind)
	header := []ui.StyledSpan{{
		Text:  "┌─ " + headerGlyph + " " + string(kind),
		Style: ui.CellStyle{FG: borderColor}.WithBold(true),
	}}
	header = mergeLineStyle(header, ui.CellStyle{BG: wash})
	body := prefixStyledLines(
		mergeLineStyles(inner, ui.CellStyle{BG: wash}),
		[]ui.StyledSpan{{Text: "│ ", Style: ui.CellStyle{FG: borderColor, BG: wash}}},
		[]ui.StyledSpan{{Text: "│ ", Style: ui.CellStyle{FG: borderColor, BG: wash}}},
	)
	footer := []ui.StyledSpan{{
		Text:  "└" + strings.Repeat("─", max(8, len(string(kind))+4)),
		Style: ui.CellStyle{FG: borderColor, BG: wash},
	}}
	var out []ui.StyledSpan
	out = append(out, header...)
	out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
	out = append(out, body...)
	out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
	out = append(out, footer...)
	return kind, out, true
}

func (r *Renderer) calloutStyle(kind calloutKind) (string, ui.CellColor, ui.CellColor) {
	switch kind {
	case calloutTip:
		return "💡", firstColor(r.palette.DiffAddedText, r.palette.MarkdownLinkText), firstColor(r.palette.DiffAddedText.WithAlpha(44), r.palette.MarkdownLinkText.WithAlpha(44))
	case calloutImportant:
		return "✱", firstColor(r.palette.MarkdownHeadingPrimary, r.palette.MarkdownStrongText), firstColor(r.palette.MarkdownHeadingPrimary.WithAlpha(52), r.palette.MarkdownStrongText.WithAlpha(52))
	case calloutWarning:
		return "⚠", firstColor(r.palette.MarkdownEmphasisText, r.palette.DiffDeletedText), firstColor(r.palette.MarkdownEmphasisText.WithAlpha(52), r.palette.DiffDeletedText.WithAlpha(52))
	case calloutCaution:
		return "⛔", firstColor(r.palette.DiffDeletedText, r.palette.MarkdownStrongText), firstColor(r.palette.DiffDeletedText.WithAlpha(52), r.palette.MarkdownStrongText.WithAlpha(52))
	default:
		return "ℹ", firstColor(r.palette.MarkdownLinkText, r.palette.MarkdownHeadingSecondary), firstColor(r.palette.MarkdownLinkText.WithAlpha(40), r.palette.MarkdownHeadingSecondary.WithAlpha(40))
	}
}

func (r *Renderer) renderDisplayMath(body string) []ui.StyledSpan {
	body = strings.TrimSpace(body)
	labelStyle := ui.CellStyle{FG: r.palette.MarkdownEmphasisText}.WithBold(true)
	bodyStyle := ui.CellStyle{
		FG: r.palette.MarkdownInlineCodeText,
		BG: r.palette.MarkdownInlineCodeBackground.WithAlpha(72),
	}
	out := []ui.StyledSpan{{Text: "┌─ math", Style: labelStyle}}
	for _, line := range strings.Split(body, "\n") {
		out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		out = ui.AppendStyledSpan(out, "  "+line, bodyStyle)
	}
	out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
	out = ui.AppendStyledSpan(out, "└──────", labelStyle)
	return out
}

func (r *Renderer) renderInlineMath(body string, style ui.CellStyle) []ui.StyledSpan {
	return []ui.StyledSpan{{
		Text:  "⟪" + strings.TrimSpace(body) + "⟫",
		Style: style.Merge(ui.CellStyle{FG: r.palette.MarkdownInlineCodeText, BG: r.palette.MarkdownInlineCodeBackground.WithAlpha(64)}),
	}}
}

func (r *Renderer) renderImage(desc ImageDescriptor) []ui.StyledSpan {
	switch r.imageMode {
	case ImageRenderReserved:
		return renderTextOnlyImage(desc, r.palette)
	default:
		return renderTextOnlyImage(desc, r.palette)
	}
}

func (r *Renderer) renderInlineImage(desc ImageDescriptor) []ui.StyledSpan {
	desc.Inline = true
	return r.renderImage(desc)
}

func (r *Renderer) standaloneImageDescriptor(node ast.Node, source []byte) (ImageDescriptor, bool) {
	first := node.FirstChild()
	if first == nil || first.NextSibling() != nil {
		return ImageDescriptor{}, false
	}
	switch typed := first.(type) {
	case *ast.Image:
		desc := imageDescriptorFromNode(typed, source, false, "")
		return desc, true
	case *ast.Link:
		img, ok := typed.FirstChild().(*ast.Image)
		if !ok || typed.FirstChild().NextSibling() != nil {
			return ImageDescriptor{}, false
		}
		desc := imageDescriptorFromNode(img, source, false, string(typed.Destination))
		return desc, true
	default:
		return ImageDescriptor{}, false
	}
}

func imageDescriptorFromNode(node *ast.Image, source []byte, inline bool, linked string) ImageDescriptor {
	alt := strings.TrimSpace(inlinePlainText(node, source))
	if alt == "" {
		alt = "image"
	}
	desc := ImageDescriptor{
		Alt:               alt,
		Destination:       strings.TrimSpace(string(node.Destination)),
		Title:             strings.TrimSpace(string(node.Title)),
		LinkedDestination: strings.TrimSpace(linked),
		Inline:            inline,
	}
	desc.SourceKind = classifyImageSource(desc.Destination)
	return desc
}

func renderInlineImageText(desc ImageDescriptor, palette theme.Palette) []ui.StyledSpan {
	labelStyle := ui.CellStyle{FG: palette.MarkdownHeadingSecondary}.WithBold(true)
	metaStyle := ui.CellStyle{FG: palette.MarkdownQuoteText}.WithItalic(true)
	var out []ui.StyledSpan
	out = appendStyled(out, "🖼 "+desc.Alt, labelStyle)
	if desc.Destination != "" {
		out = appendStyled(out, " ("+desc.Destination+")", metaStyle)
	}
	if desc.LinkedDestination != "" && desc.LinkedDestination != desc.Destination {
		out = appendStyled(out, " ↗ "+desc.LinkedDestination, metaStyle)
	}
	return out
}

func renderBlockImageText(desc ImageDescriptor, palette theme.Palette) []ui.StyledSpan {
	borderStyle := ui.CellStyle{FG: palette.MarkdownHeadingSecondary}.WithBold(true)
	bodyStyle := ui.CellStyle{FG: palette.MarkdownText, BG: firstColor(palette.MarkdownMarkBackground, palette.MarkdownLinkText.WithAlpha(40))}
	metaStyle := ui.CellStyle{FG: palette.MarkdownQuoteText, BG: bodyStyle.BG}.WithItalic(true)
	var out []ui.StyledSpan
	out = appendStyled(out, "┌─ 🖼 Image", borderStyle)
	out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
	out = appendStyled(out, "│ Alt: "+desc.Alt, bodyStyle)
	if desc.Destination != "" {
		out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		out = appendStyled(out, "│ Src: "+desc.Destination, metaStyle)
	}
	if desc.Title != "" {
		out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		out = appendStyled(out, "│ Title: "+desc.Title, metaStyle)
	}
	if desc.LinkedDestination != "" && desc.LinkedDestination != desc.Destination {
		out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		out = appendStyled(out, "│ Link: "+desc.LinkedDestination, metaStyle)
	}
	out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
	out = appendStyled(out, "└────────", borderStyle)
	return out
}

func inlinePlainText(node ast.Node, source []byte) string {
	var b strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		switch typed := child.(type) {
		case *ast.Text:
			b.Write(typed.Segment.Value(source))
			if typed.HardLineBreak() || typed.SoftLineBreak() {
				b.WriteByte('\n')
			}
		case *ast.String:
			b.Write(typed.Value)
		default:
			if child.HasChildren() {
				b.WriteString(inlinePlainText(child, source))
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func extractDisplayMath(node ast.Node, source []byte) (string, bool) {
	text := inlinePlainText(node, source)
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "$$") || !strings.HasSuffix(text, "$$") || len(text) < 4 {
		return "", false
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "$$"), "$$")), true
}

func parseHTMLAttr(tag, attr string) string {
	re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(attr) + `\s*=\s*\"([^\"]+)\"`)
	match := re.FindStringSubmatch(tag)
	if len(match) == 2 {
		return htmlEntityDecode(match[1])
	}
	re = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(attr) + `\s*=\s*'([^']+)'`)
	match = re.FindStringSubmatch(tag)
	if len(match) == 2 {
		return htmlEntityDecode(match[1])
	}
	return ""
}

func safeHTMLPlainText(value string) string {
	replacer := strings.NewReplacer(
		"<sup>", "^(",
		"</sup>", ")",
		"<sub>", "_(",
		"</sub>", ")",
		"<br>", "\n",
		"<br/>", "\n",
		"<br />", "\n",
	)
	value = replacer.Replace(value)
	value = regexp.MustCompile(`(?i)<abbr[^>]*title=\"([^\"]+)\"[^>]*>([^<]+)</abbr>`).ReplaceAllString(value, `$2 ($1)`)
	value = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, "")
	return htmlEntityDecode(strings.TrimSpace(value))
}

func htmlEntityDecode(value string) string {
	replacer := strings.NewReplacer(
		"&lt;", "<",
		"&gt;", ">",
		"&amp;", "&",
		"&quot;", "\"",
		"&#39;", "'",
	)
	return replacer.Replace(value)
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

func fitTableWidths(widths []int, widthHint int) []int {
	if len(widths) == 0 || widthHint <= 0 {
		return widths
	}
	available := widthHint - (3*len(widths) + 1)
	if available <= 0 {
		available = len(widths)
	}
	fitted := append([]int(nil), widths...)
	for i := range fitted {
		if fitted[i] < 1 {
			fitted[i] = 1
		}
	}
	for sumInts(fitted) > available {
		widest := 0
		for i := 1; i < len(fitted); i++ {
			if fitted[i] > fitted[widest] {
				widest = i
			}
		}
		if fitted[widest] <= 1 {
			break
		}
		fitted[widest]--
	}
	return fitted
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func wrapTableCell(input string, width int) []string {
	if width <= 0 {
		return []string{strings.TrimSpace(input)}
	}
	var wrapped []string
	for _, line := range strings.Split(strings.TrimSpace(input), "\n") {
		if strings.TrimSpace(line) == "" {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, strings.Split(ui.PlainWordWrap(line, width), "\n")...)
	}
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}

func appendStyled(out []ui.StyledSpan, text string, style ui.CellStyle) []ui.StyledSpan {
	if text == "" {
		return out
	}
	return append(out, ui.StyledSpan{Text: text, Style: style})
}

func firstColor(values ...ui.CellColor) ui.CellColor {
	for _, value := range values {
		if value.Valid() {
			return value
		}
	}
	return ui.CellColor{}
}

func mergeLineStyles(lines [][]ui.StyledSpan, overlay ui.CellStyle) [][]ui.StyledSpan {
	out := make([][]ui.StyledSpan, len(lines))
	for idx, line := range lines {
		out[idx] = mergeLineStyle(line, overlay)
	}
	return out
}

func mergeLineStyle(line []ui.StyledSpan, overlay ui.CellStyle) []ui.StyledSpan {
	if len(line) == 0 {
		return []ui.StyledSpan{{Text: "", Style: overlay}}
	}
	out := make([]ui.StyledSpan, 0, len(line))
	for _, span := range line {
		span.Style = span.Style.Merge(overlay)
		out = append(out, span)
	}
	return out
}

func trimStyledLinePrefix(line []ui.StyledSpan, prefix string) []ui.StyledSpan {
	if prefix == "" || len(line) == 0 {
		return line
	}
	remaining := prefix
	out := make([]ui.StyledSpan, 0, len(line))
	for _, span := range line {
		if remaining == "" {
			out = append(out, span)
			continue
		}
		if strings.HasPrefix(remaining, span.Text) {
			remaining = strings.TrimPrefix(remaining, span.Text)
			continue
		}
		if strings.HasPrefix(span.Text, remaining) {
			span.Text = strings.TrimPrefix(span.Text, remaining)
			remaining = ""
			if span.Text != "" {
				out = append(out, span)
			}
			continue
		}
		out = append(out, span)
	}
	return out
}
