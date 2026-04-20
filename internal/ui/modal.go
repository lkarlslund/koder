package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type Modal struct {
	Title    string
	Subtitle string
	Body     string
	Footer   string
	Width    int
}

func (m Modal) View(palette theme.Palette) string {
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(palette.MarkdownText).Render(strings.TrimSpace(m.Title)),
	}
	if subtitle := strings.TrimSpace(m.Subtitle); subtitle != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(subtitle))
	}
	if body := strings.TrimSpace(m.Body); body != "" {
		lines = append(lines, "", body)
	}
	if footer := strings.TrimSpace(m.Footer); footer != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(palette.AssistantTimestampText).Render(footer))
	}

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(palette.SidebarBorder).
		Background(palette.SidebarBackground).
		Foreground(palette.SidebarForeground).
		Padding(1, 2)
	if m.Width > 0 {
		style = style.Width(m.Width)
	}
	return style.Render(strings.Join(lines, "\n"))
}
