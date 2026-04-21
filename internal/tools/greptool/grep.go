package greptool

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindGrep }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition() (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindGrep, "Search for text within workspace files", `{"type":"object","properties":{"pattern":{"type":"string","description":"Text or regex to search for"},"path":{"type":"string","description":"Optional workspace directory to search from"},"include":{"type":"string","description":"Optional glob for files to include"}},"required":["pattern"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	pattern := strings.TrimSpace(tools.FirstArg(args, "pattern", "query", "search"))
	if pattern == "" {
		return nil, errors.New("pattern is empty")
	}
	out := map[string]string{"pattern": pattern}
	if root := tools.NormalizePathInput(tools.FirstArg(args, "path", "root", "dir")); root != "" {
		out["path"] = root
	}
	if include := strings.TrimSpace(tools.FirstArg(args, "include", "glob")); include != "" {
		out["include"] = include
	}
	return out, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"pattern": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["pattern"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	subtitle := preview
	if subtitle != "" {
		subtitle = "Query: " + subtitle
	}
	return tools.Presentation{Title: "Search text", Subtitle: subtitle, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["pattern"])
}
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if _, err := exec.LookPath("rg"); err == nil {
		rootAbs, _, err := tools.WorkspaceDir(runtime.Workdir, req.Args["path"])
		if err != nil {
			return tools.Result{}, err
		}
		args := []string{"-n", req.Args["pattern"]}
		if include := strings.TrimSpace(req.Args["include"]); include != "" {
			args = append(args, "--glob", include)
		}
		args = append(args, ".")
		cmd := exec.CommandContext(ctx, "rg", args...)
		cmd.Dir = rootAbs
		output, err := cmd.CombinedOutput()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return tools.Result{
					Output: "No matches found",
					Meta: map[string]string{
						"pattern":   req.Args["pattern"],
						"include":   req.Args["include"],
						"base_path": strings.TrimSpace(req.Args["path"]),
						"matches":   "0",
					},
					Stored: tools.GrepStoredResult{
						Pattern:  req.Args["pattern"],
						BasePath: strings.TrimSpace(req.Args["path"]),
						Include:  req.Args["include"],
						Output:   "No matches found",
					},
				}, nil
			}
			if len(output) == 0 {
				return tools.Result{}, err
			}
		}
		text, truncated := tools.TruncateText(string(output), tools.DefaultToolOutputLimit)
		return tools.Result{
			Output: text,
			Meta: map[string]string{
				"pattern":   req.Args["pattern"],
				"include":   req.Args["include"],
				"base_path": strings.TrimSpace(req.Args["path"]),
				"truncated": tools.BoolString(truncated),
			},
			Stored: tools.GrepStoredResult{
				Pattern:   req.Args["pattern"],
				BasePath:  strings.TrimSpace(req.Args["path"]),
				Include:   req.Args["include"],
				Output:    text,
				Truncated: truncated,
			},
		}, nil
	}
	return tools.Result{}, errors.New("grep requires ripgrep (rg) to be installed")
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}
