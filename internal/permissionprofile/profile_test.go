package permissionprofile

import (
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
)

func testRules() Rules {
	return Rules{
		Profile: "default",
		Profiles: map[string]Profile{
			"default": {
				Rules: []Rule{
					{Tool: domain.ToolKindRead, Pattern: "*", Action: domain.PermissionModeAllow},
					{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeAsk},
					{Tool: domain.ToolKindApplyPatch, Pattern: "*", Action: domain.PermissionModeAsk},
				},
			},
			"readonly": {
				Rules: []Rule{
					{Tool: domain.ToolKindRead, Pattern: "*", Action: domain.PermissionModeAllow},
				},
			},
		},
	}
}

func TestEvaluateDefaultProfile(t *testing.T) {
	cfg := testRules()

	if got := Evaluate(cfg, "default", nil, Request{Tool: domain.ToolKindRead, Pattern: "README.md"}); got.Mode != domain.PermissionModeAllow {
		t.Fatalf("unexpected read mode: %s", got)
	}
	if got := Evaluate(cfg, "default", nil, Request{Tool: domain.ToolKindBash, Pattern: "ls"}); got.Mode != domain.PermissionModeAsk {
		t.Fatalf("unexpected bash mode: %s", got)
	}
	if got := Evaluate(cfg, "default", nil, Request{Tool: domain.ToolKindRead, Pattern: "internal/domain/types.go"}); got.Mode != domain.PermissionModeAllow {
		t.Fatalf("unexpected read mode for nested path: %s", got)
	}
	if got := Evaluate(cfg, "default", nil, Request{Tool: domain.ToolKindBash, Pattern: `git add internal/domain/types.go && git commit -m "Update types.go" && git push`}); got.Mode != domain.PermissionModeAsk {
		t.Fatalf("unexpected bash mode for path-containing command: %s", got)
	}
}

func TestEvaluateReadonlyProfile(t *testing.T) {
	cfg := testRules()

	if got := Evaluate(cfg, "readonly", nil, Request{Tool: domain.ToolKindApplyPatch, Pattern: "main.go"}); got.Mode != domain.PermissionModeDeny {
		t.Fatalf("unexpected apply_patch mode: %s", got)
	}
}

func TestEvaluateBuiltinAskMode(t *testing.T) {
	cfg := testRules()
	got := Evaluate(cfg, ProfileAsk, nil, Request{Tool: domain.ToolKindRead, Pattern: "README.md"})
	if got.Mode != domain.PermissionModeAsk {
		t.Fatalf("unexpected mode: %s", got.Mode)
	}
	if got.Reason == "" {
		t.Fatal("expected approval reason")
	}
}

func TestEvaluateBuiltinReadAskMode(t *testing.T) {
	cfg := testRules()
	projectRoot := t.TempDir()
	inside := Evaluate(cfg, ProfileReadAsk, nil, Request{
		Tool:        domain.ToolKindRead,
		Access:      AccessRead,
		Pattern:     "README.md",
		ProjectRoot: projectRoot,
		Targets:     []string{projectRoot + "/README.md"},
	})
	if inside.Mode != domain.PermissionModeAllow {
		t.Fatalf("expected in-project read allow, got %s", inside.Mode)
	}
	codeSearch := Evaluate(cfg, ProfileReadAsk, nil, Request{
		Tool:        domain.ToolKindCodeSearch,
		Access:      AccessRead,
		Pattern:     "RunPrompt",
		ProjectRoot: projectRoot,
		Targets:     []string{projectRoot},
	})
	if codeSearch.Mode != domain.PermissionModeAllow {
		t.Fatalf("expected in-project code search allow, got %s", codeSearch.Mode)
	}
	outside := Evaluate(cfg, ProfileReadAsk, nil, Request{
		Tool:           domain.ToolKindRead,
		Access:         AccessRead,
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
	cfg := testRules()
	projectRoot := t.TempDir()
	writeAllowed := Evaluate(cfg, ProfileWriteAsk, nil, Request{
		Tool:        domain.ToolKindWrite,
		Access:      AccessWrite,
		Pattern:     "main.go",
		ProjectRoot: projectRoot,
		Targets:     []string{projectRoot + "/main.go"},
	})
	if writeAllowed.Mode != domain.PermissionModeAllow {
		t.Fatalf("expected in-project write allow, got %s", writeAllowed.Mode)
	}
	bash := Evaluate(cfg, ProfileWriteAsk, nil, Request{
		Tool:        domain.ToolKindBash,
		Access:      AccessShell,
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
	cfg := testRules()
	got := Evaluate(cfg, ProfileFullAccess, nil, Request{Tool: domain.ToolKindBash, Pattern: "pwd"})
	if got.Mode != domain.PermissionModeAllow {
		t.Fatalf("unexpected mode: %s", got.Mode)
	}
}

func TestEvaluateSessionOverridesTakePrecedence(t *testing.T) {
	cfg := testRules()
	got := Evaluate(cfg, ProfileAsk, []domain.PermissionOverride{{
		Tool:    domain.ToolKindBash,
		Pattern: "git *",
		Action:  domain.PermissionModeAllow,
	}}, Request{Tool: domain.ToolKindBash, Pattern: "git status"})
	if got.Mode != domain.PermissionModeAllow {
		t.Fatalf("expected override allow, got %s", got.Mode)
	}
}

func TestEvaluateProfileMatchesToolWildcards(t *testing.T) {
	cfg := Rules{
		Profile: "custom",
		Profiles: map[string]Profile{
			"custom": {
				Rules: []Rule{
					{Tool: domain.ToolKind("exec_*"), Pattern: "*", Action: domain.PermissionModeAsk},
					{Tool: domain.ToolKind("custom_vendor_tool"), Pattern: "*", Action: domain.PermissionModeAllow},
				},
			},
		},
	}
	if got := Evaluate(cfg, "custom", nil, Request{Tool: domain.ToolKind("exec_resize"), Pattern: "tty"}); got.Mode != domain.PermissionModeAsk {
		t.Fatalf("expected wildcard tool ask, got %s", got.Mode)
	}
	if got := Evaluate(cfg, "custom", nil, Request{Tool: domain.ToolKind("custom_vendor_tool"), Pattern: "anything"}); got.Mode != domain.PermissionModeAllow {
		t.Fatalf("expected custom tool allow, got %s", got.Mode)
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
