package markdown

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/lkarlslund/koder/internal/ui"
)

type codeFenceOptions struct {
	Language    string
	ShowNumbers bool
	Highlights  map[int]bool
	Focus       map[int]bool
}

type codeLineAnnotation struct {
	ID   string
	Text string
}

var (
	codeFenceAttrPattern = regexp.MustCompile(`\{([^}]*)\}`)
	codeRangePattern     = regexp.MustCompile(`^\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*$`)
	codeNotePattern      = regexp.MustCompile(`^\[!(\d+)\]:\s*(.+?)\s*$`)
	codeMarkerPattern    = regexp.MustCompile(`^(.*?)(\s*(?://|#|--)\s*)(.*?)\[\!(\d+)\]\s*$`)
)

func (r *Renderer) renderStyledCodeBlockBundle(node ast.Node, source []byte) ([]ui.StyledSpan, ast.Node, bool) {
	fenced, ok := node.(*ast.FencedCodeBlock)
	if !ok {
		return nil, node.NextSibling(), false
	}
	noteDefs, next := collectCodeNoteDefinitions(fenced.NextSibling(), source)
	return r.renderStyledFencedCodeBlock(fenced, source, noteDefs), next, true
}

func collectCodeNoteDefinitions(start ast.Node, source []byte) (map[string]string, ast.Node) {
	notes := map[string]string{}
	current := start
	for current != nil {
		paragraph, ok := current.(*ast.Paragraph)
		if !ok {
			break
		}
		textValue := strings.TrimSpace(string(paragraph.Text(source)))
		if textValue == "" {
			break
		}
		lines := strings.Split(textValue, "\n")
		parsed := map[string]string{}
		for _, line := range lines {
			match := codeNotePattern.FindStringSubmatch(strings.TrimSpace(line))
			if len(match) != 3 {
				return notes, current
			}
			parsed[match[1]] = match[2]
		}
		for id, value := range parsed {
			notes[id] = value
		}
		current = current.NextSibling()
	}
	return notes, current
}

func (r *Renderer) renderStyledFencedCodeBlock(node *ast.FencedCodeBlock, source []byte, noteDefs map[string]string) []ui.StyledSpan {
	opts := parseCodeFenceOptions(node, source)
	label := opts.Language
	if label == "" {
		label = "code"
	}
	lines := extractCodeLines(node.Lines(), source)
	annotations := collectCodeLineAnnotations(lines, noteDefs)
	rawCode := strings.Join(lines, "\n")
	lineTokens := r.highlightCodeLines(opts.Language, rawCode, len(lines))
	baseStyle := ui.CellStyle{
		FG: r.palette.MarkdownCodeBlockText,
		BG: r.palette.MarkdownInlineCodeBackground.WithAlpha(72),
	}
	borderStyle := ui.CellStyle{FG: r.palette.MarkdownCodeBlockBorder}
	out := []ui.StyledSpan{{Text: "┌─ " + label, Style: borderStyle}}
	digits := len(strconv.Itoa(max(1, len(lines))))
	badgeWidth := annotationBadgeWidth(annotations)
	for idx := range lines {
		out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		lineSpans := lineTokens[idx]
		if len(lineSpans) == 0 {
			lineSpans = []ui.StyledSpan{{Text: lines[idx]}}
		}
		lineOverlay, marker, markerColor := r.codeLineOverlay(opts, lines[idx], idx+1)
		lineBase := baseStyle.Merge(lineOverlay)
		gutter := r.codeGutter(idx+1, digits, badgeWidth, marker, markerColor, opts.ShowNumbers, annotations[idx], lineBase)
		out = append(out, gutter...)
		lineBase = applyFocusStyle(lineBase, opts.Focus, idx+1, r.palette.MarkdownCodeFocusDim)
		for _, span := range lineSpans {
			style := lineBase.Merge(span.Style)
			style = applyFocusStyle(style, opts.Focus, idx+1, r.palette.MarkdownCodeFocusDim)
			out = appendStyled(out, span.Text, style)
		}
	}
	out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
	out = ui.AppendStyledSpan(out, "└"+strings.Repeat("─", max(2, len(label)+2)), borderStyle)
	if notes := r.renderCodeNotes(annotations, noteDefs); len(notes) > 0 {
		out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		out = append(out, notes...)
	}
	return out
}

