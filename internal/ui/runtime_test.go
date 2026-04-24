package ui

import (
	"testing"
	"time"

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

func (w *stubWindow) HandleKey(msg KeyEvent) (bool, Cmd) {
	w.keys = append(w.keys, msg.String())
	w.Dirty = true
	return true, nil
}

func (w *stubWindow) HandleMouse(msg MouseEvent) (bool, Cmd) {
	if msg.Action == MouseActionPress {
		w.mousePresses++
	}
	w.Dirty = true
	return true, nil
}

func (w *stubWindow) HandleTimer(TimerEvent) (bool, Cmd) {
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

type paletteWindow struct {
	BaseWindow
	bounds Rect
}

func (w *paletteWindow) Bounds(Rect) Rect {
	return w.bounds
}

func (w *paletteWindow) Render(ctx *Context, bounds Rect) Surface {
	s := BlankSurface(bounds.W, bounds.H)
	s.WriteText(0, 0, "x", CellStyle{FG: cellColor(ctx.Palette.MarkdownText)})
	return s
}

type paletteElement struct{}

func (paletteElement) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: 1, H: 1})
}

func (paletteElement) Render(ctx *Context, bounds Rect) Surface {
	s := BlankSurface(max(1, bounds.W), max(1, bounds.H))
	s.WriteText(0, 0, "x", CellStyle{FG: cellColor(ctx.Palette.MarkdownText)})
	return s.normalize(bounds.W, bounds.H)
}

type elementWindow struct {
	BaseWindow
	bounds  Rect
	element Element
}

func (w *elementWindow) Bounds(Rect) Rect {
	return w.bounds
}

func (w *elementWindow) Render(ctx *Context, bounds Rect) Surface {
	if w.element == nil {
		return BlankSurface(bounds.W, bounds.H)
	}
	return w.element.Render(ctx, bounds)
}

func (w *elementWindow) InvalidateCaches(ctx *Context) {
	InvalidateElementCaches(ctx, w.element)
	w.Dirty = true
}

func TestRootRoutesKeysToFocusedWindow(t *testing.T) {
	root := NewRoot(theme.Default().Palette, Rect{W: 20, H: 10})
	main := newStubWindow("main", 0, Rect{W: 20, H: 10})
	modal := newStubWindow("modal", 10, Rect{X: 4, Y: 2, W: 8, H: 4})
	modal.ModalFlag = true
	root.SetMainWindow(main)
	root.PushWindow(modal)

	handled, _ := root.HandleEvent(KeyEvent{Type: KeyEnter})
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
	handled, _ := root.HandleEvent(KeyEvent{Type: KeyTab})
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

	handled, _ := root.HandleEvent(MouseEvent{X: 5, Y: 3, Action: MouseActionPress, Button: MouseButtonLeft})
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

func TestRootSetPaletteUpdatesRenderContext(t *testing.T) {
	first := theme.Resolve("tokyonight").Palette
	second := theme.Resolve("flexoki").Palette

	root := NewRoot(first, Rect{W: 4, H: 2})
	root.SetMainWindow(&paletteWindow{
		BaseWindow: BaseWindow{WindowID: "main", VisibleFlag: true},
		bounds:     Rect{W: 4, H: 2},
	})

	initial := root.RenderFrame()
	beforeR, beforeG, beforeB, beforeOK := initial.SurfaceCellFG(0, 0)
	if !beforeOK {
		t.Fatal("expected initial foreground color")
	}

	root.SetPalette(second)
	updated := root.RenderFrame()
	afterR, afterG, afterB, afterOK := updated.SurfaceCellFG(0, 0)
	if !afterOK {
		t.Fatal("expected updated foreground color")
	}
	if beforeR == afterR && beforeG == afterG && beforeB == afterB {
		t.Fatal("expected palette change to affect render output")
	}
}

func TestInvalidateElementCachesReachesNestedCachedChild(t *testing.T) {
	first := theme.Resolve("tokyonight").Palette
	second := theme.Resolve("flexoki").Palette

	cached := NewCachedElement(paletteElement{}, 1)
	element := Inset{
		Padding: UniformInsets(1),
		Child: VisibleElement{
			Child: cached,
		},
	}

	before := element.Render(&Context{Palette: first}, Rect{W: 4, H: 3})
	beforeR, beforeG, beforeB, beforeOK := before.SurfaceCellFG(1, 1)
	if !beforeOK {
		t.Fatal("expected foreground color before invalidation")
	}

	InvalidateElementCaches(&Context{Palette: first}, element)
	after := element.Render(&Context{Palette: second}, Rect{W: 4, H: 3})
	afterR, afterG, afterB, afterOK := after.SurfaceCellFG(1, 1)
	if !afterOK {
		t.Fatal("expected foreground color after invalidation")
	}
	if beforeR == afterR && beforeG == afterG && beforeB == afterB {
		t.Fatal("expected nested cached element to repaint after invalidation")
	}
}

func TestRootSetPaletteInvalidatesCachedDescendants(t *testing.T) {
	first := theme.Resolve("tokyonight").Palette
	second := theme.Resolve("flexoki").Palette

	root := NewRoot(first, Rect{W: 6, H: 4})
	root.SetMainWindow(&elementWindow{
		BaseWindow: BaseWindow{WindowID: "main", VisibleFlag: true, Dirty: true},
		bounds:     Rect{W: 6, H: 4},
		element: Inset{
			Padding: UniformInsets(1),
			Child: VisibleElement{
				Child: NewCachedElement(paletteElement{}, 1),
			},
		},
	})

	before := root.RenderFrame()
	beforeR, beforeG, beforeB, beforeOK := before.SurfaceCellFG(1, 1)
	if !beforeOK {
		t.Fatal("expected foreground color before palette update")
	}

	root.SetPalette(second)
	after := root.RenderFrame()
	afterR, afterG, afterB, afterOK := after.SurfaceCellFG(1, 1)
	if !afterOK {
		t.Fatal("expected foreground color after palette update")
	}
	if beforeR == afterR && beforeG == afterG && beforeB == afterB {
		t.Fatal("expected root palette update to invalidate cached descendants")
	}
}
