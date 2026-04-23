package ui

import (
	"testing"
	"time"

	tea "github.com/lkarlslund/koder/internal/ui/tea"
	"github.com/lkarlslund/koder/internal/theme"
)

type stubWindow struct {
	BaseWindow
	bounds       Rect
	keys         []string
	mousePresses int
	timerEvents  int
	fill         string
}

func newStubWindow(id WindowID, z int, bounds Rect) *stubWindow {
	return &stubWindow{
		BaseWindow: BaseWindow{
			WindowID:      id,
			Order:         z,
			FocusableFlag: true,
			VisibleFlag:   true,
			Dirty:         true,
		},
		bounds: bounds,
		fill:   string(id[0]),
	}
}

func (w *stubWindow) Bounds(Rect) Rect {
	return w.bounds
}

func (w *stubWindow) HandleKey(msg KeyEvent) (bool, tea.Cmd) {
	w.keys = append(w.keys, msg.String())
	w.Dirty = true
	return true, nil
}

func (w *stubWindow) HandleMouse(msg MouseEvent) (bool, tea.Cmd) {
	if msg.Action == tea.MouseActionPress {
		w.mousePresses++
	}
	w.Dirty = true
	return true, nil
}

func (w *stubWindow) HandleTimer(TimerEvent) (bool, tea.Cmd) {
	w.timerEvents++
	w.Dirty = true
	return true, nil
}

func (w *stubWindow) Render(*Context, Rect) Surface {
	s := BlankSurface(w.bounds.W, w.bounds.H)
	for y := 0; y < w.bounds.H; y++ {
		for x := 0; x < w.bounds.W; x++ {
			s.WriteText(x, y, w.fill, CellStyle{})
		}
	}
	return s
}

func TestRootRoutesKeysToFocusedWindow(t *testing.T) {
	root := NewRoot(theme.Default().Palette, Rect{W: 20, H: 10})
	main := newStubWindow("main", 0, Rect{W: 20, H: 10})
	modal := newStubWindow("modal", 10, Rect{X: 4, Y: 2, W: 8, H: 4})
	modal.ModalFlag = true
	root.SetMainWindow(main)
	root.PushWindow(modal)

	handled, _ := root.HandleEvent(KeyEvent{Type: tea.KeyEnter})
	if !handled {
		t.Fatal("expected focused window to handle key")
	}
	if len(main.keys) != 0 {
		t.Fatalf("expected main window to stay unfocused, got %v", main.keys)
	}
	if len(modal.keys) != 1 || modal.keys[0] != "enter" {
		t.Fatalf("expected modal window to receive enter, got %v", modal.keys)
	}
	if got := root.FocusedWindow(); got != "modal" {
		t.Fatalf("expected modal focus, got %q", got)
	}
}

func TestRootRestoresFocusWhenModalCloses(t *testing.T) {
	root := NewRoot(theme.Default().Palette, Rect{W: 20, H: 10})
	main := newStubWindow("main", 0, Rect{W: 20, H: 10})
	modal := newStubWindow("modal", 10, Rect{X: 4, Y: 2, W: 8, H: 4})
	modal.ModalFlag = true
	root.SetMainWindow(main)
	root.PushWindow(modal)

	root.PopWindow("modal")
	if got := root.FocusedWindow(); got != "main" {
		t.Fatalf("expected focus to return to main, got %q", got)
	}
	handled, _ := root.HandleEvent(KeyEvent{Type: tea.KeyTab})
	if !handled {
		t.Fatal("expected main window to handle key after modal closes")
	}
	if len(main.keys) != 1 || main.keys[0] != "tab" {
		t.Fatalf("expected main window key history, got %v", main.keys)
	}
}

func TestRootRoutesMouseToTopmostHitWindow(t *testing.T) {
	root := NewRoot(theme.Default().Palette, Rect{W: 20, H: 10})
	main := newStubWindow("main", 0, Rect{W: 20, H: 10})
	modal := newStubWindow("modal", 10, Rect{X: 4, Y: 2, W: 8, H: 4})
	modal.ModalFlag = true
	root.SetMainWindow(main)
	root.PushWindow(modal)

	handled, _ := root.HandleEvent(MouseEvent{X: 5, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !handled {
		t.Fatal("expected topmost hit window to handle mouse")
	}
	if modal.mousePresses != 1 {
		t.Fatalf("expected modal mouse press count 1, got %d", modal.mousePresses)
	}
	if main.mousePresses != 0 {
		t.Fatalf("expected main window to receive no mouse presses, got %d", main.mousePresses)
	}
}

func TestRootComposesWindowsInZOrder(t *testing.T) {
	root := NewRoot(theme.Default().Palette, Rect{W: 20, H: 10})
	main := newStubWindow("main", 0, Rect{W: 20, H: 10})
	main.fill = "m"
	modal := newStubWindow("modal", 10, Rect{X: 4, Y: 2, W: 8, H: 4})
	modal.fill = "o"
	root.SetMainWindow(main)
	root.PushWindow(modal)

	frame := root.RenderFrame()
	if got := frame.SurfaceCellText(0, 0); got != "m" {
		t.Fatalf("expected main window at origin, got %q", got)
	}
	if got := frame.SurfaceCellText(5, 3); got != "o" {
		t.Fatalf("expected modal overlay at 5,3, got %q", got)
	}
}

func TestTimerSchedulerStopsOwnerTimers(t *testing.T) {
	var timers TimerScheduler
	first := timers.StartTimer("composer", TimerSpec{Interval: time.Second, Repeat: true})
	second := timers.StartTimer("composer", TimerSpec{Interval: time.Second, Repeat: true})
	timers.StartTimer("other", TimerSpec{Interval: time.Second, Repeat: true})
	timers.StopOwnerTimers("composer")

	active := timers.Active("")
	if len(active) != 1 {
		t.Fatalf("expected one active timer after stop-owner, got %v", active)
	}
	if active[0] == first || active[0] == second {
		t.Fatalf("expected composer timers to stop, got %v", active)
	}
}

func TestRootRoutesTimerEventsToOwningWindow(t *testing.T) {
	root := NewRoot(theme.Default().Palette, Rect{W: 20, H: 10})
	main := newStubWindow("main", 0, Rect{W: 20, H: 10})
	root.SetMainWindow(main)

	handled, _ := root.HandleEvent(TimerEvent{ID: 1, Owner: "main", At: time.Now()})
	if !handled {
		t.Fatal("expected timer event to be handled")
	}
	if main.timerEvents != 1 {
		t.Fatalf("expected one timer event, got %d", main.timerEvents)
	}
}
