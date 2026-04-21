package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lkarlslund/koder/internal/theme"
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
	SessionID int64
}

type SessionDialog struct {
	Query   string
	Index   int
	Items   []SessionItem
	ShowCWD bool
	view    []SessionItem
	focus   pickerDialogFocus
	buttons ButtonRow
}

func NewSessionDialog(items []SessionItem, showCWD bool) SessionDialog {
	d := SessionDialog{Items: items, ShowCWD: showCWD}
	d.buttons = ButtonRow{
		Buttons: []Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: HorizontalAlignRight,
	}
	d.refilter()
	return d
}

func (d *SessionDialog) Update(msg tea.KeyMsg) SessionDialogAction {
	d.ensureButtons()
	var action SessionDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = SessionDialogAction{Kind: SessionDialogActionCancel} }

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
	case "backspace":
		if d.focus == pickerDialogFocusList && d.Query != "" {
			d.Query = d.Query[:len(d.Query)-1]
			d.refilter()
		}
	default:
		if d.focus == pickerDialogFocusList && msg.Type == tea.KeyRunes {
			d.Query += msg.String()
			d.refilter()
		}
	}
	return SessionDialogAction{}
}

func (d SessionDialog) View(width int, palette theme.Palette) string {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 110
	}
	dialogWidth = maxInt(96, dialogWidth)
	contentWidth := maxInt(80, dialogWidth-6)
	idWidth := 8
	timeWidth := 10
	tokensWidth := minInt(18, maxInt(14, contentWidth/6))
	cwdWidth := 0
	gapCount := 4
	if d.ShowCWD {
		cwdWidth = minInt(24, maxInt(16, contentWidth/5))
		gapCount = 5
	}
	titleWidth := maxInt(16, contentWidth-idWidth-timeWidth-timeWidth-tokensWidth-cwdWidth-(gapCount*2))

	listLines := []string{
		renderSessionTableHeader(idWidth, timeWidth, tokensWidth, cwdWidth, titleWidth, d.ShowCWD, palette),
	}
	if len(d.view) == 0 {
		listLines = append(listLines, "No matches")
	} else {
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
			listLines = append(listLines, renderSessionTableRow(item, idWidth, timeWidth, tokensWidth, cwdWidth, titleWidth, d.ShowCWD, palette, idx == d.Index))
		}
	}

	details := "No session selected"
	if item, ok := d.current(); ok {
		details = clampPreviewLines(sessionPreviewText(item, contentWidth), 10)
	}

	tablePane := lipgloss.NewStyle().
		Width(contentWidth).
		Background(palette.SidebarBackground).
		Foreground(palette.SidebarForeground).
		Render(strings.Join(listLines, "\n"))
	detailPane := lipgloss.NewStyle().
		Width(contentWidth).
		BorderTop(true).
		BorderForeground(palette.SidebarBorder).
		PaddingTop(1).
		PaddingLeft(1).
		PaddingRight(1).
		Background(lipgloss.Color("#000000")).
		Foreground(palette.SidebarForeground).
		Render(details)

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		fmt.Sprintf("Filter: %s", d.Query),
		"",
		tablePane,
		"",
		detailPane,
		"",
		d.buttonRow(contentWidth).View(palette),
	)

	return Modal{
		Title:  "Resume Session",
		Body:   body,
		Footer: "Enter resumes the highlighted session. Esc creates a new session.",
		Width:  dialogWidth,
	}.View(palette)
}

