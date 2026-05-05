package tokenestimate

import "strings"

// Text returns a cheap token estimate for live UI accounting.
func Text(text string) int {
	if text == "" {
		return 0
	}
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return (len(text) + 3) / 4
}
