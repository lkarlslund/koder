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

func (w *stubWindow) PaintWindow(_ *Context, bounds Rect, dst *Surface) {
	canvas := NewCanvas(dst, bounds)
	for y := 0; y < bounds.H; y++ {
		for x := 0; x < bounds.W; x++ {
			canvas.WriteText(x, y, w.fill, CellStyle{})
		}
	}
}

func (w *stubWindow) WindowDirtyRects() []Rect {
	return []Rect{{W: w.bounds.W, H: w.bounds.H}}
}

func (w *stubWindow) CanPaintWindow() bool {
	return true
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

func (w *paletteWindow) PaintWindow(ctx *Context, bounds Rect, dst *Surface) {
	canvas := NewCanvas(dst, bounds)
	canvas.WriteText(0, 0, "x", CellStyle{FG: cellColor(ctx.Palette.MarkdownText)})
}

func (w *paletteWindow) WindowDirtyRects() []Rect {
	return []Rect{{W: w.bounds.W, H: w.bounds.H}}
}

func (w *paletteWindow) CanPaintWindow() bool {
	return true
}

type paletteElement struct{ PassiveNode }

func (paletteElement) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: 1, H: 1})
}

func (paletteElement) Render(ctx *Context, bounds Rect) Surface {
	s := BlankSurface(max(1, bounds.W), max(1, bounds.H))
	s.WriteText(0, 0, "x", CellStyle{FG: cellColor(ctx.Palette.MarkdownText)})
	return s.normalize(bounds.W, bounds.H)
}

func (paletteElement) Paint(ctx *Context, canvas Canvas) {
	canvas.WriteText(0, 0, "x", CellStyle{FG: cellColor(ctx.Palette.MarkdownText)})
}

type elementWindow struct {
	BaseWindow
	bounds  Rect
	element Node
}

func (w *elementWindow) Bounds(Rect) Rect {
	return w.bounds
}

func (w *elementWindow) PaintWindow(ctx *Context, bounds Rect, dst *Surface) {
	if w.element == nil {
		return
	}
	paintNodeInto(ctx, w.element, bounds, dst)
}

func (w *elementWindow) WindowDirtyRects() []Rect {
	return []Rect{{W: w.bounds.W, H: w.bounds.H}}
}

func (w *elementWindow) CanPaintWindow() bool {
	return true
}

func (w *elementWindow) InvalidateCaches(ctx *Context) {
	InvalidateNodeCaches(ctx, w.element)
	w.Dirty = true
}

type paintedWindow struct {
	BaseWindow
	bounds     Rect
	fill       string
	dirtyRects []Rect
	painted    int
}

func (w *paintedWindow) Bounds(Rect) Rect {
	return w.bounds
}

func (w *paintedWindow) CanPaintWindow() bool {
	return true
}

func (w *paintedWindow) PaintWindow(_ *Context, bounds Rect, dst *Surface) {
	w.painted++
	canvas := NewCanvas(dst, bounds)
	canvas.WriteText(0, 0, w.fill, CellStyle{})
}

func (w *paintedWindow) WindowDirtyRects() []Rect {
	return append([]Rect(nil), w.dirtyRects...)
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

func TestRootPaintsWindowPainterIntoFrame(t *testing.T) {
	root := NewRoot(theme.Default().Palette, Rect{W: 10, H: 4})
	main := &paintedWindow{
		BaseWindow: BaseWindow{WindowID: "main", VisibleFlag: true, Dirty: true},
		bounds:     Rect{X: 2, Y: 1, W: 4, H: 2},
		fill:       "p",
		dirtyRects: []Rect{{X: 0, Y: 0, W: 1, H: 1}},
	}
	root.SetMainWindow(main)

	frame := root.RenderFrame()
	if main.painted != 1 {
		t.Fatalf("expected painted window to paint once, got %d", main.painted)
	}
	if got := frame.SurfaceCellText(2, 1); got != "p" {
		t.Fatalf("expected painted cell at translated window origin, got %q", got)
	}
	rects, ok := frame.DirtyRects()
	if !ok || len(rects) == 0 {
		t.Fatalf("expected translated dirty rects, got %#v", rects)
	}
	want := Rect{X: 2, Y: 1, W: 1, H: 1}
	found := false
	for _, rect := range rects {
		if rect == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected translated dirty rect %v in %#v", want, rects)
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

func TestInvalidateNodeCachesReachesNestedCachedChild(t *testing.T) {
	first := theme.Resolve("tokyonight").Palette
	second := theme.Resolve("flexoki").Palette

	cached := NewCachedElement(AsNode(paletteElement{}), 1)
	element := Inset{
		Padding: UniformInsets(1),
		Child: AsNode(VisibleElement{
			Child: cached,
		}),
	}

	before := PaintNodeSurface(&Context{Palette: first}, AsNode(element), Rect{W: 4, H: 3})
	beforeR, beforeG, beforeB, beforeOK := before.SurfaceCellFG(1, 1)
	if !beforeOK {
		t.Fatal("expected foreground color before invalidation")
	}

	InvalidateNodeCaches(&Context{Palette: first}, AsNode(element))
	after := PaintNodeSurface(&Context{Palette: second}, AsNode(element), Rect{W: 4, H: 3})
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
		element: AsNode(Inset{
			Padding: UniformInsets(1),
			Child: AsNode(VisibleElement{
				Child: NewCachedElement(AsNode(paletteElement{}), 1),
			}),
		}),
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

type controlElement struct {
	PassiveNode
	id string
}

type countingControlElement struct {
	PassiveNode
	id          string
	renderCalls *int
}

func (e controlElement) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: 4, H: 1})
}

func (e controlElement) Render(ctx *Context, bounds Rect) Surface {
	if ctx != nil && ctx.Runtime != nil {
		ctx.Runtime.Register(Control{
			ID:      e.id,
			Rect:    Rect{X: bounds.X, Y: bounds.Y, W: max(1, bounds.W), H: max(1, bounds.H)},
			Enabled: true,
		})
	}
	return SurfaceFromString("test").normalize(bounds.W, bounds.H)
}

func (e controlElement) Paint(ctx *Context, canvas Canvas) {
	if ctx != nil && ctx.Runtime != nil {
		ctx.Runtime.Register(Control{
			ID:      e.id,
			Rect:    Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: max(1, canvas.Width()), H: max(1, canvas.Height())},
			Enabled: true,
		})
	}
	canvas.WriteText(0, 0, "test", CellStyle{})
}

