package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/theme"
)

type ProviderItem struct {
	ID          string
	Title       string
	Description string
	Details     []string
}

type DisconnectDialogActionKind int

const (
	DisconnectDialogActionNone DisconnectDialogActionKind = iota
	DisconnectDialogActionSelect
	DisconnectDialogActionCancel
)

type DisconnectDialogAction struct {
	Kind       DisconnectDialogActionKind
	ProviderID string
}

type DisconnectDialog struct {
	Query string
	Index int
	Items []ProviderItem
	view  []ProviderItem
}

func NewDisconnectDialog(items []ProviderItem) DisconnectDialog {
	d := DisconnectDialog{Items: items}
	d.refilter()
	return d
}

func (d *DisconnectDialog) Update(msg tea.KeyMsg) DisconnectDialogAction {
	switch msg.String() {
	case "esc":
		return DisconnectDialogAction{Kind: DisconnectDialogActionCancel}
	case "enter":
		item, ok := d.current()
		if !ok {
			return DisconnectDialogAction{Kind: DisconnectDialogActionCancel}
		}
		return DisconnectDialogAction{Kind: DisconnectDialogActionSelect, ProviderID: item.ID}
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
	return DisconnectDialogAction{}
}

func (d DisconnectDialog) View(width int, palette theme.Palette) string {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 84
	}
	dialogWidth = maxInt(72, dialogWidth)
	listWidth := 28
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
			listLines = append(listLines, RenderSelectableRow(item.Title, item.Description, item.ID, listWidth, palette, idx == d.Index))
		}
	}

	details := "No provider selected"
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
		Title:  "Disconnect Provider",
		Body:   body,
		Footer: "Enter to disconnect, Esc to cancel",
		Width:  dialogWidth,
	}.View(palette)
}

func (d *DisconnectDialog) move(delta int) {
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

func (d *DisconnectDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.Query))
	d.view = d.view[:0]
	for _, item := range d.Items {
		haystack := strings.ToLower(item.Title + " " + item.Description + " " + item.ID)
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

func (d DisconnectDialog) current() (ProviderItem, bool) {
	if len(d.view) == 0 || d.Index < 0 || d.Index >= len(d.view) {
		return ProviderItem{}, false
	}
	return d.view[d.Index], true
}
