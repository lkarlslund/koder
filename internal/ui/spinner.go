package ui

import "slices"

type SpinnerStyle struct {
	ID          string
	Label       string
	Description string
	Frames      []string
}

var spinnerStyles = []SpinnerStyle{
	{ID: "dots", Label: "Braille Dots", Description: "Smooth braille dot spinner", Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}},
	{ID: "blocks", Label: "Braille Blocks", Description: "Heavier braille block spinner", Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}},
	{ID: "quadrants", Label: "Quadrants", Description: "Simple quadrant motion", Frames: []string{"◴", "◷", "◶", "◵"}},
	{ID: "corners", Label: "Corners", Description: "Corner quadrant rotation", Frames: []string{"◰", "◳", "◲", "◱"}},
	{ID: "circles", Label: "Half Circles", Description: "Readable half-circle spinner", Frames: []string{"◐", "◓", "◑", "◒"}},
	{ID: "arrows", Label: "Arrows", Description: "Directional arrow cycle", Frames: []string{"←", "↖", "↑", "↗", "→", "↘", "↓", "↙"}},
	{ID: "quarters", Label: "Quarter Blocks", Description: "Minimal quarter-block spinner", Frames: []string{"▖", "▘", "▝", "▗"}},
}

func SpinnerStyles() []SpinnerStyle {
	out := make([]SpinnerStyle, len(spinnerStyles))
	copy(out, spinnerStyles)
	return out
}

func SpinnerNames() []string {
	out := make([]string, 0, len(spinnerStyles))
	for _, item := range spinnerStyles {
		out = append(out, item.ID)
	}
	return out
}

func SpinnerStyleByID(id string) SpinnerStyle {
	for _, item := range spinnerStyles {
		if item.ID == id {
			return item
		}
	}
	return spinnerStyles[0]
}

func NormalizeSpinner(id string) string {
	return SpinnerStyleByID(id).ID
}

func SpinnerFrame(id string, frame int) string {
	style := SpinnerStyleByID(id)
	if len(style.Frames) == 0 {
		return ""
	}
	if frame < 0 {
		frame = 0
	}
	return style.Frames[frame%len(style.Frames)]
}

func SpinnerIndex(id string) int {
	names := SpinnerNames()
	return slices.Index(names, NormalizeSpinner(id))
}
