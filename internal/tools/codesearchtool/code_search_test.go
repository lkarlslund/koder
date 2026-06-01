package codesearchtool

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestMain(m *testing.M) {
	if os.Getenv("KODER_FAKE_LSP") == "1" {
		runFakeLSP()
		return
	}
	os.Exit(m.Run())
}

func TestExecuteReportsDetectedLanguagesAndAvailability(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	installFakeServer(t, "gopls")

	result, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{"action": actionLanguages},
	})
	if err != nil {
		t.Fatalf("execute languages: %v", err)
	}
	if !strings.Contains(result.Output, "go: Go (gopls) - available") {
		t.Fatalf("expected available go server, got:\n%s", result.Output)
	}
}

func TestExecuteQueriesWorkspaceSymbolsThroughLSP(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	writeFile(t, workdir, "main.go", "package main\nfunc Target() {}\n")
	installFakeServer(t, "gopls")
	t.Setenv("KODER_FAKE_LSP_URI", fileURI(filepath.Join(workdir, "main.go")))

	result, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{"query": "Target"},
	})
	if err != nil {
		t.Fatalf("execute workspace symbol: %v", err)
	}
	if !strings.Contains(result.Output, "go: main.go:2:6: function main.Target") {
		t.Fatalf("expected LSP workspace symbol result, got:\n%s", result.Output)
	}
}

func TestExecuteReusesWarmLanguageServer(t *testing.T) {
	withTestLSPManager(t, time.Hour, 10*time.Millisecond)
	workdir := t.TempDir()
	counter := filepath.Join(t.TempDir(), "lsp.log")
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	writeFile(t, workdir, "main.go", "package main\nfunc Target() {}\n")
	installFakeServer(t, "gopls")
	t.Setenv("KODER_FAKE_LSP_COUNTER", counter)
	t.Setenv("KODER_FAKE_LSP_URI", fileURI(filepath.Join(workdir, "main.go")))

	for i := 0; i < 2; i++ {
		result, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
			Tool: domain.ToolKindCodeSearch,
			Args: map[string]string{"query": "Target"},
		})
		if err != nil {
			t.Fatalf("execute workspace symbol %d: %v", i, err)
		}
		if !strings.Contains(result.Output, "go: main.go:2:6: function main.Target") {
			t.Fatalf("expected LSP workspace symbol result, got:\n%s", result.Output)
		}
	}
	if got := fakeLSPEventCount(t, counter, "start"); got != 1 {
		t.Fatalf("expected one fake LSP start, got %d", got)
	}
}

func TestIdleReaperShutsDownWarmLanguageServer(t *testing.T) {
	withTestLSPManager(t, 20*time.Millisecond, 10*time.Millisecond)
	workdir := t.TempDir()
	counter := filepath.Join(t.TempDir(), "lsp.log")
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	writeFile(t, workdir, "main.go", "package main\nfunc Target() {}\n")
	installFakeServer(t, "gopls")
	t.Setenv("KODER_FAKE_LSP_COUNTER", counter)
	t.Setenv("KODER_FAKE_LSP_URI", fileURI(filepath.Join(workdir, "main.go")))

	_, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{"query": "Target"},
	})
	if err != nil {
		t.Fatalf("execute workspace symbol: %v", err)
	}
	waitForFakeLSPEvent(t, counter, "shutdown", 1)
}

func TestExecuteRetriesAfterBrokenLanguageServer(t *testing.T) {
	withTestLSPManager(t, time.Hour, 10*time.Millisecond)
	workdir := t.TempDir()
	counter := filepath.Join(t.TempDir(), "lsp.log")
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	writeFile(t, workdir, "main.go", "package main\nfunc Target() {}\n")
	installFakeServer(t, "gopls")
	t.Setenv("KODER_FAKE_LSP_COUNTER", counter)
	t.Setenv("KODER_FAKE_LSP_FAIL_FIRST_WORKSPACE_SYMBOL", "1")
	t.Setenv("KODER_FAKE_LSP_URI", fileURI(filepath.Join(workdir, "main.go")))

	result, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{"query": "Target"},
	})
	if err != nil {
		t.Fatalf("execute workspace symbol: %v", err)
	}
	if !strings.Contains(result.Output, "go: main.go:2:6: function main.Target") {
		t.Fatalf("expected retry result, got:\n%s", result.Output)
	}
	if got := fakeLSPEventCount(t, counter, "start"); got != 2 {
		t.Fatalf("expected retry to start a second fake LSP, got %d starts", got)
	}
}

