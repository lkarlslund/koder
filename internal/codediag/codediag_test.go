package codediag

import (
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
