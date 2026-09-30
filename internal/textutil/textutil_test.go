package textutil

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"æøå!", 2, "æø"},
		{"abc", 0, ""},
	}
	for _, tc := range cases {
		if got := TruncateRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("TruncateRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestTruncateBytesKeepsValidUTF8(t *testing.T) {
	for n := -1; n <= 8; n++ {
		got := TruncateBytes("aæøå", n)
		if !utf8.ValidString(got) || len(got) > max(n, 0) {
			t.Fatalf("TruncateBytes(%d) = %q", n, got)
		}
	}
	if got := TruncateBytes("aæøå", 4); got != "aæ" {
		t.Fatalf("expected cut before split rune, got %q", got)
	}
}

func TestEllipsize(t *testing.T) {
	if got := Ellipsize("  short  ", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
	if got := Ellipsize("abcdef", 4); got != "abc…" {
		t.Fatalf("got %q", got)
	}
	if got := Ellipsize("ææææ", 4); got != "æ…" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int]string{
		512:             "512 B",
		4 * 1024:        "4.0 KB",
		64 * 1024:       "64 KB",
		3 * 1024 * 1024: "3.0 MB",
	}
	for size, want := range cases {
		if got := FormatBytes(size); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", size, got, want)
		}
	}
}
