package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
)

type MenuItem struct {
	Title       string
	Description string
}

type HistoryMenu struct {
	Palette  theme.Palette
	Query    string
	Items    []MenuItem
	Selected int
	Width    int
}

type ApprovalPromptProps struct {
	Palette      theme.Palette
	Title        string
	Body         string
	ApproveLabel string
	DenyLabel    string
	ApproveFocus bool
	DenyFocus    bool
	Hints        string
}

type PickerDialogProps struct {
	Palette theme.Palette
	Title   string
	Hint    string
	Query   string
	Items   []MenuItem
	Index   int
}

type SlashMenu struct {
	Title    string
	Items    []MenuItem
	Selected int
}

func (m SlashMenu) render() Surface {
	element := m.element(m.contentWidth())
	if element == nil {
		return Surface{}
	}
	width := m.panelWidth(m.contentWidth())
	return element.Render(&Context{Palette: theme.Default().Palette}, Rect{W: width})
}

func (m SlashMenu) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(m.element(m.contentWidth()).Measure(ctx, constraints))
}

func (m SlashMenu) Render(ctx *Context, bounds Rect) Surface {
	width := m.panelWidth(m.contentWidth())
	if bounds.W > 0 {
		width = min(width, bounds.W)
	}
	return m.element(max(0, width-4)).Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H})
}

func (m SlashMenu) element(contentWidth int) Element {
	if len(m.Items) == 0 {
		return nil
	}
	children := make([]Child, 0, len(m.Items)+1)
	children = append(children, Fixed(Label{
		Text: m.Title,
		Style: lipgloss.NewStyle().
			Bold(true),
	}))
	for idx, item := range m.Items {
		children = append(children, Fixed(SelectableRow{
			Primary:        item.Title,
			Secondary:      item.Description,
			Width:          contentWidth,
			PrimaryWidth:   min(16, max(10, contentWidth/4)),
			SecondaryWidth: max(8, contentWidth-min(16, max(10, contentWidth/4))-2),
			Selected:       idx == m.Selected,
			Focused:        idx == m.Selected,
		}))
	}
	return Panel{
		Child:        Column{Children: children},
		Padding:      SymmetricInsets(1, 0),
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
	}
}

func (m SlashMenu) contentWidth() int {
	primaryWidth := ansi.StringWidth(strings.TrimSpace(m.Title))
	secondaryWidth := 0
	for _, item := range m.Items {
		primaryWidth = max(primaryWidth, ansi.StringWidth(compactInlineText(item.Title)))
		secondaryWidth = max(secondaryWidth, ansi.StringWidth(compactInlineText(item.Description)))
	}
	primaryWidth = max(12, min(18, primaryWidth))
	return max(20, primaryWidth+2+secondaryWidth)
}

func (m SlashMenu) panelWidth(contentWidth int) int {
	return max(0, contentWidth) + 4
}

func (m HistoryMenu) render() Surface {
	return m.element().Render(&Context{Palette: m.Palette}, Rect{W: m.width()})
}

func (m HistoryMenu) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(m.element().Measure(ctx, constraints))
}

func (m HistoryMenu) Render(ctx *Context, bounds Rect) Surface {
	width := m.width()
	if bounds.W > 0 {
		width = min(width, bounds.W)
	}
	return m.element().Render(ctx, Rect{X: bounds.X, Y: bounds.Y, W: width, H: bounds.H})
}

func (m HistoryMenu) element() Element {
	width := m.width()
	contentWidth := max(1, width-4)
	muted := lipgloss.NewStyle().Foreground(m.Palette.AssistantTimestampText)
	children := []Child{
		Fixed(Label{Text: "History", Style: lipgloss.NewStyle().Bold(true)}),
		Fixed(Label{Text: "filter: " + m.Query, Style: muted}),
	}
	if len(m.Items) == 0 {
		children = append(children,
			Fixed(Spacer{H: 1}),
			Fixed(Label{Text: "  no matches"}),
		)
	} else {
		children = append(children, Fixed(Spacer{H: 1}))
		for idx, item := range m.Items {
			children = append(children, Fixed(SelectableRow{
				Primary:   item.Title,
				Secondary: item.Description,
				Width:     contentWidth,
				Selected:  idx == m.Selected,
				Focused:   idx == m.Selected,
			}))
		}
	}
	children = append(children,
		Fixed(Spacer{H: 1}),
		Fixed(Label{
			Text:  "enter accept  esc cancel  ctrl-r/down older  ctrl-s/up newer",
			Style: muted,
		}),
	)
	return Panel{
		Child:        Column{Children: children},
		Width:        width,
		Padding:      SymmetricInsets(1, 0),
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
	}
}

