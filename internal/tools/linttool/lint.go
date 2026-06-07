package linttool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/codediag"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Lint file",
		Description: "Run centralized diagnostics for one workspace file.",
		Usage:       "Run syntax, LSP, and file-local lint diagnostics for one workspace file. Use this after edits when you need targeted validation without running a full test suite. mode defaults to auto; use all to include file-local shell linters where configured.",
		Parameters:  `{"type":"object","properties":{"path":{"type":"string","description":"Workspace file to diagnose"},"mode":{"type":"string","enum":["auto","syntax","lsp","shell","all"],"description":"Diagnostic sources to run. auto runs syntax and LSP; all also runs configured file-local shell linters."},"include_existing":{"type":"boolean","description":"Include diagnostics already present in the current file. Defaults to true."},"timeout_ms":{"type":"integer","description":"Per-run timeout in milliseconds, capped internally."}},"required":["path"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() domain.ToolKind      { return domain.ToolKindLint }
func (tool) BypassesPermission() bool { return false }

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(tools.FirstArg(args, "path", "file", "file_path", "filepath"))
	if path == "" {
		return nil, errors.New("path is empty")
	}
	out := map[string]string{"path": path}
	if mode := strings.ToLower(strings.TrimSpace(tools.FirstArg(args, "mode"))); mode != "" {
		switch mode {
		case "auto", "syntax", "lsp", "shell", "all":
			out["mode"] = mode
		default:
			return nil, errors.New("mode must be one of: auto, syntax, lsp, shell, all")
		}
	}
	if include := strings.TrimSpace(tools.FirstArg(args, "include_existing", "includeExisting")); include != "" {
		out["include_existing"] = include
	}
	if timeout := strings.TrimSpace(tools.FirstArg(args, "timeout_ms", "timeoutMS", "timeout")); timeout != "" {
		value, err := tools.ParseFlexibleInt(timeout)
		if err != nil || value <= 0 {
			return nil, errors.New("timeout_ms must be a positive integer")
		}
		out["timeout_ms"] = fmt.Sprintf("%d", value)
	}
	return out, nil
}

func (tool) Preview(req tools.Request) string {
	if mode := strings.TrimSpace(req.Args["mode"]); mode != "" {
		return req.Args["path"] + " (" + mode + ")"
	}
	return req.Args["path"]
}

func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
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
	data, err := os.ReadFile(abs)
	if err != nil {
		return tools.Result{}, err
	}
	mode := strings.TrimSpace(req.Args["mode"])
	timeout := 2 * time.Second
	if raw := strings.TrimSpace(req.Args["timeout_ms"]); raw != "" {
		if ms, err := tools.ParseFlexibleInt(raw); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}
	includeExisting := true
	if raw := strings.TrimSpace(req.Args["include_existing"]); raw != "" {
		includeExisting = strings.EqualFold(raw, "true")
	}
	report := codediag.CheckFile(ctx, runtime.Workdir, rel, string(data), codediag.Options{
		Mode:            mode,
		IncludeExisting: includeExisting,
		Timeout:         timeout,
	})
	diagnostics := codediag.Text(report)
	summary := "No diagnostics for " + rel
	output := summary
	if diagnostics != "" {
		summary = fmt.Sprintf("Diagnostics for %s", rel)
		output = summary + "\n\n" + diagnostics
	}
	return tools.Result{
		Output: output,
		Meta: map[string]string{
			"path": rel,
			"mode": normalizedMode(mode),
		},
		Stored: tools.LintStoredResult{
			Path:        rel,
			Mode:        normalizedMode(mode),
			Summary:     summary,
			Diagnostics: diagnostics,
			DiagnosticReport: tools.DiagnosticReportStored{
				Diagnostics: storedDiagnostics(report.Diagnostics),
				Skipped:     report.Skipped,
			},
		},
	}, nil
}

func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "lint", result.Output
}

func normalizedMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "auto"
	}
	return mode
}

func storedDiagnostics(in []codediag.Diagnostic) []tools.DiagnosticStored {
	out := make([]tools.DiagnosticStored, 0, len(in))
	for _, diagnostic := range in {
		out = append(out, tools.DiagnosticStored{
			Source:   string(diagnostic.Source),
			Path:     diagnostic.Path,
			Line:     diagnostic.Line,
			Column:   diagnostic.Column,
			Severity: diagnostic.Severity,
			Tool:     diagnostic.Tool,
			Code:     diagnostic.Code,
			Message:  diagnostic.Message,
		})
	}
	return out
}