func TestExecuteWarnsAboutDetectedMissingLanguageServer(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	t.Setenv("PATH", t.TempDir())

	result, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{"action": actionLanguages},
	})
	if err != nil {
		t.Fatalf("execute languages: %v", err)
	}
	if !strings.Contains(result.Output, "Missing language servers:") || !strings.Contains(result.Output, `go: Go requires command "gopls"`) {
		t.Fatalf("expected missing server warning, got:\n%s", result.Output)
	}
}

func TestDetectsAdditionalPopularLanguages(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "astro.config.mjs", "export default {}\n")
	writeFile(t, workdir, "build.zig", "const std = @import(\"std\");\n")
	writeFile(t, workdir, "composer.json", "{}\n")
	writeFile(t, workdir, "mix.exs", "defmodule Example.MixProject do\nend\n")
	writeFile(t, workdir, "schema.prisma", "datasource db { provider = \"sqlite\" url = \"file:dev.db\" }\n")
	writeFile(t, workdir, "style.scss", ".target { color: red; }\n")
	writeFile(t, workdir, "typst.toml", "[package]\nname = \"example\"\n")

	detected, err := detectLanguages(workdir)
	if err != nil {
		t.Fatalf("detect languages: %v", err)
	}
	got := map[string]bool{}
	for _, server := range detected {
		got[server.ID] = true
	}
	for _, id := range []string{"astro", "zig", "php", "elixir", "prisma", "css", "typst"} {
		if !got[id] {
			t.Fatalf("expected detected language %q in %#v", id, got)
		}
	}
}

func TestExpandedLanguageServerPathMapping(t *testing.T) {
	tests := []struct {
		path     string
		serverID string
		langID   string
	}{
		{path: "component.astro", serverID: "astro", langID: "astro"},
		{path: "types.pyi", serverID: "python", langID: "python"},
		{path: "script.ksh", serverID: "bash", langID: "shellscript"},
		{path: "main.zon", serverID: "zig", langID: "zig"},
		{path: "Package.swift", serverID: "swift", langID: "swift"},
		{path: "ViewController.m", serverID: "swift", langID: "objective-c"},
		{path: "schema.prisma", serverID: "prisma", langID: "prisma"},
		{path: "paper.bib", serverID: "latex", langID: "bibtex"},
		{path: "deploy/Dockerfile", serverID: "dockerfile", langID: "dockerfile"},
		{path: "Containerfile", serverID: "dockerfile", langID: "dockerfile"},
		{path: "main.typ", serverID: "typst", langID: "typst"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			server, ok := languageForPath(tt.path)
			if !ok {
				t.Fatalf("expected server for %s", tt.path)
			}
			if server.ID != tt.serverID {
				t.Fatalf("server ID: got %q want %q", server.ID, tt.serverID)
			}
			if got := languageIDForPath(server, tt.path); got != tt.langID {
				t.Fatalf("language ID: got %q want %q", got, tt.langID)
			}
		})
	}
}

func TestExecuteQueriesDocumentSymbolsThroughLSP(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	writeFile(t, workdir, "main.go", "package main\nfunc Target() {}\n")
	installFakeServer(t, "gopls")
	t.Setenv("KODER_FAKE_LSP_URI", fileURI(filepath.Join(workdir, "main.go")))

	result, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{
			"action": actionDocumentSymbols,
			"path":   "main.go",
		},
	})
	if err != nil {
		t.Fatalf("execute document symbols: %v", err)
	}
	if !strings.Contains(result.Output, "go: main.go:2:6: function Target") {
		t.Fatalf("expected LSP document symbol result, got:\n%s", result.Output)
	}
}

