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
		case "initialized", "textDocument/didOpen":
		case "workspace/symbol":
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
