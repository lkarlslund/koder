package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

func RenderSidebar(content string, palette theme.Palette) string {
	return lipgloss.NewStyle().
		Width(30).
		Padding(0, 1).
		Background(palette.SidebarBackground).
		Foreground(palette.SidebarForeground).
		BorderLeft(true).
		BorderForeground(palette.SidebarBorder).
		Render(strings.TrimRight(content, "\n"))
}

func RenderBody(main, sidebar string, showSidebar bool) string {
	main = lipgloss.NewStyle().Padding(0, 1).Render(main)
	if !showSidebar {
		return main
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, main, sidebar)
}

func RenderFooter(parts []string) string {
	return lipgloss.NewStyle().
		BorderTop(true).
		Padding(0, 1).
		Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}
