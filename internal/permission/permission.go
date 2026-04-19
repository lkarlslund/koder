package permission

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

type Request struct {
	Tool    domain.ToolKind
	Pattern string
}

func Evaluate(cfg config.PermissionRules, profileName string, req Request) domain.PermissionMode {
	profile, ok := cfg.Profiles[profileName]
	if !ok {
		profile = cfg.Profiles[cfg.Profile]
	}
	pattern := req.Pattern
	if pattern == "" {
		pattern = "*"
	}

	for _, rule := range slices.Backward(profile.Rules) {
		if rule.Tool != req.Tool {
			continue
		}
		if wildcardMatch(rule.Pattern, pattern) {
			return rule.Action
		}
	}
	return domain.PermissionModeDeny
}

func ProfileNames(cfg config.PermissionRules) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	slices.Sort(names)
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
	matched, err := filepath.Match(pattern, value)
	if err == nil {
		return matched
	}
	return pattern == value
}
