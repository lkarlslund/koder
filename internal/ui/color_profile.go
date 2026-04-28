package ui

import (
	"strconv"
	"strings"
)

type ColorProfile int

const (
	ColorProfileNoColor ColorProfile = iota
	ColorProfileANSI16
	ColorProfileANSI256
	ColorProfileTrueColor
)

func (p ColorProfile) String() string {
	switch p {
	case ColorProfileNoColor:
		return "none"
	case ColorProfileANSI16:
		return "16"
	case ColorProfileANSI256:
		return "256"
	case ColorProfileTrueColor:
		return "truecolor"
	default:
		return "unknown"
	}
}

func ParseColorProfileOverride(value string) (ColorProfile, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return 0, false
	case "none", "nocolor", "no-color":
		return ColorProfileNoColor, true
	case "16", "ansi", "ansi16":
		return ColorProfileANSI16, true
	case "256", "ansi256":
		return ColorProfileANSI256, true
	case "truecolor", "24bit", "24-bit":
		return ColorProfileTrueColor, true
	default:
		return 0, false
	}
}

func DetectColorProfileFromEnv(getenv func(string) string, isTTY bool) ColorProfile {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if strings.TrimSpace(getenv("NO_COLOR")) != "" {
		return ColorProfileNoColor
	}
	if profile, ok := ParseColorProfileOverride(getenv("KODER_COLOR_PROFILE")); ok {
		return profile
	}
	if !isTTY {
		return ColorProfileNoColor
	}
	colorTerm := strings.ToLower(strings.TrimSpace(getenv("COLORTERM")))
	if colorTerm == "truecolor" || colorTerm == "24bit" {
		return ColorProfileTrueColor
	}
	termName := strings.ToLower(strings.TrimSpace(getenv("TERM")))
	if strings.Contains(termName, "truecolor") || strings.Contains(termName, "direct") {
		return ColorProfileTrueColor
	}
	if strings.Contains(termName, "256color") {
		return ColorProfileANSI256
	}
	if termLooksColorCapable(termName) {
		return ColorProfileANSI16
	}
	return ColorProfileANSI16
}

func termLooksColorCapable(termName string) bool {
	if termName == "" {
		return false
	}
	colorTerms := []string{
		"ansi",
		"cygwin",
		"linux",
		"rxvt",
		"screen",
		"tmux",
		"vt100",
		"xterm",
	}
	for _, candidate := range colorTerms {
		if strings.Contains(termName, candidate) {
			return true
		}
	}
	return false
}

type terminalColorKind uint8

const (
	terminalColorNone terminalColorKind = iota
	terminalColorANSI16
	terminalColorANSI256
	terminalColorRGB
)

type terminalColorCode struct {
	kind  terminalColorKind
	index uint8
	r     uint8
	g     uint8
	b     uint8
}

func terminalColorFromRGB(profile ColorProfile, r, g, b uint8, valid bool) terminalColorCode {
	if !valid || profile == ColorProfileNoColor {
		return terminalColorCode{}
	}
	switch profile {
	case ColorProfileANSI16:
		return terminalColorCode{kind: terminalColorANSI16, index: nearestPaletteIndex(r, g, b, ansi16Palette[:])}
	case ColorProfileANSI256:
		return terminalColorCode{kind: terminalColorANSI256, index: nearestPaletteIndex(r, g, b, xterm256Palette[:])}
	default:
		return terminalColorCode{kind: terminalColorRGB, r: r, g: g, b: b}
	}
}

func appendTerminalColorSGR(params []string, profile ColorProfile, isForeground bool, r, g, b uint8, valid bool) []string {
	code := terminalColorFromRGB(profile, r, g, b, valid)
	switch code.kind {
	case terminalColorNone:
		return params
	case terminalColorRGB:
		prefix := "38"
		if !isForeground {
			prefix = "48"
		}
		return append(params, prefix, "2",
			strconv.Itoa(int(code.r)),
			strconv.Itoa(int(code.g)),
			strconv.Itoa(int(code.b)),
		)
	case terminalColorANSI256:
		prefix := "38"
		if !isForeground {
			prefix = "48"
		}
		return append(params, prefix, "5", strconv.Itoa(int(code.index)))
	case terminalColorANSI16:
		base := 30
		brightBase := 90
		if !isForeground {
			base = 40
			brightBase = 100
		}
		index := int(code.index)
		if index >= 8 {
			return append(params, strconv.Itoa(brightBase+index-8))
		}
		return append(params, strconv.Itoa(base+index))
	default:
		return params
	}
}

type terminalPaletteColor struct {
	r uint8
	g uint8
	b uint8
}

var ansi16Palette = [16]terminalPaletteColor{
	{0x00, 0x00, 0x00},
	{0x80, 0x00, 0x00},
	{0x00, 0x80, 0x00},
	{0x80, 0x80, 0x00},
	{0x00, 0x00, 0x80},
	{0x80, 0x00, 0x80},
	{0x00, 0x80, 0x80},
	{0xc0, 0xc0, 0xc0},
	{0x80, 0x80, 0x80},
	{0xff, 0x00, 0x00},
	{0x00, 0xff, 0x00},
	{0xff, 0xff, 0x00},
	{0x00, 0x00, 0xff},
	{0xff, 0x00, 0xff},
	{0x00, 0xff, 0xff},
	{0xff, 0xff, 0xff},
}

var xterm256Palette = buildXterm256Palette()

func buildXterm256Palette() [256]terminalPaletteColor {
	var palette [256]terminalPaletteColor
	copy(palette[:16], ansi16Palette[:])
	levels := [6]uint8{0x00, 0x5f, 0x87, 0xaf, 0xd7, 0xff}
	idx := 16
	for r := 0; r < 6; r++ {
		for g := 0; g < 6; g++ {
			for b := 0; b < 6; b++ {
				palette[idx] = terminalPaletteColor{r: levels[r], g: levels[g], b: levels[b]}
				idx++
			}
		}
	}
	for i := 0; i < 24; i++ {
		level := uint8(8 + i*10)
		palette[idx] = terminalPaletteColor{r: level, g: level, b: level}
		idx++
	}
	return palette
}

func nearestPaletteIndex(r, g, b uint8, palette []terminalPaletteColor) uint8 {
	bestIndex := 0
	bestDistance := -1
	for idx, candidate := range palette {
		dr := int(r) - int(candidate.r)
		dg := int(g) - int(candidate.g)
		db := int(b) - int(candidate.b)
		distance := dr*dr + dg*dg + db*db
		if bestDistance == -1 || distance < bestDistance {
			bestDistance = distance
			bestIndex = idx
			if distance == 0 {
				break
			}
		}
	}
	return uint8(bestIndex)
}
