package ui

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
)

type WindowFrame struct {
	PassiveNode
	Title       string
	Content     Node
	Width       int
	Height      int
	Padding     Insets
	Background  CellColor
	Foreground  CellColor
	BorderColor CellColor
	TitleStyle  CellStyle
	CloseStyle  CellStyle
	ShowClose   bool
	CloseLabel  string
	CloseID     string
}

func (w WindowFrame) Measure(ctx *Context, constraints Constraints) Size {
	return w.border(ctx).Measure(ctx, constraints)
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
			FG: cellColor(palette.MarkdownText),
			BG: cellColor(background),
		}.WithBold(true).Merge(w.TitleStyle),
		EndLabelStyle: CellStyle{
			FG: cellColor(palette.AssistantTimestampText),
			BG: cellColor(background),
		}.WithBold(true).Merge(w.CloseStyle),
		Style:        RoundedBorder(),
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

func (w WindowFrame) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	paintNodeInto(ctx, w.border(ctx), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}

func (w WindowFrame) Children() []Node {
	if w.Content == nil {
		return nil
	}
	return []Node{w.Content}
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
