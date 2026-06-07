package edittool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

type editMatch struct {
	stage     string
	oldString string
	newString string
	ranges    []textRange
}

type editMatcher struct {
	content    string
	oldString  string
	newString  string
	replaceAll bool
}

type textRange struct {
	start int
	end   int
}

var (
	readFile      = os.ReadFile
	writeTextFile = tools.WriteTextFile
)

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Edit file",
		Description: "Edit an existing text file by replacing exact text.",
		Usage:       "Edit an existing text file by replacing exact text. Prefer this over file_write when modifying an existing file or adding sections to a larger file. Use the smallest old_string that uniquely identifies the target, usually 2-4 adjacent lines. Break large changes into smaller section edits instead of rewriting the whole file. If old_string is not found or occurs multiple times, retry with more surrounding context or use replace_all when you intentionally want every occurrence changed. Do not rewrite the whole file just because one edit attempt failed.",
		Parameters:  `{"type":"object","properties":{"path":{"type":"string","description":"File to edit"},"old_string":{"type":"string","description":"Exact existing text to replace. Must match file contents exactly, including whitespace. Prefer the smallest unique snippet."},"new_string":{"type":"string","description":"Replacement text for old_string only, not the full file"},"replace_all":{"type":"boolean","description":"Replace every occurrence only when every match should change"}},"required":["path","old_string","new_string"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() domain.ToolKind      { return domain.ToolKindFileEdit }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(tools.FirstArg(args, "path", "file", "file_path", "filepath"))
	oldString := tools.FirstArg(args, "old_string", "oldString", "oldText", "old")
	newString := tools.FirstArg(args, "new_string", "newString", "newText", "new")
	if path == "" {
		return nil, errors.New("path is empty")
	}
	if oldString == "" {
		return nil, errors.New("old_string is empty")
	}
	if oldString == newString {
		return nil, errors.New("old_string and new_string are identical")
	}
	out := map[string]string{
		"path":       path,
		"old_string": oldString,
		"new_string": newString,
	}
	if replaceAll := strings.TrimSpace(tools.FirstArg(args, "replace_all", "replaceAll")); replaceAll != "" {
		out["replace_all"] = replaceAll
	}
	return out, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["path"] }
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	abs, rel, err := tools.WritablePath(runtime, req.Args["path"])
	if err != nil {
		return tools.Result{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return tools.Result{}, err
	}
	if info.IsDir() {
		return tools.Result{}, fmt.Errorf("%s is a directory", rel)
	}
	beforeBytes, err := readFile(abs)
	if err != nil {
		return tools.Result{}, err
	}
	before := string(beforeBytes)
	replaceAll := strings.EqualFold(strings.TrimSpace(req.Args["replace_all"]), "true")
	match, err := editMatcher{
		content:    before,
		oldString:  req.Args["old_string"],
		newString:  req.Args["new_string"],
		replaceAll: replaceAll,
	}.Resolve()
	if err != nil {
		return tools.Result{}, fmt.Errorf("%w in %s", err, rel)
	}
	occurrences := len(match.ranges)
	after := replaceRanges(before, match.ranges, match.newString)
	if err := writeTextFile(abs, after, info.Mode().Perm()); err != nil {
		return tools.Result{}, err
	}
	verifyBytes, err := readFile(abs)
	if err != nil {
		return tools.Result{}, fmt.Errorf("post-write verification failed for %s: could not re-read file: %w", rel, err)
	}
	if string(verifyBytes) != after {
		return tools.Result{}, fmt.Errorf("post-write verification failed for %s: on-disk content differs from intended write (wrote %d bytes, read back %d bytes). The edit did not persist as intended; re-read the file and try again", rel, len(after), len(verifyBytes))
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(before, after, false)
	mode := "replaced 1 occurrence"
	if replaceAll {
		mode = fmt.Sprintf("replaced %d occurrences", occurrences)
	}
	summary := fmt.Sprintf("Edited %s (%s)", rel, mode)
	hunks, truncated := buildStoredHunksFromRanges(before, match.ranges, match.newString)
	return tools.Result{
		Output:   summary,
		DiffText: dmp.DiffPrettyText(diffs),
		Meta: map[string]string{
			"path":         rel,
			"replace_all":  tools.BoolString(replaceAll),
			"occurrences":  fmt.Sprintf("%d", occurrences),
			"matcher":      match.stage,
			"verification": "ok",
		},
		Stored: tools.EditStoredResult{
			Path:         rel,
			ReplaceAll:   replaceAll,
			Occurrences:  occurrences,
			Summary:      summary,
			Matcher:      match.stage,
			Verification: "ok",
			Diff:         buildUnifiedStoredDiff(rel, before, after),
			Hunks:        hunks,
			Truncated:    truncated,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "file_edit", result.Output
}

const maxStoredHunks = 8

func (m editMatcher) Resolve() (editMatch, error) {
	stages := []struct {
		name string
		run  func() []textRange
	}{
		{name: "exact", run: m.findExact},
		{name: "line_endings", run: m.findLineEndingNormalized},
		{name: "line_trimmed", run: m.findLineTrimmed},
		{name: "whitespace_normalized", run: m.findWhitespaceNormalized},
		{name: "indentation_flexible", run: m.findIndentationFlexible},
		{name: "escape_normalized", run: m.findEscapeNormalized},
		{name: "trimmed_boundary", run: m.findTrimmedBoundary},
		{name: "unicode_normalized", run: m.findUnicodeNormalized},
		{name: "block_anchor", run: m.findBlockAnchor},
		{name: "context_aware", run: m.findContextAware},
	}

	for _, stage := range stages {
		ranges := uniqueRanges(stage.run())
		if len(ranges) == 0 {
			continue
		}
		if !m.replaceAll && len(ranges) != 1 {
			return editMatch{}, fmt.Errorf("target text matched %d regions via %s; provide more surrounding context or set replace_all=true", len(ranges), stage.name)
		}
		newString := m.rewriteNewString(stage.name)
		if stage.name != "exact" {
			if err := detectEscapedQuoteDrift(m.oldString, newString, matchedText(m.content, ranges)); err != nil {
				return editMatch{}, err
			}
		}
		return editMatch{
			stage:     stage.name,
			oldString: m.content[ranges[0].start:ranges[0].end],
			newString: newString,
			ranges:    ranges,
		}, nil
	}
	return editMatch{}, fmt.Errorf("target text not found\n\n%s", formatClosestSections(m.content, m.oldString))
}

func (m editMatcher) rewriteNewString(stage string) string {
	switch stage {
	case "line_endings":
		return convertLineEndings(normalizeLineEndings(m.newString), detectLineEnding(m.content))
	default:
		return m.newString
	}
}

func (m editMatcher) findExact() []textRange {
	return rangesForCandidate(m.content, m.oldString)
}

func (m editMatcher) findLineEndingNormalized() []textRange {
	ending := detectLineEnding(m.content)
	candidate := convertLineEndings(normalizeLineEndings(m.oldString), ending)
	if candidate != m.oldString {
		return rangesForCandidate(m.content, candidate)
	}
	return nil
}

func (m editMatcher) findTrimmedBoundary() []textRange {
	trimmed := strings.TrimSpace(m.oldString)
	if trimmed == "" || trimmed == m.oldString {
		return nil
	}
	var out []textRange
	if strings.Contains(m.content, trimmed) {
		out = append(out, rangesForCandidate(m.content, trimmed)...)
	}
	lines, spans := splitLinesWithSpans(m.content)
	findLines := strings.Split(m.oldString, "\n")
	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if strings.TrimSpace(block) == trimmed {
			out = append(out, textRange{start: spans[i].start, end: spans[i+len(findLines)-1].end})
		}
	}
	return out
}

func (m editMatcher) findLineTrimmed() []textRange {
	originalLines, spans := splitLinesWithSpans(m.content)
	searchLines := trimTrailingEmptyLine(strings.Split(m.oldString, "\n"))
	if len(searchLines) == 0 || len(originalLines) < len(searchLines) {
		return nil
	}
	var out []textRange
	for i := 0; i <= len(originalLines)-len(searchLines); i++ {
		matches := true
		for j := range searchLines {
			if strings.TrimSpace(originalLines[i+j]) != strings.TrimSpace(searchLines[j]) {
				matches = false
				break
			}
		}
		if matches {
			out = append(out, textRange{start: spans[i].start, end: spans[i+len(searchLines)-1].end})
		}
	}
	return out
}

func (m editMatcher) findIndentationFlexible() []textRange {
	contentLines, spans := splitLinesWithSpans(m.content)
	findLines := strings.Split(m.oldString, "\n")
	if len(findLines) == 0 || len(contentLines) < len(findLines) {
		return nil
	}
	normalizedFind := removeCommonIndentation(m.oldString)
	var out []textRange
	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if removeCommonIndentation(block) == normalizedFind {
			out = append(out, textRange{start: spans[i].start, end: spans[i+len(findLines)-1].end})
		}
	}
	return out
}

func (m editMatcher) findWhitespaceNormalized() []textRange {
	normalizedContent := normalizeWhitespaceWithMap(m.content)
	normalizedFind := normalizeWhitespacePlain(m.oldString)
	if normalizedFind == "" || normalizedContent.text == m.content && normalizedFind == m.oldString {
		return nil
	}
	return mapNormalizedMatches(normalizedContent, rangesForCandidate(normalizedContent.text, normalizedFind))
}

func (m editMatcher) findUnicodeNormalized() []textRange {
	normalizedContent := normalizeUnicodeWithMap(m.content)
	normalizedFind := normalizeLooseText(m.oldString)
	if normalizedContent.text == m.content && normalizedFind == m.oldString {
		return nil
	}
	ranges := rangesForCandidate(normalizedContent.text, normalizedFind)
	if len(ranges) == 0 {
		normalizedLines, normalizedSpans := splitLinesWithSpans(normalizedContent.text)
		originalLines := strings.Split(m.oldString, "\n")
		trimmedFind := strings.Join(trimLines(originalLines), "\n")
		var normalizedLineRanges []textRange
		for i := 0; i <= len(normalizedLines)-len(originalLines); i++ {
			if strings.Join(trimLines(normalizedLines[i:i+len(originalLines)]), "\n") == trimmedFind {
				normalizedLineRanges = append(normalizedLineRanges, textRange{start: normalizedSpans[i].start, end: normalizedSpans[i+len(originalLines)-1].end})
			}
		}
		ranges = normalizedLineRanges
	}
	return mapNormalizedMatches(normalizedContent, ranges)
}

func (m editMatcher) findEscapeNormalized() []textRange {
	unescaped := unescapeString(m.oldString)
	if unescaped == m.oldString {
		return nil
	}
	return rangesForCandidate(m.content, unescaped)
}

func (m editMatcher) findBlockAnchor() []textRange {
	searchLines := trimTrailingEmptyLine(strings.Split(m.oldString, "\n"))
	originalLines, spans := splitLinesWithSpans(m.content)
	if len(searchLines) < 3 || len(originalLines) < len(searchLines) {
		return nil
	}
	searchLines = trimLines(searchLines)
	first := normalizeLooseText(searchLines[0])
	last := normalizeLooseText(searchLines[len(searchLines)-1])
	var candidates []int
	for i := 0; i <= len(originalLines)-len(searchLines); i++ {
		if normalizeLooseText(strings.TrimSpace(originalLines[i])) == first &&
			normalizeLooseText(strings.TrimSpace(originalLines[i+len(searchLines)-1])) == last {
			candidates = append(candidates, i)
		}
	}
	threshold := 0.50
	if len(candidates) > 1 {
		threshold = 0.70
	}
	var out []textRange
	for _, i := range candidates {
		block := trimLines(originalLines[i : i+len(searchLines)])
		if blockSimilarity(block, searchLines) < threshold {
			continue
		}
		out = append(out, textRange{start: spans[i].start, end: spans[i+len(searchLines)-1].end})
	}
	return out
}

func (m editMatcher) findContextAware() []textRange {
	searchLines := trimTrailingEmptyLine(strings.Split(m.oldString, "\n"))
	contentLines, spans := splitLinesWithSpans(m.content)
	if len(searchLines) < 3 || len(contentLines) < len(searchLines) {
		return nil
	}
	searchLines = trimLines(searchLines)
	var out []textRange
	for i := 0; i <= len(contentLines)-len(searchLines); i++ {
		block := trimLines(contentLines[i : i+len(searchLines)])
		matches := 0
		for j := range searchLines {
			if lineSimilarity(searchLines[j], block[j]) >= 0.80 {
				matches++
			}
		}
		if float64(matches) >= float64(len(searchLines))*0.5 {
			out = append(out, textRange{start: spans[i].start, end: spans[i+len(searchLines)-1].end})
		}
	}
	return out
}

func normalizeLineEndings(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	return strings.ReplaceAll(input, "\r", "\n")
}

func detectLineEnding(input string) string {
	if strings.Contains(input, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func convertLineEndings(input, ending string) string {
	if ending == "\n" {
		return input
	}
	return strings.ReplaceAll(input, "\n", ending)
}

func normalizeLooseText(input string) string {
	replacer := strings.NewReplacer(
		"\u00a0", " ",
		"‘", "'", "’", "'",
		"“", `"`, "”", `"`,
		"—", "--", "–", "-",
		"…", "...",
	)
	return replacer.Replace(input)
}

type normalizedText struct {
	text  string
	start []int
	end   []int
}

func normalizeUnicodeWithMap(input string) normalizedText {
	return normalizeWithMap(input, func(r rune) string {
		switch r {
		case '\u00a0':
			return " "
		case '‘', '’':
			return "'"
		case '“', '”':
			return `"`
		case '—':
			return "--"
		case '–':
			return "-"
		case '…':
			return "..."
		default:
			return string(r)
		}
	})
}

