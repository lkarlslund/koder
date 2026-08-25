package skilltool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

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

func (tool) ID() tools.ID             { return tools.Skill }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	spec.Usage = skills.ToolDescriptionWithOptions(spec.Usage, runtime.Workdir, skillOptions(runtime))
	return spec, true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	name := strings.TrimSpace(args["name"])
	if name == "" {
		return nil, errors.New("name is empty")
	}
	return map[string]string{"name": name}, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["name"] }
func (tool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	skill, ok := skills.FindWithOptions(runtime.Workdir, req.Args["name"], skillOptions(runtime))
	if !ok {
		return tools.Result{}, skillNotFound(runtime, req.Args["name"])
	}
	body, err := os.ReadFile(skill.Path)
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.SkillStoredResult{
		Name:      skill.Name,
		Path:      skill.Path,
		Content:   string(body),
		Truncated: false,
	}
	return tools.Result{
		Output: stored.ModelText(),
		Meta: map[string]string{
			"name":          skill.Name,
			"path":          skill.Path,
			"resource_root": skill.Directory,
		},
		Stored: stored,
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "skill", result.Output
}

func skillNotFound(runtime tools.Runtime, name string) error {
	candidates := skills.AvailableNamesWithOptions(runtime.Workdir, skillOptions(runtime))
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return fmt.Errorf("skill %q not found", name)
	}
	return fmt.Errorf("skill %q not found; available skills: %s", name, strings.Join(candidates, ", "))
}

func skillOptions(runtime tools.Runtime) skills.DiscoverOptions {
	var managed []string
	if strings.TrimSpace(runtime.ManagedSkillsDir) == "" {
		managed = nil
	} else {
		managed = []string{runtime.ManagedSkillsDir}
	}
	return skills.DiscoverOptions{
		ManagedRoots:    managed,
		DisabledPaths:   runtime.DisabledSkillPaths,
		CatalogMaxChars: runtime.SkillCatalogMaxChars,
	}
}
