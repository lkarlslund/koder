package domain

import (
	"fmt"
	"strings"
)

var toolKindTextAliases = map[string]ToolKind{
	"execcleanupbackground":     ToolKindExecCleanup,
	"milestoneadditems":         ToolKindMilestoneAdd,
	"milestoneplananddecompose": ToolKindMilestonePlan,
	"milestoneupdateitem":       ToolKindMilestoneUpdate,
}

// MarshalText lets ToolKind be used as a stable text key in formats such as TOML.
func (i ToolKind) MarshalText() ([]byte, error) {
	if !i.IsAToolKind() {
		return nil, fmt.Errorf("%d does not belong to ToolKind values", i)
	}
	return []byte(i.String()), nil
}

// UnmarshalText accepts enum names plus the snake_case names used by persisted tool settings.
func (i *ToolKind) UnmarshalText(text []byte) error {
	raw := strings.TrimSpace(string(text))
	if raw == "" {
		return fmt.Errorf("empty ToolKind")
	}
	if kind, err := ToolKindString(raw); err == nil {
		*i = kind
		return nil
	}
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(raw))
	if kind, ok := toolKindTextAliases[normalized]; ok {
		*i = kind
		return nil
	}
	if kind, err := ToolKindString(normalized); err == nil {
		*i = kind
		return nil
	}
	return fmt.Errorf("%s does not belong to ToolKind values", raw)
}
