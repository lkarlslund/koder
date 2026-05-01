package permission

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

const (
	ProfileAsk        = "ask"
	ProfileReadAsk    = "read-ask"
	ProfileWriteAsk   = "write-ask"
	ProfileFullAccess = "full-access"
)

type ProfileOption struct {
	Name        string
	Label       string
	Description string
}

type Request struct {
	Tool           domain.ToolKind
	Pattern        string
	ProjectRoot    string
	Targets        []string
	OutsideProject bool
	Ambiguous      bool
}

type Decision struct {
	Mode   domain.PermissionMode
	Reason string
}

func Evaluate(cfg config.PermissionRules, profileName string, overrides []domain.PermissionOverride, req Request) Decision {
	pattern := req.Pattern
	if pattern == "" {
		pattern = "*"
	}
	for _, rule := range slices.Backward(overrides) {
		if rule.Tool != req.Tool {
			continue
		}
		if wildcardMatch(rule.Pattern, pattern) {
			return Decision{Mode: rule.Action}
		}
	}
	if IsBuiltinProfile(profileName) {
		return evaluateBuiltin(profileName, req)
	}
	profile, ok := cfg.Profiles[profileName]
	if !ok {
		profile = cfg.Profiles[cfg.Profile]
	}

	for _, rule := range slices.Backward(profile.Rules) {
		if rule.Tool != req.Tool {
			continue
		}
		if wildcardMatch(rule.Pattern, pattern) {
			return Decision{Mode: rule.Action}
		}
	}
	return Decision{Mode: domain.PermissionModeDeny}
}

func BuiltinProfiles() []ProfileOption {
	return []ProfileOption{
		{Name: ProfileAsk, Label: "ask", Description: "Ask before every permission-governed tool action"},
		{Name: ProfileReadAsk, Label: "read / ask", Description: "Allow reads in the current project, ask for everything else"},
		{Name: ProfileWriteAsk, Label: "write / ask", Description: "Allow reads and writes in the current project, ask for shell commands and anything outside"},
		{Name: ProfileFullAccess, Label: "full access", Description: "Allow all permission-governed tool actions"},
	}
}

func IsBuiltinProfile(name string) bool {
	switch strings.TrimSpace(name) {
	case ProfileAsk, ProfileReadAsk, ProfileWriteAsk, ProfileFullAccess:
		return true
	default:
		return false
	}
}

func DisplayName(name string) string {
	for _, item := range BuiltinProfiles() {
		if item.Name == strings.TrimSpace(name) {
			return item.Label
		}
	}
	if strings.TrimSpace(name) == "" {
		return "-"
	}
	return name
}

func ProfileNames(cfg config.PermissionRules) []string {
	names := make([]string, 0, len(cfg.Profiles)+len(BuiltinProfiles()))
	seen := map[string]struct{}{}
	for _, item := range BuiltinProfiles() {
		names = append(names, item.Name)
		seen[item.Name] = struct{}{}
	}
	var extra []string
	for name := range cfg.Profiles {
		if _, ok := seen[name]; ok {
			continue
		}
		extra = append(extra, name)
	}
	sort.Strings(extra)
	names = append(names, extra...)
	return names
}

func Validate(mode domain.PermissionMode) error {
	switch mode {
	case domain.PermissionModeAllow, domain.PermissionModeAsk, domain.PermissionModeDeny:
		return nil
	default:
		return fmt.Errorf("invalid permission mode %q", mode)
	}
}

func wildcardMatch(pattern, value string) bool {
	if pattern == "" {
		pattern = "*"
	}
	if pattern == "*" || pattern == "**" {
		return true
	}
	var expr strings.Builder
	expr.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			expr.WriteString(".*")
		case '?':
			expr.WriteString(".")
		default:
			expr.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	expr.WriteString("$")
	matched, err := regexp.MatchString(expr.String(), value)
	if err != nil {
		return pattern == value
	}
	return matched
}

func evaluateBuiltin(profileName string, req Request) Decision {
	switch profileName {
	case ProfileAsk:
		return Decision{Mode: domain.PermissionModeAsk, Reason: "this mode requires approval for all tool actions"}
	case ProfileReadAsk:
		if req.Tool == domain.ToolKindBash || req.Tool == domain.ToolKindExecCommand {
			return Decision{Mode: domain.PermissionModeAsk, Reason: "shell commands require approval in this mode"}
		}
		if isProjectReadTool(req.Tool) && req.targetsProjectOnly() {
			return Decision{Mode: domain.PermissionModeAllow}
		}
		return Decision{Mode: domain.PermissionModeAsk, Reason: req.reason("this mode only auto-allows reads in the current project")}
	case ProfileWriteAsk:
		if req.Tool == domain.ToolKindBash || req.Tool == domain.ToolKindExecCommand {
			return Decision{Mode: domain.PermissionModeAsk, Reason: "shell commands require approval in this mode"}
		}
		if isProjectReadOrWriteTool(req.Tool) && req.targetsProjectOnly() {
			return Decision{Mode: domain.PermissionModeAllow}
		}
		return Decision{Mode: domain.PermissionModeAsk, Reason: req.reason("this mode only auto-allows reads and writes in the current project")}
	case ProfileFullAccess:
		return Decision{Mode: domain.PermissionModeAllow}
	default:
		return Decision{Mode: domain.PermissionModeDeny}
	}
}

func isProjectReadTool(tool domain.ToolKind) bool {
	switch tool {
	case domain.ToolKindRead, domain.ToolKindGlob, domain.ToolKindGrep:
		return true
	default:
		return false
	}
}

func isProjectReadOrWriteTool(tool domain.ToolKind) bool {
	if isProjectReadTool(tool) {
		return true
	}
	switch tool {
	case domain.ToolKindApplyPatch, domain.ToolKindEdit, domain.ToolKindWrite:
		return true
	default:
		return false
	}
}

func (req Request) targetsProjectOnly() bool {
	if strings.TrimSpace(req.ProjectRoot) == "" {
		return false
	}
	if req.Ambiguous || req.OutsideProject {
		return false
	}
	if len(req.Targets) == 0 {
		return false
	}
	projectRoot := filepath.Clean(req.ProjectRoot)
	for _, target := range req.Targets {
		if strings.TrimSpace(target) == "" {
			return false
		}
		target = filepath.Clean(target)
		rel, err := filepath.Rel(projectRoot, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

func (req Request) reason(fallback string) string {
	switch {
	case req.OutsideProject:
		return "target is outside the current project folder"
	case req.Ambiguous:
		return "request target could not be classified relative to the current project folder"
	default:
		return fallback
	}
}
