package toolkind

import (
	"encoding/json"
	"fmt"
	"strings"
)

type States map[Kind]bool

var stateKeyAliases = map[string]Kind{
	"execcleanupbackground":     ToolKindExecCleanup,
	"edit":                      ToolKindFileEdit,
	"glob":                      ToolKindFileGlob,
	"grep":                      ToolKindFileGrep,
	"milestoneadditems":         ToolKindMilestoneAdd,
	"milestoneplananddecompose": ToolKindMilestonePlan,
	"milestoneupdateitem":       ToolKindMilestoneUpdate,
	"read":                      ToolKindFileRead,
	"write":                     ToolKindFileWrite,
}

func (s *States) UnmarshalJSON(data []byte) error {
	var raw map[string]bool
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	states := make(States, len(raw))
	for name, enabled := range raw {
		kind, err := ParsePersisted(name)
		if err != nil {
			continue
		}
		states[kind] = enabled
	}
	*s = states
	return nil
}

func ParsePersisted(name string) (Kind, error) {
	if kind, err := KindString(name); err == nil {
		return kind, nil
	}
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(name)))
	if kind, ok := stateKeyAliases[normalized]; ok {
		return kind, nil
	}
	for _, kind := range KindValues() {
		canonical := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(kind.String()))
		if canonical == normalized {
			return kind, nil
		}
	}
	return 0, fmt.Errorf("%s does not belong to ToolKind values", name)
}
