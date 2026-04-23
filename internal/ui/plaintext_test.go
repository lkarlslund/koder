package ui

import "testing"

func TestPlainWordWrapWrapsByWords(t *testing.T) {
	got := PlainWordWrap("alpha beta gamma", 10)
	want := "alpha beta\ngamma"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestPlainWordWrapSplitsLongWord(t *testing.T) {
	got := PlainWordWrap("abcdefghij", 4)
	want := "abcd\nefgh\nij"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestPlainWordWrapHandlesWideRunes(t *testing.T) {
	got := PlainWordWrap("ab表cd", 4)
	want := "ab表\ncd"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestPlainWordWrapPreservesBlankLines(t *testing.T) {
	got := PlainWordWrap("alpha\n\nbeta gamma", 5)
	want := "alpha\n\nbeta\ngamma"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