func normalizeWhitespaceWithMap(input string) normalizedText {
	var out strings.Builder
	start := []int{0}
	end := []int{0}
	inWhitespace := false
	whitespaceStart := 0
	for pos, r := range input {
		size := len(string(r))
		if r == ' ' || r == '\t' {
			if !inWhitespace {
				whitespaceStart = pos
				out.WriteByte(' ')
				start = append(start, whitespaceStart)
				end = append(end, pos+size)
				inWhitespace = true
			} else {
				end[len(end)-1] = pos + size
			}
			continue
		}
		inWhitespace = false
		repl := string(r)
		for i := range []byte(repl) {
			out.WriteByte(repl[i])
			start = append(start, pos+i)
			end = append(end, pos+i+1)
		}
	}
	return normalizedText{text: out.String(), start: start, end: end}
}

func normalizeWhitespacePlain(input string) string {
	return normalizeWhitespaceWithMap(input).text
}

func normalizeWithMap(input string, replace func(rune) string) normalizedText {
	var out strings.Builder
	start := []int{0}
	end := []int{0}
	for pos, r := range input {
		original := string(r)
		origEnd := pos + len(original)
		repl := replace(r)
		for i := range []byte(repl) {
			out.WriteByte(repl[i])
			start = append(start, pos)
			end = append(end, origEnd)
		}
	}
	return normalizedText{text: out.String(), start: start, end: end}
}

