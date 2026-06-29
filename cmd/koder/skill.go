package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/agents"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/skills"
)

const maxSkillNameLen = 64
const maxDescriptionLen = 1024

func newSkillCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage koder skills",
	}
	cmd.AddCommand(newSkillValidateCommand(), newSkillVerifyCommand(root), newSkillListCommand(root))
	return cmd
}

// newSkillValidateCommand returns `koder skill validate <path>`.
func newSkillValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <path>",
		Short: "Validate a skill's SKILL.md",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return validateSkill(args[0])
		},
	}
}

// newSkillVerifyCommand returns `koder skill verify <name>`.
func newSkillVerifyCommand(root *rootOptions) *cobra.Command {
	var workdir string
	verifyCmd := &cobra.Command{
		Use:   "verify <name>",
		Short: "Verify a known skill by name through discovery",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir := strings.TrimSpace(workdir)
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			opts, err := skillDiscoverOptions(root)
			if err != nil {
				return err
			}
			sk, found := skills.FindWithOptions(dir, args[0], opts)
			if !found {
				return fmt.Errorf("skill %q not found; run 'koder skill list' to see available skills", args[0])
			}
			fmt.Fprintf(os.Stderr, "Found skill %q at %s\n", sk.Name, sk.Path)
			return validateSkill(sk.Path)
		},
	}
	verifyCmd.Flags().StringVar(&workdir, "workdir", "", "Working directory for skill discovery (default: $PWD)")
	return verifyCmd
}

// discoveryPath holds one directory koder searches for skills.
type discoveryPath struct {
	path  string
	scope skills.Scope
}

// collectDiscoveryPaths returns the directories koder would search
// for skills, matching the logic in skills.Discover.
func collectDiscoveryPaths(workdir string, opts skills.DiscoverOptions) []discoveryPath {
	projectRoot := agents.FindProjectRoot(workdir)
	workdir = cleanPathAbs(workdir)
	projectRoot = cleanPathAbs(projectRoot)

	var out []discoveryPath
	current := workdir
	for {
		out = append(out, discoveryPath{
			path:  filepath.Join(current, ".agents", "skills"),
			scope: skills.ScopeProject,
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

	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		out = append(out, discoveryPath{
			path:  filepath.Join(home, ".agents", "skills"),
			scope: skills.ScopeUser,
		})
		out = append(out, discoveryPath{
			path:  filepath.Join(home, ".koder", "skills"),
			scope: skills.ScopeUser,
		})
	}
	for _, root := range opts.UserRoots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		out = append(out, discoveryPath{
			path:  root,
			scope: skills.ScopeUser,
		})
	}

	return out
}

// newSkillListCommand returns `koder skill list`.
func newSkillListCommand(root *rootOptions) *cobra.Command {
	var workdir string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List discovered skills",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := strings.TrimSpace(workdir)
			if dir == "" {
				var err error
				dir, err = os.Getwd()
				if err != nil {
					return err
				}
			}

			out := cmd.OutOrStdout()
			opts, err := skillDiscoverOptions(root)
			if err != nil {
				return err
			}
			items := skills.DiscoverWithOptions(dir, opts)
			paths := collectDiscoveryPaths(dir, opts)

			if len(items) == 0 {
				fmt.Fprintln(out, "No skills found.")
				fmt.Fprintln(out)
				fmt.Fprintln(out, "Searched:")
				for _, p := range paths {
					exists := dirExists(p.path)
					status := "not found"
					if exists {
						status = "exists (no skills)"
					}
					fmt.Fprintf(out, "  [%s] %s (%s)\n", p.scope, p.path, status)
				}
				fmt.Fprintln(out)
				fmt.Fprintln(out, "To create a skill, place a directory with SKILL.md under one of the paths above.")
				fmt.Fprintln(out, "User skills go in ~/.agents/skills/<name>/SKILL.md")
				return nil
			}

			// Print found skills grouped by path
			for _, s := range items {
				scope := string(s.Scope)
				fmt.Fprintf(out, "[%s] %s\n", scope, s.Name)
				fmt.Fprintf(out, "       %s\n", s.Description)
				fmt.Fprintf(out, "       %s\n", s.Directory)
			}

			// Also show paths that were searched but contributed nothing
			usedPaths := make(map[string]bool)
			for _, s := range items {
				usedPaths[filepath.Dir(filepath.Dir(s.Path))] = true
			}
			for _, p := range paths {
				if !usedPaths[p.path] {
					exists := dirExists(p.path)
					if !exists {
						fmt.Fprintf(out, "\nSkipped: %s (not found)\n", p.path)
					}
				}
			}

			return nil
		},
	}
	listCmd.Flags().StringVar(&workdir, "workdir", "", "Working directory for skill discovery (default: $PWD)")
	return listCmd
}

