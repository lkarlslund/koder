package greptool

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

type outputMode string

const (
	outputModeContent          outputMode = "content"
	outputModeFilesWithMatches outputMode = "files_with_matches"
	outputModeCount            outputMode = "count"
)

type searchOptions struct {
	Pattern    string
	Path       string
	Include    string
	Type       string
	OutputMode outputMode
	IgnoreCase bool
	HeadLimit  int
}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Search text",
		Description: "Search workspace file contents with regular expressions.",
		Usage:       "Search workspace file contents with ripgrep-compatible regular expressions. ALWAYS prefer this tool over running rg or grep through exec_command for code and text search, because this tool is permission-aware and returns structured search results. Use it to discover which files contain a pattern. Use file_glob to find files by name, and file_read to inspect a file once you know which one to open. Supports full regex syntax, file filtering with include globs or type names, output modes for matching lines, matching file paths, or per-file match counts, and optional case-insensitive search. If you need a broad exploratory search that will likely take several rounds, use the task tool instead.",
		Parameters:  `{"type":"object","properties":{"pattern":{"type":"string","description":"Regular expression to search for in file contents. Uses ripgrep-compatible regex syntax."},"path":{"type":"string","description":"Optional workspace file or directory to search from. Defaults to the workspace root."},"include":{"type":"string","description":"Optional glob to filter searched files, for example \"*.go\" or \"**/*.{ts,tsx}\"."},"type":{"type":"string","description":"Optional ripgrep file type filter, for example \"go\", \"py\", or \"rust\"."},"output_mode":{"type":"string","enum":["content","files_with_matches","count"],"description":"How to return matches. \"content\" shows matching lines with file paths and line numbers. \"files_with_matches\" lists only matching file paths. \"count\" shows per-file matched line counts."},"ignore_case":{"type":"boolean","description":"Whether to search case-insensitively."},"head_limit":{"type":"integer","description":"Optional maximum number of output lines or entries to return."}},"required":["pattern"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return domain.ToolKindFileGrep }
func (tool) BypassesPermission() bool { return false }

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	pattern := strings.TrimSpace(tools.FirstArg(args, "pattern", "query", "search"))
	if pattern == "" {
		return nil, errors.New("pattern is empty")
	}
	out := map[string]string{"pattern": pattern}
	if root := tools.NormalizePathInput(tools.FirstArg(args, "path", "root", "dir")); root != "" {
		out["path"] = root
	}
	if include := strings.TrimSpace(tools.FirstArg(args, "include", "glob")); include != "" {
		out["include"] = include
	}
	if kind := strings.TrimSpace(tools.FirstArg(args, "type", "file_type")); kind != "" {
		out["type"] = kind
	}
	if rawMode := strings.TrimSpace(tools.FirstArg(args, "output_mode", "mode")); rawMode != "" {
		mode := outputMode(rawMode)
		if !validOutputMode(mode) {
			return nil, errors.New("output_mode must be one of: content, files_with_matches, count")
		}
		out["output_mode"] = rawMode
	}
	if rawIgnoreCase := strings.TrimSpace(tools.FirstArg(args, "ignore_case", "case_insensitive", "i")); rawIgnoreCase != "" {
		ignoreCase, err := parseBool(rawIgnoreCase)
		if err != nil {
			return nil, errors.New("ignore_case must be true or false")
		}
		out["ignore_case"] = strconv.FormatBool(ignoreCase)
	}
	if rawLimit := strings.TrimSpace(tools.FirstArg(args, "head_limit", "limit")); rawLimit != "" {
		limit, err := tools.ParseFlexibleInt(rawLimit)
		if err != nil || limit <= 0 {
			return nil, errors.New("head_limit must be a positive integer")
		}
		out["head_limit"] = strconv.Itoa(limit)
	}
	return out, nil
}

func (tool) Preview(req tools.Request) string { return req.Args["pattern"] }

func (tool) Presentation(req tools.Request) tools.Presentation {
	pattern := strings.TrimSpace(req.Args["pattern"])
	subtitle := pattern
	if scope := grepScopeLabel(req.Args["path"], req.Args["include"]); scope != "" {
		if subtitle == "" {
			subtitle = scope
		} else {
			subtitle += " in " + scope
		}
	}
	return tools.Presentation{Title: "Search text", Subtitle: subtitle, Preview: pattern}
}

func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	options, err := optionsFromRequest(req)
	if err != nil {
		return tools.Result{}, err
	}
	rootAbs, rootLabel, searchTarget, singleFile, err := grepScope(runtime.Workdir, options.Path)
	if err != nil {
		return tools.Result{}, err
	}

	var (
		output    string
		searchErr error
	)
	if _, err := exec.LookPath("rg"); err == nil {
		output, searchErr = runRipgrep(ctx, rootAbs, searchTarget, options)
	} else {
		output, searchErr = runFallbackSearch(rootAbs, searchTarget, singleFile, options)
	}
	if searchErr != nil {
		return tools.Result{}, searchErr
	}
	if strings.TrimSpace(output) == "" {
		return grepNoMatchesResult(rootLabel, options), nil
	}

	lines := splitNonEmptyLines(output)
	lines = normalizeOutputLines(lines)
	lines, limited := applyHeadLimit(lines, options.HeadLimit)
	if len(lines) == 0 {
		return grepNoMatchesResult(rootLabel, options), nil
	}
	if limited {
		lines = append(lines, fmt.Sprintf("... truncated to first %d results ...", options.HeadLimit))
	}
	text, truncated := tools.TruncateText(strings.Join(lines, "\n"), tools.DefaultToolOutputLimit)
	truncated = truncated || limited
	return tools.Result{
		Output: text,
		Meta: map[string]string{
			"pattern":     options.Pattern,
			"include":     options.Include,
			"type":        options.Type,
			"output_mode": string(options.OutputMode),
			"ignore_case": strconv.FormatBool(options.IgnoreCase),
			"base_path":   rootLabel,
			"truncated":   tools.BoolString(truncated),
		},
		Stored: tools.GrepStoredResult{
			Pattern:   options.Pattern,
			BasePath:  rootLabel,
			Include:   options.Include,
			Output:    text,
			Truncated: truncated,
		},
	}, nil
}

func optionsFromRequest(req tools.Request) (searchOptions, error) {
	options := searchOptions{
		Pattern:    strings.TrimSpace(req.Args["pattern"]),
		Path:       strings.TrimSpace(req.Args["path"]),
		Include:    strings.TrimSpace(req.Args["include"]),
		Type:       strings.TrimSpace(req.Args["type"]),
		OutputMode: outputMode(strings.TrimSpace(req.Args["output_mode"])),
	}
	if options.Pattern == "" {
		return searchOptions{}, errors.New("pattern is empty")
	}
	if options.OutputMode == "" {
		options.OutputMode = outputModeContent
	}
	if !validOutputMode(options.OutputMode) {
		return searchOptions{}, errors.New("output_mode must be one of: content, files_with_matches, count")
	}
	if rawIgnoreCase := strings.TrimSpace(req.Args["ignore_case"]); rawIgnoreCase != "" {
		ignoreCase, err := parseBool(rawIgnoreCase)
		if err != nil {
			return searchOptions{}, errors.New("ignore_case must be true or false")
		}
		options.IgnoreCase = ignoreCase
	}
	if rawLimit := strings.TrimSpace(req.Args["head_limit"]); rawLimit != "" {
		limit, err := tools.ParseFlexibleInt(rawLimit)
		if err != nil || limit <= 0 {
			return searchOptions{}, errors.New("head_limit must be a positive integer")
		}
		options.HeadLimit = limit
	}
	return options, nil
}

func validOutputMode(mode outputMode) bool {
	switch mode {
	case outputModeContent, outputModeFilesWithMatches, outputModeCount:
		return true
	default:
		return false
	}
}

func parseBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, errors.New("invalid boolean")
	}
}

func grepNoMatchesResult(rootLabel string, options searchOptions) tools.Result {
	return tools.Result{
		Output: "No matches found",
		Meta: map[string]string{
			"pattern":     options.Pattern,
			"include":     options.Include,
			"type":        options.Type,
			"output_mode": string(options.OutputMode),
			"ignore_case": strconv.FormatBool(options.IgnoreCase),
			"base_path":   rootLabel,
			"matches":     "0",
		},
		Stored: tools.GrepStoredResult{
			Pattern:  options.Pattern,
			BasePath: rootLabel,
			Include:  options.Include,
			Output:   "No matches found",
		},
	}
}

func grepScopeLabel(path, include string) string {
	path = strings.TrimSpace(path)
	include = strings.TrimSpace(include)
	switch {
	case path != "" && include != "":
		return path + " (" + include + ")"
	case path != "":
		return path
	default:
		return include
	}
}

func grepScope(workdir string, raw string) (rootAbs string, rootLabel string, searchTarget string, singleFile bool, err error) {
	workspaceRoot, err := filepath.Abs(strings.TrimSpace(workdir))
	if err != nil {
		return "", "", "", false, fmt.Errorf("resolve workspace dir: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return workspaceRoot, ".", ".", false, nil
	}
	abs, rel, err := tools.WorkspacePath(workdir, raw)
	if err != nil {
		return "", "", "", false, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", "", false, err
	}
	if info.IsDir() {
		return abs, rel, ".", false, nil
	}
	return workspaceRoot, rel, rel, true, nil
}

func runRipgrep(ctx context.Context, rootAbs, searchTarget string, options searchOptions) (string, error) {
	args := []string{"--color", "never"}
	switch options.OutputMode {
	case outputModeContent:
		args = append(args, "-n", "--with-filename")
	case outputModeFilesWithMatches:
		args = append(args, "-l")
	case outputModeCount:
		args = append(args, "-c", "--with-filename")
	}
	if options.IgnoreCase {
		args = append(args, "-i")
	}
	if options.Type != "" {
		args = append(args, "--type", options.Type)
	}
	if options.Include != "" {
		args = append(args, "--glob", options.Include)
	}
	args = append(args, options.Pattern, searchTarget)
	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = rootAbs
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		if len(output) == 0 {
			return "", err
		}
		return "", fmt.Errorf("rg failed: %s", strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func runFallbackSearch(rootAbs, searchTarget string, singleFile bool, options searchOptions) (string, error) {
	re, err := compilePattern(options.Pattern, options.IgnoreCase)
	if err != nil {
		return "", err
	}
	files, err := fallbackFiles(rootAbs, searchTarget, singleFile, options)
	if err != nil {
		return "", err
	}
	switch options.OutputMode {
	case outputModeFilesWithMatches:
		return fallbackFilesWithMatches(files, re)
	case outputModeCount:
		return fallbackCount(files, re)
	default:
		return fallbackContent(files, re)
	}
}

func compilePattern(pattern string, ignoreCase bool) (*regexp.Regexp, error) {
	if ignoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}
	return re, nil
}

type candidateFile struct {
	Abs     string
	Display string
}

func fallbackFiles(rootAbs, searchTarget string, singleFile bool, options searchOptions) ([]candidateFile, error) {
	if singleFile {
		abs := filepath.Join(rootAbs, searchTarget)
		return []candidateFile{{
			Abs:     abs,
			Display: filepath.ToSlash(searchTarget),
		}}, nil
	}
	var files []candidateFile
	err := filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		includeMatches, err := matchesInclude(rel, options.Include)
		if err != nil {
			return err
		}
		if !matchesType(rel, options.Type) || !includeMatches {
			return nil
		}
		files = append(files, candidateFile{Abs: path, Display: rel})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b candidateFile) int {
		return strings.Compare(a.Display, b.Display)
	})
	return files, nil
}

func matchesInclude(rel, include string) (bool, error) {
	include = strings.TrimSpace(include)
	if include == "" {
		return true, nil
	}
	match, err := filepath.Match(include, rel)
	if err == nil && match {
		return true, nil
	}
	if !strings.Contains(include, "/") {
		match, err = filepath.Match(include, filepath.Base(rel))
		if err != nil {
			return false, fmt.Errorf("invalid include glob %q: %w", include, err)
		}
		return match, nil
	}
	if err != nil {
		return false, fmt.Errorf("invalid include glob %q: %w", include, err)
	}
	return false, nil
}

func matchesType(rel, kind string) bool {
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(rel))
	if strings.HasPrefix(kind, ".") {
		return ext == kind
	}
	expected := map[string]string{
		"go":   ".go",
		"js":   ".js",
		"jsx":  ".jsx",
		"ts":   ".ts",
		"tsx":  ".tsx",
		"py":   ".py",
		"rb":   ".rb",
		"rs":   ".rs",
		"java": ".java",
		"c":    ".c",
		"h":    ".h",
		"cpp":  ".cpp",
		"cc":   ".cc",
		"cxx":  ".cxx",
		"cs":   ".cs",
		"php":  ".php",
		"json": ".json",
		"yaml": ".yaml",
		"yml":  ".yml",
		"toml": ".toml",
		"md":   ".md",
		"sh":   ".sh",
	}[kind]
	if expected == "" {
		expected = "." + kind
	}
	return ext == expected
}

func fallbackContent(files []candidateFile, re *regexp.Regexp) (string, error) {
	var lines []string
	for _, file := range files {
		body, err := os.ReadFile(file.Abs)
		if err != nil {
			return "", err
		}
		for idx, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
			if re.MatchString(line) {
				lines = append(lines, fmt.Sprintf("%s:%d:%s", file.Display, idx+1, line))
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

func fallbackFilesWithMatches(files []candidateFile, re *regexp.Regexp) (string, error) {
	var lines []string
	for _, file := range files {
		body, err := os.ReadFile(file.Abs)
		if err != nil {
			return "", err
		}
		if re.Match(body) {
			lines = append(lines, file.Display)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func fallbackCount(files []candidateFile, re *regexp.Regexp) (string, error) {
	var lines []string
	for _, file := range files {
		body, err := os.ReadFile(file.Abs)
		if err != nil {
			return "", err
		}
		count := 0
		for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
			if re.MatchString(line) {
				count++
			}
		}
		if count > 0 {
			lines = append(lines, fmt.Sprintf("%s:%d", file.Display, count))
		}
	}
	return strings.Join(lines, "\n"), nil
}

func splitNonEmptyLines(output string) []string {
	output = strings.TrimRight(output, "\n")
	if strings.TrimSpace(output) == "" {
		return nil
	}
	return strings.Split(output, "\n")
}

func normalizeOutputLines(lines []string) []string {
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "./")
	}
	return lines
}

func applyHeadLimit(lines []string, limit int) ([]string, bool) {
	if limit <= 0 || len(lines) <= limit {
		return lines, false
	}
	return lines[:limit], true
}
