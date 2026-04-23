package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoANSIImportsInProductionUIOrDialogs(t *testing.T) {
	root := repoRoot(t)
	paths := []string{
		filepath.Join(root, "internal/ui"),
		filepath.Join(root, "internal/tui/dialogs"),
	}
	for _, base := range paths {
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			text := string(raw)
			if strings.Contains(text, `github.com/charmbracelet/x/ansi`) {
				t.Fatalf("unexpected x/ansi import in %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoStyledStringRenderingInDialogs(t *testing.T) {
	root := repoRoot(t)
	base := filepath.Join(root, "internal/tui/dialogs")
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(raw)
		for _, forbidden := range []string{
			".View(",
			"lipgloss.JoinHorizontal(",
			"lipgloss.JoinVertical(",
			"lipgloss.NewStyle().",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("unexpected %q usage in %s", forbidden, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(dir, "..", ".."))
}
