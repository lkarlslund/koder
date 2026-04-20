package permission

import (
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestEvaluateDefaultProfile(t *testing.T) {
	cfg := config.Default()

	if got := Evaluate(cfg.Permissions, "default", Request{Tool: domain.ToolKindRead, Pattern: "README.md"}); got.Mode != domain.PermissionModeAllow {
		t.Fatalf("unexpected read mode: %s", got)
	}
	if got := Evaluate(cfg.Permissions, "default", Request{Tool: domain.ToolKindBash, Pattern: "ls"}); got.Mode != domain.PermissionModeAsk {
		t.Fatalf("unexpected bash mode: %s", got)
	}
	if got := Evaluate(cfg.Permissions, "default", Request{Tool: domain.ToolKindRead, Pattern: "internal/domain/types.go"}); got.Mode != domain.PermissionModeAllow {
		t.Fatalf("unexpected read mode for nested path: %s", got)
	}
	if got := Evaluate(cfg.Permissions, "default", Request{Tool: domain.ToolKindBash, Pattern: `git add internal/domain/types.go && git commit -m "Update types.go" && git push`}); got.Mode != domain.PermissionModeAsk {
		t.Fatalf("unexpected bash mode for path-containing command: %s", got)
	}
}

func TestEvaluateReadonlyProfile(t *testing.T) {
	cfg := config.Default()

	if got := Evaluate(cfg.Permissions, "readonly", Request{Tool: domain.ToolKindApplyPatch, Pattern: "main.go"}); got.Mode != domain.PermissionModeDeny {
		t.Fatalf("unexpected apply_patch mode: %s", got)
	}
}

func TestEvaluateBuiltinAskMode(t *testing.T) {
	cfg := config.Default()
	got := Evaluate(cfg.Permissions, ProfileAsk, Request{Tool: domain.ToolKindRead, Pattern: "README.md"})
	if got.Mode != domain.PermissionModeAsk {
		t.Fatalf("unexpected mode: %s", got.Mode)
	}
	if got.Reason == "" {
		t.Fatal("expected approval reason")
	}
}

func TestEvaluateBuiltinReadAskMode(t *testing.T) {
	cfg := config.Default()
	projectRoot := t.TempDir()
	inside := Evaluate(cfg.Permissions, ProfileReadAsk, Request{
		Tool:        domain.ToolKindRead,
		Pattern:     "README.md",
		ProjectRoot: projectRoot,
		Targets:     []string{projectRoot + "/README.md"},
	})
	if inside.Mode != domain.PermissionModeAllow {
		t.Fatalf("expected in-project read allow, got %s", inside.Mode)
	}
	outside := Evaluate(cfg.Permissions, ProfileReadAsk, Request{
		Tool:           domain.ToolKindRead,
		Pattern:        "/tmp/README.md",
		ProjectRoot:    projectRoot,
		Targets:        []string{t.TempDir() + "/README.md"},
		OutsideProject: true,
	})
	if outside.Mode != domain.PermissionModeAsk {
		t.Fatalf("expected outside-project read ask, got %s", outside.Mode)
	}
	if outside.Reason != "target is outside the current project folder" {
		t.Fatalf("unexpected outside-project reason: %q", outside.Reason)
	}
}

func TestEvaluateBuiltinWriteAskMode(t *testing.T) {
	cfg := config.Default()
	projectRoot := t.TempDir()
	writeAllowed := Evaluate(cfg.Permissions, ProfileWriteAsk, Request{
		Tool:        domain.ToolKindWrite,
		Pattern:     "main.go",
		ProjectRoot: projectRoot,
		Targets:     []string{projectRoot + "/main.go"},
	})
	if writeAllowed.Mode != domain.PermissionModeAllow {
		t.Fatalf("expected in-project write allow, got %s", writeAllowed.Mode)
	}
	bash := Evaluate(cfg.Permissions, ProfileWriteAsk, Request{
		Tool:        domain.ToolKindBash,
		Pattern:     "pwd",
		ProjectRoot: projectRoot,
	})
	if bash.Mode != domain.PermissionModeAsk {
		t.Fatalf("expected bash ask, got %s", bash.Mode)
	}
	if bash.Reason != "shell commands require approval in this mode" {
		t.Fatalf("unexpected bash reason: %q", bash.Reason)
	}
}

func TestEvaluateBuiltinFullAccessMode(t *testing.T) {
	cfg := config.Default()
	got := Evaluate(cfg.Permissions, ProfileFullAccess, Request{Tool: domain.ToolKindBash, Pattern: "pwd"})
	if got.Mode != domain.PermissionModeAllow {
		t.Fatalf("unexpected mode: %s", got.Mode)
	}
}

func TestWildcardMatchSupportsSlashAndSpaces(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		{pattern: "*", value: "internal/domain/types.go", want: true},
		{pattern: "*", value: `git add internal/domain/types.go && git push`, want: true},
		{pattern: "internal/*.go", value: "internal/domain/types.go", want: true},
		{pattern: "git *", value: "git status", want: true},
		{pattern: "git * push", value: "git add file && git push", want: true},
		{pattern: "*.md", value: "internal/domain/types.go", want: false},
	}
	for _, tc := range tests {
		if got := wildcardMatch(tc.pattern, tc.value); got != tc.want {
			t.Fatalf("wildcardMatch(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}
