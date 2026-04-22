package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type Sidebar struct {
	Content string
	Height  int
}

func (s Sidebar) View(palette theme.Palette) string {
	style := lipgloss.NewStyle().
		Width(30).
		Padding(0, 1).
		Background(palette.SidebarBackground).
		Foreground(palette.SidebarForeground).
		BorderLeft(true).
		BorderForeground(palette.SidebarBorder)
	if s.Height > 0 {
		style = style.Height(s.Height).MaxHeight(s.Height)
	}
	return style.Render(strings.TrimRight(s.Content, "\n"))
}

type BodyLayout struct {
	Main        string
	Sidebar     string
	ShowSidebar bool
}

func (l BodyLayout) View() string {
	main := lipgloss.NewStyle().Padding(0, 1).Render(l.Main)
	if !l.ShowSidebar {
		return main
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, main, l.Sidebar)
}

type Footer struct {
	Parts []string
}

func (f Footer) View() string {
	return lipgloss.NewStyle().
		BorderTop(true).
		Padding(0, 1).
		Render(lipgloss.JoinVertical(lipgloss.Left, f.Parts...))
}
