package session

import (
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
)

func AppendPermissionRule(rules []accesssettings.PermissionOverride, rule accesssettings.PermissionOverride) []accesssettings.PermissionOverride {
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if rule.Pattern == "" {
		rule.Pattern = "*"
	}
	next := make([]accesssettings.PermissionOverride, 0, len(rules)+1)
	for _, existing := range rules {
		if existing.Tool == rule.Tool && strings.TrimSpace(existing.Pattern) == rule.Pattern {
			continue
		}
		next = append(next, existing)
	}
	return append(next, rule)
}
