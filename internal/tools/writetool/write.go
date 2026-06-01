package writetool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sergi/go-diff/diffmatchpatch"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/lkarlslund/koder/internal/codediag"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Write file",
		Description: "Create a workspace file, or intentionally overwrite one when force_overwrite is true.",
		Usage:       "Create a new file in the workspace. Prefer file_write for new or small files, ideally under 200 lines. For larger files, write a minimal skeleton first, then add sections incrementally with file_edit. For existing files, prefer file_edit for targeted changes. Write refuses to overwrite existing files unless force_overwrite is explicitly true; only force overwrite when a full-file rewrite is absolutely necessary.",
		Parameters:  `{"type":"object","properties":{"path":{"type":"string","description":"File to create or intentionally overwrite"},"content":{"type":"string","description":"Complete contents of the file. Prefer under 200 lines; for larger files, write a skeleton first and fill sections with file_edit. Do not use placeholders."},"force_overwrite":{"type":"boolean","description":"Set to true only when intentionally replacing the complete contents of an existing file. Omit or false for new-file creation."}},"required":["path","content"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindFileWrite }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(tools.FirstArg(args, "path", "file", "file_path", "filepath"))
	content := tools.FirstArg(args, "content", "text", "body")
	if path == "" {
		return nil, errors.New("path is empty")
	}
	out := map[string]string{"path": path, "content": content}
	if forceOverwrite := strings.TrimSpace(tools.FirstArg(args, "force_overwrite", "forceOverwrite")); forceOverwrite != "" {
		out["force_overwrite"] = forceOverwrite
	}
	return out, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["path"] }
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	abs, rel, err := tools.WritablePath(runtime, req.Args["path"])
	if err != nil {
		return tools.Result{}, err
	}
	beforeBytes, readErr := os.ReadFile(abs)
	mode := os.FileMode(0o644)
	action := "created"
	if readErr == nil {
		if !strings.EqualFold(strings.TrimSpace(req.Args["force_overwrite"]), "true") {
			return tools.Result{}, fmt.Errorf("write refuses to overwrite existing file %s without force_overwrite=true. Prefer file_edit for targeted changes; only force overwrite when a full-file rewrite is absolutely necessary", rel)
		}
		if info, statErr := os.Stat(abs); statErr == nil {
			mode = info.Mode().Perm()
		}
		action = "overwrote"
	}
	if err := tools.WriteTextFile(abs, req.Args["content"], mode); err != nil {
		return tools.Result{}, err
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(beforeBytes), req.Args["content"], false)
	summary := fmt.Sprintf("%s %s", cases.Title(language.English).String(action), rel)
	report := codediag.CheckFile(ctx, runtime.Workdir, rel, req.Args["content"], codediag.Options{Mode: "auto", IncludeExisting: true, Timeout: 2 * time.Second})
	diagnostics := codediag.NewProblemsText(report)
	output := summary
	if diagnostics != "" {
		output += "\n\nProblems detected after writing file:\n" + diagnostics
	}
	content, truncated := tools.TruncateText(req.Args["content"], tools.DefaultToolOutputLimit)
	return tools.Result{
		Output:   output,
		DiffText: dmp.DiffPrettyText(diffs),
		Meta: map[string]string{
			"path":   rel,
			"action": action,
		},
		Stored: tools.WriteStoredResult{
			Path:        rel,
			Action:      action,
			Summary:     summary,
			Content:     content,
			Diagnostics: diagnostics,
			DiagnosticReport: tools.DiagnosticReportStored{
				Diagnostics: storedDiagnostics(report.Diagnostics),
				Skipped:     report.Skipped,
			},
			Truncated: truncated,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "file_write", result.Output
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
