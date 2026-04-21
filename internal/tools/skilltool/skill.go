package skilltool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindSkill }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(runtime tools.Runtime) (provider.ToolDefinition, bool) {
	description := skills.ToolDescription("Load a reusable local skill by name", runtime.Workdir)
	return tools.FunctionDefinition(domain.ToolKindSkill, description, `{"type":"object","properties":{"name":{"type":"string","description":"Skill name to load"}},"required":["name"],"additionalProperties":false}`), true
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
	skill, ok := skills.Find(runtime.Workdir, req.Args["name"])
	if !ok {
		return tools.Result{}, skillNotFound(runtime.Workdir, req.Args["name"])
	}
	body, err := os.ReadFile(skill.Path)
	if err != nil {
		return tools.Result{}, err
	}
	text, truncated := tools.TruncateText(string(body), tools.DefaultToolOutputLimit)
	return tools.Result{
		Output: text,
		Meta: map[string]string{
			"name":      skill.Name,
			"path":      skill.Path,
			"truncated": tools.BoolString(truncated),
		},
		Stored: tools.SkillStoredResult{
			Name:      skill.Name,
			Path:      skill.Path,
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

func skillNotFound(workdir string, name string) error {
	candidates := skills.AvailableNames(workdir)
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return fmt.Errorf("skill %q not found", name)
	}
	return fmt.Errorf("skill %q not found; available skills: %s", name, strings.Join(candidates, ", "))
}
