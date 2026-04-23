package ui

import "strings"

func SurfaceText(s Surface) string {
	return strings.Join(s.Lines(), "\n")
}
