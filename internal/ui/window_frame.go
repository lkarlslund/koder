package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type WindowFrame struct {
	Title       string
	Content     Element
	Width       int
	Height      int
	Padding     Insets
	Background  lipgloss.Color
	Foreground  lipgloss.Color
	BorderColor lipgloss.Color
	TitleStyle  CellStyle
	CloseStyle  CellStyle
	ShowClose   bool
	CloseLabel  string
	CloseID     string
}

func (w WindowFrame) WalkChildren(ctx *Context, visit func(Element)) {
	if visit == nil {
		return
	}
	visit(w.border(ctx))
}

func (w WindowFrame) Measure(ctx *Context, constraints Constraints) Size {
	return w.border(ctx).Measure(ctx, constraints)
}

func (w WindowFrame) Render(ctx *Context, bounds Rect) Surface {
	return w.border(ctx).Render(ctx, bounds)
}

func (w WindowFrame) border(ctx *Context) Border {
	palette := themePalette(ctx)
	background := firstColor(w.Background, palette.SidebarBackground)
	closeLabel := ""
	if w.ShowClose {
		closeLabel = w.CloseLabel
		if closeLabel == "" {
			closeLabel = "[X]"
		}
	}
	padding := w.Padding
	if padding == (Insets{}) {
		padding = Insets{Left: 2, Right: 2, Top: 1, Bottom: 1}
	}
	return Border{
		Child:        w.Content,
		Width:        w.Width,
		Height:       w.Height,
		Padding:      padding,
		Background:   background,
		Foreground:   firstColor(w.Foreground, palette.SidebarForeground),
		BorderColor:  firstColor(w.BorderColor, palette.SidebarBorder),
		TopLabel:     bracketLabel(w.Title),
		EndLabel:     closeLabel,
		EndControlID: firstString(w.CloseID, "window-close"),
		TopLabelStyle: CellStyle{
			FG:   cellColor(palette.MarkdownText),
			BG:   cellColor(background),
			Bold: true,
		}.Merge(w.TitleStyle),
		EndLabelStyle: CellStyle{
			FG:   cellColor(palette.AssistantTimestampText),
			BG:   cellColor(background),
			Bold: true,
		}.Merge(w.CloseStyle),
		Style:        lipgloss.RoundedBorder(),
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
	}
}

func bracketLabel(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	return "[" + title + "]"
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func themePalette(ctx *Context) theme.Palette {
	if ctx == nil {
		return theme.Default().Palette
	}
	return ctx.Palette
}