func (d *SessionDialog) HandleMouse(localX, localY, width int, palette theme.Palette) SessionDialogAction {
	d.ensureButtons()
	var action SessionDialogAction
	d.buttons.Buttons[0].OnPress = func() { action = d.selectCurrent() }
	d.buttons.Buttons[1].OnPress = func() { action = SessionDialogAction{Kind: SessionDialogActionCancel} }

	lines := strings.Split(d.View(width, palette), "\n")
	if localY < 0 || localY >= len(lines) {
		return SessionDialogAction{}
	}
	line := ansi.Strip(lines[localY])
	buttons := d.buttonRow(width)
	if strings.Contains(line, "OK") && strings.Contains(line, "Cancel") {
		if start, ok := buttonRowOffset(line, buttons, palette); ok {
			d.focus = pickerDialogFocusButtons
			if idx, hit := buttons.IndexAtX(localX-start, palette); hit {
				d.buttons.Index = idx
				d.buttons.ActivateFocused()
				return action
			}
		}
	}
	for idx, item := range d.view {
		if strings.TrimSpace(item.Title) == "" {
			continue
		}
		if !strings.Contains(line, item.Title) {
			continue
		}
		d.Index = idx
		d.focus = pickerDialogFocusList
		return d.selectCurrent()
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
	id, err := strconv.ParseInt(item.Value, 10, 64)
	if err != nil {
		return SessionDialogAction{}
	}
	return SessionDialogAction{Kind: SessionDialogActionSelect, SessionID: id}
}

func (d *SessionDialog) ensureButtons() {
	if len(d.buttons.Buttons) != 0 {
		return
	}
	d.buttons = ButtonRow{
		Buttons: []Button{
			{ID: "ok", Label: "OK", Hotkey: 'o', Primary: true},
			{ID: "cancel", Label: "Cancel", Hotkey: 'c'},
		},
		Align: HorizontalAlignRight,
	}
}

func (d SessionDialog) buttonRow(width int) ButtonRow {
	buttons := d.buttons
	buttons.Width = maxInt(0, width)
	buttons.Align = HorizontalAlignRight
	return buttons
}

func renderSessionTableHeader(idWidth, timeWidth, tokensWidth, cwdWidth, titleWidth int, showCWD bool, palette theme.Palette) string {
	style := lipgloss.NewStyle().
		Foreground(palette.AssistantTimestampText).
		Bold(true)
	return style.Render(joinSessionColumns("ID", idWidth, "Created", timeWidth, "Modified", timeWidth, "Tokens", tokensWidth, "CWD", cwdWidth, "Title", titleWidth, showCWD))
}

func renderSessionTableRow(item SessionItem, idWidth, timeWidth, tokensWidth, cwdWidth, titleWidth int, showCWD bool, palette theme.Palette, selected bool) string {
	row := joinSessionColumns(
		item.SessionID,
		idWidth,
		item.CreatedAt,
		timeWidth,
		item.ModifiedAt,
		timeWidth,
		item.TokenSummary,
		tokensWidth,
		item.CWD,
		cwdWidth,
		item.Title,
		titleWidth,
		showCWD,
	)
	totalWidth := idWidth + timeWidth + timeWidth + tokensWidth + titleWidth + 8
	if showCWD {
		totalWidth += cwdWidth + 2
	}
	style := lipgloss.NewStyle().Width(totalWidth)
	if selected {
		style = style.Background(palette.UserTextBackground).Foreground(palette.UserTextForeground)
	}
	return style.Render(row)
}

func joinSessionColumns(id string, idWidth int, created string, createdWidth int, modified string, modifiedWidth int, tokens string, tokensWidth int, cwd string, cwdWidth int, title string, titleWidth int, showCWD bool) string {
	cols := []string{
		lipgloss.NewStyle().Width(idWidth).Render(truncateText(strings.TrimSpace(id), idWidth)),
		lipgloss.NewStyle().Width(createdWidth).Render(truncateText(strings.TrimSpace(created), createdWidth)),
		lipgloss.NewStyle().Width(modifiedWidth).Render(truncateText(strings.TrimSpace(modified), modifiedWidth)),
		lipgloss.NewStyle().Width(tokensWidth).Render(truncateText(strings.TrimSpace(tokens), tokensWidth)),
	}
	if showCWD {
		cols = append(cols, lipgloss.NewStyle().Width(cwdWidth).Render(truncateText(strings.TrimSpace(cwd), cwdWidth)))
	}
	cols = append(cols, lipgloss.NewStyle().Width(titleWidth).Render(truncateText(strings.TrimSpace(title), titleWidth)))
	return strings.Join(cols, "  ")
}

func sessionPreviewText(item SessionItem, width int) string {
	if preview := strings.TrimSpace(item.Preview); preview != "" {
		return preview
	}
	if desc := strings.TrimSpace(item.Description); desc != "" {
		return wrapPlain(desc, width)
	}
	return ""
}

func clampPreviewLines(text string, maxLines int) string {
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
