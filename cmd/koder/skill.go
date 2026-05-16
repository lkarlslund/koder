package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/skills"
)

const maxSkillNameLen = 64
const maxDescriptionLen = 1024

func newSkillCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage koder skills",
	}
	cmd.AddCommand(newSkillValidateCommand(), newSkillListCommand())
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

// newSkillListCommand returns `koder skill list`.
func newSkillListCommand() *cobra.Command {
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
			items := skills.Discover(dir)
			if len(items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No skills found.")
				return nil
			}
			for _, s := range items {
				scope := string(s.Scope)
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s - %s\n", scope, s.Name, s.Description)
			}
			return nil
		},
	}
	listCmd.Flags().StringVar(&workdir, "workdir", "", "Working directory for skill discovery (default: $PWD)")
	return listCmd
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
