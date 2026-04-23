package tea

import (
	"fmt"
	"io"
	"os"
	"strconv"
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
	renderedRows []string
	rendered     SurfaceView
	didRender    bool
}

type inputEventReader interface {
	ReadEvents() ([]input.Event, error)
	Close() error
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
	frame := ""
	if surfaceModel, ok := p.model.(SurfaceModel); ok {
		surface := surfaceModel.ViewSurface()
		if !p.didRender {
			frame = renderFrameSurface(surface)
			p.didRender = true
		} else {
			frame = diffFrameSurface(p.rendered, surface)
		}
		p.rendered = surface
		p.renderedRows = nil
	} else {
		var lines []string
		viewModel, ok := p.model.(ViewModel)
		if !ok {
			return nil
		}
		view := viewModel.View()
		lines = strings.Split(view, "\n")
		if !p.didRender {
			frame = renderFrameLines(lines)
			p.didRender = true
		} else {
			frame = diffFrameLines(p.renderedRows, lines)
		}
		p.renderedRows = append(p.renderedRows[:0], lines...)
		p.rendered = nil
	}
	if frame == "" {
		return nil
	}
	_, err := io.WriteString(out, frame)
	return err
}

func renderFrame(view string) string {
	return renderFrameLines(strings.Split(view, "\n"))
}

func renderFrameLines(lines []string) string {
	var buf strings.Builder
	buf.WriteString("\x1b[H\x1b[2J")
	for idx, line := range lines {
		fmt.Fprintf(&buf, "\x1b[%d;1H", idx+1)
		buf.WriteString(line)
	}
	return buf.String()
}

func renderFrameSurface(surface SurfaceView) string {
	var buf strings.Builder
	buf.WriteString("\x1b[H\x1b[2J")
	height := 0
	if surface != nil {
		height = surface.SurfaceHeight()
	}
	for idx := 0; idx < height; idx++ {
		fmt.Fprintf(&buf, "\x1b[%d;1H", idx+1)
		buf.WriteString(serializeSurfaceRow(surface, idx))
	}
	return buf.String()
}

func diffFrameLines(previous, current []string) string {
	var buf strings.Builder
	maxRows := len(previous)
	if len(current) > maxRows {
		maxRows = len(current)
	}
	for idx := 0; idx < maxRows; idx++ {
		var prevLine, currLine string
		if idx < len(previous) {
			prevLine = previous[idx]
		}
		if idx < len(current) {
			currLine = current[idx]
		}
		if prevLine == currLine {
			continue
		}
		fmt.Fprintf(&buf, "\x1b[%d;1H", idx+1)
		if currLine != "" {
			buf.WriteString(currLine)
		}
		buf.WriteString("\x1b[K")
	}
	return buf.String()
}

func diffFrameSurface(previous, current SurfaceView) string {
	var buf strings.Builder
	maxRows := 0
	if previous != nil && previous.SurfaceHeight() > maxRows {
		maxRows = previous.SurfaceHeight()
	}
	if current != nil && current.SurfaceHeight() > maxRows {
		maxRows = current.SurfaceHeight()
	}
	for idx := 0; idx < maxRows; idx++ {
		if surfaceRowsEqual(previous, current, idx) {
			continue
		}
		fmt.Fprintf(&buf, "\x1b[%d;1H", idx+1)
		if current != nil && idx < current.SurfaceHeight() {
			buf.WriteString(serializeSurfaceRow(current, idx))
		}
		buf.WriteString("\x1b[K")
	}
	return buf.String()
}

func surfaceRowsEqual(previous, current SurfaceView, y int) bool {
	prevWidth := 0
	currWidth := 0
	if previous != nil && y < previous.SurfaceHeight() {
		prevWidth = previous.SurfaceWidth()
	}
	if current != nil && y < current.SurfaceHeight() {
		currWidth = current.SurfaceWidth()
	}
	maxWidth := prevWidth
	if currWidth > maxWidth {
		maxWidth = currWidth
	}
	for x := 0; x < maxWidth; x++ {
		if !surfaceCellsEqual(previous, current, x, y) {
			return false
		}
	}
	return true
}

func surfaceCellsEqual(previous, current SurfaceView, x, y int) bool {
	return surfaceCellText(previous, x, y) == surfaceCellText(current, x, y) &&
		surfaceCellWidth(previous, x, y) == surfaceCellWidth(current, x, y) &&
		surfaceCellContinuation(previous, x, y) == surfaceCellContinuation(current, x, y) &&
		surfaceCellFG(previous, x, y) == surfaceCellFG(current, x, y) &&
		surfaceCellBG(previous, x, y) == surfaceCellBG(current, x, y) &&
		surfaceCellBold(previous, x, y) == surfaceCellBold(current, x, y) &&
		surfaceCellItalic(previous, x, y) == surfaceCellItalic(current, x, y) &&
		surfaceCellUnderline(previous, x, y) == surfaceCellUnderline(current, x, y)
}

