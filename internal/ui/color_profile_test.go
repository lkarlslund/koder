package ui

import (
	"strings"
	"testing"
)

func TestDetectColorProfileFromEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		env   map[string]string
		isTTY bool
		want  ColorProfile
	}{
		{name: "no color env", env: map[string]string{"NO_COLOR": "1"}, isTTY: true, want: ColorProfileNoColor},
		{name: "override none", env: map[string]string{"KODER_COLOR_PROFILE": "none"}, isTTY: true, want: ColorProfileNoColor},
		{name: "override 16", env: map[string]string{"KODER_COLOR_PROFILE": "16"}, isTTY: true, want: ColorProfileANSI16},
		{name: "override 256", env: map[string]string{"KODER_COLOR_PROFILE": "256"}, isTTY: true, want: ColorProfileANSI256},
		{name: "override truecolor", env: map[string]string{"KODER_COLOR_PROFILE": "truecolor"}, isTTY: true, want: ColorProfileTrueColor},
		{name: "not tty", env: map[string]string{"TERM": "xterm-256color"}, isTTY: false, want: ColorProfileNoColor},
		{name: "colorterm truecolor", env: map[string]string{"COLORTERM": "truecolor"}, isTTY: true, want: ColorProfileTrueColor},
		{name: "colorterm 24bit", env: map[string]string{"COLORTERM": "24bit"}, isTTY: true, want: ColorProfileTrueColor},
		{name: "term direct", env: map[string]string{"TERM": "xterm-direct"}, isTTY: true, want: ColorProfileTrueColor},
		{name: "term 256", env: map[string]string{"TERM": "screen-256color"}, isTTY: true, want: ColorProfileANSI256},
		{name: "term ansi16", env: map[string]string{"TERM": "xterm"}, isTTY: true, want: ColorProfileANSI16},
		{name: "unknown tty fallback", env: map[string]string{"TERM": "mysteryterm"}, isTTY: true, want: ColorProfileANSI16},
		{name: "bad override falls back", env: map[string]string{"KODER_COLOR_PROFILE": "weird", "TERM": "xterm-256color"}, isTTY: true, want: ColorProfileANSI256},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectColorProfileFromEnv(func(key string) string {
				return tt.env[key]
			}, tt.isTTY)
			if got != tt.want {
				t.Fatalf("DetectColorProfileFromEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRenderStyledTextANSIWithProfile(t *testing.T) {
	t.Parallel()

	style := CellStyle{
		FG: NewCellColorRGB(255, 0, 0),
		BG: NewCellColorRGB(0, 0, 255),
	}.WithBold(true).WithUnderline(true)
	spans := []StyledSpan{{Text: "hello", Style: style}}

	tests := []struct {
		name    string
		profile ColorProfile
		want    []string
		notWant []string
	}{
		{
			name:    "truecolor",
			profile: ColorProfileTrueColor,
			want:    []string{"\x1b[", "1", "4", "38;2;255;0;0", "48;2;0;0;255", "hello", "\x1b[0m"},
			notWant: []string{"38;5;", "48;5;", "[31", "[41"},
		},
		{
			name:    "ansi256",
			profile: ColorProfileANSI256,
			want:    []string{"\x1b[", "1", "4", "38;5;9", "48;5;12", "hello", "\x1b[0m"},
			notWant: []string{"38;2;", "48;2;"},
		},
		{
			name:    "ansi16",
			profile: ColorProfileANSI16,
			want:    []string{"\x1b[", "1", "4", "91", "104", "hello", "\x1b[0m"},
			notWant: []string{"38;2;", "48;2;", "38;5;", "48;5;"},
		},
		{
			name:    "no color keeps attrs",
			profile: ColorProfileNoColor,
			want:    []string{"\x1b[1;4mhello\x1b[0m"},
			notWant: []string{"38;", "48;"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderStyledTextANSIWithProfile(spans, tt.profile)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("expected %q in %q", want, got)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(got, notWant) {
					t.Fatalf("did not expect %q in %q", notWant, got)
				}
			}
		})
	}
}

func TestNearestPaletteIndexExactMatches(t *testing.T) {
	t.Parallel()

	if got := nearestPaletteIndex(255, 0, 0, ansi16Palette[:]); got != 9 {
		t.Fatalf("nearestPaletteIndex red = %d, want 9", got)
	}
	if got := nearestPaletteIndex(0, 255, 0, xterm256Palette[:]); got != 10 {
		t.Fatalf("nearestPaletteIndex green = %d, want 10", got)
	}
	if got := nearestPaletteIndex(95, 135, 175, xterm256Palette[:]); got != 67 {
		t.Fatalf("nearestPaletteIndex cube match = %d, want 67", got)
	}
	if got := nearestPaletteIndex(128, 128, 128, xterm256Palette[:]); got != 8 {
		t.Fatalf("nearestPaletteIndex grayscale = %d, want 8", got)
	}
}