func skillDiscoverOptions(root *rootOptions) (skills.DiscoverOptions, error) {
	if root == nil {
		root = &rootOptions{}
	}
	cfg, err := config.LoadWithOptions(root.loadOptions())
	if err != nil {
		return skills.DiscoverOptions{}, err
	}
	return skills.DiscoverOptions{
		UserRoots: []string{filepath.Join(cfg.ManagedAssetsDir(), "skills")},
	}, nil
}

// validateSkill validates a SKILL.md at the given path.
// The path can be a SKILL.md file or a skill directory containing SKILL.md.
func validateSkill(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return err
	}

	if info.IsDir() {
		abs = filepath.Join(abs, "SKILL.md")
	}

	if !strings.HasSuffix(strings.ToLower(abs), ".md") {
		return fmt.Errorf("path must be a SKILL.md file or a skill directory: %s", abs)
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	var issues []string
	name, description := parseSkillFrontmatter(string(body))

	// Check frontmatter exists
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return fmt.Errorf("invalid SKILL.md: missing YAML frontmatter (file must start with ---)")
	}

	// Validate name
	if strings.TrimSpace(name) == "" {
		issues = append(issues, "missing 'name' in frontmatter")
	} else {
		name = strings.TrimSpace(name)
		if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(name) {
			issues = append(issues, fmt.Sprintf("name %q should be hyphen-case (lowercase letters, digits, and hyphens only)", name))
		} else if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
			issues = append(issues, fmt.Sprintf("name %q cannot start or end with a hyphen", name))
		} else if strings.Contains(name, "--") {
			issues = append(issues, fmt.Sprintf("name %q cannot contain consecutive hyphens", name))
		} else if len(name) > maxSkillNameLen {
			issues = append(issues, fmt.Sprintf("name is too long (%d characters); maximum is %d", len(name), maxSkillNameLen))
		}
	}

	// Validate description
	if strings.TrimSpace(description) == "" {
		issues = append(issues, "missing 'description' in frontmatter")
	} else {
		desc := strings.TrimSpace(description)
		if strings.ContainsAny(desc, "<>") {
			issues = append(issues, "description cannot contain angle brackets (< or >)")
		}
		if len(desc) > maxDescriptionLen {
			issues = append(issues, fmt.Sprintf("description is too long (%d characters); maximum is %d", len(desc), maxDescriptionLen))
		}
	}

	if len(issues) > 0 {
		fmt.Fprintf(os.Stderr, "skill validation failed for %s:\n", abs)
		for _, issue := range issues {
			fmt.Fprintf(os.Stderr, "  - %s\n", issue)
		}
		return fmt.Errorf("validation failed with %d issue(s)", len(issues))
	}

	fmt.Fprintf(os.Stderr, "OK: %s\n", abs)
	fmt.Fprintf(os.Stderr, "  name:        %s\n", strings.TrimSpace(name))
	fmt.Fprintf(os.Stderr, "  description: %s\n", strings.TrimSpace(description))
	return nil
}

// parseSkillFrontmatter mirrors the logic in internal/skills/skills.go
func parseSkillFrontmatter(body string) (string, string) {
	scanner := bufio.NewScanner(strings.NewReader(body))
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", ""
	}
	var name, description string
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
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

// dirExists reports whether the path is an existing directory.
func dirExists(p string) bool {
	info, err := os.Stat(p)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// cleanPathAbs returns the absolute clean path or the original on error.
func cleanPathAbs(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(abs)
}
