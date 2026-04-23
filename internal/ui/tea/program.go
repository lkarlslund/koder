package tea

import (
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/input"
	"github.com/charmbracelet/x/term"
)

type Program struct {
	model           Model
	altScreen       bool
	noSignalHandler bool

	mu           sync.Mutex
	title        string
	mouseEnabled bool
	sent         chan Msg
}

func NewProgram(model Model, opts ...ProgramOption) *Program {
	p := &Program{
		model: model,
		sent:  make(chan Msg, 256),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

func (p *Program) Send(msg Msg) {
	select {
	case p.sent <- msg:
	default:
		go func() { p.sent <- msg }()
	}
}

func (p *Program) Run() (Model, error) {
	in := os.Stdin
	out := os.Stdout

	restoreVT, err := enableVirtualTerminalIO(in, out)
	if err != nil {
		return p.model, err
	}
	defer restoreVT()

	oldState, err := term.MakeRaw(in.Fd())
	if err != nil {
		return p.model, err
	}
	defer term.Restore(in.Fd(), oldState)

	if p.altScreen {
		writeString(out, "\x1b[?1049h")
		defer writeString(out, "\x1b[?1049l")
	}
	writeString(out, "\x1b[?25l")
	defer writeString(out, "\x1b[?25h")

	events := make(chan Msg, 512)
	done := make(chan struct{})
	defer close(done)

	reader, err := input.NewReader(in, os.Getenv("TERM"), 0)
	if err != nil {
		return p.model, err
	}
	defer reader.Close()

	go p.readInput(reader, events, done)
	go p.watchSize(in, events, done)
	go p.forwardSent(events, done)

	if cmd := p.model.Init(); cmd != nil {
		p.runCmd(cmd, events)
	}
	if width, height, sizeErr := term.GetSize(in.Fd()); sizeErr == nil {
		events <- WindowSizeMsg{Width: width, Height: height}
	}
	if err := p.render(out); err != nil {
		return p.model, err
	}

	for {
		msg, ok := <-events
		if !ok {
			return p.model, nil
		}
		if quit, err := p.handleRuntimeMsg(msg, out); quit || err != nil {
			return p.model, err
		}
		next, cmd := p.model.Update(msg)
		p.model = next
		if cmd != nil {
			p.runCmd(cmd, events)
		}
		if err := p.render(out); err != nil {
			return p.model, err
		}
	}
}

func (p *Program) handleRuntimeMsg(msg Msg, out io.Writer) (bool, error) {
	switch typed := msg.(type) {
	case nil:
		return false, nil
	case QuitMsg:
		return true, nil
	case BatchMsg:
		for _, cmd := range typed {
			p.runCmd(cmd, p.sent)
		}
		return false, nil
	case mouseModeMsg:
		p.setMouseMode(out, typed.enabled)
		return false, nil
	case windowTitleMsg:
		p.setWindowTitle(out, typed.title)
		return false, nil
	default:
		return false, nil
	}
}

func (p *Program) runCmd(cmd Cmd, out chan<- Msg) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if msg == nil {
			return
		}
		out <- msg
	}()
}

func (p *Program) render(out io.Writer) error {
	view := p.model.View()
	_, err := io.WriteString(out, "\x1b[H\x1b[2J"+view)
	return err
}

func (p *Program) setWindowTitle(out io.Writer, title string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if title == p.title {
		return
	}
	p.title = title
	writeString(out, "\x1b]0;"+title+"\x07")
}

func (p *Program) setMouseMode(out io.Writer, enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mouseEnabled == enabled {
		return
	}
	p.mouseEnabled = enabled
	if enabled {
		writeString(out, "\x1b[?1002h\x1b[?1006h")
		return
	}
	writeString(out, "\x1b[?1002l\x1b[?1006l")
}

func (p *Program) forwardSent(out chan<- Msg, done <-chan struct{}) {
	for {
		select {
		case msg := <-p.sent:
			out <- msg
		case <-done:
			return
		}
	}
}

func (p *Program) watchSize(in *os.File, out chan<- Msg, done <-chan struct{}) {
	lastW, lastH := 0, 0
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w, h, err := term.GetSize(in.Fd())
			if err != nil {
				continue
			}
			if w == lastW && h == lastH {
				continue
			}
			lastW, lastH = w, h
			out <- WindowSizeMsg{Width: w, Height: h}
		case <-done:
			return
		}
	}
}