func mapNormalizedMatches(normalized normalizedText, ranges []textRange) []textRange {
	if len(ranges) == 0 {
		return nil
	}
	out := make([]textRange, 0, len(ranges))
	for _, r := range ranges {
		if r.start < 0 || r.end > len(normalized.text) || r.start+1 >= len(normalized.start) || r.end >= len(normalized.end) || r.end <= r.start {
			continue
		}
		out = append(out, textRange{start: normalized.start[r.start+1], end: normalized.end[r.end]})
	}
	return uniqueRanges(out)
}

func removeCommonIndentation(input string) string {
	lines := strings.Split(input, "\n")
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return input
	}
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}
	return strings.Join(lines, "\n")
}

func unescapeString(input string) string {
	replacer := strings.NewReplacer("\\n", "\n", "\\t", "\t", "\\r", "\r", "\\'", "'", "\\\"", `"`, "\\`", "`", "\\\\", "\\", "\\$", "$")
	return replacer.Replace(input)
}

func trimTrailingEmptyLine(lines []string) []string {
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	return lines
}

func blockSimilarity(actual, search []string) float64 {
	if len(actual) < 2 || len(search) < 2 {
		return 0
	}
	if len(actual) <= 2 || len(search) <= 2 {
		return 1
	}
	return sequenceSimilarity(strings.Join(actual[1:len(actual)-1], "\n"), strings.Join(search[1:len(search)-1], "\n"))
}

