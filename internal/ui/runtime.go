package ui

import (
	"cmp"
	"github.com/lkarlslund/koder/internal/theme"
	"slices"
	"time"
)

type Event interface{}
type KeyEvent = KeyMsg
type MouseEvent = MouseMsg

type ElementID string
type WindowID string
type TimerID int64

type FocusEvent struct {
	Focused bool
}

type TimerEvent struct {
	ID    TimerID
	Owner string
	At    time.Time
}

type TimerSpec struct {
	Interval time.Duration
	Repeat   bool
}

type TimerScheduler struct {
	nextID TimerID
	timers map[TimerID]scheduledTimer
}

type scheduledTimer struct {
	id       TimerID
	owner    string
	spec     TimerSpec
	nextFire time.Time
}

func (s *TimerScheduler) StartTimer(owner string, spec TimerSpec) TimerID {
	if spec.Interval <= 0 {
		return 0
	}
	if s.timers == nil {
		s.timers = make(map[TimerID]scheduledTimer)
	}
	s.nextID++
	timer := scheduledTimer{
		id:       s.nextID,
		owner:    owner,
		spec:     spec,
		nextFire: time.Now().Add(spec.Interval),
	}
	s.timers[timer.id] = timer
	return timer.id
}

func (s *TimerScheduler) StopTimer(id TimerID) {
	if s == nil || id == 0 {
		return
	}
	delete(s.timers, id)
}

func (s *TimerScheduler) StopOwnerTimers(owner string) {
	if s == nil || owner == "" {
		return
	}
	for id, timer := range s.timers {
		if timer.owner == owner {
			delete(s.timers, id)
		}
	}
}

