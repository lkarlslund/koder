package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
)

type ModelDialogActionKind int

const (
	ModelDialogActionNone ModelDialogActionKind = iota
	ModelDialogActionSelect
	ModelDialogActionCancel
)

type ModelDialogAction struct {
	Kind    ModelDialogActionKind
	ModelID string
}

type ModelDialog struct {
	ProviderID string
	Query      string
	Index      int
	Models     []domain.Model
	view       []domain.Model
}

func NewModelDialog(providerID string, models []domain.Model, current string) ModelDialog {
	d := ModelDialog{
		ProviderID: providerID,
		Models:     models,
	}
	d.refilter()
	for idx, item := range d.view {
		if item.ID == strings.TrimSpace(current) {
			d.Index = idx
			break
		}
	}
	return d
}

func (d *ModelDialog) Update(msg tea.KeyMsg) ModelDialogAction {
	switch msg.String() {
	case "esc":
		return ModelDialogAction{Kind: ModelDialogActionCancel}
	case "enter":
		item, ok := d.current()
		if !ok {
			return ModelDialogAction{Kind: ModelDialogActionCancel}
		}
		return ModelDialogAction{Kind: ModelDialogActionSelect, ModelID: item.ID}
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
	return ModelDialogAction{}
}

func (d ModelDialog) View(width int, palette theme.Palette) string {
	dialogWidth := width
	if dialogWidth <= 0 {
		dialogWidth = 84
	}
	dialogWidth = maxInt(72, dialogWidth)
	listWidth := 34
	detailWidth := maxInt(30, dialogWidth-listWidth-9)

	listLines := []string{}
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
			listLines = append(listLines, RenderSelectableRow(item.ID, item.OwnedBy, capabilityBadges(item), listWidth, palette, idx == d.Index))
		}
	}

	details := "No model selected"
	if item, ok := d.current(); ok {
		lines := []string{
			lipgloss.NewStyle().Bold(true).Render(item.ID),
			fmt.Sprintf("Provider: %s", d.ProviderID),
		}
		if strings.TrimSpace(item.OwnedBy) != "" {
			lines = append(lines, fmt.Sprintf("Owner:    %s", item.OwnedBy))
		}
		if badges := capabilityBadges(item); badges != "" {
			lines = append(lines, fmt.Sprintf("Supports: %s", badges))
		}
		details = strings.Join(lines, "\n")
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
		Title:  "Select Model",
		Body:   body,
		Footer: "Enter to select, Esc to cancel",
		Width:  dialogWidth,
	}.View(palette)
}

func (d *ModelDialog) move(delta int) {
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

func (d *ModelDialog) refilter() {
	query := strings.ToLower(strings.TrimSpace(d.Query))
	d.view = d.view[:0]
	for _, item := range d.Models {
		haystack := strings.ToLower(item.ID + " " + item.OwnedBy)
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

func (d ModelDialog) current() (domain.Model, bool) {
	if len(d.view) == 0 || d.Index < 0 || d.Index >= len(d.view) {
		return domain.Model{}, false
	}
	return d.view[d.Index], true
}

func modelLine(model domain.Model) string {
	if badges := capabilityBadges(model); badges != "" {
		return model.ID + "  [" + badges + "]"
	}
	return model.ID
}

func capabilityBadges(model domain.Model) string {
	var badges []string
	if model.SupportsImages {
		badges = append(badges, "image")
	}
	if model.SupportsPDFs {
		badges = append(badges, "pdf")
	}
	if len(badges) == 0 && model.CapabilitiesKnown {
		badges = append(badges, "text")
	}
	return strings.Join(badges, ", ")
}
