package ui

import "testing"

func TestPrevWordBoundary(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		cursor int
		want   int
	}{
		{name: "end of word", input: "hello world", cursor: len([]rune("hello world")), want: len([]rune("hello "))},
		{name: "skips spaces", input: "hello world  ", cursor: len([]rune("hello world  ")), want: len([]rune("hello "))},
		{name: "punctuation cluster", input: "foo/bar", cursor: len([]rune("foo/bar")), want: len([]rune("foo/"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PrevWordBoundary([]rune(tt.input), tt.cursor); got != tt.want {
				t.Fatalf("PrevWordBoundary(%q, %d) = %d, want %d", tt.input, tt.cursor, got, tt.want)
			}
		})
	}
}

func TestNextWordBoundary(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		cursor int
		want   int
	}{
		{name: "advances to next word start", input: "hello world", cursor: 0, want: len([]rune("hello "))},
		{name: "skips leading spaces", input: "  hello", cursor: 0, want: len([]rune("  "))},
		{name: "punctuation cluster", input: "foo/bar", cursor: len([]rune("foo")), want: len([]rune("foo/"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextWordBoundary([]rune(tt.input), tt.cursor); got != tt.want {
				t.Fatalf("NextWordBoundary(%q, %d) = %d, want %d", tt.input, tt.cursor, got, tt.want)
			}
		})
	}
}

func TestDeleteBeforeCursorString(t *testing.T) {
	got, cursor := DeleteBeforeCursorString("hello world", len([]rune("hello world")), true)
	if got != "hello " || cursor != len([]rune("hello ")) {
		t.Fatalf("DeleteBeforeCursorString word delete = (%q, %d)", got, cursor)
	}

	got, cursor = DeleteBeforeCursorString("hé", len([]rune("hé")), false)
	if got != "h" || cursor != len([]rune("h")) {
		t.Fatalf("DeleteBeforeCursorString rune delete = (%q, %d)", got, cursor)
	}
}

func TestDeleteAfterCursorString(t *testing.T) {
	got, cursor := DeleteAfterCursorString("hello world", 0, true)
	if got != "world" || cursor != 0 {
		t.Fatalf("DeleteAfterCursorString word delete = (%q, %d)", got, cursor)
	}

	got, cursor = DeleteAfterCursorString("hé", 0, false)
	if got != "é" || cursor != 0 {
		t.Fatalf("DeleteAfterCursorString rune delete = (%q, %d)", got, cursor)
	}
}
