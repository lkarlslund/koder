package edittool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindEdit }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindEdit, "Edit an existing text file by replacing exact text", `{"type":"object","properties":{"path":{"type":"string","description":"File to edit"},"old_string":{"type":"string","description":"Exact text to replace"},"new_string":{"type":"string","description":"Replacement text"},"replace_all":{"type":"boolean","description":"Replace every occurrence instead of exactly one"}},"required":["path","old_string","new_string"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(tools.FirstArg(args, "path", "file", "file_path", "filepath"))
	oldString := tools.FirstArg(args, "old_string", "oldString", "oldText", "old")
	newString := tools.FirstArg(args, "new_string", "newString", "newText", "new")
	if path == "" {
		return nil, errors.New("path is empty")
	}
	if oldString == "" {
		return nil, errors.New("old_string is empty")
	}
	if oldString == newString {
		return nil, errors.New("old_string and new_string are identical")
	}
	out := map[string]string{
		"path":       path,
		"old_string": oldString,
		"new_string": newString,
	}
	if replaceAll := strings.TrimSpace(tools.FirstArg(args, "replace_all", "replaceAll")); replaceAll != "" {
		out["replace_all"] = replaceAll
	}
	return out, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"path": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["path"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: "Edit file", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["path"])
}
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
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
	beforeBytes, err := os.ReadFile(abs)
	if err != nil {
		return tools.Result{}, err
	}
	before := string(beforeBytes)
	oldString := req.Args["old_string"]
	newString := req.Args["new_string"]
	occurrences := strings.Count(before, oldString)
	if occurrences == 0 {
		return tools.Result{}, fmt.Errorf("target text not found in %s", rel)
	}
	replaceAll := strings.EqualFold(strings.TrimSpace(req.Args["replace_all"]), "true")
	if !replaceAll && occurrences != 1 {
		return tools.Result{}, fmt.Errorf("target text occurs %d times in %s; use replace_all to replace every occurrence", occurrences, rel)
	}
	after := before
	if replaceAll {
		after = strings.ReplaceAll(before, oldString, newString)
	} else {
		after = strings.Replace(before, oldString, newString, 1)
	}
	if err := tools.WriteTextFile(abs, after, info.Mode().Perm()); err != nil {
		return tools.Result{}, err
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(before, after, false)
	mode := "replaced 1 occurrence"
	if replaceAll {
		mode = fmt.Sprintf("replaced %d occurrences", occurrences)
	}
	summary := fmt.Sprintf("Edited %s (%s)", rel, mode)
	hunks, truncated := buildStoredHunks(before, oldString, newString, replaceAll)
	return tools.Result{
		Output:   summary,
		DiffText: dmp.DiffPrettyText(diffs),
		Meta: map[string]string{
			"path":        rel,
			"replace_all": tools.BoolString(replaceAll),
			"occurrences": fmt.Sprintf("%d", occurrences),
		},
		Stored: tools.EditStoredResult{
			Path:        rel,
			ReplaceAll:  replaceAll,
			Occurrences: occurrences,
			Summary:     summary,
			Hunks:       hunks,
			Truncated:   truncated,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "edit", result.Output
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

const maxStoredHunks = 8

func buildStoredHunks(before, oldString, newString string, replaceAll bool) ([]tools.EditStoredHunk, bool) {
	if strings.TrimSpace(oldString) == "" {
		return nil, false
	}
	oldLines := splitStoredLines(oldString)
	newLines := splitStoredLines(newString)
	var hunks []tools.EditStoredHunk
	searchFrom := 0
	for {
		idx := strings.Index(before[searchFrom:], oldString)
		if idx < 0 {
			break
		}
		abs := searchFrom + idx
		oldStart := 1 + strings.Count(before[:abs], "\n")
		newStart := oldStart
		hunks = append(hunks, tools.EditStoredHunk{
			OldStart: oldStart,
			NewStart: newStart,
			OldLines: oldLines,
			NewLines: newLines,
		})
		if len(hunks) >= maxStoredHunks {
			return hunks, true
		}
		searchFrom = abs + len(oldString)
		if !replaceAll {
			break
		}
	}
	return hunks, false
}

func splitStoredLines(input string) []string {
	lines := strings.Split(strings.TrimSuffix(input, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{""}
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.ReplaceAll(line, "\t", "    "))
	}
	return out
}
