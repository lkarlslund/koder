package skills

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/agents"
)

const fileName = "SKILL.md"

type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
)

type Skill struct {
	Name        string
	Description string
	Path        string
	Directory   string
	Scope       Scope
}

type DiscoverOptions struct {
	UserRoots []string
}

func Discover(workdir string) []Skill {
	return DiscoverWithOptions(workdir, DiscoverOptions{})
}

func DiscoverWithOptions(workdir string, opts DiscoverOptions) []Skill {
	projectRoot := agents.FindProjectRoot(workdir)
	roots := projectRoots(workdir, projectRoot)
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, rootSpec{
			Path:  filepath.Join(home, ".agents", "skills"),
			Scope: ScopeUser,
		})
		roots = append(roots, rootSpec{
			Path:  filepath.Join(home, ".koder", "skills"),
			Scope: ScopeUser,
		})
	}
	for _, root := range opts.UserRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		roots = append(roots, rootSpec{
			Path:  root,
			Scope: ScopeUser,
		})
	}

	seen := map[string]struct{}{}
	out := make([]Skill, 0, len(roots))
	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		slices.SortFunc(entries, func(a, b os.DirEntry) int {
			return strings.Compare(a.Name(), b.Name())
		})
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillPath := filepath.Join(root.Path, entry.Name(), fileName)
			skill, ok := loadSkill(skillPath, root.Scope, entry.Name())
			if !ok {
				continue
			}
			key := normalizeName(skill.Name)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, skill)
		}
	}
	return out
}

func Find(workdir string, name string) (Skill, bool) {
	return FindWithOptions(workdir, name, DiscoverOptions{})
}

func FindWithOptions(workdir string, name string, opts DiscoverOptions) (Skill, bool) {
	needle := normalizeName(name)
	for _, skill := range DiscoverWithOptions(workdir, opts) {
		if normalizeName(skill.Name) == needle {
			return skill, true
		}
	}
	return Skill{}, false
}

func AvailableNames(workdir string) []string {
	return AvailableNamesWithOptions(workdir, DiscoverOptions{})
}

func AvailableNamesWithOptions(workdir string, opts DiscoverOptions) []string {
	items := DiscoverWithOptions(workdir, opts)
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names
}

func ToolDescription(base string, workdir string) string {
	return ToolDescriptionWithOptions(base, workdir, DiscoverOptions{})
}

func ToolDescriptionWithOptions(base string, workdir string, opts DiscoverOptions) string {
	items := DiscoverWithOptions(workdir, opts)
	if len(items) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(base))
	b.WriteString("\n\n<available_skills>")
	for _, item := range items {
		b.WriteString("\n<skill>")
		b.WriteString("\n<name>")
		b.WriteString(item.Name)
		b.WriteString("</name>")
		if desc := strings.TrimSpace(item.Description); desc != "" {
			b.WriteString("\n<description>")
			b.WriteString(truncate(desc, 280))
			b.WriteString("</description>")
		}
		b.WriteString("\n</skill>")
	}
	b.WriteString("\n</available_skills>")
	return b.String()
}

func PromptContext(workdir string) string {
	return PromptContextWithOptions(workdir, DiscoverOptions{})
}

func PromptContextWithOptions(workdir string, opts DiscoverOptions) string {
	items := DiscoverWithOptions(workdir, opts)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Available skills:\n")
	b.WriteString("- Users may explicitly request a skill by writing $skill-name in their prompt.\n")
	b.WriteString("- When a user explicitly requests a listed skill, call the skill tool with that exact skill name.\n")
	b.WriteString("- Skills are lazy-loaded; use the listing below to decide when to load one.\n")
	b.WriteString("<available_skills>")
	for _, item := range items {
		b.WriteString("\n<skill>")
		b.WriteString("\n<name>")
		b.WriteString(item.Name)
		b.WriteString("</name>")
		if desc := strings.TrimSpace(item.Description); desc != "" {
			b.WriteString("\n<description>")
			b.WriteString(truncate(desc, 280))
			b.WriteString("</description>")
		}
		b.WriteString("\n</skill>")
	}
	b.WriteString("\n</available_skills>")
	return b.String()
}

type rootSpec struct {
	Path  string
	Scope Scope
}

func projectRoots(workdir string, projectRoot string) []rootSpec {
	var roots []rootSpec
	current := cleanPath(workdir)
	projectRoot = cleanPath(projectRoot)
	for {
		roots = append(roots, rootSpec{
			Path:  filepath.Join(current, ".agents", "skills"),
			Scope: ScopeProject,
		})
		if current == projectRoot {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return roots
}

func loadSkill(path string, scope Scope, fallbackName string) (Skill, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, false
	}
	name, description := parseFrontmatter(string(body))
	if strings.TrimSpace(name) == "" {
		name = fallbackName
	}
	skill := Skill{
		Name:        normalizeName(name),
		Description: strings.TrimSpace(description),
		Path:        cleanPath(path),
		Directory:   cleanPath(filepath.Dir(path)),
		Scope:       scope,
	}
	if skill.Name == "" {
		return Skill{}, false
	}
	return skill, true
}

func parseFrontmatter(body string) (string, string) {
	scanner := bufio.NewScanner(strings.NewReader(body))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", ""
	}
	var (
		name        string
		description string
	)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.ToLower(key))
		value = cleanFrontmatterValue(value)
		switch key {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

func cleanFrontmatterValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return strings.TrimSpace(value)
}

func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, " ", "-")
	return name
}

func cleanPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func truncate(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxLen || maxLen <= 3 {
		return value
	}
	return strings.TrimSpace(value[:maxLen-3]) + "..."
}

func DebugString(items []Skill) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%s:%s", item.Scope, item.Name))
	}
	return strings.Join(parts, ", ")
}