func (p *Program) readInput(reader *input.Reader, out chan<- Msg, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}
		evs, err := reader.ReadEvents()
		if err != nil {
			if err == io.EOF {
				return
			}
			continue
		}
		for _, ev := range evs {
			msg, ok := convertInputEvent(ev)
			if ok {
				out <- msg
			}
		}
	}
}

func writeString(w io.Writer, s string) {
	_, _ = io.WriteString(w, s)
}

func convertInputEvent(ev input.Event) (Msg, bool) {
	switch typed := ev.(type) {
	case input.KeyPressEvent:
		return convertKeyPress(typed), true
	case input.MouseClickEvent:
		m := typed.Mouse()
		return MouseMsg{X: m.X, Y: m.Y, Button: convertMouseButton(m.Button), Action: MouseActionPress, Alt: m.Mod.Contains(input.ModAlt)}, true
	case input.MouseWheelEvent:
		m := typed.Mouse()
		return MouseMsg{X: m.X, Y: m.Y, Button: convertMouseButton(m.Button), Action: MouseActionPress, Alt: m.Mod.Contains(input.ModAlt)}, true
	case input.MouseReleaseEvent:
		m := typed.Mouse()
		return MouseMsg{X: m.X, Y: m.Y, Button: convertMouseButton(m.Button), Action: MouseActionRelease, Alt: m.Mod.Contains(input.ModAlt)}, true
	case input.WindowSizeEvent:
		return WindowSizeMsg{Width: typed.Width, Height: typed.Height}, true
	default:
		return nil, false
	}
}

func convertKeyPress(ev input.KeyPressEvent) KeyMsg {
	key := ev.Key()
	msg := KeyMsg{Alt: key.Mod.Contains(input.ModAlt)}
	switch key.Code {
	case input.KeyLeft:
		msg.Type = KeyLeft
	case input.KeyRight:
		msg.Type = KeyRight
	case input.KeyUp:
		msg.Type = KeyUp
	case input.KeyDown:
		msg.Type = KeyDown
	case input.KeyHome:
		msg.Type = KeyHome
	case input.KeyEnd:
		msg.Type = KeyEnd
	case input.KeyBackspace:
		msg.Type = KeyBackspace
	case input.KeyDelete:
		msg.Type = KeyDelete
	case input.KeyEnter:
		msg.Type = KeyEnter
	case input.KeyTab:
		if key.Mod.Contains(input.ModShift) {
			msg.Type = KeyShiftTab
		} else {
			msg.Type = KeyTab
		}
	case input.KeyEsc:
		msg.Type = KeyEsc
	case input.KeySpace:
		msg.Type = KeySpace
	default:
		if key.Mod.Contains(input.ModCtrl) {
			switch strings.ToLower(key.Text) {
			case "a":
				msg.Type = KeyCtrlA
			case "c":
				msg.Type = KeyCtrlC
			case "e":
				msg.Type = KeyCtrlE
			case "g":
				msg.Type = KeyCtrlG
			case "r":
				msg.Type = KeyCtrlR
			case "v":
				msg.Type = KeyCtrlV
			case "y":
				msg.Type = KeyCtrlY
			}
		}
		if msg.Type == KeyUnknown {
			switch strings.ToLower(ev.String()) {
			case "ctrl+a":
				msg.Type = KeyCtrlA
			case "ctrl+c":
				msg.Type = KeyCtrlC
			case "ctrl+e":
				msg.Type = KeyCtrlE
			case "ctrl+g":
				msg.Type = KeyCtrlG
			case "ctrl+r":
				msg.Type = KeyCtrlR
			case "ctrl+v":
				msg.Type = KeyCtrlV
			case "ctrl+y":
				msg.Type = KeyCtrlY
			}
		}
	}
	if msg.Type == KeyUnknown {
		if key.Text == " " {
			msg.Type = KeySpace
		} else if key.Text != "" {
			msg.Type = KeyRunes
			msg.Runes = []rune(key.Text)
		}
	}
	return msg
}

func convertMouseButton(button input.MouseButton) MouseButton {
	switch button {
	case input.MouseLeft:
		return MouseButtonLeft
	case input.MouseMiddle:
		return MouseButtonMiddle
	case input.MouseRight:
		return MouseButtonRight
	case input.MouseWheelUp:
		return MouseButtonWheelUp
	case input.MouseWheelDown:
		return MouseButtonWheelDown
	default:
		return MouseButtonNone
	}
}
