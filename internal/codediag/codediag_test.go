package codediag

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCheckEditReportsOnlyIntroducedSyntaxDiagnostics(t *testing.T) {
	before := "package main\nfunc ok() {}\n"
	after := "package main\nfunc broken( {\n"

	report := CheckEdit(t.Context(), t.TempDir(), "main.go", before, after, Options{Mode: "syntax"})
	text := Text(report)
	if len(report.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %#v", report.Diagnostics)
	}
	if !strings.Contains(text, "syntax: main.go") || !strings.Contains(text, "error") {
		t.Fatalf("unexpected diagnostic text: %q", text)
	}
}

func TestCheckEditSuppressesExistingSyntaxDiagnostics(t *testing.T) {
	before := "package main\nfunc broken( {\n"
	after := "package main\nfunc broken( {\n"

	report := CheckEdit(t.Context(), t.TempDir(), "main.go", before, after, Options{Mode: "syntax"})
	if len(report.Diagnostics) != 0 {
		t.Fatalf("expected no introduced diagnostics, got %#v", report.Diagnostics)
	}
}

func TestCheckFileReportsJSONSyntaxDiagnostic(t *testing.T) {
	report := CheckFile(t.Context(), t.TempDir(), "config.json", "{", Options{Mode: "syntax"})
	if len(report.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %#v", report.Diagnostics)
	}
	if report.Diagnostics[0].Path != "config.json" {
		t.Fatalf("unexpected path: %#v", report.Diagnostics[0])
	}
}

func TestCheckFileReportsMarkdownMermaidSyntaxDiagnostic(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	content := "Before\n\n```mermaid\nflowchart TD\nA -->\n```\n"

	report := CheckFile(t.Context(), t.TempDir(), "README.md", content, Options{Mode: "syntax"})
	if len(report.Skipped) > 0 {
		t.Skipf("mermaid validation skipped: %v", report.Skipped)
	}
	if len(report.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %#v", report.Diagnostics)
	}
	diagnostic := report.Diagnostics[0]
	if diagnostic.Tool != "mermaid" || diagnostic.Code != "renderer" || diagnostic.Line < 4 {
		t.Fatalf("unexpected mermaid diagnostic: %#v", diagnostic)
	}
	if !strings.Contains(Text(report), "syntax/mermaid: README.md") {
		t.Fatalf("expected mermaid diagnostic text, got %q", Text(report))
	}
}

func TestCheckFileIgnoresMermaidNodeSanitizerEnvironmentError(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	content := "```mermaid\nflowchart TD\nA[\"hello\"] --> B[\"world\"]\n```\n"

	report := CheckFile(t.Context(), t.TempDir(), "README.md", content, Options{Mode: "syntax"})
	if len(report.Skipped) > 0 {
		t.Skipf("mermaid validation skipped: %v", report.Skipped)
	}
	if len(report.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics for valid labeled mermaid diagram, got %#v", report.Diagnostics)
	}
}

func TestCheckEditSuppressesExistingMarkdownMermaidSyntaxDiagnostic(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	before := "```mermaid\nflowchart TD\nA -->\n```\n"
	after := before

	report := CheckEdit(t.Context(), t.TempDir(), "README.md", before, after, Options{Mode: "syntax"})
	if len(report.Skipped) > 0 {
		t.Skipf("mermaid validation skipped: %v", report.Skipped)
	}
	if len(report.Diagnostics) != 0 {
		t.Fatalf("expected no introduced diagnostics, got %#v", report.Diagnostics)
	}
}

func TestNewProblemsTextReportsOnlyErrorsConcise(t *testing.T) {
	text := NewProblemsText(Report{Diagnostics: []Diagnostic{
		{Source: SourceLSP, Tool: "go", Path: "main.go", Line: 12, Severity: "warning", Message: "unused"},
		{Source: SourceSyntax, Path: "config.json", Line: 2, Severity: "error", Message: "invalid JSON"},
	}})
	if text != "config.json\n- [syntax error] Line 2: invalid JSON" {
		t.Fatalf("unexpected problems text: %q", text)
	}
}
