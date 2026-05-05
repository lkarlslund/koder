package tokenestimate

import "strings"

// Text returns a cheap token estimate for live UI accounting.
func Text(text string) int {
	if text == "" {
		return 0
	}
	count := len(strings.Fields(text))
	if count == 0 && strings.TrimSpace(text) != "" {
		return 1
	}
	return count
}
