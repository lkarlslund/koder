package tokenestimate

import "testing"

func TestText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "empty", text: "", want: 0},
		{name: "words", text: "one two three", want: 3},
		{name: "punctuation", text: "...\n", want: 1},
		{name: "spaces only", text: " \t\n", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Text(tt.text); got != tt.want {
				t.Fatalf("Text(%q) = %d, want %d", tt.text, got, tt.want)
			}
		})
	}
}
