package codediag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type markdownCodeFence struct {
	Language  string
	Content   string
	StartLine int
}

type mermaidValidationRequest struct {
	Index  int    `json:"index"`
	Source string `json:"source"`
}

type mermaidValidationResult struct {
	Index   int    `json:"index"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}

func markdownRendererDiagnostics(ctx context.Context, path, content string) Report {
	fences := markdownCodeFences(content)
	var mermaid []mermaidValidationRequest
	for index, fence := range fences {
		if isMermaidFence(fence.Language) {
			mermaid = append(mermaid, mermaidValidationRequest{Index: index, Source: fence.Content})
		}
	}
	if len(mermaid) == 0 {
		return Report{}
	}
	results, skipped := validateMermaidDiagrams(ctx, mermaid)
	if skipped != "" {
		return Report{Skipped: []string{skipped}}
	}
	report := Report{Diagnostics: make([]Diagnostic, 0, len(results))}
	for _, result := range results {
		if result.Index < 0 || result.Index >= len(fences) {
			continue
		}
		fence := fences[result.Index]
		line := fence.StartLine
		if result.Line > 0 {
			line += result.Line - 1
		}
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Source:   SourceSyntax,
			Path:     path,
			Line:     line,
			Column:   result.Column,
			Severity: "error",
			Tool:     "mermaid",
			Code:     "renderer",
			Message:  strings.TrimSpace(result.Message),
		})
	}
	return report
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

func validateMermaidDiagrams(ctx context.Context, diagrams []mermaidValidationRequest) ([]mermaidValidationResult, string) {
	if len(diagrams) == 0 {
		return nil, ""
	}
	if _, err := exec.LookPath("node"); err != nil {
		return nil, "syntax/mermaid: node not found"
	}
	bundle, err := mermaidBundlePath()
	if err != nil {
		return nil, "syntax/mermaid: " + err.Error()
	}
	input, err := json.Marshal(diagrams)
	if err != nil {
		return nil, "syntax/mermaid: encode validation request: " + err.Error()
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "node", "-e", mermaidValidationScript, bundle)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if runCtx.Err() != nil {
		return nil, "syntax/mermaid: validation timed out"
	}
	if err != nil {
		return nil, fmt.Sprintf("syntax/mermaid: validation failed: %s", strings.TrimSpace(string(output)))
	}
	var results []mermaidValidationResult
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, "syntax/mermaid: decode validation result: " + err.Error()
	}
	return results, ""
}

func mermaidBundlePath() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot resolve source path")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "webui", "assets", "vendor", "mermaid", "mermaid.min.js"))
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("vendored mermaid parser unavailable: %w", err)
	}
	return path, nil
}

const mermaidValidationScript = `
const fs = require('fs');
const vm = require('vm');
const bundle = process.argv[1];
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
vm.runInThisContext(fs.readFileSync(bundle, 'utf8'), {filename: bundle});
globalThis.mermaid.initialize({startOnLoad: false, securityLevel: 'strict'});
(async () => {
  const out = [];
  for (const item of input) {
    try {
      await globalThis.mermaid.parse(String(item.source || ''));
    } catch (err) {
      const hash = err && err.hash ? err.hash : {};
      const loc = hash.loc || {};
      out.push({
        index: item.index,
        line: Number(hash.line || loc.first_line || 0),
        column: Number(loc.first_column || 0) + 1,
        message: String((err && (err.str || err.message)) || err || 'Mermaid parse failed')
      });
    }
  }
  process.stdout.write(JSON.stringify(out));
})().catch(err => {
  console.error(err && err.stack ? err.stack : String(err));
  process.exit(1);
});
`
