package greptool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
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
	return tools.Presentation{Title: "Search text", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	pattern := strings.TrimSpace(req.Args["pattern"])
	subtitle := pattern
	if scope := grepScopeLabel(req.Args["path"], req.Args["include"]); scope != "" {
		if subtitle == "" {
			subtitle = scope
		} else {
			subtitle += " in " + scope
		}
	}
	return tools.Presentation{Title: "Search text", Subtitle: subtitle, Preview: pattern}
}
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if _, err := exec.LookPath("rg"); err == nil {
		rootAbs, rootLabel, searchTarget, singleFile, err := grepScope(runtime.Workdir, req.Args["path"])
		if err != nil {
			return tools.Result{}, err
		}
		args := []string{"-n", req.Args["pattern"]}
		if include := strings.TrimSpace(req.Args["include"]); include != "" {
			args = append(args, "--glob", include)
		}
		args = append(args, searchTarget)
		cmd := exec.CommandContext(ctx, "rg", args...)
		cmd.Dir = rootAbs
		output, err := cmd.CombinedOutput()
		outputText := string(output)
		if singleFile {
			outputText = prefixSingleFileMatches(rootLabel, outputText)
		}
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return tools.Result{
					Output: "No matches found",
					Meta: map[string]string{
						"pattern":   req.Args["pattern"],
						"include":   req.Args["include"],
						"base_path": rootLabel,
						"matches":   "0",
					},
					Stored: tools.GrepStoredResult{
						Pattern:  req.Args["pattern"],
						BasePath: rootLabel,
						Include:  req.Args["include"],
						Output:   "No matches found",
					},
				}, nil
			}
			if len(output) == 0 {
				return tools.Result{}, err
			}
		}
		text, truncated := tools.TruncateText(outputText, tools.DefaultToolOutputLimit)
		return tools.Result{
			Output: text,
			Meta: map[string]string{
				"pattern":   req.Args["pattern"],
				"include":   req.Args["include"],
				"base_path": rootLabel,
				"truncated": tools.BoolString(truncated),
			},
			Stored: tools.GrepStoredResult{
				Pattern:   req.Args["pattern"],
				BasePath:  rootLabel,
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

func grepScopeLabel(path, include string) string {
	path = strings.TrimSpace(path)
	include = strings.TrimSpace(include)
	switch {
	case path != "" && include != "":
		return path + " (" + include + ")"
	case path != "":
		return path
	default:
		return include
	}
}

func grepScope(workdir string, raw string) (rootAbs string, rootLabel string, searchTarget string, singleFile bool, err error) {
	workspaceRoot, err := filepath.Abs(strings.TrimSpace(workdir))
	if err != nil {
		return "", "", "", false, fmt.Errorf("resolve workspace dir: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return workspaceRoot, ".", ".", false, nil
	}
	abs, rel, err := tools.WorkspacePath(workdir, raw)
	if err != nil {
		return "", "", "", false, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", "", false, err
	}
	if info.IsDir() {
		return abs, rel, ".", false, nil
	}
	return workspaceRoot, rel, rel, true, nil
}

func prefixSingleFileMatches(path string, output string) string {
	output = strings.TrimRight(output, "\n")
	if strings.TrimSpace(output) == "" {
		return output
	}
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines[i] = path + ":" + line
	}
	return strings.Join(lines, "\n") + "\n"
}
