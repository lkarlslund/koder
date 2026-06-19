package codediag

import (
	"context"
	"strconv"
	"strings"

	mermaid "github.com/sammcj/mermaid-check"
)

type markdownCodeFence struct {
	Language  string
	Content   string
	StartLine int
}

func markdownRendererDiagnostics(ctx context.Context, path, content string) Report {
	_ = ctx
	fences := markdownCodeFences(content)
	var report Report
	for _, fence := range fences {
		if !isMermaidFence(fence.Language) {
			continue
		}
		if _, err := mermaid.Parse(fence.Content); err != nil {
			lineOffset, column := mermaidErrorPosition(err.Error())
			if column == 0 {
				column = 1
			}
			line := fence.StartLine
			if lineOffset > 0 {
				line += lineOffset - 1
			}
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Source:   SourceSyntax,
				Path:     path,
				Line:     line,
				Column:   column,
				Severity: "error",
				Tool:     "mermaid",
				Code:     "parser",
				Message:  strings.TrimSpace(err.Error()),
			})
		}
	}
	return report
}

func mermaidErrorPosition(message string) (int, int) {
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, "line ") {
		return 0, 0
	}
	rest := strings.TrimPrefix(message, "line ")
	idx := strings.IndexByte(rest, ':')
	if idx <= 0 {
		return 0, 0
	}
	line, err := strconv.Atoi(strings.TrimSpace(rest[:idx]))
	if err != nil || line <= 0 {
		return 0, 0
	}
	return line, 1
}

func markdownCodeFences(content string) []markdownCodeFence {
	lines := strings.SplitAfter(content, "\n")
	var fences []markdownCodeFence
	var inFence bool
	var marker rune
	var markerLen int
	var language string
	var startLine int
	var body strings.Builder
	for i, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r\n")
		trimmed := strings.TrimLeft(line, " \t")
		if !inFence {
			nextMarker, nextLen, ok := openingFence(trimmed)
			if !ok {
				continue
			}
			inFence = true
			marker = nextMarker
			markerLen = nextLen
			language = fenceLanguage(strings.TrimSpace(trimmed[nextLen:]))
			startLine = i + 2
			body.Reset()
			continue
		}
		if closingFence(trimmed, marker, markerLen) {
			fences = append(fences, markdownCodeFence{Language: language, Content: strings.TrimRight(body.String(), "\n"), StartLine: startLine})
			inFence = false
			continue
		}
		body.WriteString(rawLine)
	}
	return fences
}

func openingFence(line string) (rune, int, bool) {
	if strings.HasPrefix(line, "```") {
		return '`', countPrefixRunes(line, '`'), true
	}
	if strings.HasPrefix(line, "~~~") {
		return '~', countPrefixRunes(line, '~'), true
	}
	return 0, 0, false
}

func closingFence(line string, marker rune, markerLen int) bool {
	if countPrefixRunes(line, marker) < markerLen {
		return false
	}
	rest := strings.TrimSpace(line[markerLen:])
	return rest == ""
}

func countPrefixRunes(line string, marker rune) int {
	var count int
	for _, ch := range line {
		if ch != marker {
			break
		}
		count++
	}
	return count
}

func fenceLanguage(info string) string {
	info = strings.TrimSpace(info)
	info = strings.Trim(info, "{}")
	for _, field := range strings.Fields(info) {
		field = strings.TrimPrefix(field, ".")
		if field != "" && !strings.Contains(field, "=") {
			return strings.ToLower(field)
		}
	}
	return ""
}

func isMermaidFence(language string) bool {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "mermaid", "mmd":
		return true
	default:
		return false
	}
}
