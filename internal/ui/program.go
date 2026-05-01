package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/input"
	"github.com/charmbracelet/x/term"
)

type Program struct {
	model           Model
	altScreen       bool
	noSignalHandler bool
	colorProfile    ColorProfile
	colorProfileSet bool

	mu            sync.Mutex
	title         string
	mouseEnabled  bool
	sent          chan Msg
	renderedRows  []string
	rendered      SurfaceView
	didRender     bool
	framePending  bool
	frameInterval time.Duration
}

type inputEventReader interface {
	ReadEvents() ([]input.Event, error)
	Close() error
}

func NewProgram(model Model, opts ...ProgramOption) *Program {
	p := &Program{
		model:         model,
		sent:          make(chan Msg, 256),
		frameInterval: time.Second / 30,
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

	if !p.colorProfileSet {
		p.colorProfile = DetectColorProfileFromEnv(os.Getenv, term.IsTerminal(out.Fd()))
	}

	restoreVT, err := enableVirtualTerminalIO(in, out)
	if err != nil {
		return p.model, err
	}
	defer restoreVT()

	oldState, err := term.MakeRaw(in.Fd())
	if err != nil {
		return p.model, err
	}
	defer func() {
		_ = term.Restore(in.Fd(), oldState)
	}()

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
		frame, isFrame := msg.(FrameMsg)
		if isFrame {
			if !p.consumeFrame(frame) {
				continue
			}
		}
		if quit, err := p.handleRuntimeMsg(msg, out); quit || err != nil {
			return p.model, err
		}
		next, cmd := p.model.Update(msg)
		p.model = next
		if cmd != nil {
			p.runCmd(cmd, events)
		}
		if isFrame || p.shouldRenderImmediately(msg) {
			if err := p.render(out); err != nil {
				return p.model, err
			}
			continue
		}
		p.requestFrame(events)
	}
}

func (p *Program) shouldRenderImmediately(msg Msg) bool {
	switch msg.(type) {
	case WindowSizeMsg:
		return true
	default:
		return false
	}
}

func (p *Program) requestFrame(out chan<- Msg) {
	if p == nil || out == nil || p.framePending {
		return
	}
	delay := p.frameInterval
	if delay <= 0 {
		delay = time.Second / 30
	}
	p.framePending = true
	p.runCmd(Tick(delay, func(t time.Time) Msg {
		return FrameMsg{At: t}
	}), out)
}

func (p *Program) consumeFrame(FrameMsg) bool {
	if p == nil {
		return false
	}
	p.framePending = false
	return true
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
	case WindowSizeMsg:
		p.invalidateRenderCache()
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
	surface := p.model.ViewSurface()
	if !p.didRender {
		frame = renderFrameSurface(surface, p.colorProfile)
		p.didRender = true
	} else {
		if rows, ok := dirtyRows(surface, p.rendered); ok {
			frame = diffFrameSurfaceRows(p.rendered, surface, rows, p.colorProfile)
		} else {
			frame = diffFrameSurface(p.rendered, surface, p.colorProfile)
		}
	}
	p.rendered = surface
	p.renderedRows = nil
	if frame == "" {
		return nil
	}
	_, err := io.WriteString(out, frame)
	return err
}

