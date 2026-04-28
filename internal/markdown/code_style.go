package markdown

import (
	"slices"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/styles"
)

const defaultCodeStyle = "github"

func CodeStyleNames() []string {
	names := slices.Clone(styles.Names())
	slices.Sort(names)
	return names
}

func NormalizeCodeStyle(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return defaultCodeStyle
	}
	if _, ok := styles.Registry[name]; ok {
		return name
	}
	return defaultCodeStyle
}

func resolveCodeStyle(name string) (string, *chroma.Style) {
	name = NormalizeCodeStyle(name)
	if style := styles.Get(name); style != nil {
		return name, style
	}
	if style := styles.Get(defaultCodeStyle); style != nil {
		return defaultCodeStyle, style
	}
	return defaultCodeStyle, styles.Fallback
}
