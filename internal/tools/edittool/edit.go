package edittool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

type editMatch struct {
	stage     string
	oldString string
	newString string
}

type editMatcher struct {
	content    string
	oldString  string
	newString  string
	replaceAll bool
	level      int
}

func init() {
	tools.Register(tool{}, tools.ToolInfo{
		Title:       "Edit file",
		Description: "Edit an existing text file by replacing exact text.",
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindEdit }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindEdit, "Edit an existing text file by replacing exact text. Prefer this over Write when modifying an existing file. Use the smallest old_string that uniquely identifies the target, usually 2-4 adjacent lines. If old_string is not found or occurs multiple times, retry with more surrounding context or use replace_all when you intentionally want every occurrence changed. Do not rewrite the whole file just because one edit attempt failed.", `{"type":"object","properties":{"path":{"type":"string","description":"File to edit"},"old_string":{"type":"string","description":"Exact existing text to replace. Must match file contents exactly, including whitespace. Prefer the smallest unique snippet."},"new_string":{"type":"string","description":"Replacement text for old_string only, not the full file"},"replace_all":{"type":"boolean","description":"Replace every occurrence only when every match should change"}},"required":["path","old_string","new_string"],"additionalProperties":false}`), true
}
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
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"path": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["path"] }
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
		level:      reqLevel(runtime.EditForgiveness),
	}.Resolve()
	if err != nil {
		return tools.Result{}, fmt.Errorf("%w in %s", err, rel)
	}
	occurrences := strings.Count(before, match.oldString)
	var after string
	if replaceAll {
		after = strings.ReplaceAll(before, match.oldString, match.newString)
	} else {
		after = strings.Replace(before, match.oldString, match.newString, 1)
	}
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
	hunks, truncated := buildStoredHunks(before, match.oldString, match.newString, replaceAll)
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

func reqLevel(level int) int {
	if level < 1 {
		return 1
	}
	if level > 5 {
		return 5
	}
	return level
}

func (m editMatcher) Resolve() (editMatch, error) {
	stages := []struct {
		name string
		run  func() []string
	}{
		{name: "exact", run: m.findExact},
	}
	if m.level >= 2 {
		stages = append(stages, struct {
			name string
			run  func() []string
		}{name: "line_endings", run: m.findLineEndingNormalized})
	}
	if m.level >= 3 {
		stages = append(stages,
			struct {
				name string
				run  func() []string
			}{name: "quotes", run: m.findQuoteNormalized},
			struct {
				name string
				run  func() []string
			}{name: "trimmed_boundary", run: m.findTrimmedBoundary},
		)
	}
	if m.level >= 4 {
		stages = append(stages,
			struct {
				name string
				run  func() []string
			}{name: "line_trimmed", run: m.findLineTrimmed},
			struct {
				name string
				run  func() []string
			}{name: "indentation_flexible", run: m.findIndentationFlexible},
			struct {
				name string
				run  func() []string
			}{name: "whitespace_normalized", run: m.findWhitespaceNormalized},
		)
	}
	if m.level >= 5 {
		stages = append(stages,
			struct {
				name string
				run  func() []string
			}{name: "escape_normalized", run: m.findEscapeNormalized},
			struct {
				name string
				run  func() []string
			}{name: "block_anchor", run: m.findBlockAnchor},
			struct {
				name string
				run  func() []string
			}{name: "context_aware", run: m.findContextAware},
		)
	}

	for _, stage := range stages {
		candidates := uniqueStrings(stage.run())
		if len(candidates) == 0 {
			continue
		}
		if len(candidates) > 1 {
			return editMatch{}, fmt.Errorf("target text matched multiple regions via %s; provide more surrounding context", stage.name)
		}
		candidate := candidates[0]
		occurrences := strings.Count(m.content, candidate)
		if !m.replaceAll && occurrences != 1 {
			return editMatch{}, fmt.Errorf("target text occurs %d times; use replace_all to replace every occurrence", occurrences)
		}
		return editMatch{
			stage:     stage.name,
			oldString: candidate,
			newString: m.rewriteNewString(stage.name),
		}, nil
	}
	return editMatch{}, errors.New("target text not found")
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

func (m editMatcher) findQuoteNormalized() []string {
	actual := findQuoteNormalizedString(m.content, m.oldString)
	if actual == "" {
		return nil
	}
	return []string{actual}
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
		if normalize(line) != "" && strings.Contains(normalize(line), normalizedFind) {
			reWords := regexp.QuoteMeta(strings.TrimSpace(m.oldString))
			reWords = strings.Join(strings.Fields(reWords), `\s+`)
			re := regexp.MustCompile(reWords)
			if match := re.FindString(line); match != "" {
				out = append(out, match)
			}
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

func normalizeQuotes(input string) string {
	replacer := strings.NewReplacer("‘", "'", "’", "'", "“", `"`, "”", `"`)
	return replacer.Replace(input)
}

func findQuoteNormalizedString(content, search string) string {
	if strings.Contains(content, search) {
		return search
	}
	normalizedContent := normalizeQuotes(content)
	normalizedSearch := normalizeQuotes(search)
	idx := strings.Index(normalizedContent, normalizedSearch)
	if idx < 0 {
		return ""
	}
	return content[idx : idx+len(search)]
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

func buildStoredHunks(before, oldString, newString string, replaceAll bool) ([]tools.EditStoredHunk, bool) {
	if strings.TrimSpace(oldString) == "" {
		return nil, false
	}
	oldLines := splitStoredLines(oldString)
	newLines := splitStoredLines(newString)
	var hunks []tools.EditStoredHunk
	searchFrom := 0
	for {
		idx := strings.Index(before[searchFrom:], oldString)
		if idx < 0 {
			break
		}
		abs := searchFrom + idx
		oldStart := 1 + strings.Count(before[:abs], "\n")
		newStart := oldStart
		hunks = append(hunks, tools.EditStoredHunk{
			OldStart: oldStart,
			NewStart: newStart,
			OldLines: oldLines,
			NewLines: newLines,
		})
		if len(hunks) >= maxStoredHunks {
			return hunks, true
		}
		searchFrom = abs + len(oldString)
		if !replaceAll {
			break
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