func rangesForCandidate(content string, candidate string) []textRange {
	if candidate == "" {
		return nil
	}
	var out []textRange
	searchFrom := 0
	for {
		idx := strings.Index(content[searchFrom:], candidate)
		if idx < 0 {
			break
		}
		start := searchFrom + idx
		out = append(out, textRange{start: start, end: start + len(candidate)})
		searchFrom = start + 1
	}
	return uniqueRanges(out)
}

func splitLinesWithSpans(content string) ([]string, []textRange) {
	raw := strings.SplitAfter(content, "\n")
	lines := make([]string, 0, len(raw))
	spans := make([]textRange, 0, len(raw))
	pos := 0
	for _, item := range raw {
		line := strings.TrimSuffix(item, "\n")
		lines = append(lines, line)
		spans = append(spans, textRange{start: pos, end: pos + len(line)})
		pos += len(item)
	}
	if len(raw) == 0 {
		return []string{""}, []textRange{{start: 0, end: 0}}
	}
	if strings.HasSuffix(content, "\n") {
		lines = append(lines, "")
		spans = append(spans, textRange{start: len(content), end: len(content)})
	}
	return lines, spans
}

func trimLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimSpace(line)
	}
	return out
}

func uniqueRanges(input []textRange) []textRange {
	if len(input) == 0 {
		return nil
	}
	sort.Slice(input, func(i, j int) bool {
		if input[i].start == input[j].start {
			return input[i].end < input[j].end
		}
		return input[i].start < input[j].start
	})
	out := input[:0]
	var prev *textRange
	for _, r := range input {
		if r.start < 0 || r.end <= r.start {
			continue
		}
		if prev != nil && prev.start == r.start && prev.end == r.end {
			continue
		}
		out = append(out, r)
		prev = &out[len(out)-1]
	}
	return out
}

