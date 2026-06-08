package writetool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Write file",
		Description: "Create a workspace file, or intentionally overwrite one when force_overwrite is true.",
		Usage:       "Create a new file in the workspace. Prefer file_write only for new small files and initial scaffolds, ideally under 200 lines. For larger files, write a small compiling skeleton first, then add behavior iteratively with focused file_edit calls. For existing files, use file_edit for targeted changes. Write refuses to overwrite existing files unless force_overwrite is explicitly true; only force overwrite when a full-file rewrite is absolutely necessary.",
		Parameters:  `{"type":"object","properties":{"path":{"type":"string","description":"File to create or intentionally overwrite"},"content":{"type":"string","description":"Complete contents of the new file. Prefer a small initial scaffold under 200 lines, then add behavior iteratively with file_edit. Do not use placeholders."},"force_overwrite":{"type":"boolean","description":"Set to true only when intentionally replacing the complete contents of an existing file. Prefer file_edit for existing files; omit or false for new-file creation."}},"required":["path","content"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return tools.FileWrite }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(args["path"])
	content := args["content"]
	if path == "" {
		return nil, errors.New("path is empty")
	}
	out := map[string]string{"path": path, "content": content}
	if forceOverwrite := strings.TrimSpace(args["force_overwrite"]); forceOverwrite != "" {
		value, err := strconv.ParseBool(forceOverwrite)
		if err != nil {
			return nil, errors.New("force_overwrite must be a boolean")
		}
		out["force_overwrite"] = strconv.FormatBool(value)
	}
	return out, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["path"] }
func (tool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	abs, rel, err := tools.WritablePath(runtime, req.Args["path"])
	if err != nil {
		return tools.Result{}, err
	}
	beforeBytes, readErr := os.ReadFile(abs)
	mode := os.FileMode(0o644)
	action := "created"
	if readErr == nil {
		if req.Args["force_overwrite"] != "true" {
			return tools.Result{}, fmt.Errorf("file_write refuses to overwrite existing file %s. Prefer file_edit for targeted changes to existing files. For larger new work, create a small initial scaffold, then add behavior iteratively with focused file_edit calls. Use force_overwrite=true only when replacing the entire file is absolutely necessary", rel)
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
	content, truncated := tools.TruncateText(req.Args["content"], tools.DefaultToolOutputLimit)
	return tools.Result{
		Output:   summary,
		DiffText: dmp.DiffPrettyText(diffs),
		Meta: map[string]string{
			"path":   rel,
			"action": action,
		},
		Stored: tools.WriteStoredResult{
			Path:      rel,
			Action:    action,
			Summary:   summary,
			Content:   content,
			Truncated: truncated,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "file_write", result.Output
}
