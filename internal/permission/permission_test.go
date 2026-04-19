package permission

import (
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestEvaluateDefaultProfile(t *testing.T) {
	cfg := config.Default()

	if got := Evaluate(cfg.Permissions, "default", Request{Tool: domain.ToolKindRead, Pattern: "README.md"}); got != domain.PermissionModeAllow {
		t.Fatalf("unexpected read mode: %s", got)
	}
	if got := Evaluate(cfg.Permissions, "default", Request{Tool: domain.ToolKindBash, Pattern: "ls"}); got != domain.PermissionModeAsk {
		t.Fatalf("unexpected bash mode: %s", got)
	}
}

func TestEvaluateReadonlyProfile(t *testing.T) {
	cfg := config.Default()

	if got := Evaluate(cfg.Permissions, "readonly", Request{Tool: domain.ToolKindApplyPatch, Pattern: "main.go"}); got != domain.PermissionModeDeny {
		t.Fatalf("unexpected apply_patch mode: %s", got)
	}
}
