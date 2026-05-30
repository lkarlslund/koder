package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/all"
)

type fakeMCPExecutor struct {
	serverID string
	toolName string
	args     map[string]any
	result   tools.Result
}

func (f *fakeMCPExecutor) ExecuteTool(_ context.Context, serverID, toolName string, args map[string]any) (tools.Result, error) {
	f.serverID = serverID
	f.toolName = toolName
	f.args = args
	return f.result, nil
}

func TestToolCallNoArgsUsesEmptyObjectArguments(t *testing.T) {
	call := tools.ToolCall(tools.Request{
		Tool:       domain.ToolKindMilestoneList,
		ToolCallID: "call_1",
	})

	if call.Function.Name != string(domain.ToolKindMilestoneList) {
		t.Fatalf("expected function name %q, got %q", domain.ToolKindMilestoneList, call.Function.Name)
	}
	if call.Function.Arguments != "{}" {
		t.Fatalf("expected empty object arguments, got %q", call.Function.Arguments)
	}
}

func TestReadCurrentDirectoryListsFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry(dir)
	result, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{"path": "."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta["mode"] != "dir" {
		t.Fatalf("expected directory mode, got %#v", result.Meta)
	}
	if result.Meta["path"] != "." {
		t.Fatalf("expected current directory path, got %#v", result.Meta)
	}
	if !strings.Contains(result.Output, "alpha.txt") {
		t.Fatalf("expected file listing to include alpha.txt, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "nested/") {
		t.Fatalf("expected file listing to include nested directory, got %q", result.Output)
	}
}

func TestBashZeroTimeoutUsesDefault(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewRegistry(dir)

	result, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{
			"command":    "printf ok",
			"timeout_ms": "0",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Output) != "ok" {
		t.Fatalf("unexpected bash output: %q", result.Output)
	}
	if got := result.Meta["timeout_ms"]; got != "300000" {
		t.Fatalf("expected default timeout metadata, got %q", got)
	}
}

func TestMCPToolUsesRegistryMCPExecutor(t *testing.T) {
	registry := tools.NewRegistry(t.TempDir())
	fake := &fakeMCPExecutor{
		result: tools.Result{Output: "mcp ok"},
	}
	registry.SetMCP(fake)

	result, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindMCP,
		Args: map[string]string{
			"server":        "exa",
			"tool":          "web_search_exa",
			"arguments_raw": `{"query":"golang"}`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "mcp ok" {
		t.Fatalf("unexpected mcp result: %#v", result)
	}
	if fake.serverID != "exa" || fake.toolName != "web_search_exa" {
		t.Fatalf("unexpected mcp target: server=%q tool=%q", fake.serverID, fake.toolName)
	}
	if got := fake.args["query"]; got != "golang" {
		t.Fatalf("unexpected mcp args: %#v", fake.args)
	}
}

func TestBashWholeFloatStringTimeoutIsAccepted(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewRegistry(dir)

	result, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{
			"command":    "printf ok",
			"timeout_ms": "60000.00000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Output) != "ok" {
		t.Fatalf("unexpected bash output: %q", result.Output)
	}
	if got := result.Meta["timeout_ms"]; got != "60000" {
		t.Fatalf("expected normalized timeout metadata, got %q", got)
	}
}

func TestBashFractionalTimeoutStillFails(t *testing.T) {
	dir := t.TempDir()
	registry := tools.NewRegistry(dir)

	_, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindBash,
		Args: map[string]string{
			"command":    "printf ok",
			"timeout_ms": "60000.5",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "timeout_ms must be a positive integer") {
		t.Fatalf("expected positive integer error, got %v", err)
	}
}

func TestReadWholeFloatStringStartAndEndLinesAreAccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("1\n2\n3\n4\n5\n6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(dir)

	result, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{
			"path":       "file.txt",
			"start_line": "3.00000",
			"end_line":   "4.00000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "3") || !strings.Contains(result.Output, "4") {
		t.Fatalf("expected sliced lines in output, got %q", result.Output)
	}
	if strings.Contains(result.Output, "1: 1") || strings.Contains(result.Output, "6: 6") {
		t.Fatalf("expected read window to apply, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "(showing lines 3-4 of 6; use start_line=5 end_line=6 only if you need the next section; prefer grep or a narrower range for specific code)") {
		t.Fatalf("expected continuation footer, got %q", result.Output)
	}
	if got := result.Meta["start_line"]; got != "3" {
		t.Fatalf("expected normalized start_line metadata, got %q", got)
	}
	if got := result.Meta["end_line"]; got != "4" {
		t.Fatalf("expected normalized end_line metadata, got %q", got)
	}
}

func TestReadFractionalStartLineFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(dir)

	_, err := registry.Execute(context.Background(), tools.Request{
		Tool: domain.ToolKindRead,
		Args: map[string]string{
			"path":       "file.txt",
			"start_line": "3.5",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "start_line must be a positive integer") {
		t.Fatalf("expected positive integer error, got %v", err)
	}
}

func TestWebSearchWholeFloatStringLimitIsAccepted(t *testing.T) {
	req, err := tools.Normalize(tools.Request{
		Tool: domain.ToolKindWebSearch,
		Args: map[string]string{
			"query": "golang",
			"limit": "4.00000",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Args["limit"]; got != "4" {
		t.Fatalf("expected normalized limit, got %q", got)
	}
}

func TestReadTextFileUsesColonLinePrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, truncated, err := tools.ReadTextFile(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("expected untruncated output")
	}
	if output != "1: alpha\n2: beta\n3: " {
		t.Fatalf("unexpected read output: %q", output)
	}
}

func TestParseReadStoredLinesAcceptsTabDelimitedLines(t *testing.T) {
	lines, footer := tools.ParseReadStoredLines("     1\talpha\n     2\tbeta")
	if footer != "" {
		t.Fatalf("expected empty footer, got %q", footer)
	}
	if len(lines) != 2 || lines[0].Number != 1 || lines[0].Text != "alpha" || lines[1].Number != 2 || lines[1].Text != "beta" {
		t.Fatalf("unexpected parsed lines: %#v", lines)
	}
}

func TestParseReadStoredLinesTreatsColonLinesAsFooter(t *testing.T) {
	lines, footer := tools.ParseReadStoredLines("1: alpha\n2: beta")
	if len(lines) != 0 {
		t.Fatalf("expected no parsed lines, got %#v", lines)
	}
	if footer != "1: alpha\n2: beta" {
		t.Fatalf("expected colon lines in footer, got %q", footer)
	}
}

func TestNormalizeFailurePreservesRequestIdentity(t *testing.T) {
	// When Normalize fails (e.g. empty bash command), the returned request
	// must preserve Tool, ToolCallID, and Args so the caller can attach an
	// error result to the persisted tool call.

	// Empty bash command
	req := tools.Request{
		Tool:       domain.ToolKindBash,
		ToolCallID: "call_abc123",
		Args:       map[string]string{"command": ""},
	}
	_, err := tools.Normalize(req)
	if err == nil {
		t.Fatal("expected error for empty bash command")
	}
	if got := req.Tool; got != domain.ToolKindBash {
		t.Errorf("expected Tool to be %q, got %q", domain.ToolKindBash, got)
	}
	if got := req.ToolCallID; got != "call_abc123" {
		t.Errorf("expected ToolCallID to be %q, got %q", "call_abc123", got)
	}
}
