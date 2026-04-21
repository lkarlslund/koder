package ui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type SessionItem struct {
	SessionID    string
	ChangedAt    string
	TokenSummary string
	Title        string
	Description  string
	Details      []string
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
	Query string
	Index int
	Items []SessionItem
	view  []SessionItem
}

func NewSessionDialog(items []SessionItem) SessionDialog {
	d := SessionDialog{Items: items}
	d.refilter()
	return d
}

func (d *SessionDialog) Update(msg tea.KeyMsg) SessionDialogAction {
	switch msg.String() {
	case "esc":
		return SessionDialogAction{Kind: SessionDialogActionCancel}
	case "enter":
		item, ok := d.current()
		if !ok {
			return SessionDialogAction{Kind: SessionDialogActionCancel}
		}
		id, err := strconv.ParseInt(item.Value, 10, 64)
		if err != nil {
			return SessionDialogAction{}
		}
		return SessionDialogAction{Kind: SessionDialogActionSelect, SessionID: id}
	case "up":
		d.move(-1)
	case "down":
		d.move(1)
	case "backspace":
		if d.Query != "" {
			d.Query = d.Query[:len(d.Query)-1]
			d.refilter()
		}
	default:
		if msg.Type == tea.KeyRunes {
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
	changedWidth := minInt(18, maxInt(14, contentWidth/6))
	tokensWidth := minInt(18, maxInt(14, contentWidth/6))
	titleWidth := maxInt(24, contentWidth-idWidth-changedWidth-tokensWidth-6)

	listLines := []string{
		renderSessionTableHeader(idWidth, changedWidth, tokensWidth, titleWidth, palette),
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
			listLines = append(listLines, renderSessionTableRow(item, idWidth, changedWidth, tokensWidth, titleWidth, palette, idx == d.Index))
		}
	}

	details := "No session selected"
	if item, ok := d.current(); ok {
		blocks := []string{
			lipgloss.NewStyle().Bold(true).Render(item.Title),
		}
		blocks = append(blocks, item.Details...)
		if desc := strings.TrimSpace(item.Description); desc != "" {
			blocks = append(blocks, "", wrapPlain(compactInlineText(desc), contentWidth))
		}
		details = strings.Join(blocks, "\n")
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
		Render(details)

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		fmt.Sprintf("Filter: %s", d.Query),
		"",
		tablePane,
		"",
		detailPane,
		"",
		RenderDialogButtons(palette, "OK", "Cancel"),
	)

	return Modal{
		Title:  "Resume Session",
		Body:   body,
		Footer: "Enter resumes the highlighted session. Esc creates a new session.",
		Width:  dialogWidth,
	}.View(palette)
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
		haystack := strings.ToLower(item.Title + " " + item.Description + " " + item.Value)
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

func renderSessionTableHeader(idWidth, changedWidth, tokensWidth, titleWidth int, palette theme.Palette) string {
	style := lipgloss.NewStyle().
		Foreground(palette.AssistantTimestampText).
		Bold(true)
	return style.Render(joinSessionColumns("ID", idWidth, "Changed", changedWidth, "Tokens", tokensWidth, "Title", titleWidth))
}

func renderSessionTableRow(item SessionItem, idWidth, changedWidth, tokensWidth, titleWidth int, palette theme.Palette, selected bool) string {
	row := joinSessionColumns(
		item.SessionID,
		idWidth,
		item.ChangedAt,
		changedWidth,
		item.TokenSummary,
		tokensWidth,
		item.Title,
		titleWidth,
	)
	style := lipgloss.NewStyle().Width(idWidth + changedWidth + tokensWidth + titleWidth + 6)
	if selected {
		style = style.Background(palette.UserTextBackground).Foreground(palette.UserTextForeground)
	}
	return style.Render(row)
}

func joinSessionColumns(id string, idWidth int, changed string, changedWidth int, tokens string, tokensWidth int, title string, titleWidth int) string {
	cols := []string{
		lipgloss.NewStyle().Width(idWidth).Render(truncateText(strings.TrimSpace(id), idWidth)),
		lipgloss.NewStyle().Width(changedWidth).Render(truncateText(strings.TrimSpace(changed), changedWidth)),
		lipgloss.NewStyle().Width(tokensWidth).Render(truncateText(strings.TrimSpace(tokens), tokensWidth)),
		lipgloss.NewStyle().Width(titleWidth).Render(truncateText(strings.TrimSpace(title), titleWidth)),
	}
	return strings.Join(cols, "  ")
}
