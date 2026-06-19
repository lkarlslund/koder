package codediag

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/lkarlslund/koder/internal/tools/codesearchtool"
)

type Source string

const (
	SourceSyntax Source = "syntax"
	SourceLSP    Source = "lsp"
	SourceLint   Source = "lint"
)

type Diagnostic struct {
	Source   Source `json:"source"`
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
}

type Report struct {
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	Skipped     []string     `json:"skipped,omitempty"`
}

type Options struct {
	Mode            string
	IncludeExisting bool
	Timeout         time.Duration
}

func CheckFile(ctx context.Context, rootAbs, relPath, content string, options Options) Report {
	options = normalizeOptions(options)
	var report Report
	if options.Mode == "auto" || options.Mode == "all" || options.Mode == "syntax" {
		syntaxReport := syntaxReport(ctx, relPath, content)
		report.Diagnostics = append(report.Diagnostics, syntaxReport.Diagnostics...)
		report.Skipped = append(report.Skipped, syntaxReport.Skipped...)
	}
	if options.Mode == "auto" || options.Mode == "all" || options.Mode == "lsp" {
		lspReport := codesearchtool.LSPDiagnostics(ctx, rootAbs, relPath, content, content, false)
		report.Diagnostics = append(report.Diagnostics, fromLSP(lspReport.Diagnostics)...)
		report.Skipped = append(report.Skipped, lspReport.Skipped...)
	}
	if options.Mode == "all" || options.Mode == "shell" {
		lintReport := shellDiagnostics(ctx, rootAbs, relPath, content, options.Timeout)
		report.Diagnostics = append(report.Diagnostics, lintReport.Diagnostics...)
		report.Skipped = append(report.Skipped, lintReport.Skipped...)
	}
	return normalizeReport(report)
}

func CheckEdit(ctx context.Context, rootAbs, relPath, before, after string, options Options) Report {
	options = normalizeOptions(options)
	var report Report
	if options.Mode == "auto" || options.Mode == "all" || options.Mode == "syntax" {
		beforeReport := syntaxReport(ctx, relPath, before)
		afterReport := syntaxReport(ctx, relPath, after)
		report.Diagnostics = append(report.Diagnostics, introduced(beforeReport.Diagnostics, afterReport.Diagnostics)...)
		report.Skipped = append(report.Skipped, afterReport.Skipped...)
	}
	if options.Mode == "auto" || options.Mode == "all" || options.Mode == "lsp" {
		lspReport := codesearchtool.LSPDiagnostics(ctx, rootAbs, relPath, before, after, !options.IncludeExisting)
		report.Diagnostics = append(report.Diagnostics, fromLSP(lspReport.Diagnostics)...)
		report.Skipped = append(report.Skipped, lspReport.Skipped...)
	}
	if options.Mode == "all" || options.Mode == "shell" {
		beforeReport := shellDiagnostics(ctx, rootAbs, relPath, before, options.Timeout)
		afterReport := shellDiagnostics(ctx, rootAbs, relPath, after, options.Timeout)
		report.Diagnostics = append(report.Diagnostics, introduced(beforeReport.Diagnostics, afterReport.Diagnostics)...)
		report.Skipped = append(report.Skipped, afterReport.Skipped...)
	}
	return normalizeReport(report)
}

