package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartupOptionsResolveDefaultsToCurrentDirectory(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if chdirErr := os.Chdir(originalWD); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	})

	workdir := t.TempDir()
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, err := (startupOptions{}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != workdir {
		t.Fatalf("expected %q, got %q", workdir, got)
	}
}

func TestStartupOptionsResolveRelativeCWD(t *testing.T) {
	base := t.TempDir()
	workdir := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if chdirErr := os.Chdir(originalWD); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	})
	if err := os.Chdir(base); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, err := (startupOptions{cwd: "workspace"}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != workdir {
		t.Fatalf("expected %q, got %q", workdir, got)
	}
}

func TestStartupOptionsResolveProjectRootAlias(t *testing.T) {
	workdir := t.TempDir()

	got, err := (startupOptions{projectRoot: workdir}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != workdir {
		t.Fatalf("expected %q, got %q", workdir, got)
	}
}

func TestStartupOptionsResolveRejectsConflictingAliases(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()

	_, err := (startupOptions{cwd: first, projectRoot: second}).resolve()
	if err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestStartupOptionsResolveRejectsFilePath(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := (startupOptions{cwd: file}).resolve()
	if err == nil {
		t.Fatal("expected directory error")
	}
}
