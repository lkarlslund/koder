package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

var toolStateKeyAliases = map[string]ToolKind{
	"execcleanupbackground":     ToolKindExecCleanup,
	"milestoneadditems":         ToolKindMilestoneAdd,
	"milestoneplananddecompose": ToolKindMilestonePlan,
	"milestoneupdateitem":       ToolKindMilestoneUpdate,
}

func (s *ToolStates) UnmarshalJSON(data []byte) error {
	var raw map[string]bool
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	states := make(ToolStates, len(raw))
	for name, enabled := range raw {
		kind, err := parsePersistedToolKind(name)
		if err != nil {
			return err
		}
		states[kind] = enabled
	}
	*s = states
	return nil
}

func parsePersistedToolKind(name string) (ToolKind, error) {
	if kind, err := ToolKindString(name); err == nil {
		return kind, nil
	}
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(name)))
	if kind, ok := toolStateKeyAliases[normalized]; ok {
		return kind, nil
	}
	for _, kind := range ToolKindValues() {
		canonical := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(kind.String()))
		if canonical == normalized {
			return kind, nil
		}
	}
	return 0, fmt.Errorf("%s does not belong to ToolKind values", name)
}
