package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/skills"
)

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

			opts, err := skillDiscoverOptions(root)
			if err != nil {
				return err
			}
			catalog := skills.InspectWithOptions(dir, opts)
			var output strings.Builder

			if len(catalog.Items) == 0 {
				output.WriteString("No skills found.\n\nSearched:\n")
				for _, root := range catalog.Roots {
					status := "not found"
					if root.Exists {
						status = "exists (no skills)"
					}
					_, _ = fmt.Fprintf(&output, "  [%s] %s (%s)\n", root.Scope, root.Path, status)
				}
				output.WriteString("\nTo create a skill, place a directory with SKILL.md under one of the paths above.\n")
				output.WriteString("User skills go in ~/.agents/skills/<name>/SKILL.md\n")
				_, err := io.WriteString(cmd.OutOrStdout(), output.String())
				return err
			}

			for _, skill := range catalog.Items {
				status := "active"
				switch {
				case !skill.Valid:
					status = "invalid: " + skill.Error
				case !skill.Enabled:
					status = "disabled"
				case !skill.Effective:
					status = "shadowed by " + skill.ShadowedBy
				}
				_, _ = fmt.Fprintf(&output, "[%s] %s (%s)\n       %s\n       %s\n", skill.Scope, skill.Name, status, skill.Description, skill.Directory)
				for _, warning := range skill.Warnings {
					_, _ = fmt.Fprintf(&output, "       warning: %s\n", warning)
				}
			}

			usedPaths := make(map[string]bool)
			for _, skill := range catalog.Items {
				usedPaths[skill.Root] = true
			}
			for _, root := range catalog.Roots {
				if !usedPaths[root.Path] {
					if !root.Exists {
						_, _ = fmt.Fprintf(&output, "\nSkipped: %s (not found)\n", root.Path)
					}
				}
			}

			_, err = io.WriteString(cmd.OutOrStdout(), output.String())
			return err
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
		ManagedRoots:    []string{filepath.Join(cfg.ManagedAssetsDir(), "skills")},
		DisabledPaths:   cfg.Skills.Disabled,
		CatalogMaxChars: cfg.Skills.CatalogMaxChars,
	}, nil
}

// validateSkill validates a SKILL.md at the given path.
// The path can be a SKILL.md file or a skill directory containing SKILL.md.
func validateSkill(path string) error {
	skill, err := skills.InspectFile(path)
	if err != nil {
		return fmt.Errorf("invalid skill: %w", err)
	}
	fmt.Fprintf(os.Stderr, "OK: %s\n", skill.Path)
	fmt.Fprintf(os.Stderr, "  name:        %s\n", skill.Name)
	fmt.Fprintf(os.Stderr, "  display:     %s\n", skill.Presentation.DisplayName)
	fmt.Fprintf(os.Stderr, "  description: %s\n", skill.Description)
	if skill.License != "" {
		fmt.Fprintf(os.Stderr, "  license:     %s\n", skill.License)
	}
	if skill.Compatibility != "" {
		fmt.Fprintf(os.Stderr, "  compatibility: %s\n", skill.Compatibility)
	}
	if skill.Presentation.LogoPath != "" {
		fmt.Fprintf(os.Stderr, "  logo:        %s\n", skill.Presentation.LogoPath)
	}
	for _, warning := range skill.Warnings {
		fmt.Fprintf(os.Stderr, "  warning:     %s\n", warning)
	}
	return nil
}
