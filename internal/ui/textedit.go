package ui

import "unicode"

type textClass int

const (
	textClassSpace textClass = iota
	textClassWord
	textClassOther
)

func PrevWordBoundary(runes []rune, cursor int) int {
	cursor = clampRuneCursor(runes, cursor)
	for cursor > 0 && classifyTextRune(runes[cursor-1]) == textClassSpace {
		cursor--
	}
	if cursor == 0 {
		return 0
	}
	class := classifyTextRune(runes[cursor-1])
	for cursor > 0 && classifyTextRune(runes[cursor-1]) == class {
		cursor--
	}
	return cursor
}

func NextWordBoundary(runes []rune, cursor int) int {
	cursor = clampRuneCursor(runes, cursor)
	if cursor >= len(runes) {
		return len(runes)
	}
	class := classifyTextRune(runes[cursor])
	if class == textClassSpace {
		for cursor < len(runes) && classifyTextRune(runes[cursor]) == textClassSpace {
			cursor++
		}
		return cursor
	}
	for cursor < len(runes) && classifyTextRune(runes[cursor]) == class {
		cursor++
	}
	for cursor < len(runes) && classifyTextRune(runes[cursor]) == textClassSpace {
		cursor++
	}
	return cursor
}

func DeleteBeforeCursorString(input string, cursor int, byWord bool) (string, int) {
	runes := []rune(input)
	start := cursor - 1
	if byWord {
		start = PrevWordBoundary(runes, cursor)
	}
	start = clampRuneIndex(start, len(runes))
	cursor = clampRuneIndex(cursor, len(runes))
	if start >= cursor {
		return input, cursor
	}
	return string(append(runes[:start], runes[cursor:]...)), start
}

func DeleteAfterCursorString(input string, cursor int, byWord bool) (string, int) {
	runes := []rune(input)
	cursor = clampRuneIndex(cursor, len(runes))
	end := cursor + 1
	if byWord {
		end = NextWordBoundary(runes, cursor)
	}
	end = clampRuneIndex(end, len(runes))
	if end <= cursor {
		return input, cursor
	}
	return string(append(runes[:cursor], runes[end:]...)), cursor
}

func clampRuneCursor(runes []rune, cursor int) int {
	return clampRuneIndex(cursor, len(runes))
}

func clampRuneIndex(cursor int, n int) int {
	if cursor < 0 {
		return 0
	}
	if cursor > n {
		return n
	}
	return cursor
}

func classifyTextRune(r rune) textClass {
	switch {
	case unicode.IsSpace(r):
		return textClassSpace
	case unicode.IsLetter(r), unicode.IsDigit(r), r == '_':
		return textClassWord
	default:
		return textClassOther
	}
}
