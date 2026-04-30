package ui

import (
	"strings"

	"github.com/lkarlslund/koder/internal/theme"
)

type MenuItem struct {
	Title       string
	Description string
}

type HistoryMenu struct {
	PassiveNode
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
	PassiveNode
	Title    string
	Items    []MenuItem
	Selected int
}

func (m SlashMenu) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(m.node(m.contentWidth()).Measure(ctx, constraints))
}

func (m SlashMenu) node(contentWidth int) Node {
	if len(m.Items) == 0 {
		return nil
	}
	children := make([]Child, 0, len(m.Items)+1)
	children = append(children, Fixed(Label{
		Text: m.Title,
		Style: NewStyle().
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
	return Border{
		Child:        NewFlexBox(DirectionVertical, children, 0),
		Padding:      SymmetricInsets(1, 0),
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
	}
}

func (m SlashMenu) contentWidth() int {
	primaryWidth := PlainWidth(strings.TrimSpace(m.Title))
	secondaryWidth := 0
	for _, item := range m.Items {
		primaryWidth = max(primaryWidth, PlainWidth(compactInlineText(item.Title)))
		secondaryWidth = max(secondaryWidth, PlainWidth(compactInlineText(item.Description)))
	}
	primaryWidth = max(12, min(18, primaryWidth))
	return max(20, primaryWidth+2+secondaryWidth)
}

func (m SlashMenu) panelWidth(contentWidth int) int {
	return max(0, contentWidth) + 4
}

func (m SlashMenu) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := m.panelWidth(m.contentWidth())
	if canvas.Width() > 0 {
		width = min(width, canvas.Width())
	}
	paintNodeInto(ctx, m.node(max(0, width-4)), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: width,
		H: canvas.Height(),
	}, canvas.surface)
}

func (m HistoryMenu) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(m.node().Measure(ctx, constraints))
}

func (m HistoryMenu) node() Node {
	width := m.width()
	contentWidth := max(1, width-4)
	muted := NewStyle().Foreground(m.Palette.AssistantTimestampText)
	children := []Child{
		Fixed(Label{Text: "History", Style: NewStyle().Bold(true)}),
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
	return Border{
		Child:        NewFlexBox(DirectionVertical, children, 0),
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

func (m HistoryMenu) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	width := m.width()
	if canvas.Width() > 0 {
		width = min(width, canvas.Width())
	}
	paintNodeInto(ctx, m.node(), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: width,
		H: canvas.Height(),
	}, canvas.surface)
}

type ApprovalPrompt struct {
	PassiveNode
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
	return ApprovalPrompt{
		Palette:      props.Palette,
		Title:        props.Title,
		Body:         props.Body,
		ApproveLabel: props.ApproveLabel,
		DenyLabel:    props.DenyLabel,
		ApproveFocus: props.ApproveFocus,
		DenyFocus:    props.DenyFocus,
		Hints:        props.Hints,
	}
}

func (p ApprovalPrompt) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(p.node().Measure(ctx, constraints))
}

func (p ApprovalPrompt) node() Node {
	buttons := ButtonRow{
		Buttons: []Button{
			{Label: p.ApproveLabel, Primary: true, Focused: p.ApproveFocus},
			{Label: p.DenyLabel, Focused: p.DenyFocus},
		},
		Index: p.focusedIndex(),
		Align: HorizontalAlignLeft,
	}
	return Border{
		Child: NewFlexBox(
			DirectionVertical,
			[]Child{
				Fixed(Label{Text: p.Title, Style: NewStyle().Bold(true)}),
				Fixed(Paragraph{Text: p.Body}),
				Fixed(buttons),
				Fixed(Label{
					Text:  p.Hints,
					Style: NewStyle().Foreground(p.Palette.AssistantTimestampText),
				}),
			},
			1,
		),
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

func (p ApprovalPrompt) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	paintNodeInto(ctx, p.node(), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}

type MenuPickerDialog struct {
	PassiveNode
	Palette theme.Palette
	Title   string
	Hint    string
	Query   string
	Items   []MenuItem
	Index   int
}

func NewMenuPickerDialog(props PickerDialogProps) MenuPickerDialog {
	return MenuPickerDialog{
		Palette: props.Palette,
		Title:   props.Title,
		Hint:    props.Hint,
		Query:   props.Query,
		Items:   props.Items,
		Index:   props.Index,
	}
}

func (d MenuPickerDialog) Measure(ctx *Context, constraints Constraints) Size {
	return constraints.Clamp(d.node().Measure(ctx, constraints))
}

func (d MenuPickerDialog) node() Node {
	width := 80
	listWidth := width - 6
	children := make([]Child, 0, len(d.Items)+5)
	if hint := strings.TrimSpace(d.Hint); hint != "" {
		children = append(children, Fixed(Label{
			Text:  hint,
			Style: NewStyle().Foreground(d.Palette.AssistantTimestampText),
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
	buttons := ButtonRow{
		Buttons: []Button{
			{Label: "OK", Primary: true},
			{Label: "Cancel"},
		},
		Width: maxInt(0, width-6),
		Align: HorizontalAlignRight,
	}
	return AsNode(WindowFrame{
		Title: strings.TrimSpace(d.Title),
		Width: width,
		Content: AsNode(NewFlexBox(
			DirectionVertical,
			[]Child{
				Fixed(AsNode(NewFlexBox(DirectionVertical, children, 0))),
				Fixed(buttons),
				Fixed(Static{Content: "Enter applies the highlighted row. Esc cancels."}),
			},
			2,
		)),
		ShowClose: true,
	})
}

func (d MenuPickerDialog) Paint(ctx *Context, canvas Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	paintNodeInto(ctx, d.node(), Rect{
		X: canvas.origin.X,
		Y: canvas.origin.Y,
		W: canvas.Width(),
		H: canvas.Height(),
	}, canvas.surface)
}