func parseCodeFenceOptions(node *ast.FencedCodeBlock, source []byte) codeFenceOptions {
	opts := codeFenceOptions{
		Highlights: map[int]bool{},
		Focus:      map[int]bool{},
	}
	if node == nil {
		return opts
	}
	opts.Language = strings.TrimSpace(string(node.Language(source)))
	var rawInfo string
	if node.Info != nil {
		rawInfo = strings.TrimSpace(string(node.Info.Text(source)))
	}
	match := codeFenceAttrPattern.FindStringSubmatch(rawInfo)
	if len(match) != 2 {
		return opts
	}
	for _, field := range strings.Fields(match[1]) {
		switch {
		case field == "linenums":
			opts.ShowNumbers = true
		case strings.HasPrefix(field, "highlight="):
			opts.Highlights = parseCodeRanges(strings.TrimPrefix(field, "highlight="))
		case strings.HasPrefix(field, "focus="):
			opts.Focus = parseCodeRanges(strings.TrimPrefix(field, "focus="))
		}
	}
	return opts
}

func parseCodeRanges(value string) map[int]bool {
	value = strings.TrimSpace(value)
	if value == "" || !codeRangePattern.MatchString(value) {
		return map[int]bool{}
	}
	out := map[int]bool{}
	for _, part := range strings.Split(value, ",") {
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			start, _ := strconv.Atoi(bounds[0])
			end, _ := strconv.Atoi(bounds[1])
			if start <= 0 || end <= 0 || end < start {
				continue
			}
			for line := start; line <= end; line++ {
				out[line] = true
			}
			continue
		}
		line, _ := strconv.Atoi(part)
		if line > 0 {
			out[line] = true
		}
	}
	return out
}

func extractCodeLines(lines *text.Segments, source []byte) []string {
	if lines == nil || lines.Len() == 0 {
		return []string{""}
	}
	out := make([]string, 0, lines.Len())
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		out = append(out, strings.TrimRight(string(segment.Value(source)), "\n"))
	}
	return out
}

func (r *Renderer) highlightCodeLines(language, code string, lineCount int) [][]ui.StyledSpan {
	lines := make([][]ui.StyledSpan, max(1, lineCount))
	if lineCount == 0 {
		return lines
	}
	language = strings.TrimSpace(language)
	if language == "" {
		return lines
	}
	lexer := lexers.Get(language)
	if lexer == nil {
		return lines
	}
	styleName, style := resolveCodeStyle(r.codeStyle)
	r.codeStyle = styleName
	if style == nil {
		return lines
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return lines
	}
	lineIndex := 0
	for token := iterator(); token != chroma.EOF; token = iterator() {
		entry := style.Get(token.Type)
		tokenStyle := chromaStyleToCellStyle(entry)
		parts := strings.Split(token.Value, "\n")
		for idx, part := range parts {
			if idx > 0 {
				lineIndex++
				if lineIndex >= len(lines) {
					break
				}
			}
			if part == "" || lineIndex >= len(lines) {
				continue
			}
			lines[lineIndex] = appendStyled(lines[lineIndex], part, tokenStyle)
		}
	}
	return lines
}

func chromaStyleToCellStyle(entry chroma.StyleEntry) ui.CellStyle {
	style := ui.CellStyle{}
	if entry.Colour.IsSet() {
		style.FG = ui.NewCellColorRGB(entry.Colour.Red(), entry.Colour.Green(), entry.Colour.Blue())
	}
	if entry.Bold == chroma.Yes {
		style = style.WithBold(true)
	}
	if entry.Italic == chroma.Yes {
		style = style.WithItalic(true)
	}
	if entry.Underline == chroma.Yes {
		style = style.WithUnderline(true)
	}
	return style
}

func collectCodeLineAnnotations(lines []string, noteDefs map[string]string) []codeLineAnnotation {
	out := make([]codeLineAnnotation, len(lines))
	for idx, line := range lines {
		match := codeMarkerPattern.FindStringSubmatch(line)
		if len(match) != 5 {
			continue
		}
		id := match[4]
		left := strings.TrimRight(match[1], " \t")
		commentBody := strings.TrimRight(match[3], " \t")
		if commentBody != "" {
			lines[idx] = left + match[2] + commentBody
		} else {
			lines[idx] = left
		}
		out[idx] = codeLineAnnotation{ID: id, Text: noteDefs[id]}
	}
	return out
}

