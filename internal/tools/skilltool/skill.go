package skilltool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Load skill",
		Description: "Load a reusable local skill by name.",
		Usage:       "Load a reusable local skill by name",
		Parameters:  `{"type":"object","properties":{"name":{"type":"string","description":"Skill name to load"}},"required":["name"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindSkill }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	spec.Usage = skills.ToolDescription(spec.Usage, runtime.Workdir)
	return spec, true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	name := strings.TrimSpace(tools.FirstArg(args, "name", "skill_name", "skill"))
	if name == "" {
		return nil, errors.New("name is empty")
	}
	return map[string]string{"name": name}, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["name"] }
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	skill, ok := skills.Find(runtime.Workdir, req.Args["name"])
	if !ok {
		return tools.Result{}, skillNotFound(runtime.Workdir, req.Args["name"])
	}
	body, err := os.ReadFile(skill.Path)
	if err != nil {
		return tools.Result{}, err
	}
	return tools.Result{
		Output: string(body),
		Meta: map[string]string{
			"name": skill.Name,
			"path": skill.Path,
		},
		Stored: tools.SkillStoredResult{
			Name:      skill.Name,
			Path:      skill.Path,
			Content:   string(body),
			Truncated: false,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "skill", result.Output
}

func skillNotFound(workdir string, name string) error {
	candidates := skills.AvailableNames(workdir)
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return fmt.Errorf("skill %q not found", name)
	}
	return fmt.Errorf("skill %q not found; available skills: %s", name, strings.Join(candidates, ", "))
}
