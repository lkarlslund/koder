package toolkind

import "strings"

func (i Kind) DisplayName() string {
	if !i.IsAKind() {
		return ""
	}
	name := strings.TrimSpace(i.String())
	if name == "" {
		return ""
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-'
	})
	for idx, part := range parts {
		if part == "" {
			continue
		}
		parts[idx] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}