func Text(report Report) string {
	report = normalizeReport(report)
	var lines []string
	for _, diagnostic := range report.Diagnostics {
		prefix := string(diagnostic.Source)
		if diagnostic.Tool != "" {
			prefix += "/" + diagnostic.Tool
		}
		location := diagnostic.Path
		if diagnostic.Line > 0 {
			location += fmt.Sprintf(":%d", diagnostic.Line)
			if diagnostic.Column > 0 {
				location += fmt.Sprintf(":%d", diagnostic.Column)
			}
		}
		severity := strings.TrimSpace(diagnostic.Severity)
		if severity == "" {
			severity = "diagnostic"
		}
		code := ""
		if diagnostic.Code != "" {
			code = " [" + diagnostic.Code + "]"
		}
		lines = append(lines, fmt.Sprintf("%s: %s: %s%s: %s", prefix, location, severity, code, diagnostic.Message))
	}
	if len(report.Skipped) > 0 {
		lines = append(lines, "Skipped diagnostics:")
		lines = append(lines, report.Skipped...)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func NewProblemsText(report Report) string {
	report = normalizeReport(report)
	byPath := map[string][]Diagnostic{}
	var paths []string
	for _, diagnostic := range report.Diagnostics {
		if !strings.EqualFold(strings.TrimSpace(diagnostic.Severity), "error") {
			continue
		}
		path := strings.TrimSpace(diagnostic.Path)
		if path == "" {
			path = "(unknown file)"
		}
		if _, ok := byPath[path]; !ok {
			paths = append(paths, path)
		}
		byPath[path] = append(byPath[path], diagnostic)
	}
	sort.Strings(paths)
	var lines []string
	for _, path := range paths {
		lines = append(lines, path)
		for _, diagnostic := range byPath[path] {
			label := string(diagnostic.Source)
			if diagnostic.Tool != "" {
				label += "/" + diagnostic.Tool
			}
			if label == "" {
				label = "diagnostic"
			}
			line := "?"
			if diagnostic.Line > 0 {
				line = fmt.Sprintf("%d", diagnostic.Line)
			}
			code := ""
			if diagnostic.Code != "" {
				code = " [" + diagnostic.Code + "]"
			}
			lines = append(lines, fmt.Sprintf("- [%s error%s] Line %s: %s", label, code, line, diagnostic.Message))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func normalizeOptions(options Options) Options {
	options.Mode = strings.ToLower(strings.TrimSpace(options.Mode))
	switch options.Mode {
	case "syntax", "lsp", "shell", "all":
	default:
		options.Mode = "auto"
	}
	if options.Timeout <= 0 || options.Timeout > 5*time.Second {
		options.Timeout = 2 * time.Second
	}
	return options
}

func normalizeReport(report Report) Report {
	sort.SliceStable(report.Diagnostics, func(i, j int) bool {
		a, b := report.Diagnostics[i], report.Diagnostics[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Message < b.Message
	})
	report.Skipped = uniqueStrings(report.Skipped)
	return report
}

func syntaxDiagnostics(path, content string) []Diagnostic {
	return syntaxReport(context.Background(), path, content).Diagnostics
}

func syntaxReport(ctx context.Context, path, content string) Report {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, path, content, parser.AllErrors); err != nil {
			return Report{Diagnostics: []Diagnostic{syntaxDiagnostic(path, err.Error())}}
		}
	case ".json":
		var value any
		if err := json.Unmarshal([]byte(content), &value); err != nil {
			return Report{Diagnostics: []Diagnostic{syntaxDiagnostic(path, err.Error())}}
		}
	case ".toml":
		var value map[string]any
		if err := toml.Unmarshal([]byte(content), &value); err != nil {
			return Report{Diagnostics: []Diagnostic{syntaxDiagnostic(path, err.Error())}}
		}
	case ".md", ".markdown", ".mdown", ".mkd":
		return markdownRendererDiagnostics(ctx, path, content)
	}
	return Report{}
}

func syntaxDiagnostic(path, message string) Diagnostic {
	line, column := parsePosition(path, message)
	return Diagnostic{Source: SourceSyntax, Path: path, Line: line, Column: column, Severity: "error", Message: message}
}

func fromLSP(in []codesearchtool.Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(in))
	for _, diagnostic := range in {
		out = append(out, Diagnostic{
			Source:   SourceLSP,
			Path:     diagnostic.Path,
			Line:     diagnostic.Line,
			Column:   diagnostic.Column,
			Severity: diagnostic.Severity,
			Tool:     diagnostic.Language,
			Code:     diagnostic.Code,
			Message:  diagnostic.Message,
		})
	}
	return out
}

type linter struct {
	Tool       string
	Extensions []string
	Command    func(string) []string
}

var linters = []linter{
	{Tool: "bash", Extensions: []string{".sh", ".bash"}, Command: func(path string) []string { return []string{"bash", "-n", path} }},
	{Tool: "sh", Extensions: []string{".zsh"}, Command: func(path string) []string { return []string{"sh", "-n", path} }},
	{Tool: "node", Extensions: []string{".js", ".mjs", ".cjs"}, Command: func(path string) []string { return []string{"node", "--check", path} }},
	{Tool: "python", Extensions: []string{".py"}, Command: func(path string) []string { return []string{"python", "-m", "py_compile", path} }},
	{Tool: "ruby", Extensions: []string{".rb"}, Command: func(path string) []string { return []string{"ruby", "-c", path} }},
	{Tool: "php", Extensions: []string{".php"}, Command: func(path string) []string { return []string{"php", "-l", path} }},
}

func shellDiagnostics(ctx context.Context, rootAbs, relPath, _ string, timeout time.Duration) Report {
	linter, ok := linterForPath(relPath)
	if !ok {
		return Report{Skipped: []string{"lint: no file-local linter configured for " + filepath.Ext(relPath)}}
	}
	command := linter.Command(relPath)
	if _, err := exec.LookPath(command[0]); err != nil {
		return Report{Skipped: []string{fmt.Sprintf("lint: command %q not found for %s", command[0], relPath)}}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, command[0], command[1:]...)
	cmd.Dir = rootAbs
	output, err := cmd.CombinedOutput()
	if runCtx.Err() != nil {
		return Report{Skipped: []string{fmt.Sprintf("lint: %s timed out", strings.Join(command, " "))}}
	}
	text := strings.TrimSpace(string(output))
	if err == nil {
		return Report{}
	}
	if text == "" {
		text = err.Error()
	}
	return Report{Diagnostics: []Diagnostic{{
		Source:   SourceLint,
		Path:     relPath,
		Severity: "error",
		Tool:     linter.Tool,
		Message:  text,
	}}}
}

func linterForPath(path string) (linter, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	for _, linter := range linters {
		for _, candidate := range linter.Extensions {
			if ext == candidate {
				return linter, true
			}
		}
	}
	return linter{}, false
}

func introduced(before, after []Diagnostic) []Diagnostic {
	if len(before) == 0 {
		return after
	}
	seen := map[string]struct{}{}
	for _, diagnostic := range before {
		seen[diagnosticKey(diagnostic)] = struct{}{}
	}
	var out []Diagnostic
	for _, diagnostic := range after {
		if _, ok := seen[diagnosticKey(diagnostic)]; ok {
			continue
		}
		out = append(out, diagnostic)
	}
	return out
}

func diagnosticKey(diagnostic Diagnostic) string {
	return fmt.Sprintf("%s:%s:%d:%d:%s:%s:%s:%s",
		diagnostic.Source,
		diagnostic.Path,
		diagnostic.Line,
		diagnostic.Column,
		diagnostic.Severity,
		diagnostic.Tool,
		diagnostic.Code,
		diagnostic.Message,
	)
}

func parsePosition(path, message string) (int, int) {
	message = strings.TrimPrefix(message, path+":")
	fields := strings.SplitN(message, ":", 3)
	if len(fields) < 2 {
		return 0, 0
	}
	var line, column int
	_, _ = fmt.Sscanf(fields[0]+":"+fields[1], "%d:%d", &line, &column)
	return line, column
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