func (r *Renderer) codeLineOverlay(opts codeFenceOptions, line string, lineNumber int) (ui.CellStyle, string, ui.CellColor) {
	overlay := ui.CellStyle{}
	marker := " "
	markerColor := r.palette.MarkdownCodeLineNumber
	switch {
	case opts.Language == "diff" && strings.HasPrefix(line, "@@"):
		marker = "@"
		markerColor = r.palette.MarkdownCodeAnnotationBadge
	case opts.Language == "diff" && strings.HasPrefix(line, "+"):
		overlay = overlay.Merge(ui.CellStyle{BG: r.palette.MarkdownCodeDiffAddedBG})
		marker = "+"
		markerColor = r.palette.DiffAddedText
	case opts.Language == "diff" && strings.HasPrefix(line, "-"):
		overlay = overlay.Merge(ui.CellStyle{BG: r.palette.MarkdownCodeDiffDeletedBG})
		marker = "-"
		markerColor = r.palette.DiffDeletedText
	}
	if opts.Highlights[lineNumber] {
		overlay = overlay.Merge(ui.CellStyle{BG: r.palette.MarkdownCodeHighlightBG})
		if marker == " " {
			marker = "▍"
			markerColor = r.palette.MarkdownCodeAnnotationBadge
		}
	}
	return overlay, marker, markerColor
}

func (r *Renderer) codeGutter(lineNumber, digits, badgeWidth int, marker string, markerColor ui.CellColor, showNumbers bool, annotation codeLineAnnotation, lineBase ui.CellStyle) []ui.StyledSpan {
	var out []ui.StyledSpan
	out = appendStyled(out, marker+" ", lineBase.Merge(ui.CellStyle{FG: markerColor}.WithBold(marker != " ")))
	if showNumbers {
		out = appendStyled(out, fmt.Sprintf("%*d ", digits, lineNumber), lineBase.Merge(ui.CellStyle{FG: r.palette.MarkdownCodeLineNumber}))
	}
	if badgeWidth > 0 {
		badge := ""
		badgeStyle := lineBase
		if annotation.ID != "" {
			badge = annotationBadge(annotation.ID)
			badgeStyle = lineBase.Merge(ui.CellStyle{
				FG: r.palette.MarkdownCodeAnnotationText,
				BG: r.palette.MarkdownCodeAnnotationBadge.WithAlpha(80),
			}.WithBold(true))
		}
		out = appendStyled(out, padRight(badge, badgeWidth)+" ", badgeStyle)
	}
	return out
}

func annotationBadgeWidth(annotations []codeLineAnnotation) int {
	width := 0
	for _, annotation := range annotations {
		if annotation.ID == "" {
			continue
		}
		width = max(width, ui.PlainWidth(annotationBadge(annotation.ID)))
	}
	return width
}

func annotationBadge(id string) string {
	if n, err := strconv.Atoi(id); err == nil && n >= 1 && n <= 20 {
		return []string{"①", "②", "③", "④", "⑤", "⑥", "⑦", "⑧", "⑨", "⑩", "⑪", "⑫", "⑬", "⑭", "⑮", "⑯", "⑰", "⑱", "⑲", "⑳"}[n-1]
	}
	return "[" + id + "]"
}

func applyFocusStyle(style ui.CellStyle, focus map[int]bool, lineNumber int, dim ui.CellColor) ui.CellStyle {
	if len(focus) == 0 || focus[lineNumber] || !dim.Valid() {
		return style
	}
	return style.Merge(ui.CellStyle{FG: dim})
}

func (r *Renderer) renderCodeNotes(annotations []codeLineAnnotation, noteDefs map[string]string) []ui.StyledSpan {
	ordered := make([]string, 0, len(noteDefs))
	seen := map[string]bool{}
	for _, annotation := range annotations {
		if annotation.ID == "" || annotation.Text == "" || seen[annotation.ID] {
			continue
		}
		ordered = append(ordered, annotation.ID)
		seen[annotation.ID] = true
	}
	if len(ordered) == 0 {
		return nil
	}
	borderStyle := ui.CellStyle{FG: r.palette.MarkdownCodeAnnotationBadge}.WithBold(true)
	bodyStyle := ui.CellStyle{FG: r.palette.MarkdownCodeAnnotationText, BG: r.palette.MarkdownCodeAnnotationBadge.WithAlpha(36)}
	out := []ui.StyledSpan{{Text: "├─ Notes", Style: borderStyle}}
	for _, id := range ordered {
		out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
		out = appendStyled(out, "│ "+annotationBadge(id)+" ", borderStyle)
		out = appendStyled(out, noteDefs[id], bodyStyle)
	}
	out = ui.AppendStyledSpan(out, "\n", ui.CellStyle{})
	out = appendStyled(out, "└──────", borderStyle)
	return out
}

func orderedCodeStylesForTests() []string {
	names := CodeStyleNames()
	slices.Sort(names)
	return names
}
