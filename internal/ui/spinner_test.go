package ui

import "testing"

func TestSpinnerFrameAndNormalization(t *testing.T) {
	if got := NormalizeSpinner(""); got != "dots" {
		t.Fatalf("expected dots default, got %q", got)
	}
	if got := SpinnerFrame("circles", 1); got != "◓" {
		t.Fatalf("unexpected spinner frame: %q", got)
	}
	if len(SpinnerNames()) < 7 {
		t.Fatalf("expected all spinner styles registered, got %#v", SpinnerNames())
	}
}
