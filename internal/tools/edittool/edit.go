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

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Edit file",
		Description: "Edit an existing text file by replacing exact text.",
		Usage:       "Edit an existing text file by replacing exact text. Prefer this over Write when modifying an existing file. Use the smallest old_string that uniquely identifies the target, usually 2-4 adjacent lines. If old_string is not found or occurs multiple times, retry with more surrounding context or use replace_all when you intentionally want every occurrence changed. Do not rewrite the whole file just because one edit attempt failed.",
		Parameters:  `{"type":"object","properties":{"path":{"type":"string","description":"File to edit"},"old_string":{"type":"string","description":"Exact existing text to replace. Must match file contents exactly, including whitespace. Prefer the smallest unique snippet."},"new_string":{"type":"string","description":"Replacement text for old_string only, not the full file"},"replace_all":{"type":"boolean","description":"Replace every occurrence only when every match should change"}},"required":["path","old_string","new_string"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindEdit }
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
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	abs, rel, err := tools.WorkspacePath(runtime.Workdir, req.Args["path"])
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
	beforeBytes, err := os.ReadFile(abs)
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
	if err := tools.WriteTextFile(abs, after, info.Mode().Perm()); err != nil {
		return tools.Result{}, err
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
			"path":        rel,
			"replace_all": tools.BoolString(replaceAll),
			"occurrences": fmt.Sprintf("%d", occurrences),
			"matcher":     match.stage,
		},
		Stored: tools.EditStoredResult{
			Path:        rel,
			ReplaceAll:  replaceAll,
			Occurrences: occurrences,
			Summary:     summary,
			Diff:        buildUnifiedStoredDiff(rel, before, after),
			Hunks:       hunks,
			Truncated:   truncated,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "edit", result.Output
}

const maxStoredHunks = 8

func (m editMatcher) Resolve() (editMatch, error) {
	stages := []struct {
		name string
		run  func() []string
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
		candidates := uniqueStrings(stage.run())
		if len(candidates) == 0 {
			continue
		}
		ranges := rangesForCandidates(m.content, candidates)
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

func (m editMatcher) findExact() []string {
	if strings.Contains(m.content, m.oldString) {
		return []string{m.oldString}
	}
	return nil
}

func (m editMatcher) findLineEndingNormalized() []string {
	ending := detectLineEnding(m.content)
	candidate := convertLineEndings(normalizeLineEndings(m.oldString), ending)
	if candidate != m.oldString && strings.Contains(m.content, candidate) {
		return []string{candidate}
	}
	return nil
}

func (m editMatcher) findTrimmedBoundary() []string {
	trimmed := strings.TrimSpace(m.oldString)
	if trimmed == "" || trimmed == m.oldString {
		return nil
	}
	var out []string
	if strings.Contains(m.content, trimmed) {
		out = append(out, trimmed)
	}
	lines := strings.Split(m.content, "\n")
	findLines := strings.Split(m.oldString, "\n")
	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if strings.TrimSpace(block) == trimmed {
			out = append(out, block)
		}
	}
	return out
}

func (m editMatcher) findLineTrimmed() []string {
	originalLines := strings.Split(m.content, "\n")
	searchLines := trimTrailingEmptyLine(strings.Split(m.oldString, "\n"))
	if len(searchLines) == 0 || len(originalLines) < len(searchLines) {
		return nil
	}
	var out []string
	for i := 0; i <= len(originalLines)-len(searchLines); i++ {
		matches := true
		for j := range searchLines {
			if strings.TrimSpace(originalLines[i+j]) != strings.TrimSpace(searchLines[j]) {
				matches = false
				break
			}
		}
		if matches {
			out = append(out, strings.Join(originalLines[i:i+len(searchLines)], "\n"))
		}
	}
	return out
}

func (m editMatcher) findIndentationFlexible() []string {
	contentLines := strings.Split(m.content, "\n")
	findLines := strings.Split(m.oldString, "\n")
	if len(findLines) == 0 || len(contentLines) < len(findLines) {
		return nil
	}
	normalizedFind := removeCommonIndentation(m.oldString)
	var out []string
	for i := 0; i <= len(contentLines)-len(findLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if removeCommonIndentation(block) == normalizedFind {
			out = append(out, block)
		}
	}
	return out
}

func (m editMatcher) findWhitespaceNormalized() []string {
	normalize := func(text string) string {
		return strings.Join(strings.Fields(text), " ")
	}
	normalizedFind := normalize(m.oldString)
	if normalizedFind == "" {
		return nil
	}
	var out []string
	lines := strings.Split(m.content, "\n")
	for _, line := range lines {
		if normalize(line) == normalizedFind {
			out = append(out, line)
			continue
		}
	}
	findLines := strings.Split(m.oldString, "\n")
	if len(findLines) > 1 {
		for i := 0; i <= len(lines)-len(findLines); i++ {
			block := strings.Join(lines[i:i+len(findLines)], "\n")
			if normalize(block) == normalizedFind {
				out = append(out, block)
			}
		}
	}
	return out
}

func (m editMatcher) findUnicodeNormalized() []string {
	normalizedFind := normalizeLooseText(m.oldString)
	var out []string
	if strings.Contains(normalizeLooseText(m.content), normalizedFind) {
		lines := strings.Split(m.content, "\n")
		findLines := strings.Split(m.oldString, "\n")
		for i := 0; i <= len(lines)-len(findLines); i++ {
			block := strings.Join(lines[i:i+len(findLines)], "\n")
			if normalizeLooseText(block) == normalizedFind {
				out = append(out, block)
			}
		}
	}
	return out
}

func (m editMatcher) findEscapeNormalized() []string {
	unescaped := unescapeString(m.oldString)
	if unescaped == m.oldString {
		return nil
	}
	if strings.Contains(m.content, unescaped) {
		return []string{unescaped}
	}
	lines := strings.Split(m.content, "\n")
	findLines := strings.Split(unescaped, "\n")
	var out []string
	for i := 0; i <= len(lines)-len(findLines); i++ {
		block := strings.Join(lines[i:i+len(findLines)], "\n")
		if unescapeString(block) == unescaped {
			out = append(out, block)
		}
	}
	return out
}

func (m editMatcher) findBlockAnchor() []string {
	searchLines := trimTrailingEmptyLine(strings.Split(m.oldString, "\n"))
	originalLines := strings.Split(m.content, "\n")
	if len(searchLines) < 3 || len(originalLines) < len(searchLines) {
		return nil
	}
	first := strings.TrimSpace(searchLines[0])
	last := strings.TrimSpace(searchLines[len(searchLines)-1])
	var out []string
	for i := 0; i < len(originalLines); i++ {
		if strings.TrimSpace(originalLines[i]) != first {
			continue
		}
		for j := i + 2; j < len(originalLines); j++ {
			if strings.TrimSpace(originalLines[j]) != last {
				continue
			}
			block := originalLines[i : j+1]
			if blockSimilarity(block, searchLines) >= 0.5 {
				out = append(out, strings.Join(block, "\n"))
			}
			break
		}
	}
	return out
}

func (m editMatcher) findContextAware() []string {
	searchLines := trimTrailingEmptyLine(strings.Split(m.oldString, "\n"))
	contentLines := strings.Split(m.content, "\n")
	if len(searchLines) < 3 || len(contentLines) < len(searchLines) {
		return nil
	}
	first := strings.TrimSpace(searchLines[0])
	last := strings.TrimSpace(searchLines[len(searchLines)-1])
	var out []string
	for i := 0; i < len(contentLines); i++ {
		if strings.TrimSpace(contentLines[i]) != first {
			continue
		}
		for j := i + 2; j < len(contentLines); j++ {
			if strings.TrimSpace(contentLines[j]) != last {
				continue
			}
			block := contentLines[i : j+1]
			if len(block) == len(searchLines) && blockSimilarity(block, searchLines) >= 0.5 {
				out = append(out, strings.Join(block, "\n"))
				break
			}
			break
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
	total := 0
	matches := 0
	limit := min(len(actual), len(search)) - 1
	for i := 1; i < limit; i++ {
		actualLine := strings.TrimSpace(actual[i])
		searchLine := strings.TrimSpace(search[i])
		if actualLine == "" && searchLine == "" {
			continue
		}
		total++
		if actualLine == searchLine {
			matches++
		}
	}
	if total == 0 {
		return 1
	}
	return float64(matches) / float64(total)
}

func uniqueStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(input))
	for _, item := range input {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func rangesForCandidates(content string, candidates []string) []textRange {
	var out []textRange
	for _, candidate := range candidates {
		searchFrom := 0
		for {
			idx := strings.Index(content[searchFrom:], candidate)
			if idx < 0 {
				break
			}
			start := searchFrom + idx
			out = append(out, textRange{start: start, end: start + len(candidate)})
			searchFrom = start + len(candidate)
			if len(candidate) == 0 {
				break
			}
		}
	}
	return uniqueRanges(out)
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
	return float64(longestCommonSubsequenceLen(a, b)) / float64(max(len(a), len(b)))
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