func replaceRanges(content string, ranges []textRange, replacement string) string {
	if len(ranges) == 0 {
		return content
	}
	ranges = append([]textRange(nil), ranges...)
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start > ranges[j].start })
	out := content
	for _, r := range ranges {
		out = out[:r.start] + replacement + out[r.end:]
	}
	return out
}

func matchedText(content string, ranges []textRange) []string {
	out := make([]string, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, content[r.start:r.end])
	}
	return out
}

func detectEscapedQuoteDrift(oldString, newString string, matched []string) error {
	for _, suspect := range []string{`\'`, `\"`} {
		if !strings.Contains(oldString, suspect) && !strings.Contains(newString, suspect) {
			continue
		}
		for _, actual := range matched {
			if strings.Contains(actual, suspect) {
				return nil
			}
		}
		return fmt.Errorf("target text only matched after unescaping %s; re-read the file and pass the actual unescaped text instead of escaped quotes", suspect)
	}
	return nil
}

func formatClosestSections(content, search string) string {
	sections := closestSections(content, search)
	if len(sections) == 0 {
		return "Re-read the file and retry with exact current text, including tabs, spaces, and line endings."
	}
	var out []string
	out = append(out, "Did you mean one of these sections?")
	for i, section := range sections {
		if i > 0 {
			out = append(out, "---")
		}
		out = append(out, section...)
	}
	out = append(out, "Re-read the file and retry with one exact snippet above; check tabs/spaces and stale file contents.")
	return strings.Join(out, "\n")
}

func closestSections(content, search string) [][]string {
	lines := strings.Split(content, "\n")
	anchor := firstMeaningfulLine(search)
	if anchor == "" {
		return nil
	}
	type scoredLine struct {
		index int
		score float64
	}
	var scored []scoredLine
	for i, line := range lines {
		score := lineSimilarity(anchor, strings.TrimSpace(line))
		if score >= 0.35 {
			scored = append(scored, scoredLine{index: i, score: score})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].index < scored[j].index
		}
		return scored[i].score > scored[j].score
	})
	searchLineCount := max(1, len(trimTrailingEmptyLine(strings.Split(search, "\n"))))
	var sections [][]string
	used := map[int]struct{}{}
	for _, hit := range scored {
		if len(sections) >= 3 {
			break
		}
		start := max(0, hit.index-2)
		end := min(len(lines), hit.index+searchLineCount+2)
		if _, ok := used[start]; ok {
			continue
		}
		used[start] = struct{}{}
		var section []string
		for i := start; i < end; i++ {
			section = append(section, fmt.Sprintf("%4d| %s", i+1, lines[i]))
		}
		sections = append(sections, section)
	}
	return sections
}

func firstMeaningfulLine(input string) string {
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return normalizeLooseText(line)
		}
	}
	return ""
}

func lineSimilarity(a, b string) float64 {
	a = strings.Join(strings.Fields(normalizeLooseText(a)), " ")
	b = strings.Join(strings.Fields(normalizeLooseText(b)), " ")
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return float64(min(len(a), len(b))) / float64(max(len(a), len(b)))
	}
	return sequenceSimilarity(a, b)
}

