package dialogs

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	. "github.com/lkarlslund/koder/internal/ui"
)

func dialogRenderWidth(bounds Rect, fallback int) int {
	width := bounds.W
	if width <= 0 {
		width = fallback
	}
	if width <= 0 {
		width = 80
	}
	return width
}

func staticBlock(text string) Element {
	return Static{Content: strings.TrimRight(text, "\n")}
}

func linesBlock(lines ...string) Element {
	children := make([]Child, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			children = append(children, Fixed(Spacer{H: 1}))
			continue
		}
		children = append(children, Fixed(Static{Content: line}))
	}
	return Column{Children: children}
}

type pickerDialogFocus int

const (
	pickerDialogFocusList pickerDialogFocus = iota
	pickerDialogFocusButtons
)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncateText(input string, width int) string {
	if width <= 0 {
		return ""
	}
	plain := compactInlineText(input)
	if PlainWidth(plain) <= width {
		return plain
	}
	if width == 1 {
		return "…"
	}
	return PlainTruncate(plain, width-1, "") + "…"
}

func compactInlineText(input string) string {
	fields := strings.Fields(strings.TrimSpace(input))
	return strings.Join(fields, " ")
}

func firstNonEmptyColor(values ...lipgloss.Color) lipgloss.Color {
	for _, value := range values {
		if strings.TrimSpace(string(value)) != "" {
			return value
		}
	}
	return ""
}

func deriveFocusedBackground(base lipgloss.Color, screen lipgloss.Color) lipgloss.Color {
	baseRGB, ok := parseHexColor(base)
	if !ok {
		return base
	}
	screenRGB, ok := parseHexColor(screen)
	if !ok {
		screenRGB = [3]float64{0, 0, 0}
	}
	baseLum := relativeLuminance(baseRGB)
	screenLum := relativeLuminance(screenRGB)
	adjust := 0.12
	if screenLum > 0.5 {
		return formatHexColor(darkenRGB(baseRGB, adjust))
	}
	if baseLum <= screenLum {
		return formatHexColor(lightenRGB(baseRGB, adjust))
	}
	return formatHexColor(lightenRGB(baseRGB, adjust))
}

func wrapPlain(input string, width int) string {
	if width <= 0 {
		return strings.TrimSpace(input)
	}
	input = strings.ReplaceAll(strings.TrimSpace(input), "\r\n", "\n")
	if input == "" {
		return ""
	}
	var out []string
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(out) == 0 || out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		for _, wrapped := range wrapWords(line, width) {
			out = append(out, wrapped)
		}
	}
	return strings.Join(out, "\n")
}

func wrapWords(line string, width int) []string {
	if PlainWidth(line) <= width {
		return []string{line}
	}
	var lines []string
	remaining := line
	for remaining != "" {
		if PlainWidth(remaining) <= width {
			lines = append(lines, remaining)
			break
		}
		segment := PlainTruncate(remaining, width, "")
		if idx := strings.LastIndex(segment, " "); idx > 0 {
			segment = strings.TrimRight(segment[:idx], " ")
		}
		if segment == "" {
			segment = PlainTruncate(remaining, width, "")
		}
		lines = append(lines, segment)
		remaining = strings.TrimSpace(strings.TrimPrefix(remaining, segment))
	}
	return lines
}

func parseHexColor(color lipgloss.Color) ([3]float64, bool) {
	value := strings.TrimSpace(string(color))
	if len(value) != 7 || !strings.HasPrefix(value, "#") {
		return [3]float64{}, false
	}
	var rgb [3]float64
	for i := 0; i < 3; i++ {
		var component uint8
		_, err := fmt.Sscanf(value[1+i*2:3+i*2], "%02x", &component)
		if err != nil {
			return [3]float64{}, false
		}
		rgb[i] = float64(component) / 255.0
	}
	return rgb, true
}

func formatHexColor(rgb [3]float64) lipgloss.Color {
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		clampColorComponent(rgb[0]*255),
		clampColorComponent(rgb[1]*255),
		clampColorComponent(rgb[2]*255),
	))
}

func clampColorComponent(value float64) int {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return int(value + 0.5)
}

func relativeLuminance(rgb [3]float64) float64 {
	return 0.2126*linearize(rgb[0]) + 0.7152*linearize(rgb[1]) + 0.0722*linearize(rgb[2])
}

func linearize(value float64) float64 {
	if value <= 0.03928 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

func lightenRGB(rgb [3]float64, amount float64) [3]float64 {
	return [3]float64{
		rgb[0] + (1-rgb[0])*amount,
		rgb[1] + (1-rgb[1])*amount,
		rgb[2] + (1-rgb[2])*amount,
	}
}

func darkenRGB(rgb [3]float64, amount float64) [3]float64 {
	return [3]float64{
		rgb[0] * (1 - amount),
		rgb[1] * (1 - amount),
		rgb[2] * (1 - amount),
	}
}
