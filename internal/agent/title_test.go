package agent

import "testing"

func TestShouldRefreshSessionTitle(t *testing.T) {
	cases := map[int]bool{
		1:  true,
		2:  false,
		3:  true,
		9:  false,
		10: true,
	}
	for input, want := range cases {
		if got := shouldRefreshSessionTitle(input); got != want {
			t.Fatalf("count %d: got %v want %v", input, got, want)
		}
	}
}

func TestNormalizeSessionTitle(t *testing.T) {
	got := normalizeSessionTitle(`"this is a much longer title than allowed"`)
	want := "this is a much longer title"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