func (s *TimerScheduler) Active(owner string) []TimerID {
	if s == nil {
		return nil
	}
	ids := make([]TimerID, 0, len(s.timers))
	for id, timer := range s.timers {
		if owner == "" || timer.owner == owner {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func (s *TimerScheduler) Due(now time.Time) []TimerEvent {
	if s == nil || len(s.timers) == 0 {
		return nil
	}
	ids := make([]TimerID, 0, len(s.timers))
	for id := range s.timers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	events := make([]TimerEvent, 0, len(ids))
	for _, id := range ids {
		timer, ok := s.timers[id]
		if !ok || timer.nextFire.After(now) {
			continue
		}
		events = append(events, TimerEvent{ID: timer.id, Owner: timer.owner, At: now})
		if timer.spec.Repeat {
			timer.nextFire = now.Add(timer.spec.Interval)
			s.timers[id] = timer
			continue
		}
		delete(s.timers, id)
	}
	return events
}

func (s *TimerScheduler) NextDelay(now time.Time) (time.Duration, bool) {
	if s == nil || len(s.timers) == 0 {
		return 0, false
	}
	var next time.Time
	for _, timer := range s.timers {
		if next.IsZero() || timer.nextFire.Before(next) {
			next = timer.nextFire
		}
	}
	if next.IsZero() {
		return 0, false
	}
	if !next.After(now) {
		return 0, true
	}
	return next.Sub(now), true
}

type Window interface {
	ID() WindowID
	Bounds(root Rect) Rect
	ZIndex() int
	Focusable() bool
	Visible() bool
	Modal() bool
	NeedsRedraw() bool
	ClearRedraw()
	Focus()
	Blur()
	HandleKey(KeyEvent) (bool, Cmd)
	HandleMouse(MouseEvent) (bool, Cmd)
	Render(ctx *Context, bounds Rect) Surface
}

type TimerHandler interface {
	HandleTimer(TimerEvent) (bool, Cmd)
}

type BaseWindow struct {
	WindowID      WindowID
	Order         int
	FocusableFlag bool
	VisibleFlag   bool
	ModalFlag     bool
	Dirty         bool
	OnFocus       func()
	OnBlur        func()
}

func (w *BaseWindow) ID() WindowID {
	return w.WindowID
}

func (w *BaseWindow) Bounds(root Rect) Rect {
	return root
}

func (w *BaseWindow) ZIndex() int {
	return w.Order
}

func (w *BaseWindow) Focusable() bool {
	return w.FocusableFlag
}

func (w *BaseWindow) Visible() bool {
	return w.VisibleFlag
}

func (w *BaseWindow) Modal() bool {
	return w.ModalFlag
}

func (w *BaseWindow) NeedsRedraw() bool {
	return w.Dirty
}

func (w *BaseWindow) ClearRedraw() {
	w.Dirty = false
}

func (w *BaseWindow) Focus() {
	if w.OnFocus != nil {
		w.OnFocus()
	}
	w.Dirty = true
}

func (w *BaseWindow) Blur() {
	if w.OnBlur != nil {
		w.OnBlur()
	}
	w.Dirty = true
}

func (w *BaseWindow) HandleKey(KeyEvent) (bool, Cmd) {
	return false, nil
}

func (w *BaseWindow) HandleMouse(MouseEvent) (bool, Cmd) {
	return false, nil
}

func (w *BaseWindow) Render(*Context, Rect) Surface {
	return Surface{}
}

type Root struct {
	palette       theme.Palette
	bounds        Rect
	main          Window
	windows       []Window
	focused       WindowID
	dirty         bool
	previous      Surface
	timerSchedule TimerScheduler
}

func NewRoot(palette theme.Palette, bounds Rect) *Root {
	return &Root{
		palette: palette,
		bounds:  bounds,
		dirty:   true,
	}
}

func (r *Root) SetBounds(bounds Rect) {
	if r == nil {
		return
	}
	if r.bounds == bounds {
		return
	}
	r.bounds = bounds
	r.RequestRedraw()
}

func (r *Root) Bounds() Rect {
	if r == nil {
		return Rect{}
	}
	return r.bounds
}

func (r *Root) SetMainWindow(window Window) {
	if r == nil {
		return
	}
	r.main = window
	r.RequestRedraw()
	if window != nil && window.Focusable() && r.focused == "" {
		r.FocusWindow(window.ID())
	}
}

func (r *Root) PushWindow(window Window) {
	if r == nil || window == nil {
		return
	}
	for idx, existing := range r.windows {
		if existing.ID() == window.ID() {
			r.windows[idx] = window
			r.RequestRedraw()
			return
		}
	}
	r.windows = append(r.windows, window)
	r.RequestRedraw()
	if window.Focusable() {
		r.FocusWindow(window.ID())
	}
}

func (r *Root) SetWindows(windows []Window) {
	if r == nil {
		return
	}
	desired := make(map[WindowID]Window, len(windows))
	ordered := make([]Window, 0, len(windows))
	for _, window := range windows {
		if window == nil {
			continue
		}
		desired[window.ID()] = window
		ordered = append(ordered, window)
	}
	for _, existing := range r.windows {
		if existing == nil {
			continue
		}
		if _, ok := desired[existing.ID()]; ok {
			continue
		}
		r.timerSchedule.StopOwnerTimers(string(existing.ID()))
		if r.focused == existing.ID() {
			existing.Blur()
			r.focused = ""
		}
	}
	r.windows = ordered
	if r.focused != "" && r.windowByID(r.focused) == nil {
		r.focused = ""
	}
	if r.focused == "" {
		if next := r.topFocusableWindow(""); next != nil {
			r.FocusWindow(next.ID())
		}
	}
	r.RequestRedraw()
}

func (r *Root) PopWindow(id WindowID) {
	if r == nil || id == "" {
		return
	}
	var removed Window
	r.windows = slices.DeleteFunc(r.windows, func(window Window) bool {
		if window == nil || window.ID() != id {
			return false
		}
		removed = window
		return true
	})
	if removed == nil {
		return
	}
	removed.Blur()
	r.timerSchedule.StopOwnerTimers(string(id))
	if r.focused == id {
		r.focused = ""
		if next := r.topFocusableWindow(""); next != nil {
			r.FocusWindow(next.ID())
		}
	}
	r.RequestRedraw()
}

func (r *Root) FocusWindow(id WindowID) {
	if r == nil || id == "" || r.focused == id {
		return
	}
	next := r.windowByID(id)
	if next == nil || !next.Focusable() {
		return
	}
	if current := r.windowByID(r.focused); current != nil {
		current.Blur()
	}
	r.focused = id
	next.Focus()
	r.RequestRedraw()
}

func (r *Root) FocusedWindow() WindowID {
	if r == nil {
		return ""
	}
	return r.focused
}

func (r *Root) RequestRedraw() {
	if r == nil {
		return
	}
	r.dirty = true
}

func (r *Root) NeedsRedraw() bool {
	if r == nil || r.dirty {
		return true
	}
	if r.main != nil && r.main.NeedsRedraw() {
		return true
	}
	for _, window := range r.windows {
		if window != nil && window.NeedsRedraw() {
			return true
		}
	}
	return false
}

func (r *Root) PreviousFrame() Surface {
	if r == nil {
		return Surface{}
	}
	return r.previous
}

func (r *Root) StartTimer(owner string, spec TimerSpec) TimerID {
	return r.timerSchedule.StartTimer(owner, spec)
}

func (r *Root) ActiveTimers(owner string) []TimerID {
	return r.timerSchedule.Active(owner)
}

func (r *Root) StopTimer(id TimerID) {
	r.timerSchedule.StopTimer(id)
}

func (r *Root) StopOwnerTimers(owner string) {
	r.timerSchedule.StopOwnerTimers(owner)
}

func (r *Root) DueTimers(now time.Time) []TimerEvent {
	return r.timerSchedule.Due(now)
}

func (r *Root) NextTimerDelay(now time.Time) (time.Duration, bool) {
	return r.timerSchedule.NextDelay(now)
}

func (r *Root) HandleEvent(event Event) (bool, Cmd) {
	if r == nil {
		return false, nil
	}
	switch typed := event.(type) {
	case KeyEvent:
		window := r.windowByID(r.focused)
		if window == nil {
			window = r.topFocusableWindow("")
			if window != nil {
				r.FocusWindow(window.ID())
			}
		}
		if window == nil {
			return false, nil
		}
		handled, cmd := window.HandleKey(typed)
		if handled {
			r.RequestRedraw()
		}
		return handled, cmd
	case MouseEvent:
		window := r.windowAt(Point{X: max(0, typed.X-1), Y: typed.Y})
		if window == nil {
			return false, nil
		}
		if typed.Action == MouseActionPress && typed.Button == MouseButtonLeft && window.Focusable() {
			r.FocusWindow(window.ID())
		}
		handled, cmd := window.HandleMouse(typed)
		if handled {
			r.RequestRedraw()
		}
		return handled, cmd
	case TimerEvent:
		for _, window := range r.allWindows() {
			handler, ok := window.(TimerHandler)
			if !ok || window == nil {
				continue
			}
			if handled, cmd := handler.HandleTimer(typed); handled {
				r.RequestRedraw()
				return true, cmd
			}
		}
	}
	return false, nil
}

func (r *Root) RenderFrame() Surface {
	if r == nil {
		return Surface{}
	}
	root := BlankSurface(max(0, r.bounds.W), max(0, r.bounds.H))
	ctx := &Context{Palette: r.palette}
	for _, window := range r.allWindows() {
		if window == nil || !window.Visible() {
			continue
		}
		bounds := clipRect(window.Bounds(r.bounds), r.bounds)
		if bounds.W <= 0 || bounds.H <= 0 {
			continue
		}
		root = root.PlaceAt(bounds.X-r.bounds.X, bounds.Y-r.bounds.Y, window.Render(ctx, Rect{W: bounds.W, H: bounds.H}).Normalize(bounds.W, bounds.H))
		window.ClearRedraw()
	}
	r.previous = root
	r.dirty = false
	return root
}

func (r *Root) allWindows() []Window {
	windows := make([]Window, 0, len(r.windows)+1)
	if r.main != nil {
		windows = append(windows, r.main)
	}
	for _, window := range r.windows {
		if window != nil {
			windows = append(windows, window)
		}
	}
	slices.SortStableFunc(windows, func(a, b Window) int {
		return cmp.Compare(a.ZIndex(), b.ZIndex())
	})
	return windows
}

func (r *Root) windowByID(id WindowID) Window {
	if id == "" {
		return nil
	}
	for _, window := range r.allWindows() {
		if window != nil && window.ID() == id {
			return window
		}
	}
	return nil
}

func (r *Root) topFocusableWindow(exclude WindowID) Window {
	windows := r.allWindows()
	for idx := len(windows) - 1; idx >= 0; idx-- {
		window := windows[idx]
		if window == nil || window.ID() == exclude || !window.Visible() || !window.Focusable() {
			continue
		}
		return window
	}
	return nil
}

func (r *Root) windowAt(p Point) Window {
	windows := r.allWindows()
	blocked := false
	for idx := len(windows) - 1; idx >= 0; idx-- {
		window := windows[idx]
		if window == nil || !window.Visible() {
			continue
		}
		bounds := clipRect(window.Bounds(r.bounds), r.bounds)
		if bounds.Contains(p) {
			return window
		}
		if window.Modal() {
			blocked = true
		}
		if blocked {
			return nil
		}
	}
	return nil
}

func clipRect(rect, bounds Rect) Rect {
	if rect.W <= 0 || rect.H <= 0 || bounds.W <= 0 || bounds.H <= 0 {
		return Rect{}
	}
	left := max(rect.X, bounds.X)
	top := max(rect.Y, bounds.Y)
	right := min(rect.X+rect.W, bounds.X+bounds.W)
	bottom := min(rect.Y+rect.H, bounds.Y+bounds.H)
	if right <= left || bottom <= top {
		return Rect{}
	}
	return Rect{X: left, Y: top, W: right - left, H: bottom - top}
}
