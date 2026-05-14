package dialogs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type SessionItem struct {
	SessionID    string
	CreatedAt    string
	ModifiedAt   string
	TokenSummary string
	Title        string
	CWD          string
	Description  string
	Preview      string
	Value        string
}

type SessionDialogActionKind int

const (
	SessionDialogActionNone SessionDialogActionKind = iota
	SessionDialogActionSelect
	SessionDialogActionCancel
)

type SessionDialogAction struct {
	Kind      SessionDialogActionKind
	SessionID domain.ID
}

type SessionDialog struct {
	ui.PassiveNode
	Query   string
	Index   int
	Items   []SessionItem
	ShowCWD bool
	view    []SessionItem
	focus   pickerDialogFocus
	buttons ui.ButtonRow
}

func NewSessionDialog(items []SessionItem, showCWD bool) SessionDialog {
	d := SessionDialog{Items: items, ShowCWD: showCWD}
	d.buttons = ui.ButtonRow{
		Buttons: []ui.Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: ui.HorizontalAlignRight,
	}
	d.refilter()
	return d
}

func (d *SessionDialog) Update(msg ui.KeyMsg) SessionDialogAction {
	d.ensureButtons()
	var action SessionDialogAction
	d.buttons.Buttons[0].OnClick = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnClick = func() { action = SessionDialogAction{Kind: SessionDialogActionCancel} }

	if d.buttons.ActivateHotkey(msg) {
		return action
	}
	switch msg.String() {
	case "esc":
		return SessionDialogAction{Kind: SessionDialogActionCancel}
	case "tab":
		d.focus = (d.focus + 1) % 2
	case "shift+tab":
		d.focus--
		if d.focus < 0 {
			d.focus = pickerDialogFocusButtons
		}
	case "enter":
		if d.focus == pickerDialogFocusButtons {
			d.buttons.ActivateFocused()
			return action
		}
		return d.selectCurrent()
	case "up":
		if d.focus == pickerDialogFocusList {
			d.move(-1)
		}
	case "down":
		if d.focus == pickerDialogFocusList {
			d.move(1)
		}
	case "left":
		if d.focus == pickerDialogFocusButtons {
			d.buttons.Move(-1)
		}
	case "right":
		if d.focus == pickerDialogFocusButtons {
			d.buttons.Move(1)
		}
	case "backspace", "alt+backspace":
		if d.focus == pickerDialogFocusList && d.Query != "" {
			d.Query, _ = ui.DeleteBeforeCursorString(d.Query, len([]rune(d.Query)), msg.Alt)
			d.refilter()
		}
	default:
		if d.focus == pickerDialogFocusList && msg.Type == ui.KeyRunes {
			d.Query += msg.String()
			d.refilter()
		}
	}
	return SessionDialogAction{}
}

func (d SessionDialog) Measure(ctx *ui.Context, constraints ui.Constraints) ui.Size {
	width := constraints.MaxW
	if width <= 0 {
		width = 110
	}
	return constraints.Clamp(d.dialog(width, ctx.Palette).Measure(ctx, ui.Constraints{MaxW: width, MaxH: constraints.MaxH}))
}

func (d SessionDialog) Surface(ctx *ui.Context, bounds ui.Rect) ui.Surface {
	maxWidth := dialogRenderWidth(bounds, 110)
	node := d.dialog(maxWidth, ctx.Palette)
	size := node.Measure(ctx, ui.Constraints{MaxW: maxWidth, MaxH: bounds.H})
	return ui.PaintNodeSurface(ctx, node, ui.Rect{W: size.W, H: bounds.H})
}

func (d SessionDialog) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if canvas.Width() <= 0 || canvas.Height() <= 0 {
		return
	}
	canvas.BlitSurface(0, 0, d.Surface(ctx, ui.Rect{W: canvas.Width(), H: canvas.Height()}))
}

