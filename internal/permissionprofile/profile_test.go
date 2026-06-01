package permissionprofile

import (
	"fmt"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/domain"
)

func testRules() Rules {
	return Rules{
		Profile: "default",
		Profiles: map[string]Profile{
			"default": {
				Rules: []Rule{
					{Tool: domain.ToolKindFileRead, Pattern: "*", Action: accesssettings.PermissionModeAllow},
					{Tool: domain.ToolKindBash, Pattern: "*", Action: accesssettings.PermissionModeAsk},
					{Tool: domain.ToolKindFileWrite, Pattern: "*", Action: accesssettings.PermissionModeAsk},
				},
			},
			"readonly": {
				Rules: []Rule{
					{Tool: domain.ToolKindFileRead, Pattern: "*", Action: accesssettings.PermissionModeAllow},
				},
			},
		},
	}
}

func TestEvaluateDefaultProfile(t *testing.T) {
	cfg := testRules()

	if got := Evaluate(cfg, "default", nil, Request{Tool: domain.ToolKindFileRead, Pattern: "README.md"}); got.Mode != accesssettings.PermissionModeAllow {
		t.Fatalf("unexpected read mode: %s", got)
	}
	if got := Evaluate(cfg, "default", nil, Request{Tool: domain.ToolKindBash, Pattern: "ls"}); got.Mode != accesssettings.PermissionModeAsk {
		t.Fatalf("unexpected bash mode: %s", got)
	}
	if got := Evaluate(cfg, "default", nil, Request{Tool: domain.ToolKindFileRead, Pattern: "internal/domain/types.go"}); got.Mode != accesssettings.PermissionModeAllow {
		t.Fatalf("unexpected read mode for nested path: %s", got)
	}
	if got := Evaluate(cfg, "default", nil, Request{Tool: domain.ToolKindBash, Pattern: `git add internal/domain/types.go && git commit -m "Update types.go" && git push`}); got.Mode != accesssettings.PermissionModeAsk {
		t.Fatalf("unexpected bash mode for path-containing command: %s", got)
	}
}

func TestEvaluateReadonlyProfile(t *testing.T) {
	cfg := testRules()

	if got := Evaluate(cfg, "readonly", nil, Request{Tool: domain.ToolKindFileWrite, Pattern: "main.go"}); got.Mode != accesssettings.PermissionModeDeny {
		t.Fatalf("unexpected write mode: %s", got)
	}
}

func TestEvaluateBuiltinFullAccessMode(t *testing.T) {
	cfg := testRules()
	got := Evaluate(cfg, ProfileFullAccess, nil, Request{Tool: domain.ToolKindBash, Pattern: "pwd"})
	if got.Mode != accesssettings.PermissionModeAllow {
		t.Fatalf("unexpected mode: %s", got.Mode)
	}
}

func TestEvaluateSessionOverridesTakePrecedence(t *testing.T) {
	cfg := testRules()
	got := Evaluate(cfg, ProfileAsk, []accesssettings.PermissionOverride{{
		Tool:    domain.ToolKindBash,
		Pattern: "git *",
		Action:  accesssettings.PermissionModeAllow,
	}}, Request{Tool: domain.ToolKindBash, Pattern: "git status"})
	if got.Mode != accesssettings.PermissionModeAllow {
		t.Fatalf("expected override allow, got %s", got.Mode)
	}
}

func TestEvaluateProfileMatchesToolWildcards(t *testing.T) {
	cfg := Rules{
		Profile: "custom",
		Profiles: map[string]Profile{
			"custom": {
				Rules: []Rule{
					{Tool: domain.ToolKindExecCommand, Pattern: "*", Action: accesssettings.PermissionModeAsk},
					{Tool: domain.ToolKindMCP, Pattern: "*", Action: accesssettings.PermissionModeAllow},
				},
			},
		},
	}
	if got := Evaluate(cfg, "custom", nil, Request{Tool: domain.ToolKindExecCommand, Pattern: "tty"}); got.Mode != accesssettings.PermissionModeAsk {
		t.Fatalf("expected tool ask, got %s", got.Mode)
	}
	if got := Evaluate(cfg, "custom", nil, Request{Tool: domain.ToolKindMCP, Pattern: "anything"}); got.Mode != accesssettings.PermissionModeAllow {
		t.Fatalf("expected custom tool allow, got %s", got.Mode)
	}
}

func TestProfileNamesPreferConfiguredProfilesBeforeBuiltinExtras(t *testing.T) {
	cfg := Rules{
		Profiles: map[string]Profile{
			"default":  {},
			"readonly": {},
			"auto":     {},
		},
	}

	got := ProfileNames(cfg)
	want := []string{"auto", "default", "readonly", "full-access"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("expected names %v, got %v", want, got)
	}
}

func TestConfiguredProfileDescriptionSummarizesRules(t *testing.T) {
	cfg := Rules{
		Profiles: map[string]Profile{
			"default": {
				Rules: []Rule{
					{Tool: domain.ToolKindFileRead, Pattern: "*", Action: accesssettings.PermissionModeAllow},
					{Tool: domain.ToolKindBash, Pattern: "*", Action: accesssettings.PermissionModeAsk},
					{Tool: domain.ToolKindFileWrite, Pattern: "*", Action: accesssettings.PermissionModeDeny},
				},
			},
		},
	}

	if got := Description("default", cfg); got != "1 allow, 1 ask, 1 deny" {
		t.Fatalf("unexpected configured profile description: %q", got)
	}
	if got := Description(ProfileFullAccess, cfg); got != "Network on, root readwrite, workspace readwrite" {
		t.Fatalf("unexpected builtin profile description: %q", got)
	}
}

func TestSandboxProfileDescription(t *testing.T) {
	cfg := Rules{
		Profiles: map[string]Profile{
			"default": {Root: string(ModeReadOnly), Workspace: string(ModeReadWrite)},
			"dev":     {Network: true, Root: string(ModeReadOnly), Workspace: string(ModeReadOnly)},
		},
	}
	if got := Description("default", cfg); got != "network off, root readonly, workspace readwrite" {
		t.Fatalf("unexpected default description: %q", got)
	}
	if got := Description("dev", cfg); got != "network on, root readonly, workspace readonly" {
		t.Fatalf("unexpected dev description: %q", got)
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
