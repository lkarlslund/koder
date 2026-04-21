package applypatchtool

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindApplyPatch }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindApplyPatch, "Apply a unified diff patch to workspace files", `{"type":"object","properties":{"patch":{"type":"string","description":"Unified diff patch to apply"}},"required":["patch"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	patch := args["patch"]
	if patch == "" {
		patch = args["diff"]
	}
	if patch == "" {
		patch = args["content"]
	}
	if strings.TrimSpace(patch) == "" {
		return nil, errors.New("patch is empty")
	}
	return map[string]string{"patch": patch}, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"patch": raw} }
func (tool) Preview(req tools.Request) string {
	paths := patchPaths(req.Args["patch"])
	if len(paths) == 0 {
		return "patch"
	}
	return tools.SummarizePaths(paths, 3)
}
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: "Apply patch", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(tool{}.Preview(req))
}
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return tools.Result{}, errors.New("apply_patch requires git to be installed")
	}
	paths := patchPaths(req.Args["patch"])
	for _, path := range paths {
		if _, _, err := tools.WorkspacePath(runtime.Workdir, path); err != nil {
			return tools.Result{}, err
		}
	}
	check := exec.Command("git", "-C", runtime.Workdir, "apply", "--check", "--unidiff-zero", "--whitespace=nowarn", "-")
	check.Stdin = strings.NewReader(req.Args["patch"])
	if output, err := check.CombinedOutput(); err != nil {
		return tools.Result{}, fmt.Errorf("patch check failed: %s", strings.TrimSpace(string(output)))
	}
	cmd := exec.Command("git", "-C", runtime.Workdir, "apply", "--unidiff-zero", "--whitespace=nowarn", "-")
	cmd.Stdin = strings.NewReader(req.Args["patch"])
	if output, err := cmd.CombinedOutput(); err != nil {
		return tools.Result{}, fmt.Errorf("apply patch: %s", strings.TrimSpace(string(output)))
	}
	return tools.Result{
		Output:   "Applied patch to " + tools.SummarizePaths(paths, 5),
		DiffText: req.Args["patch"],
		Meta: map[string]string{
			"changed_files": strings.Join(paths, ","),
			"file_count":    strconv.Itoa(len(paths)),
		},
		Stored: tools.ApplyPatchStoredResult{
			Summary:      "Applied patch to " + tools.SummarizePaths(paths, 5),
			ChangedFiles: paths,
			FileCount:    len(paths),
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	if output := strings.TrimSpace(result.Output); output != "" {
		return "apply_patch", output
	}
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

var patchPathPattern = regexp.MustCompile(`(?m)^(?:\+\+\+|---)\s+(?:a/|b/)?([^\t\n]+)`)

func patchPaths(patch string) []string {
	seen := map[string]struct{}{}
	var paths []string
	for _, match := range patchPathPattern.FindAllStringSubmatch(patch, -1) {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		if path == "" || path == "/dev/null" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}