func (d SessionDialog) dialog(width int, palette theme.Palette) ui.Node {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 110
	}
	if width <= 0 {
		dialogWidth = maxInt(96, dialogWidth)
	}
	contentWidth := dialogWidth - 6
	if contentWidth <= 0 {
		contentWidth = dialogWidth
	}
	idWidth := 8
	timeWidth := 10
	tokensWidth := minInt(18, maxInt(8, contentWidth/6))
	cwdWidth := 0
	gapCount := 4
	if d.ShowCWD {
		cwdWidth = minInt(24, maxInt(8, contentWidth/5))
		gapCount = 5
	}
	titleWidth := maxInt(8, contentWidth-idWidth-timeWidth-timeWidth-tokensWidth-cwdWidth-(gapCount*2))
	columns := []ui.TableColumn{
		{Title: "ID", Width: idWidth},
		{Title: "Created", Width: timeWidth},
		{Title: "Modified", Width: timeWidth},
		{Title: "Tokens", Width: tokensWidth},
	}
	if d.ShowCWD {
		columns = append(columns, ui.TableColumn{Title: "CWD", Width: cwdWidth})
	}
	columns = append(columns, ui.TableColumn{Title: "Title", Width: titleWidth})

	rows := []ui.TableRow{}
	if len(d.view) > 0 {
		start := 0
		if d.Index >= 5 {
			start = d.Index - 4
		}
		end := len(d.view)
		if end > start+10 {
			end = start + 10
		}
		for idx := start; idx < end; idx++ {
			item := d.view[idx]
			cells := []string{item.SessionID, item.CreatedAt, item.ModifiedAt, item.TokenSummary}
			if d.ShowCWD {
				cells = append(cells, item.CWD)
			}
			cells = append(cells, item.Title)
			rows = append(rows, ui.TableRow{
				ControlID: "session-row-" + strconv.Itoa(idx),
				Cells:     cells,
				Selected:  idx == d.Index,
				Focused:   idx == d.Index && d.focus == pickerDialogFocusList,
			})
		}
	}

	details := "No session selected"
	if item, ok := d.current(); ok {
		details = d.clampPreviewLines(d.previewText(item, contentWidth), 10)
	}

	list := staticBlock("No matches")
	if len(rows) > 0 {
		list = ui.AsNode(ui.Table{
			Width:      contentWidth,
			Columns:    columns,
			Rows:       rows,
			ShowHeader: true,
		})
	}

	buttons := d.buttonRow(contentWidth)
	buttons.Width = maxInt(0, dialogWidth-6)
	return ui.AsNode(ui.WindowFrame{
		Title: "Resume Session",
		Width: dialogWidth,
		Content: ui.AsNode(ui.NewFlexBox(
			ui.DirectionVertical,
			[]ui.Child{
				ui.Fixed(ui.AsNode(ui.NewFlexBox(
					ui.DirectionVertical,
					[]ui.Child{
						ui.Fixed(staticBlock(fmt.Sprintf("Filter: %s", d.Query))),
						ui.Fixed(ui.Spacer{H: 1}),
						ui.Fixed(ui.AsNode(ui.Section{Width: contentWidth, Child: list})),
						ui.Fixed(ui.Spacer{H: 1}),
						ui.Fixed(ui.AsNode(ui.Section{
							Title:       "Preview",
							Width:       contentWidth,
							Padding:     ui.Insets{Top: 1, Left: 1, Right: 1},
							Background:  palette.ScreenBackground,
							Foreground:  palette.SidebarForeground,
							BorderColor: palette.SidebarBorder,
							Child:       ui.AsNode(ui.TextPane{Content: details}),
						})),
					},
					0,
				))),
				ui.Fixed(buttons),
				ui.Fixed(ui.Static{Content: "Enter resumes the highlighted session. Esc creates a new session."}),
			},
			2,
		)),
		ShowClose: true,
	})
}

func (d *SessionDialog) ActivateControl(controlID string) SessionDialogAction {
	d.ensureButtons()
	var action SessionDialogAction
	d.buttons.Buttons[0].OnClick = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnClick = func() { action = SessionDialogAction{Kind: SessionDialogActionCancel} }
	switch controlID {
	case "ok", "cancel":
		d.focus = pickerDialogFocusButtons
		for idx, button := range d.buttons.Buttons {
			if button.ID == controlID {
				d.buttons.Index = idx
				d.buttons.ActivateFocused()
				return action
			}
		}
	default:
		if strings.HasPrefix(controlID, "session-row-") {
			idx, err := strconv.Atoi(strings.TrimPrefix(controlID, "session-row-"))
			if err != nil || idx < 0 || idx >= len(d.view) {
				return SessionDialogAction{}
			}
			d.Index = idx
			d.focus = pickerDialogFocusList
			return d.selectCurrent()
		}
	}
	return SessionDialogAction{}
}

func (d *SessionDialog) move(delta int) {
	if len(d.view) == 0 {
		d.Index = 0
		return
	}
	d.Index += delta
	if d.Index < 0 {
		d.Index = 0
	}
	if d.Index >= len(d.view) {
		d.Index = len(d.view) - 1
	}
}

func (d *SessionDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.Query))
	d.view = d.view[:0]
	for _, item := range d.Items {
		haystack := strings.ToLower(item.Title + " " + item.Description + " " + item.Value + " " + item.CWD)
		if query == "" || strings.Contains(haystack, query) {
			d.view = append(d.view, item)
		}
	}
	if len(d.view) == 0 {
		d.Index = 0
		return
	}
	if d.Index >= len(d.view) {
		d.Index = len(d.view) - 1
	}
	if d.Index < 0 {
		d.Index = 0
	}
}

func (d SessionDialog) current() (SessionItem, bool) {
	if len(d.view) == 0 || d.Index < 0 || d.Index >= len(d.view) {
		return SessionItem{}, false
	}
	return d.view[d.Index], true
}

func (d SessionDialog) selectCurrent() SessionDialogAction {
	item, ok := d.current()
	if !ok {
		return SessionDialogAction{Kind: SessionDialogActionCancel}
	}
	id := domain.ID(strings.TrimSpace(item.Value))
	if id == "" {
		return SessionDialogAction{}
	}
	return SessionDialogAction{Kind: SessionDialogActionSelect, SessionID: id}
}

func (d *SessionDialog) ensureButtons() {
	if len(d.buttons.Buttons) != 0 {
		return
	}
	d.buttons = ui.ButtonRow{
		Buttons: []ui.Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: ui.HorizontalAlignRight,
	}
}

func (d SessionDialog) buttonRow(width int) ui.ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width)
	buttons.Align = ui.HorizontalAlignRight
	return buttons
}

func (d SessionDialog) previewText(item SessionItem, width int) string {
	if preview := strings.TrimSpace(item.Preview); preview != "" {
		return preview
	}
	if desc := strings.TrimSpace(item.Description); desc != "" {
		return wrapPlain(desc, width)
	}
	return ""
}

func (d SessionDialog) clampPreviewLines(text string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	lines = lines[:maxLines]
	lines[maxLines-1] = strings.TrimRight(lines[maxLines-1], " ") + " …"
	return strings.Join(lines, "\n")
}
