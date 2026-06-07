package toolkind

import (
	"encoding/json"
	"strings"
)

type States map[Kind]bool

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
	return KindString(strings.TrimSpace(name))
}
