package ui

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
)

// Dialog is a declarative modal shell for app-specific dialog state that lives
// outside the ui package.
type Dialog struct {
	Title    string
	Subtitle string
	Sections []string
	Buttons  ButtonRow
	Footer   string
	Width    int
}

func (d Dialog) View(palette theme.Palette) string {
	parts := make([]string, 0, len(d.Sections)+1)
	for _, section := range d.Sections {
		if strings.TrimSpace(section) == "" {
			continue
		}
		parts = append(parts, strings.TrimRight(section, "\n"))
	}
	if len(d.Buttons.Buttons) > 0 {
		parts = append(parts, d.Buttons.View(palette))
	}
	return Modal{
		Title:    d.Title,
		Subtitle: d.Subtitle,
		Body:     strings.Join(parts, "\n\n"),
		Footer:   d.Footer,
		Width:    d.Width,
	}.View(palette)
}
