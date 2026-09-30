// Package textutil holds small string helpers shared across koder.
package textutil

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TruncateRunes returns at most n runes of s.
func TruncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for index := range s {
		if count == n {
			return s[:index]
		}
		count++
	}
	return s
}

// TruncateBytes returns the longest prefix of s that is at most n bytes and
// does not split a UTF-8 sequence.
func TruncateBytes(s string, n int) string {
	if n >= len(s) {
		return s
	}
	if n <= 0 {
		return ""
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Ellipsize trims s and, when it exceeds maxBytes, cuts it on a character
// boundary and appends "…".
func Ellipsize(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	return TruncateBytes(s, maxBytes-1) + "…"
}

// FormatBytes renders a byte count for status text, such as "512 B",
// "4.0 KB", "64 KB", or "1.5 MB".
func FormatBytes(size int) string {
	const unit = 1024
	switch {
	case size < unit:
		return fmt.Sprintf("%d B", size)
	case size < 10*unit:
		return fmt.Sprintf("%.1f KB", float64(size)/unit)
	case size < unit*unit:
		return fmt.Sprintf("%.0f KB", float64(size)/unit)
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(unit*unit))
	}
}