func (e countingControlElement) Measure(_ *Context, constraints Constraints) Size {
	return constraints.Clamp(Size{W: 4, H: 1})
}

func (e countingControlElement) Render(ctx *Context, bounds Rect) Surface {
	if e.renderCalls != nil {
		*e.renderCalls = *e.renderCalls + 1
	}
	if ctx != nil && ctx.Runtime != nil {
		ctx.Runtime.Register(Control{
			ID:      e.id,
			Rect:    Rect{X: bounds.X, Y: bounds.Y, W: max(1, bounds.W), H: max(1, bounds.H)},
			Enabled: true,
		})
	}
	return SurfaceFromString("test").normalize(bounds.W, bounds.H)
}

func (e countingControlElement) Paint(ctx *Context, canvas Canvas) {
	if e.renderCalls != nil {
		*e.renderCalls = *e.renderCalls + 1
	}
	if ctx != nil && ctx.Runtime != nil {
		ctx.Runtime.Register(Control{
			ID:      e.id,
			Rect:    Rect{X: canvas.origin.X, Y: canvas.origin.Y, W: max(1, canvas.Width()), H: max(1, canvas.Height())},
			Enabled: true,
		})
	}
	canvas.WriteText(0, 0, "test", CellStyle{})
}

func TestCachedElementReRegistersControlsOnCachedRender(t *testing.T) {
	cached := NewCachedElement(AsNode(controlElement{id: "cached-control"}), 1)
	ctx := &Context{Palette: theme.Resolve("tokyonight").Palette}

	firstRuntime := &Runtime{}
	ctx.Runtime = firstRuntime
	_ = cached.RenderCached(ctx, 8)
	if len(firstRuntime.Controls()) != 1 {
		t.Fatalf("expected first render to register one control, got %d", len(firstRuntime.Controls()))
	}

	secondRuntime := &Runtime{}
	ctx.Runtime = secondRuntime
	_ = cached.RenderCached(ctx, 8)
	if len(secondRuntime.Controls()) != 1 {
		t.Fatalf("expected cached render to re-register one control, got %d", len(secondRuntime.Controls()))
	}
	if secondRuntime.Controls()[0].ID != "cached-control" {
		t.Fatalf("unexpected control on cached render: %#v", secondRuntime.Controls()[0])
	}
}

func TestCachedElementCacheHitDoesNotReRenderChild(t *testing.T) {
	renderCalls := 0
	cached := NewCachedElement(AsNode(countingControlElement{id: "cached-control", renderCalls: &renderCalls}), 1)
	ctx := &Context{Palette: theme.Resolve("tokyonight").Palette}

	firstRuntime := &Runtime{}
	ctx.Runtime = firstRuntime
	_ = cached.RenderCached(ctx, 8)
	if renderCalls != 1 {
		t.Fatalf("expected first render call count 1, got %d", renderCalls)
	}

	secondRuntime := &Runtime{}
	ctx.Runtime = secondRuntime
	_ = cached.RenderCached(ctx, 8)
	if renderCalls != 1 {
		t.Fatalf("expected cached render hit to avoid child rerender, got %d calls", renderCalls)
	}
	if len(secondRuntime.Controls()) != 1 {
		t.Fatalf("expected cached render to still register one control, got %d", len(secondRuntime.Controls()))
	}
}