func (p *Program) invalidateRenderCache() {
	p.rendered = nil
	p.renderedRows = nil
	p.didRender = false
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

func renderFrameSurface(surface SurfaceView, profile ColorProfile) string {
	var buf strings.Builder
	buf.WriteString("\x1b[H\x1b[2J")
	height := 0
	if surface != nil {
		height = surface.SurfaceHeight()
	}
	for idx := 0; idx < height; idx++ {
		fmt.Fprintf(&buf, "\x1b[%d;1H", idx+1)
		buf.WriteString(serializeSurfaceViewRow(surface, idx, profile))
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

func diffFrameSurface(previous, current SurfaceView, profile ColorProfile) string {
	start := 0
	end := max(surfaceHeight(previous), surfaceHeight(current)) - 1
	if end < start {
		return ""
	}
	rows := make([]RowDamage, 0, end-start+1)
	for y := start; y <= end; y++ {
		rows = append(rows, RowDamage{Y: y, StartX: 0})
	}
	return diffFrameSurfaceRows(previous, current, rows, profile)
}

func diffFrameSurfaceRows(previous, current SurfaceView, rows []RowDamage, profile ColorProfile) string {
	var buf strings.Builder
	prevRows := 0
	currRows := 0
	if previous != nil {
		prevRows = previous.SurfaceHeight()
	}
	if current != nil {
		currRows = current.SurfaceHeight()
	}
	maxRows := max(prevRows, currRows)
	if maxRows <= 0 {
		return ""
	}
	if len(rows) == 0 {
		return ""
	}
	for _, row := range rows {
		idx := row.Y
		if idx < 0 || idx >= maxRows {
			continue
		}
		if surfaceRowsEqual(previous, current, idx) {
			continue
		}
		if idx >= currRows {
			if idx < prevRows {
				fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K", idx+1)
			}
			continue
		}
		startX := max(0, row.StartX)
		fmt.Fprintf(&buf, "\x1b[%d;%dH", idx+1, startX+1)
		if current != nil && idx < current.SurfaceHeight() {
			buf.WriteString(serializeSurfaceViewRowSegment(current, idx, startX, profile))
		}
		buf.WriteString("\x1b[K")
	}
	return buf.String()
}

func surfaceHeight(surface SurfaceView) int {
	if surface == nil {
		return 0
	}
	return surface.SurfaceHeight()
}

func dirtyRows(current, previous SurfaceView) ([]RowDamage, bool) {
	if current == nil || previous == nil {
		return nil, false
	}
	if current.SurfaceWidth() != previous.SurfaceWidth() || current.SurfaceHeight() != previous.SurfaceHeight() {
		return nil, false
	}
	if provider, ok := current.(DirtyRectsProvider); ok {
		if rects, ok := provider.DirtyRects(); ok {
			rows := DamageRows(rects)
			if len(rows) > 0 {
				return rows, true
			}
		}
	}
	provider, ok := current.(DirtyRowRangeProvider)
	if !ok {
		return nil, false
	}
	start, end, ok := provider.DirtyRowRange()
	if !ok {
		return nil, false
	}
	rows := make([]RowDamage, 0, end-start+1)
	for y := start; y <= end; y++ {
		rows = append(rows, RowDamage{Y: y, StartX: 0})
	}
	return rows, true
}

func surfaceRowsEqual(previous, current SurfaceView, y int) bool {
	if prevSurface, ok := previous.(Surface); ok {
		if currSurface, ok := current.(Surface); ok && prevSurface.isCellBuffer() && currSurface.isCellBuffer() &&
			y >= 0 && y < prevSurface.h && y < currSurface.h && prevSurface.w == currSurface.w {
			start := y * prevSurface.w
			end := start + prevSurface.w
			prevRow := prevSurface.cells[start:end]
			currRow := currSurface.cells[start:end]
			for idx := range prevRow {
				if prevRow[idx] != currRow[idx] {
					return false
				}
			}
			return true
		}
	}
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
		surfaceCellFGState(previous, x, y) == surfaceCellFGState(current, x, y) &&
		surfaceCellBGState(previous, x, y) == surfaceCellBGState(current, x, y) &&
		surfaceCellBold(previous, x, y) == surfaceCellBold(current, x, y) &&
		surfaceCellItalic(previous, x, y) == surfaceCellItalic(current, x, y) &&
		surfaceCellUnderline(previous, x, y) == surfaceCellUnderline(current, x, y) &&
		surfaceCellStrikethrough(previous, x, y) == surfaceCellStrikethrough(current, x, y)
}

func serializeSurfaceViewRow(surface SurfaceView, y int, profile ColorProfile) string {
	return serializeSurfaceViewRowSegment(surface, y, 0, profile)
}

func serializeSurfaceViewRowSegment(surface SurfaceView, y, startX int, profile ColorProfile) string {
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
		b.WriteString(applyStyle(profile, currentStyle, segment.String()))
		segment.Reset()
	}
	if startX < 0 {
		startX = 0
	}
	for x := startX; x < surface.SurfaceWidth(); x++ {
		if surface.SurfaceCellContinuation(x, y) {
			continue
		}
		style := styleState{
			fg:        surfaceCellFGState(surface, x, y),
			bg:        surfaceCellBGState(surface, x, y),
			bold:      surfaceCellBold(surface, x, y),
			italic:    surfaceCellItalic(surface, x, y),
			underline: surfaceCellUnderline(surface, x, y),
			strike:    surfaceCellStrikethrough(surface, x, y),
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
	strike    bool
}

type rgbState struct {
	r     uint8
	g     uint8
	b     uint8
	valid bool
}

func applyStyle(profile ColorProfile, style styleState, text string) string {
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
	if style.strike {
		params = append(params, "9")
	}
	params = appendTerminalColorSGR(params, profile, true, style.fg.r, style.fg.g, style.fg.b, style.fg.valid)
	params = appendTerminalColorSGR(params, profile, false, style.bg.r, style.bg.g, style.bg.b, style.bg.valid)
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

func surfaceCellFGState(surface SurfaceView, x, y int) rgbState {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return rgbState{}
	}
	r, g, b, ok := surface.SurfaceCellFG(x, y)
	return rgbState{r: r, g: g, b: b, valid: ok}
}

func surfaceCellBGState(surface SurfaceView, x, y int) rgbState {
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

func surfaceCellStrikethrough(surface SurfaceView, x, y int) bool {
	if surface == nil || y < 0 || y >= surface.SurfaceHeight() || x < 0 || x >= surface.SurfaceWidth() {
		return false
	}
	return surface.SurfaceCellStrikethrough(x, y)
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
		return convertEventSequence(flattenInputEvents(typed))
	case input.KeyPressEvent:
		return convertEventSequence([]input.Event{typed})
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

func flattenInputEvents(events []input.Event) []input.Event {
	flat := make([]input.Event, 0, len(events))
	for _, ev := range events {
		if nested, ok := ev.(input.MultiEvent); ok {
			flat = append(flat, flattenInputEvents(nested)...)
			continue
		}
		flat = append(flat, ev)
	}
	return flat
}

func convertEventSequence(events []input.Event) []Msg {
	msgs := make([]Msg, 0, len(events))
	for idx := 0; idx < len(events); idx++ {
		if altMsg, consumed, ok := decodeEscPrefixedAlt(events, idx); ok {
			msgs = append(msgs, altMsg)
			idx += consumed - 1
			continue
		}
		msgs = append(msgs, convertSingleInputEvent(events[idx])...)
	}
	return msgs
}

func decodeEscPrefixedAlt(events []input.Event, idx int) (Msg, int, bool) {
	if idx+1 >= len(events) {
		return nil, 0, false
	}
	first, ok := events[idx].(input.KeyPressEvent)
	if !ok || first.Code != input.KeyEsc {
		return nil, 0, false
	}
	second, ok := events[idx+1].(input.KeyPressEvent)
	if !ok {
		return nil, 0, false
	}
	msg := convertKeyPress(second)
	if msg.Type == KeyUnknown || msg.Type == KeyEsc || msg.Alt || !shouldSynthesizeEscPrefixedAlt(msg) {
		return nil, 0, false
	}
	msg.Alt = true
	return msg, 2, true
}

func shouldSynthesizeEscPrefixedAlt(msg KeyMsg) bool {
	switch msg.Type {
	case KeyLeft, KeyRight, KeyUp, KeyDown, KeyPgUp, KeyPgDown, KeyHome, KeyEnd,
		KeyBackspace, KeyDelete, KeyEnter, KeyTab, KeyShiftTab, KeySpace:
		return true
	case KeyRunes:
		return len(msg.Runes) == 1 && (msg.Runes[0] == '[' || msg.Runes[0] == ']')
	default:
		return false
	}
}

func convertSingleInputEvent(ev input.Event) []Msg {
	switch typed := ev.(type) {
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
	case input.KeyPgUp:
		if key.Mod.Contains(input.ModCtrl) {
			msg.Type = KeyCtrlPgUp
		} else {
			msg.Type = KeyPgUp
		}
	case input.KeyPgDown:
		if key.Mod.Contains(input.ModCtrl) {
			msg.Type = KeyCtrlPgDown
		} else {
			msg.Type = KeyPgDown
		}
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
			case "b":
				msg.Type = KeyCtrlB
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
	}
	if msg.Type == KeyUnknown {
		if key.Text == " " {
			msg.Type = KeySpace
		} else if key.Text != "" {
			msg.Type = KeyRunes
			msg.Runes = []rune(key.Text)
		} else if !key.Mod.Contains(input.ModCtrl) && key.Code > 0 && key.Code <= 0x10ffff {
			r := rune(key.Code)
			if r != utf8.RuneError && unicode.IsPrint(r) {
				msg.Type = KeyRunes
				msg.Runes = []rune{r}
			}
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
