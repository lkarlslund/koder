package ui

// Style is a chainable builder for CellStyle.
type Style struct {
	cell CellStyle
}

// NewStyle returns an empty style builder.
func NewStyle() Style {
	return Style{}
}

// Foreground sets the foreground color.
func (s Style) Foreground(color CellColor) Style {
	s.cell.FG = color
	return s
}

// Background sets the background color.
func (s Style) Background(color CellColor) Style {
	s.cell.BG = color
	return s
}

// Bold enables or disables bold text.
func (s Style) Bold(enabled bool) Style {
	s.cell = s.cell.WithBold(enabled)
	return s
}

// Italic enables or disables italic text.
func (s Style) Italic(enabled bool) Style {
	s.cell = s.cell.WithItalic(enabled)
	return s
}

// Underline enables or disables underline text.
func (s Style) Underline(enabled bool) Style {
	s.cell = s.cell.WithUnderline(enabled)
	return s
}

// Strikethrough enables or disables strikethrough text.
func (s Style) Strikethrough(enabled bool) Style {
	s.cell = s.cell.WithStrikethrough(enabled)
	return s
}

// CellStyle returns the immutable cell style built so far.
func (s Style) CellStyle() CellStyle {
	return s.cell
}

// GetBold reports whether bold text is enabled.
func (s Style) GetBold() bool {
	return s.cell.Bold()
}

// GetItalic reports whether italic text is enabled.
func (s Style) GetItalic() bool {
	return s.cell.Italic()
}

// GetUnderline reports whether underline text is enabled.
func (s Style) GetUnderline() bool {
	return s.cell.Underline()
}

// GetForeground returns the configured foreground color.
func (s Style) GetForeground() CellColor {
	return s.cell.FG
}

// GetBackground returns the configured background color.
func (s Style) GetBackground() CellColor {
	return s.cell.BG
}

// BorderGlyphs contains the runes used to draw a rectangular border.
type BorderGlyphs struct {
	Top         string
	Bottom      string
	Left        string
	Right       string
	TopLeft     string
	TopRight    string
	BottomLeft  string
	BottomRight string
}

// NormalBorder returns square-corner border glyphs.
func NormalBorder() BorderGlyphs {
	return BorderGlyphs{
		Top:         "─",
		Bottom:      "─",
		Left:        "│",
		Right:       "│",
		TopLeft:     "┌",
		TopRight:    "┐",
		BottomLeft:  "└",
		BottomRight: "┘",
	}
}

// RoundedBorder returns rounded-corner border glyphs.
func RoundedBorder() BorderGlyphs {
	return BorderGlyphs{
		Top:         "─",
		Bottom:      "─",
		Left:        "│",
		Right:       "│",
		TopLeft:     "╭",
		TopRight:    "╮",
		BottomLeft:  "╰",
		BottomRight: "╯",
	}
}