func serializeSurfaceRow(surface SurfaceView, y int) string {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() {
		return ""
	}
	var b strings.Builder
	var currentStyle styleState
	var segment strings.Builder
	flush := func() {
		if segment.Len() == 0 {
			return
		}
		b.WriteString(applyStyle(currentStyle, segment.String()))
		segment.Reset()
	}
	for x := 0; x < surface.SurfaceWidth(); x++ {
		if surface.SurfaceCellContinuation(x, y) {
			continue
		}
		style := styleState{
			fg:        surfaceCellFG(surface, x, y),
			bg:        surfaceCellBG(surface, x, y),
			bold:      surfaceCellBold(surface, x, y),
			italic:    surfaceCellItalic(surface, x, y),
			underline: surfaceCellUnderline(surface, x, y),
		}
		text := surfaceCellText(surface, x, y)
		if text == "" {
			text = " "
		}
		if segment.Len() > 0 && currentStyle != style {
			flush()
		}
		currentStyle = style
		segment.WriteString(text)
	}
	flush()
	return b.String()
}

type styleState struct {
	fg        rgbState
	bg        rgbState
	bold      bool
	italic    bool
	underline bool
}

type rgbState struct {
	r     uint8
	g     uint8
	b     uint8
	valid bool
}

func applyStyle(style styleState, text string) string {
	if text == "" || (style == styleState{}) {
		return text
	}
	params := make([]string, 0, 10)
	if style.bold {
		params = append(params, "1")
	}
	if style.italic {
		params = append(params, "3")
	}
	if style.underline {
		params = append(params, "4")
	}
	if style.fg.valid {
		params = append(params,
			"38", "2",
			strconv.Itoa(int(style.fg.r)),
			strconv.Itoa(int(style.fg.g)),
			strconv.Itoa(int(style.fg.b)),
		)
	}
	if style.bg.valid {
		params = append(params,
			"48", "2",
			strconv.Itoa(int(style.bg.r)),
			strconv.Itoa(int(style.bg.g)),
			strconv.Itoa(int(style.bg.b)),
		)
	}
	if len(params) == 0 {
		return text
	}
	return "\x1b[" + strings.Join(params, ";") + "m" + text + "\x1b[0m"
}

func surfaceCellText(surface SurfaceView, x, y int) string {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return " "
	}
	return surface.SurfaceCellText(x, y)
}

func surfaceCellWidth(surface SurfaceView, x, y int) int {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return 1
	}
	return surface.SurfaceCellWidth(x, y)
}

func surfaceCellContinuation(surface SurfaceView, x, y int) bool {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return false
	}
	return surface.SurfaceCellContinuation(x, y)
}

func surfaceCellFG(surface SurfaceView, x, y int) rgbState {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return rgbState{}
	}
	r, g, b, ok := surface.SurfaceCellFG(x, y)
	return rgbState{r: r, g: g, b: b, valid: ok}
}

func surfaceCellBG(surface SurfaceView, x, y int) rgbState {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return rgbState{}
	}
	r, g, b, ok := surface.SurfaceCellBG(x, y)
	return rgbState{r: r, g: g, b: b, valid: ok}
}

func surfaceCellBold(surface SurfaceView, x, y int) bool {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return false
	}
	return surface.SurfaceCellBold(x, y)
}

func surfaceCellItalic(surface SurfaceView, x, y int) bool {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return false
	}
	return surface.SurfaceCellItalic(x, y)
}

func surfaceCellUnderline(surface SurfaceView, x, y int) bool {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return false
	}
	return surface.SurfaceCellUnderline(x, y)
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

const idleInputPollDelay = 10 * time.Millisecond

func (p *Program) readInput(reader inputEventReader, out chan<- Msg, done <-chan struct{}) {
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
			time.Sleep(idleInputPollDelay)
			continue
		}
		if len(evs) == 0 {
			time.Sleep(idleInputPollDelay)
			continue
		}
		for _, ev := range evs {
			for _, msg := range convertInputEvents(ev) {
				out <- msg
			}
		}
	}
}

func writeString(w io.Writer, s string) {
	_, _ = io.WriteString(w, s)
}

func convertInputEvents(ev input.Event) []Msg {
	switch typed := ev.(type) {
	case input.MultiEvent:
		msgs := make([]Msg, 0, len(typed))
		for _, nested := range typed {
			msgs = append(msgs, convertInputEvents(nested)...)
		}
		return msgs
	case input.KeyPressEvent:
		return []Msg{convertKeyPress(typed)}
	case input.KeyReleaseEvent:
		return nil
	case input.MouseClickEvent:
		m := typed.Mouse()
		return []Msg{MouseMsg{X: m.X, Y: m.Y, Button: convertMouseButton(m.Button), Action: MouseActionPress, Alt: m.Mod.Contains(input.ModAlt)}}
	case input.MouseWheelEvent:
		m := typed.Mouse()
		return []Msg{MouseMsg{X: m.X, Y: m.Y, Button: convertMouseButton(m.Button), Action: MouseActionPress, Alt: m.Mod.Contains(input.ModAlt)}}
	case input.MouseReleaseEvent:
		m := typed.Mouse()
		return []Msg{MouseMsg{X: m.X, Y: m.Y, Button: convertMouseButton(m.Button), Action: MouseActionRelease, Alt: m.Mod.Contains(input.ModAlt)}}
	case input.WindowSizeEvent:
		return []Msg{WindowSizeMsg{Width: typed.Width, Height: typed.Height}}
	default:
		return nil
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
	case input.KeyEnter, input.KeyKpEnter:
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
			ctrlKey := strings.ToLower(key.Text)
			if ctrlKey == "" && key.Code > 0 && key.Code <= 0x7f {
				ctrlKey = strings.ToLower(string(rune(key.Code)))
			}
			switch ctrlKey {
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
