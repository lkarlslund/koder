package webui

import (
	"strings"
	"testing"
)

func TestLooksBinary(t *testing.T) {
	pad := func(n int) string { return strings.Repeat("a", n) }
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"empty", "", false},
		{"short text", "# Miljøvurdering\n", false},
		{"nul byte", "abc\x00def", true},
		{"invalid utf-8", "abc\xffdef", true},
		// The preview cut lands inside a multi-byte character.
		{"two-byte rune across cut", pad(fileBrowserBinaryPreview-1) + "ø" + pad(100), false},
		{"three-byte rune across cut", pad(fileBrowserBinaryPreview-2) + "€" + pad(100), false},
		{"four-byte rune across cut", pad(fileBrowserBinaryPreview-3) + "😀" + pad(100), false},
		{"rune ends at cut", pad(fileBrowserBinaryPreview-2) + "ø" + pad(100), false},
		{"invalid byte at cut", pad(fileBrowserBinaryPreview-1) + "\xff" + pad(100), true},
	}
	for _, c := range cases {
		if got := looksBinary([]byte(c.data)); got != c.want {
			t.Errorf("%s: looksBinary = %v, want %v", c.name, got, c.want)
		}
	}
}
