package ui

import (
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

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

func TestSpinnerOwnsTimerAndInvalidatesItself(t *testing.T) {
	palette := theme.Default().Palette
	spinner := &Spinner{}
	spinner.Set("circles", "Working", true, palette)
	root := NewRoot(palette, Rect{W: 20, H: 1})
	SyncSpinnerTimers(root, spinner)

	if timers := root.ActiveTimers(SpinnerTimerOwner); len(timers) == 0 {
		t.Fatal("expected active spinner to own a timer")
	}
	spinner.Layout(&Context{}, Rect{W: 20, H: 1})
	spinner.ClearDirty()
	if !HandleSpinnerTimer(spinner, TimerEvent{Owner: SpinnerTimerOwner}) {
		t.Fatal("expected spinner timer to be handled")
	}
	if !spinner.NeedsPaint() {
		t.Fatal("expected spinner to invalidate itself on timer tick")
	}
	if got := spinner.line(); got != "◓  Working" {
		t.Fatalf("expected next spinner frame, got %q", got)
	}
}

func TestSpinnerStopsTimerWhenInactive(t *testing.T) {
	palette := theme.Default().Palette
	spinner := &Spinner{}
	spinner.Set("dots", "Working", true, palette)
	root := NewRoot(palette, Rect{W: 20, H: 1})
	SyncSpinnerTimers(root, spinner)
	if timers := root.ActiveTimers(SpinnerTimerOwner); len(timers) == 0 {
		t.Fatal("expected active spinner timer")
	}

	spinner.Set("dots", "Working", false, palette)
	SyncSpinnerTimers(root, spinner)
	if timers := root.ActiveTimers(SpinnerTimerOwner); len(timers) != 0 {
		t.Fatalf("expected inactive spinner to stop timer, got %v", timers)
	}
}
