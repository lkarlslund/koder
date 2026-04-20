package permission

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

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