func TestExecuteQueriesDefinitionThroughLSP(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	writeFile(t, workdir, "main.go", "package main\nfunc Target() {}\n")
	installFakeServer(t, "gopls")
	t.Setenv("KODER_FAKE_LSP_URI", fileURI(filepath.Join(workdir, "main.go")))

	result, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{
			"action":    actionDefinition,
			"path":      "main.go",
			"line":      "2",
			"character": "8",
		},
	})
	if err != nil {
		t.Fatalf("execute definition: %v", err)
	}
	if !strings.Contains(result.Output, "go: main.go:2:6") {
		t.Fatalf("expected LSP definition result, got:\n%s", result.Output)
	}
}

func TestExecuteQueriesReferencesThroughLSP(t *testing.T) {
	workdir := t.TempDir()
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	writeFile(t, workdir, "main.go", "package main\nfunc Target() {}\n")
	installFakeServer(t, "gopls")
	t.Setenv("KODER_FAKE_LSP_URI", fileURI(filepath.Join(workdir, "main.go")))

	result, err := tool{}.Execute(t.Context(), tools.Runtime{Workdir: workdir}, tools.Request{
		Tool: domain.ToolKindCodeSearch,
		Args: map[string]string{
			"action":    actionReferences,
			"path":      "main.go",
			"line":      "2",
			"character": "8",
		},
	})
	if err != nil {
		t.Fatalf("execute references: %v", err)
	}
	if !strings.Contains(result.Output, "go: main.go:2:6") {
		t.Fatalf("expected LSP references result, got:\n%s", result.Output)
	}
}

func TestLSPDiagnosticsCollectsIntroducedDiagnostics(t *testing.T) {
	withTestLSPManager(t, time.Hour, 10*time.Millisecond)
	workdir := t.TempDir()
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	writeFile(t, workdir, "main.go", "package main\nfunc Target() {}\n")
	installFakeServer(t, "gopls")
	t.Setenv("KODER_FAKE_LSP_URI", fileURI(filepath.Join(workdir, "main.go")))
	t.Setenv("KODER_FAKE_LSP_DIAGNOSTIC_ON_CHANGE", "1")

	report := LSPDiagnostics(t.Context(), workdir, "main.go", "package main\nfunc Target() {}\n", "package main\nfunc Target() { broken }\n", true)
	if len(report.Diagnostics) != 1 {
		t.Fatalf("expected one introduced diagnostic, got %#v skipped=%#v", report.Diagnostics, report.Skipped)
	}
	diagnostic := report.Diagnostics[0]
	if diagnostic.Path != "main.go" || diagnostic.Line != 2 || !strings.Contains(diagnostic.Message, "fake diagnostic") {
		t.Fatalf("unexpected diagnostic: %#v", diagnostic)
	}
}

func TestLSPDiagnosticsSkipsWhenDiagnosticsDoNotPublish(t *testing.T) {
	withTestLSPManager(t, time.Hour, 10*time.Millisecond)
	workdir := t.TempDir()
	writeFile(t, workdir, "go.mod", "module example.com/test\n")
	writeFile(t, workdir, "main.go", "package main\nfunc Target() {}\n")
	installFakeServer(t, "gopls")
	t.Setenv("KODER_FAKE_LSP_URI", fileURI(filepath.Join(workdir, "main.go")))
	t.Setenv("KODER_FAKE_LSP_NO_OPEN_DIAGNOSTIC", "1")

	start := time.Now()
	report := LSPDiagnostics(t.Context(), workdir, "main.go", "package main\nfunc Target() {}\n", "package main\nfunc Target() {}\n", false)
	if len(report.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics without fresh publish, got %#v", report.Diagnostics)
	}
	if len(report.Skipped) != 1 || !strings.Contains(report.Skipped[0], "timed out waiting for diagnostics") {
		t.Fatalf("expected timeout skip, got %#v", report.Skipped)
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("expected diagnostics wait near 1s, got %s", elapsed)
	}
}