func (m HistoryMenu) width() int {
	if m.Width > 0 {
		return m.Width
	}
	return 72
}

type ApprovalPrompt struct {
	Palette      theme.Palette
	Title        string
	Body         string
	ApproveLabel string
	DenyLabel    string
	ApproveFocus bool
	DenyFocus    bool
	Hints        string
}

func NewApprovalPrompt(props ApprovalPromptProps) ApprovalPrompt {
	return ApprovalPrompt(props)
}

func (p ApprovalPrompt) render() Surface {
	element := p.element()
	size := element.Measure(&Context{Palette: p.Palette}, Constraints{})
	return element.Render(&Context{Palette: p.Palette}, Rect{W: size.W, H: size.H})
}

func (p ApprovalPrompt) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(p.element().Measure(ctx, constraints))
}

func (p ApprovalPrompt) Render(ctx *Context, bounds Rect) Surface {
	return p.element().Render(ctx, bounds)
}

func (p ApprovalPrompt) element() Element {
	buttons := ButtonRow{
		Buttons: []Button{
			{Label: p.ApproveLabel, Primary: true, Focused: p.ApproveFocus},
			{Label: p.DenyLabel, Focused: p.DenyFocus},
		},
		Index: p.focusedIndex(),
		Align: HorizontalAlignLeft,
	}
	return Panel{
		Child: Column{
			Children: []Child{
				Fixed(Label{Text: p.Title, Style: lipgloss.NewStyle().Bold(true)}),
				Fixed(Paragraph{Text: p.Body}),
				Fixed(buttons),
				Fixed(Label{
					Text:  p.Hints,
					Style: lipgloss.NewStyle().Foreground(p.Palette.AssistantTimestampText),
				}),
			},
			Spacing: 1,
		},
		Padding:      SymmetricInsets(1, 0),
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
	}
}

func (p ApprovalPrompt) focusedIndex() int {
	if p.DenyFocus && !p.ApproveFocus {
		return 1
	}
	return 0
}

type MenuPickerDialog struct {
	Palette theme.Palette
	Title   string
	Hint    string
	Query   string
	Items   []MenuItem
	Index   int
}

func NewMenuPickerDialog(props PickerDialogProps) MenuPickerDialog {
	return MenuPickerDialog(props)
}

func (d MenuPickerDialog) render() Surface {
	element := d.element()
	size := element.Measure(&Context{Palette: d.Palette}, NewConstraints(80, 0))
	return element.Render(&Context{Palette: d.Palette}, Rect{W: size.W, H: size.H})
}

func (d MenuPickerDialog) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(d.element().Measure(ctx, constraints))
}

func (d MenuPickerDialog) Render(ctx *Context, bounds Rect) Surface {
	return d.element().Render(ctx, bounds)
}

func (d MenuPickerDialog) element() Element {
	width := 80
	listWidth := width - 6
	children := make([]Child, 0, len(d.Items)+5)
	if hint := strings.TrimSpace(d.Hint); hint != "" {
		children = append(children, Fixed(Label{
			Text:  hint,
			Style: lipgloss.NewStyle().Foreground(d.Palette.AssistantTimestampText),
		}))
	}
	if len(children) > 0 {
		children = append(children, Fixed(Spacer{H: 1}))
	}
	children = append(children, Fixed(Label{Text: "filter: " + d.Query}))
	children = append(children, Fixed(Spacer{H: 1}))
	if len(d.Items) == 0 {
		children = append(children, Fixed(Label{Text: "  no matches"}))
	} else {
		for idx, item := range d.Items {
			children = append(children, Fixed(SelectableRow{
				Primary:   item.Title,
				Secondary: item.Description,
				Width:     listWidth,
				Selected:  idx == d.Index,
				Focused:   idx == d.Index,
			}))
		}
	}
	return Dialog{
		Title: " " + strings.TrimSpace(d.Title),
		Body:  Column{Children: children},
		Buttons: ButtonRow{
			Buttons: []Button{
				{Label: "OK", Primary: true},
				{Label: "Cancel"},
			},
			Width: listWidth,
			Align: HorizontalAlignRight,
		},
		Footer: "Enter applies the highlighted row. Esc cancels.",
		Width:  width,
	}
}
