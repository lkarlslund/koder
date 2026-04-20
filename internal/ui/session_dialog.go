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
	Title       string
	Description string
	Details     []string
	Value       string
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
		dialogWidth = 84
	}
	dialogWidth = maxInt(72, dialogWidth)
	listWidth := 30
	detailWidth := maxInt(36, dialogWidth-listWidth-9)

	listLines := []string{}
	if len(d.view) == 0 {
		listLines = append(listLines, "No matches")
	} else {
		start := 0
		if d.Index >= 5 {
			start = d.Index - 4
		}
		end := len(d.view)
		if end > start+9 {
			end = start + 9
		}
		for idx := start; idx < end; idx++ {
			item := d.view[idx]
			line := fmt.Sprintf("#%s  %s", item.Value, truncateText(item.Title, maxInt(8, listWidth-6-len(item.Value))))
			if idx == d.Index {
				line = lipgloss.NewStyle().
					Background(palette.UserTextBackground).
					Foreground(palette.UserAccentBar).
					Bold(true).
					Render(line)
			}
			listLines = append(listLines, line)
		}
	}

	details := "No session selected"
	if item, ok := d.current(); ok {
		blocks := []string{
			lipgloss.NewStyle().Bold(true).Render(item.Title),
		}
		blocks = append(blocks, item.Details...)
		if desc := strings.TrimSpace(item.Description); desc != "" {
			blocks = append(blocks, "", truncateText(desc, detailWidth))
		}
		details = strings.Join(blocks, "\n")
	}

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		fmt.Sprintf("Filter: %s", d.Query),
		"",
		lipgloss.JoinHorizontal(
			lipgloss.Top,
			lipgloss.NewStyle().Width(listWidth).BorderRight(true).BorderForeground(palette.SidebarBorder).PaddingRight(1).Render(strings.Join(listLines, "\n")),
			" ",
			lipgloss.NewStyle().Width(detailWidth).PaddingLeft(1).Render(details),
		),
	)

	return Modal{
		Title:    "Resume Session",
		Body:     body,
		Footer:   "Enter to select, Esc to start new session",
		Width:    dialogWidth,
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
