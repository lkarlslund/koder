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
