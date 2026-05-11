package ui

import (
	"errors"
	"strings"
	"time"
)

// Msg is a value delivered to the application update loop.
//
// Messages represent terminal input, runtime events, timer ticks, command
// results, or application-specific events. They are intentionally open-ended
// so callers can define their own message types without extending this package.
type Msg any

// Model is the application state machine driven by Program.
//
// Program calls Init once at startup, then calls Update for every Msg. After
// each renderable update, ViewSurface supplies the retained terminal surface
// that should be diffed against the previously rendered frame.
type Model interface {
	Init() Cmd
	Update(Msg) (Model, Cmd)
	ViewSurface() SurfaceView
}

// DirtyModel is an optional Model extension for retained views.
//
// Program consults it on coalesced frame ticks so a clean retained tree can
// skip ViewSurface and terminal diffing entirely.
type DirtyModel interface {
	ViewDirty() bool
}

// SurfaceView exposes the immutable cell data Program needs to paint a frame.
//
// Implementations may be backed by a concrete Surface or by another retained
// representation. Program only depends on this read-only interface so frame
// diffing can compare old and new surfaces without knowing how they are stored.
type SurfaceView interface {
	SurfaceWidth() int
	SurfaceHeight() int
	SurfaceCellText(x, y int) string
	SurfaceCellWidth(x, y int) int
	SurfaceCellContinuation(x, y int) bool
	SurfaceCellFG(x, y int) (uint8, uint8, uint8, bool)
	SurfaceCellBG(x, y int) (uint8, uint8, uint8, bool)
	SurfaceCellBold(x, y int) bool
	SurfaceCellItalic(x, y int) bool
	SurfaceCellUnderline(x, y int) bool
	SurfaceCellStrikethrough(x, y int) bool
}

// DirtyRowRangeProvider allows a SurfaceView to report a contiguous dirty span.
//
// Program uses this as a fallback when a surface cannot provide exact dirty
// rectangles. Returning ok=false means the renderer should do a full diff.
type DirtyRowRangeProvider interface {
	DirtyRowRange() (start int, end int, ok bool)
}

// Cmd is asynchronous work that eventually returns a Msg.
//
// Commands are run by Program in their own goroutine. Returning nil means the
// command completed without producing an event.
type Cmd func() Msg

// BatchMsg carries multiple commands through the message loop.
//
// It is normally produced by Batch and handled internally by Program.
type BatchMsg []Cmd

// QuitMsg requests Program.Run to exit cleanly.
type QuitMsg struct{}

// WindowSizeMsg reports the current terminal dimensions in cells.
type WindowSizeMsg struct {
	Width  int
	Height int
}

// FrameMsg is the scheduled render tick used to coalesce repaint work.
//
// Program sends this after ordinary messages have invalidated state, allowing
// many fast message updates to produce a single terminal frame.
type FrameMsg struct {
	At time.Time
}

// mouseModeMsg toggles terminal mouse tracking from inside the event loop.
type mouseModeMsg struct {
	enabled bool
}

// windowTitleMsg updates the terminal title from inside the event loop.
type windowTitleMsg struct {
	title string
}

// ErrInterrupted identifies a command or task cancelled by user interruption.
var ErrInterrupted = errors.New("interrupted")

// Quit is a command that emits QuitMsg.
var Quit Cmd = func() Msg { return QuitMsg{} }

// Batch combines commands into one command and drops nil entries.
//
// If every command is nil, Batch returns nil so callers can pass its result
// directly without creating no-op command work.
func Batch(cmds ...Cmd) Cmd {
	filtered := make([]Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd == nil {
			continue
		}
		filtered = append(filtered, cmd)
	}
	if len(filtered) == 0 {
		return nil
	}
	return func() Msg { return BatchMsg(filtered) }
}

// Tick returns a command that waits for d and maps the wake time to a Msg.
//
// A nil mapping function returns nil, matching the no-op Cmd convention.
func Tick(d time.Duration, fn func(time.Time) Msg) Cmd {
	if fn == nil {
		return nil
	}
	return func() Msg {
		timer := time.NewTimer(d)
		defer timer.Stop()
		t := <-timer.C
		return fn(t)
	}
}

// EnableMouseCellMotion enables terminal mouse press and drag events.
func EnableMouseCellMotion() Msg {
	return mouseModeMsg{enabled: true}
}

