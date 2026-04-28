package globtool

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindGlob }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(
		domain.ToolKindGlob,
		"Find workspace paths by glob pattern when you do not yet know the exact file path. Use this for local file discovery, grep for file contents, and read once you know which file to open. Patterns are matched against workspace-relative paths using slash-separated paths such as **/*.go, cmd/*, or internal/**/testdata/*.json. path optionally narrows the search to a subdirectory. limit caps the number of returned matches.",
		`{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern relative to the workspace"},"path":{"type":"string","description":"Optional workspace directory to search from"},"limit":{"type":"integer","description":"Optional maximum number of matches to return"}},"required":["pattern"],"additionalProperties":false}`,
	), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	pattern := strings.TrimSpace(tools.FirstArg(args, "pattern", "glob"))
	if pattern == "" {
		return nil, errors.New("pattern is empty")
	}
	out := map[string]string{"pattern": pattern}
	if root := tools.NormalizePathInput(tools.FirstArg(args, "path", "root", "dir")); root != "" {
		out["path"] = root
	}
	if rawLimit := strings.TrimSpace(tools.FirstArg(args, "limit", "count")); rawLimit != "" {
		value, err := tools.ParseFlexibleInt(rawLimit)
		if err != nil || value <= 0 {
			return nil, errors.New("limit must be a positive integer")
		}
		out["limit"] = strconv.Itoa(value)
	}
	return out, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"pattern": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["pattern"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	if preview != "" {
		preview = "Pattern: " + preview
	}
	return tools.Presentation{Title: "Find files", Subtitle: preview, Preview: strings.TrimPrefix(preview, "Pattern: ")}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["pattern"])
}
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	rootAbs, _, err := tools.WorkspaceDir(runtime.Workdir, req.Args["path"])
	if err != nil {
		return tools.Result{}, err
	}
	pattern := req.Args["pattern"]
	limit := 0
	if rawLimit := strings.TrimSpace(req.Args["limit"]); rawLimit != "" {
		value, err := strconv.Atoi(rawLimit)
		if err != nil || value <= 0 {
			return tools.Result{}, errors.New("limit must be a positive integer")
		}
		limit = value
	}
	var matches []string
	walkErr := fs.WalkDir(os.DirFS(rootAbs), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		slashPath := filepath.ToSlash(path)
		matched, matchErr := matchGlobPattern(pattern, slashPath)
		if matchErr == nil && matched {
			matches = append(matches, slashPath)
		}
		return nil
	})
	if walkErr != nil {
		return tools.Result{}, walkErr
	}
	sort.Strings(matches)
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	body := strings.Join(matches, "\n")
	body, truncated := tools.TruncateText(body, tools.DefaultToolOutputLimit)
	storedMatches := append([]string(nil), matches...)
	footer := ""
	if truncated {
		storedMatches, footer = splitTruncatedLines(body)
	}
	return tools.Result{
		Output: body,
		Meta: map[string]string{
			"pattern":   pattern,
			"base_path": strings.TrimSpace(req.Args["path"]),
			"limit":     strings.TrimSpace(req.Args["limit"]),
			"matches":   strconv.Itoa(len(matches)),
			"truncated": tools.BoolString(truncated),
		},
		Stored: tools.GlobStoredResult{
			Pattern:   pattern,
			BasePath:  strings.TrimSpace(req.Args["path"]),
			Matches:   storedMatches,
			Footer:    footer,
			Truncated: truncated,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func splitTruncatedLines(body string) ([]string, string) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return nil, ""
	}
	if !strings.HasPrefix(lines[len(lines)-1], "... truncated") {
		return lines, ""
	}
	return lines[:len(lines)-1], lines[len(lines)-1]
}

func matchGlobPattern(pattern string, path string) (bool, error) {
	expr, err := regexp.Compile(globPatternToRegexp(pattern))
	if err != nil {
		return false, err
	}
	return expr.MatchString(path), nil
}

func globPatternToRegexp(pattern string) string {
	var builder strings.Builder
	builder.WriteString("^")
	for idx := 0; idx < len(pattern); idx++ {
		ch := pattern[idx]
		switch ch {
		case '*':
			if idx+1 < len(pattern) && pattern[idx+1] == '*' {
				if idx+2 < len(pattern) && pattern[idx+2] == '/' {
					builder.WriteString(`(?:.*/)?`)
					idx += 2
					continue
				}
				builder.WriteString(".*")
				idx++
				continue
			}
			builder.WriteString(`[^/]*`)
		case '?':
			builder.WriteString(`[^/]`)
		default:
			builder.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	builder.WriteString("$")
	return builder.String()
}