func sequenceSimilarity(a, b string) float64 {
	if a == "" && b == "" {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	return float64(2*longestCommonSubsequenceLen(a, b)) / float64(len(a)+len(b))
}

func longestCommonSubsequenceLen(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			if a[i] == b[j] {
				cur[j+1] = prev[j] + 1
				continue
			}
			cur[j+1] = max(cur[j], prev[j+1])
		}
		prev, cur = cur, prev
		clear(cur)
	}
	return prev[len(b)]
}

func buildStoredHunksFromRanges(before string, ranges []textRange, newString string) ([]tools.EditStoredHunk, bool) {
	if len(ranges) == 0 {
		return nil, false
	}
	var hunks []tools.EditStoredHunk
	for _, r := range ranges {
		oldString := before[r.start:r.end]
		oldStart := 1 + strings.Count(before[:r.start], "\n")
		hunks = append(hunks, tools.EditStoredHunk{
			OldStart: oldStart,
			NewStart: oldStart,
			OldLines: splitStoredLines(oldString),
			NewLines: splitStoredLines(newString),
		})
		if len(hunks) >= maxStoredHunks {
			return hunks, true
		}
	}
	return hunks, false
}

func splitStoredLines(input string) []string {
	lines := strings.Split(strings.TrimSuffix(input, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{""}
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.ReplaceAll(line, "\t", "    "))
	}
	return out
}

func buildUnifiedStoredDiff(path, before, after string) string {
	dmp := diffmatchpatch.New()
	chars1, chars2, lineArray := dmp.DiffLinesToChars(before, after)
	diffs := dmp.DiffMain(chars1, chars2, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)

	const contextLines = 2
	type hunk struct {
		oldStart int
		newStart int
		oldCount int
		newCount int
		lines    []string
	}

	oldLine := 1
	newLine := 1
	prefix := []string{}
	var current *hunk
	var hunks []hunk

	startHunk := func() {
		current = &hunk{
			oldStart: max(1, oldLine-len(prefix)),
			newStart: max(1, newLine-len(prefix)),
			oldCount: len(prefix),
			newCount: len(prefix),
			lines:    prefixedLines(prefix, " "),
		}
	}
	flushHunk := func() {
		if current == nil {
			return
		}
		hunks = append(hunks, *current)
		current = nil
	}
	addEqualToHunk := func(lines []string) {
		for _, line := range lines {
			current.lines = append(current.lines, " "+line)
			current.oldCount++
			current.newCount++
			oldLine++
			newLine++
		}
	}

	for _, diff := range diffs {
		lines := splitDiffLines(diff.Text)
		switch diff.Type {
		case diffmatchpatch.DiffEqual:
			if current == nil {
				prefix = append(prefix, lines...)
				if len(prefix) > contextLines {
					prefix = prefix[len(prefix)-contextLines:]
				}
				oldLine += len(lines)
				newLine += len(lines)
				continue
			}
			if len(lines) <= contextLines {
				addEqualToHunk(lines)
				continue
			}
			addEqualToHunk(lines[:contextLines])
			oldLine += len(lines) - contextLines
			newLine += len(lines) - contextLines
			prefix = append([]string(nil), lines[len(lines)-contextLines:]...)
			flushHunk()
		case diffmatchpatch.DiffDelete:
			if current == nil {
				startHunk()
			}
			prefix = nil
			for _, line := range lines {
				current.lines = append(current.lines, "-"+line)
				current.oldCount++
				oldLine++
			}
		case diffmatchpatch.DiffInsert:
			if current == nil {
				startHunk()
			}
			prefix = nil
			for _, line := range lines {
				current.lines = append(current.lines, "+"+line)
				current.newCount++
				newLine++
			}
		}
	}
	flushHunk()
	if len(hunks) == 0 {
		return ""
	}

	var lines []string
	path = strings.TrimSpace(path)
	if path != "" {
		lines = append(lines, "--- "+path, "+++ "+path)
	}
	for _, hunk := range hunks {
		lines = append(lines, fmt.Sprintf("@@ -%d,%d +%d,%d @@", hunk.oldStart, hunk.oldCount, hunk.newStart, hunk.newCount))
		lines = append(lines, hunk.lines...)
	}
	return strings.Join(lines, "\n")
}

func splitDiffLines(input string) []string {
	if input == "" {
		return nil
	}
	raw := strings.SplitAfter(input, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		if line == "" {
			continue
		}
		lines = append(lines, strings.TrimSuffix(line, "\n"))
	}
	return lines
}

func prefixedLines(lines []string, prefix string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, prefix+line)
	}
	return out
}
