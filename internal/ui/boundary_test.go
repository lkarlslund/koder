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

func TestNoRenderToInProductionUIOrDialogs(t *testing.T) {
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
			if strings.Contains(string(raw), "RenderTo(") {
				t.Fatalf("unexpected RenderTo usage in %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoLegacyRenderMethodsOrCallsInProductionUIOrTUI(t *testing.T) {
	root := repoRoot(t)
	paths := []string{
		filepath.Join(root, "internal/ui"),
		filepath.Join(root, "internal/tui"),
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
			if strings.Contains(text, "func (") && strings.Contains(text, ") Render(") {
				t.Fatalf("unexpected legacy Render method in %s", path)
			}
			lines := strings.Split(text, "\n")
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if !strings.Contains(trimmed, ".Render(") {
					continue
				}
				t.Fatalf("unexpected legacy Render call in %s: %s", path, trimmed)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoElementBridgeInProductionUIOrTUI(t *testing.T) {
	root := repoRoot(t)
	paths := []string{
		filepath.Join(root, "internal/ui"),
		filepath.Join(root, "internal/tui"),
	}
	forbidden := []string{
		"type Element interface",
		"ui.Element",
		"PaintElementSurface(",
		"renderElementInto(",
		"InvalidateElementCaches(",
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
			for _, token := range forbidden {
				if strings.Contains(text, token) {
					t.Fatalf("unexpected %q in %s", token, path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoStyledStringRenderingInTextarea(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "internal/ui/textarea/textarea.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{
		".Render(",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unexpected %q usage in %s", forbidden, path)
		}
	}
}

func TestNoLipglossInProductionUIOrTUI(t *testing.T) {
	root := repoRoot(t)
	paths := []string{
		filepath.Join(root, "internal/ui"),
		filepath.Join(root, "internal/tui"),
		filepath.Join(root, "internal/theme"),
		filepath.Join(root, "internal/markdown"),
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
			if strings.Contains(text, "github.com/charmbracelet/lipgloss") || strings.Contains(text, "lipgloss.") {
				t.Fatalf("unexpected lipgloss usage in %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
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
