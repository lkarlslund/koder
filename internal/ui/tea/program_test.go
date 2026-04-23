package tea

import (
	"strings"
	"testing"
)

func TestRenderFrameAddressesRowsWithoutNewlines(t *testing.T) {
	got := renderFrame("alpha\nbeta\ngamma")
	if strings.Contains(got, "alpha\nbeta") {
		t.Fatalf("frame should not stream raw newlines between rows: %q", got)
	}
	wantParts := []string{
		"\x1b[H\x1b[2J",
		"\x1b[1;1Halpha",
		"\x1b[2;1Hbeta",
		"\x1b[3;1Hgamma",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("frame missing %q in %q", want, got)
		}
	}
}

func TestRenderFrameLinesAddressesRows(t *testing.T) {
	got := renderFrameLines([]string{"alpha", "beta", "gamma"})
	wantParts := []string{
		"\x1b[H\x1b[2J",
		"\x1b[1;1Halpha",
		"\x1b[2;1Hbeta",
		"\x1b[3;1Hgamma",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("frame missing %q in %q", want, got)
		}
	}
}