// DisableMouse disables terminal mouse tracking.
func DisableMouse() Msg {
	return mouseModeMsg{enabled: false}
}

// SetWindowTitle returns a command that updates the terminal window title.
func SetWindowTitle(title string) Cmd {
	return func() Msg { return windowTitleMsg{title: title} }
}

// KeyType classifies keyboard input after terminal escape decoding.
type KeyType int

const (
	KeyUnknown KeyType = iota
	KeyRunes
	KeySpace
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyPgUp
	KeyPgDown
	KeyHome
	KeyEnd
	KeyBackspace
	KeyDelete
	KeyEnter
	KeyTab
	KeyShiftTab
	KeyEsc
	KeyCtrlA
	KeyCtrlB
	KeyCtrlC
	KeyCtrlE
	KeyCtrlG
	KeyCtrlPgUp
	KeyCtrlPgDown
	KeyCtrlR
	KeyCtrlV
	KeyCtrlY
)

// KeyMsg is normalized keyboard input delivered to Model.Update.
//
// Runes is populated for printable text input. Alt is true for both native
// modifier sequences and ESC-prefixed key sequences that are commonly emitted
// by terminals for option/alt shortcuts.
type KeyMsg struct {
	Type  KeyType
	Runes []rune
	Alt   bool
}

// String returns a stable, human-readable representation of the key.
func (k KeyMsg) String() string {
	base := ""
	switch k.Type {
	case KeyRunes:
		base = string(k.Runes)
	case KeySpace:
		base = " "
	case KeyLeft:
		base = "left"
	case KeyRight:
		base = "right"
	case KeyUp:
		base = "up"
	case KeyDown:
		base = "down"
	case KeyPgUp:
		base = "pgup"
	case KeyPgDown:
		base = "pgdown"
	case KeyHome:
		base = "home"
	case KeyEnd:
		base = "end"
	case KeyBackspace:
		base = "backspace"
	case KeyDelete:
		base = "delete"
	case KeyEnter:
		base = "enter"
	case KeyTab:
		base = "tab"
	case KeyShiftTab:
		base = "shift+tab"
	case KeyEsc:
		base = "esc"
	case KeyCtrlA:
		base = "ctrl+a"
	case KeyCtrlB:
		base = "ctrl+b"
	case KeyCtrlC:
		base = "ctrl+c"
	case KeyCtrlE:
		base = "ctrl+e"
	case KeyCtrlG:
		base = "ctrl+g"
	case KeyCtrlPgUp:
		base = "ctrl+pgup"
	case KeyCtrlPgDown:
		base = "ctrl+pgdown"
	case KeyCtrlR:
		base = "ctrl+r"
	case KeyCtrlV:
		base = "ctrl+v"
	case KeyCtrlY:
		base = "ctrl+y"
	default:
		if len(k.Runes) > 0 {
			base = string(k.Runes)
		}
	}
	if k.Alt {
		if base == "" {
			return "alt+"
		}
		if strings.HasPrefix(base, "alt+") {
			return base
		}
		return "alt+" + base
	}
	return base
}

// MouseAction classifies the kind of mouse event.
type MouseAction int

const (
	MouseActionPress MouseAction = iota
	MouseActionRelease
	MouseActionMotion
)

// MouseButton identifies the mouse button or wheel direction.
type MouseButton int

const (
	MouseButtonNone MouseButton = iota
	MouseButtonLeft
	MouseButtonMiddle
	MouseButtonRight
	MouseButtonWheelUp
	MouseButtonWheelDown
)

// MouseMsg is normalized mouse input delivered to Model.Update.
type MouseMsg struct {
	X      int
	Y      int
	Action MouseAction
	Button MouseButton
	Alt    bool
}

// ProgramOption configures Program at construction time.
type ProgramOption func(*Program)

// WithAltScreen makes Program render in the terminal alternate screen buffer.
func WithAltScreen() ProgramOption {
	return func(p *Program) { p.altScreen = true }
}

// WithoutSignalHandler disables Program's signal handling hooks.
func WithoutSignalHandler() ProgramOption {
	return func(p *Program) { p.noSignalHandler = true }
}

// WithColorProfile overrides automatic terminal color profile detection.
func WithColorProfile(profile ColorProfile) ProgramOption {
	return func(p *Program) {
		p.colorProfile = profile
		p.colorProfileSet = true
	}
}
