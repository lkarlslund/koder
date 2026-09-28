package webui

import "unicode/utf16"

// composerByteOffset translates a browser selection offset to a Go byte offset.
// Invalid offsets clamp to the end; a split surrogate pair snaps to its start.
func composerByteOffset(text string, offset int) int {
	if offset < 0 {
		return len(text)
	}
	units := 0
	for i, r := range text {
		units += utf16.RuneLen(r)
		if units > offset {
			return i
		}
	}
	return len(text)
}
