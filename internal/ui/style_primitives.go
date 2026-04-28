package ui

type Style struct {
	cell CellStyle
}

func NewStyle() Style {
	return Style{}
}

func (s Style) Foreground(color CellColor) Style {
	s.cell.FG = color
	return s
}

func (s Style) Background(color CellColor) Style {
	s.cell.BG = color
	return s
}

func (s Style) Bold(enabled bool) Style {
	s.cell = s.cell.WithBold(enabled)
	return s
}

func (s Style) Italic(enabled bool) Style {
	s.cell = s.cell.WithItalic(enabled)
	return s
}

func (s Style) Underline(enabled bool) Style {
	s.cell = s.cell.WithUnderline(enabled)
	return s
}

func (s Style) Strikethrough(enabled bool) Style {
	s.cell = s.cell.WithStrikethrough(enabled)
	return s
}

func (s Style) CellStyle() CellStyle {
	return s.cell
}

func (s Style) GetBold() bool {
	return s.cell.Bold()
}

func (s Style) GetItalic() bool {
	return s.cell.Italic()
}

func (s Style) GetUnderline() bool {
	return s.cell.Underline()
}

func (s Style) GetForeground() CellColor {
	return s.cell.FG
}

func (s Style) GetBackground() CellColor {
	return s.cell.BG
}

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