func installFakeServer(t *testing.T, name string) {
	t.Helper()
	binDir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(binDir, name)
	content := "#!/bin/sh\nKODER_FAKE_LSP=1 exec " + shellQuote(exe) + "\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func runFakeLSP() {
	startIndex := recordFakeLSPEvent("start")
	reader := bufio.NewReader(os.Stdin)
	for {
		msg, err := fakeReadMessage(reader)
		if err != nil {
			return
		}
		method, _ := msg["method"].(string)
		id, hasID := fakeID(msg["id"])
		switch method {
		case "initialize":
			fakeWriteResponse(id, map[string]any{"capabilities": map[string]any{}})
		case "initialized":
		case "textDocument/didOpen":
			if os.Getenv("KODER_FAKE_LSP_BASELINE_DIAGNOSTIC") == "1" {
				fakeWriteDiagnostic("baseline diagnostic")
			} else if os.Getenv("KODER_FAKE_LSP_NO_OPEN_DIAGNOSTIC") != "1" {
				fakeWriteDiagnostics(nil)
			}
		case "textDocument/didChange":
			if os.Getenv("KODER_FAKE_LSP_DIAGNOSTIC_ON_CHANGE") == "1" {
				fakeWriteDiagnostic("fake diagnostic")
			}
		case "workspace/symbol":
			if os.Getenv("KODER_FAKE_LSP_FAIL_FIRST_WORKSPACE_SYMBOL") == "1" && startIndex == 1 {
				return
			}
			fakeWriteResponse(id, []map[string]any{fakeSymbol("main.Target")})
		case "textDocument/documentSymbol":
			fakeWriteResponse(id, []map[string]any{{
				"name":           "Target",
				"kind":           12,
				"range":          fakeRange(),
				"selectionRange": fakeRange(),
			}})
		case "textDocument/definition":
			fakeWriteResponse(id, fakeLocation())
		case "textDocument/references":
			fakeWriteResponse(id, []map[string]any{fakeLocation()})
		case "shutdown":
			recordFakeLSPEvent("shutdown")
			fakeWriteResponse(id, nil)
		case "exit":
			return
		default:
			if hasID {
				fakeWriteResponse(id, nil)
			}
		}
	}
}

func withTestLSPManager(t *testing.T, idleTimeout, sweepInterval time.Duration) {
	t.Helper()
	old := defaultLSPManager
	manager := newLSPManager(idleTimeout, sweepInterval, 100*time.Millisecond)
	defaultLSPManager = manager
	t.Cleanup(func() {
		manager.close()
		defaultLSPManager = old
	})
}

func recordFakeLSPEvent(event string) int {
	path := os.Getenv("KODER_FAKE_LSP_COUNTER")
	if path == "" {
		return 0
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0
	}
	defer file.Close()
	_, _ = fmt.Fprintln(file, event)
	return fakeLSPEventCountNoFail(path, event)
}

func fakeLSPEventCount(t *testing.T, path, event string) int {
	t.Helper()
	count, err := readFakeLSPEventCount(path, event)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func readFakeLSPEventCount(path, event string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if line == event {
			count++
		}
	}
	return count, nil
}

func fakeLSPEventCountNoFail(path, event string) int {
	count, _ := readFakeLSPEventCount(path, event)
	return count
}

func waitForFakeLSPEvent(t *testing.T, path, event string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := fakeLSPEventCount(t, path, event); got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %q events, got %d", want, event, fakeLSPEventCount(t, path, event))
}

func fakeSymbol(name string) map[string]any {
	return map[string]any{
		"name":          name,
		"kind":          12,
		"containerName": "",
		"location":      fakeLocation(),
	}
}

func fakeLocation() map[string]any {
	return map[string]any{
		"uri":   os.Getenv("KODER_FAKE_LSP_URI"),
		"range": fakeRange(),
	}
}

func fakeRange() map[string]any {
	return map[string]any{
		"start": map[string]int{"line": 1, "character": 5},
		"end":   map[string]int{"line": 1, "character": 11},
	}
}

func fakeReadMessage(reader *bufio.Reader) (map[string]any, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &length)
		}
	}
	if length < 0 {
		return nil, io.ErrUnexpectedEOF
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	var msg map[string]any
	return msg, json.Unmarshal(body, &msg)
}

func fakeWriteResponse(id int, result any) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func fakeWriteDiagnostic(message string) {
	fakeWriteDiagnostics([]map[string]any{{
		"range":    fakeRange(),
		"severity": 1,
		"source":   "fake",
		"code":     "F001",
		"message":  message,
	}})
}

func fakeWriteDiagnostics(diagnostics []map[string]any) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]any{
			"uri":         os.Getenv("KODER_FAKE_LSP_URI"),
			"diagnostics": diagnostics,
		},
	})
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(body), body)
}

func fakeID(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
