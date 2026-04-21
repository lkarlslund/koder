package skilltool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindSkill }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition() (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindSkill, "Load a reusable local skill by name", `{"type":"object","properties":{"name":{"type":"string","description":"Skill name to load"}},"required":["name"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	name := strings.TrimSpace(tools.FirstArg(args, "name", "skill_name", "skill"))
	if name == "" {
		return nil, errors.New("name is empty")
	}
	return map[string]string{"name": name}, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"name": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["name"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: "Load skill", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["name"])
}
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	path, err := findSkill(runtime.Workdir, req.Args["name"])
	if err != nil {
		return tools.Result{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return tools.Result{}, err
	}
	text, truncated := tools.TruncateText(string(body), tools.DefaultToolOutputLimit)
	return tools.Result{
		Output: text,
		Meta: map[string]string{
			"name":      req.Args["name"],
			"path":      path,
			"truncated": tools.BoolString(truncated),
		},
		Stored: tools.SkillStoredResult{
			Name:      req.Args["name"],
			Path:      path,
			Content:   text,
			Truncated: truncated,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "skill", result.Output
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func findSkill(workdir string, name string) (string, error) {
	roots := []string{
		filepath.Join(workdir, "skills"),
		filepath.Join(workdir, ".koder", "skills"),
	}
	if configDir, err := os.UserConfigDir(); err == nil {
		roots = append(roots, filepath.Join(configDir, "koder", "skills"))
	}
	normalized := normalizeName(name)
	var candidates []string
	seen := map[string]struct{}{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if normalizeName(entry.Name()) != normalized {
				continue
			}
			path := filepath.Join(root, entry.Name(), "SKILL.md")
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, ok := seen[entry.Name()]; ok {
				continue
			}
			seen[entry.Name()] = struct{}{}
			candidates = append(candidates, entry.Name())
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return "", fmt.Errorf("skill %q not found", name)
	}
	return "", fmt.Errorf("skill %q not found; available skills: %s", name, strings.Join(candidates, ", "))
}

func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, " ", "-")
	return name
}
