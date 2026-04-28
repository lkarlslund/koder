package colorx

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/image/colornames"
)

type Color struct {
	value uint32
	valid bool
}

func RGB(r, g, b uint8) Color {
	return RGBA(r, g, b, 0xff)
}

func RGBA(r, g, b, a uint8) Color {
	return Color{
		value: uint32(r)<<24 | uint32(g)<<16 | uint32(b)<<8 | uint32(a),
		valid: true,
	}
}

func Invalid() Color {
	return Color{}
}

func (c Color) Valid() bool {
	return c.valid
}

func (c Color) R() uint8 {
	return uint8(c.value >> 24)
}

func (c Color) G() uint8 {
	return uint8(c.value >> 16)
}

func (c Color) B() uint8 {
	return uint8(c.value >> 8)
}

func (c Color) A() uint8 {
	return uint8(c.value)
}

func (c Color) WithAlpha(a uint8) Color {
	if !c.valid {
		return c
	}
	return RGBA(c.R(), c.G(), c.B(), a)
}

func (c Color) String() string {
	if !c.valid {
		return ""
	}
	if c.A() == 0xff {
		return fmt.Sprintf("#%02x%02x%02x", c.R(), c.G(), c.B())
	}
	return fmt.Sprintf("#%02x%02x%02x%02x", c.R(), c.G(), c.B(), c.A())
}

var rgbFuncPattern = regexp.MustCompile(`(?i)^rgba?\((.+)\)$`)

func ParseCSSColor(value string) Color {
	value = strings.TrimSpace(value)
	if value == "" {
		return Invalid()
	}
	if strings.HasPrefix(value, "#") {
		return parseHexColor(value)
	}
	if match := rgbFuncPattern.FindStringSubmatch(value); len(match) == 2 {
		return parseRGBFunc(match[1])
	}
	if named, ok := colornames.Map[strings.ToLower(value)]; ok {
		return RGBA(named.R, named.G, named.B, named.A)
	}
	switch strings.ToLower(value) {
	case "transparent", "none":
		return RGBA(0, 0, 0, 0)
	}
	return Invalid()
}

func parseHexColor(value string) Color {
	raw := strings.TrimPrefix(strings.TrimSpace(value), "#")
	switch len(raw) {
	case 3:
		r, ok := expandNibble(raw[0])
		if !ok {
			return Invalid()
		}
		g, ok := expandNibble(raw[1])
		if !ok {
			return Invalid()
		}
		b, ok := expandNibble(raw[2])
		if !ok {
			return Invalid()
		}
		return RGBA(r, g, b, 0xff)
	case 4:
		r, ok := expandNibble(raw[0])
		if !ok {
			return Invalid()
		}
		g, ok := expandNibble(raw[1])
		if !ok {
			return Invalid()
		}
		b, ok := expandNibble(raw[2])
		if !ok {
			return Invalid()
		}
		a, ok := expandNibble(raw[3])
		if !ok {
			return Invalid()
		}
		return RGBA(r, g, b, a)
	case 6, 8:
		parsed, err := strconv.ParseUint(raw, 16, 32)
		if err != nil {
			return Invalid()
		}
		if len(raw) == 6 {
			return RGBA(uint8(parsed>>16), uint8(parsed>>8), uint8(parsed), 0xff)
		}
		return RGBA(uint8(parsed>>24), uint8(parsed>>16), uint8(parsed>>8), uint8(parsed))
	default:
		return Invalid()
	}
}

func parseRGBFunc(args string) Color {
	parts := splitColorArgs(args)
	if len(parts) != 3 && len(parts) != 4 {
		return Invalid()
	}
	r, ok := parseRGBChannel(parts[0])
	if !ok {
		return Invalid()
	}
	g, ok := parseRGBChannel(parts[1])
	if !ok {
		return Invalid()
	}
	b, ok := parseRGBChannel(parts[2])
	if !ok {
		return Invalid()
	}
	a := uint8(0xff)
	if len(parts) == 4 {
		parsedAlpha, ok := parseAlphaChannel(parts[3])
		if !ok {
			return Invalid()
		}
		a = parsedAlpha
	}
	return RGBA(r, g, b, a)
}

func splitColorArgs(args string) []string {
	fields := strings.FieldsFunc(args, func(r rune) bool {
		return r == ',' || r == '/' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func parseRGBChannel(value string) (uint8, bool) {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "%") {
		percent, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
		if err != nil || percent < 0 || percent > 100 {
			return 0, false
		}
		return uint8(percent*255/100 + 0.5), true
	}
	channel, err := strconv.Atoi(value)
	if err != nil || channel < 0 || channel > 255 {
		return 0, false
	}
	return uint8(channel), true
}

func parseAlphaChannel(value string) (uint8, bool) {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "%") {
		percent, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
		if err != nil || percent < 0 || percent > 100 {
			return 0, false
		}
		return uint8(percent*255/100 + 0.5), true
	}
	alpha, err := strconv.ParseFloat(value, 64)
	if err != nil || alpha < 0 || alpha > 1 {
		return 0, false
	}
	return uint8(alpha*255 + 0.5), true
}

func expandNibble(ch byte) (uint8, bool) {
	value, err := strconv.ParseUint(string([]byte{ch}), 16, 8)
	if err != nil {
		return 0, false
	}
	return uint8(value)<<4 | uint8(value), true
}
