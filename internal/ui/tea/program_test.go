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

func TestDiffFrameLinesSkipsUnchangedRows(t *testing.T) {
	got := diffFrameLines([]string{"alpha", "beta"}, []string{"alpha", "beta"})
	if got != "" {
		t.Fatalf("expected no output for unchanged frame, got %q", got)
	}
}

func TestDiffFrameLinesUpdatesOnlyChangedRows(t *testing.T) {
	got := diffFrameLines([]string{"alpha", "beta", "gamma"}, []string{"alpha", "BETA", "gamma"})
	if strings.Contains(got, "\x1b[1;1Halpha") || strings.Contains(got, "\x1b[3;1Hgamma") {
		t.Fatalf("expected unchanged rows to be skipped, got %q", got)
	}
	if !strings.Contains(got, "\x1b[2;1HBETA\x1b[K") {
		t.Fatalf("expected changed row to be rewritten with clear, got %q", got)
	}
}

func TestDiffFrameLinesClearsRemovedRows(t *testing.T) {
	got := diffFrameLines([]string{"alpha", "beta"}, []string{"alpha"})
	if !strings.Contains(got, "\x1b[2;1H\x1b[K") {
		t.Fatalf("expected removed row to be cleared, got %q", got)
	}
}
