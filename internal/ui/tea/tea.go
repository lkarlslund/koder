package tea

import (
	"errors"
	"strings"
	"time"
)

type Msg any

type Model interface {
	Init() Cmd
	Update(Msg) (Model, Cmd)
	View() string
}

type SurfaceModel interface {
	ViewSurface() SurfaceView
}

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
}

type Cmd func() Msg

type BatchMsg []Cmd

type QuitMsg struct{}

type WindowSizeMsg struct {
	Width  int
	Height int
}

type mouseModeMsg struct {
	enabled bool
}

type windowTitleMsg struct {
	title string
}

var ErrInterrupted = errors.New("interrupted")

var Quit Cmd = func() Msg { return QuitMsg{} }

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

func EnableMouseCellMotion() Msg {
	return mouseModeMsg{enabled: true}
}

func DisableMouse() Msg {
	return mouseModeMsg{enabled: false}
}

func SetWindowTitle(title string) Cmd {
	return func() Msg { return windowTitleMsg{title: title} }
}

type KeyType int

const (
	KeyUnknown KeyType = iota
	KeyRunes
	KeySpace
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyBackspace
	KeyDelete
	KeyEnter
	KeyTab
	KeyShiftTab
	KeyEsc
	KeyCtrlA
	KeyCtrlC
	KeyCtrlE
	KeyCtrlG
	KeyCtrlR
	KeyCtrlV
	KeyCtrlY
)

type KeyMsg struct {
	Type  KeyType
	Runes []rune
	Alt   bool
}

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
	case KeyCtrlC:
		base = "ctrl+c"
	case KeyCtrlE:
		base = "ctrl+e"
	case KeyCtrlG:
		base = "ctrl+g"
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

type MouseAction int

const (
	MouseActionPress MouseAction = iota
	MouseActionRelease
	MouseActionMotion
)

type MouseButton int

const (
	MouseButtonNone MouseButton = iota
	MouseButtonLeft
	MouseButtonMiddle
	MouseButtonRight
	MouseButtonWheelUp
	MouseButtonWheelDown
)

type MouseMsg struct {
	X      int
	Y      int
	Action MouseAction
	Button MouseButton
	Alt    bool
}

type ProgramOption func(*Program)

func WithAltScreen() ProgramOption {
	return func(p *Program) { p.altScreen = true }
}

func WithoutSignalHandler() ProgramOption {
	return func(p *Program) { p.noSignalHandler = true }
}
