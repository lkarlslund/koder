package ui

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/input"
)

func TestRenderFrameAddressesRowsWithoutNewlines(t *testing.T) {
	got := renderFrame("alpha\nbeta\ngamma")
	if strings.Contains(got, "alpha\nbeta") {
		t.Fatalf("frame should not stream raw newlines between rows: %q", got)
	}
	wantParts := []string{
		"\x1b[H\x1b[2J",
		"\x1b[1;1Halpha",
		"\x1b[2;1Hbeta",
		"\x1b[3;1Hgamma",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("frame missing %q in %q", want, got)
		}
	}
}

func TestRenderFrameLinesAddressesRows(t *testing.T) {
	got := renderFrameLines([]string{"alpha", "beta", "gamma"})
	wantParts := []string{
		"\x1b[H\x1b[2J",
		"\x1b[1;1Halpha",
		"\x1b[2;1Hbeta",
		"\x1b[3;1Hgamma",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Fatalf("frame missing %q in %q", want, got)
		}
	}
}

func TestDiffFrameLinesSkipsUnchangedRows(t *testing.T) {
	got := diffFrameLines([]string{"alpha", "beta"}, []string{"alpha", "beta"})
	if got != "" {
		t.Fatalf("expected no output for unchanged frame, got %q", got)
	}
}

func TestDiffFrameLinesUpdatesOnlyChangedRows(t *testing.T) {
	got := diffFrameLines([]string{"alpha", "beta", "gamma"}, []string{"alpha", "BETA", "gamma"})
	if strings.Contains(got, "\x1b[1;1Halpha") || strings.Contains(got, "\x1b[3;1Hgamma") {
		t.Fatalf("expected unchanged rows to be skipped, got %q", got)
	}
	if !strings.Contains(got, "\x1b[2;1HBETA\x1b[K") {
		t.Fatalf("expected changed row to be rewritten with clear, got %q", got)
	}
}

func TestDiffFrameLinesClearsRemovedRows(t *testing.T) {
	got := diffFrameLines([]string{"alpha", "beta"}, []string{"alpha"})
	if !strings.Contains(got, "\x1b[2;1H\x1b[K") {
		t.Fatalf("expected removed row to be cleared, got %q", got)
	}
}

func TestDiffFrameSurfaceClearsRemovedRows(t *testing.T) {
	previous := fakeSurface{
		w: 4,
		h: 3,
		cells: []fakeCell{
			{text: "a"}, {text: "a"}, {text: "a"}, {text: "a"},
			{text: "b"}, {text: "b"}, {text: "b"}, {text: "b"},
			{text: "c"}, {text: "c"}, {text: "c"}, {text: "c"},
		},
	}
	current := fakeSurface{
		w: 4,
		h: 1,
		cells: []fakeCell{
			{text: "a"}, {text: "a"}, {text: "a"}, {text: "a"},
		},
	}

	got := diffFrameSurface(previous, current, ColorProfileTrueColor)
	if !strings.Contains(got, "\x1b[2;1H\x1b[2K") {
		t.Fatalf("expected second removed row to be fully cleared, got %q", got)
	}
	if !strings.Contains(got, "\x1b[3;1H\x1b[2K") {
		t.Fatalf("expected third removed row to be fully cleared, got %q", got)
	}
}

func TestDiffFrameSurfaceRowsUsesDirtyRange(t *testing.T) {
	previous := fakeSurface{
		w: 4,
		h: 3,
		cells: []fakeCell{
			{text: "a"}, {text: "a"}, {text: "a"}, {text: "a"},
			{text: "b"}, {text: "b"}, {text: "b"}, {text: "b"},
			{text: "c"}, {text: "c"}, {text: "c"}, {text: "c"},
		},
	}
	current := fakeSurface{
		w:          4,
		h:          3,
		dirtyStart: 1,
		dirtyEnd:   1,
		dirty:      true,
		cells: []fakeCell{
			{text: "a"}, {text: "a"}, {text: "a"}, {text: "a"},
			{text: "B"}, {text: "B"}, {text: "B"}, {text: "B"},
			{text: "c"}, {text: "c"}, {text: "c"}, {text: "c"},
		},
	}

	rows, ok := dirtyRows(current, previous)
	if !ok || len(rows) != 1 || rows[0].Y != 1 || rows[0].StartX != 0 {
		t.Fatalf("unexpected dirty rows: ok=%v rows=%v", ok, rows)
	}
	got := diffFrameSurfaceRows(previous, current, rows, ColorProfileTrueColor)
	if !strings.Contains(got, "\x1b[2;1H") {
		t.Fatalf("expected dirty row to be updated, got %q", got)
	}
	if strings.Contains(got, "\x1b[1;1H") || strings.Contains(got, "\x1b[3;1H") {
		t.Fatalf("expected unchanged rows outside dirty range to be skipped, got %q", got)
	}
}

func TestDiffFrameSurfaceRowsStartsAtFirstDirtyColumn(t *testing.T) {
	previous := fakeSurface{
		w: 5,
		h: 1,
		cells: []fakeCell{
			{text: "a"}, {text: "b"}, {text: "c"}, {text: "d"}, {text: "e"},
		},
	}
	current := fakeSurface{
		w:          5,
		h:          1,
		dirtyRects: []Rect{{X: 2, Y: 0, W: 1, H: 1}},
		cells: []fakeCell{
			{text: "a"}, {text: "b"}, {text: "Z"}, {text: "d"}, {text: "e"},
		},
	}

	rows, ok := dirtyRows(current, previous)
	if !ok || len(rows) != 1 || rows[0].StartX != 2 {
		t.Fatalf("unexpected dirty rows: ok=%v rows=%v", ok, rows)
	}
	got := diffFrameSurfaceRows(previous, current, rows, ColorProfileTrueColor)
	if !strings.Contains(got, "\x1b[1;3HZde\x1b[K") {
		t.Fatalf("expected flush to begin at third column, got %q", got)
	}
	if strings.Contains(got, "\x1b[1;1H") {
		t.Fatalf("expected no repaint from first column, got %q", got)
	}
}

func TestWindowSizeInvalidatesRenderCache(t *testing.T) {
	model := &fakeModel{
		surface: fakeSurface{
			w: 2,
			h: 1,
			cells: []fakeCell{
				{text: "a"}, {text: "b"},
			},
		},
	}
	p := &Program{
		model:     model,
		rendered:  model.surface,
		didRender: true,
	}

	if quit, err := p.handleRuntimeMsg(WindowSizeMsg{Width: 80, Height: 24}, &bytes.Buffer{}); quit || err != nil {
		t.Fatalf("unexpected runtime result: quit=%v err=%v", quit, err)
	}
	if p.didRender {
		t.Fatal("expected resize to invalidate cached render state")
	}

	var out bytes.Buffer
	if err := p.render(&out); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.HasPrefix(out.String(), "\x1b[H\x1b[2J") {
		t.Fatalf("expected resize-triggered render to perform full redraw, got %q", out.String())
	}
}

func TestRenderFrameSurfaceEmitsRealSGRSequences(t *testing.T) {
	s := fakeSurface{
		w: 5,
		h: 1,
		cells: []fakeCell{
			{text: "H", fgr: [3]uint8{200, 211, 245}, fgv: true, bgr: [3]uint8{30, 32, 48}, bgv: true, bold: true},
			{text: "e", fgr: [3]uint8{200, 211, 245}, fgv: true, bgr: [3]uint8{30, 32, 48}, bgv: true, bold: true},
			{text: "l", fgr: [3]uint8{200, 211, 245}, fgv: true, bgr: [3]uint8{30, 32, 48}, bgv: true, bold: true},
			{text: "l", fgr: [3]uint8{200, 211, 245}, fgv: true, bgr: [3]uint8{30, 32, 48}, bgv: true, bold: true},
			{text: "o", fgr: [3]uint8{200, 211, 245}, fgv: true, bgr: [3]uint8{30, 32, 48}, bgv: true, bold: true},
		},
	}
	got := renderFrameSurface(s, ColorProfileTrueColor)
	if !strings.Contains(got, "\x1b[1;38;2;200;211;245") {
		t.Fatalf("expected real SGR foreground sequence, got %q", got)
	}
	if strings.Contains(got, "[38;2;200;211;245") && !strings.Contains(got, "\x1b[1;38;2;200;211;245") {
		t.Fatalf("expected no bare CSI tail without ESC, got %q", got)
	}
}

func TestRenderFrameSurfaceUsesConfiguredColorProfile(t *testing.T) {
	s := fakeSurface{
		w: 1,
		h: 1,
		cells: []fakeCell{
			{text: "X", fgr: [3]uint8{255, 0, 0}, fgv: true, bgr: [3]uint8{0, 0, 255}, bgv: true},
		},
	}

	got16 := renderFrameSurface(s, ColorProfileANSI16)
	if !strings.Contains(got16, "\x1b[1;1H\x1b[91;104mX\x1b[0m") {
		t.Fatalf("expected ANSI16 render, got %q", got16)
	}
	if strings.Contains(got16, "38;2;") || strings.Contains(got16, "38;5;") {
		t.Fatalf("expected downgraded color sequence, got %q", got16)
	}

	got256 := renderFrameSurface(s, ColorProfileANSI256)
	if !strings.Contains(got256, "\x1b[1;1H\x1b[38;5;9;48;5;12mX\x1b[0m") {
		t.Fatalf("expected ANSI256 render, got %q", got256)
	}
}

func TestReadInputBacksOffWhenReaderReturnsNoEvents(t *testing.T) {
	reader := &fakeInputReader{}
	done := make(chan struct{})
	out := make(chan Msg, 8)
	p := &Program{}

	go p.readInput(reader, out, done)
	time.Sleep(35 * time.Millisecond)
	close(done)
	time.Sleep(5 * time.Millisecond)

	if calls := reader.calls.Load(); calls > 8 {
		t.Fatalf("expected readInput to back off on empty reads, got %d calls in ~35ms", calls)
	}
}

func TestConvertInputEventsFlattensMultiEvent(t *testing.T) {
	msgs := convertInputEvents(input.MultiEvent{
		input.KeyPressEvent{Code: input.KeyEnter},
		input.KeyReleaseEvent{Code: input.KeyEnter},
		input.WindowSizeEvent{Width: 80, Height: 24},
	})
	if len(msgs) != 2 {
		t.Fatalf("expected two messages from multi-event, got %d", len(msgs))
	}
	key, ok := msgs[0].(KeyMsg)
	if !ok || key.Type != KeyEnter {
		t.Fatalf("expected first message to be enter key, got %#v", msgs[0])
	}
	size, ok := msgs[1].(WindowSizeMsg)
	if !ok || size.Width != 80 || size.Height != 24 {
		t.Fatalf("expected second message to be window size, got %#v", msgs[1])
	}
}

func TestConvertInputEventsTreatsEscPrefixedRunesAsAlt(t *testing.T) {
	msgs := convertInputEvents(input.MultiEvent{
		input.KeyPressEvent{Code: input.KeyEsc},
		input.KeyPressEvent{Code: 'p'},
	})
	if len(msgs) != 1 {
		t.Fatalf("expected one combined alt message, got %d", len(msgs))
	}
	key, ok := msgs[0].(KeyMsg)
	if !ok {
		t.Fatalf("expected key message, got %#v", msgs[0])
	}
	if key.Type != KeyRunes || string(key.Runes) != "p" || !key.Alt {
		t.Fatalf("expected alt+p message, got %#v", key)
	}
}

func TestConvertInputEventsLeavesBareEscAlone(t *testing.T) {
	msgs := convertInputEvents(input.KeyPressEvent{Code: input.KeyEsc})
	if len(msgs) != 1 {
		t.Fatalf("expected one esc message, got %d", len(msgs))
	}
	key, ok := msgs[0].(KeyMsg)
	if !ok || key.Type != KeyEsc || key.Alt {
		t.Fatalf("expected bare esc, got %#v", msgs[0])
	}
}

func TestConvertKeyPressMapsKeypadEnter(t *testing.T) {
	got := convertKeyPress(input.KeyPressEvent{Code: input.KeyKpEnter})
	if got.Type != KeyEnter {
		t.Fatalf("expected keypad enter to map to enter, got %#v", got)
	}
}

func TestConvertKeyPressMapsCtrlCombosByCodeWhenTextEmpty(t *testing.T) {
	got := convertKeyPress(input.KeyPressEvent{Code: 'c', Mod: input.ModCtrl})
	if got.Type != KeyCtrlC {
		t.Fatalf("expected ctrl+c to map from code even without text, got %#v", got)
	}
	got = convertKeyPress(input.KeyPressEvent{Code: 'b', Mod: input.ModCtrl})
	if got.Type != KeyCtrlB {
		t.Fatalf("expected ctrl+b to map from code even without text, got %#v", got)
	}
}

func TestConvertKeyPressMapsPageKeysAndCtrlPageKeys(t *testing.T) {
	got := convertKeyPress(input.KeyPressEvent{Code: input.KeyPgUp})
	if got.Type != KeyPgUp {
		t.Fatalf("expected pgup to map to KeyPgUp, got %#v", got)
	}
	got = convertKeyPress(input.KeyPressEvent{Code: input.KeyPgDown})
	if got.Type != KeyPgDown {
		t.Fatalf("expected pgdown to map to KeyPgDown, got %#v", got)
	}
	got = convertKeyPress(input.KeyPressEvent{Code: input.KeyPgUp, Mod: input.ModCtrl})
	if got.Type != KeyCtrlPgUp {
		t.Fatalf("expected ctrl+pgup to map to KeyCtrlPgUp, got %#v", got)
	}
	got = convertKeyPress(input.KeyPressEvent{Code: input.KeyPgDown, Mod: input.ModCtrl})
	if got.Type != KeyCtrlPgDown {
		t.Fatalf("expected ctrl+pgdown to map to KeyCtrlPgDown, got %#v", got)
	}
}

func TestConvertKeyPressMapsPrintableRunesFromCodeFallback(t *testing.T) {
	got := convertKeyPress(input.KeyPressEvent{Code: 'i'})
	if got.Type != KeyRunes || string(got.Runes) != "i" {
		t.Fatalf("expected printable rune code fallback, got %#v", got)
	}
}

type fakeCell struct {
	text         string
	width        int
	continuation bool
	fgr          [3]uint8
	fgv          bool
	bgr          [3]uint8
	bgv          bool
	bold         bool
	italic       bool
	underline    bool
	strike       bool
}

type fakeInputReader struct {
	calls atomic.Int64
}

func (f *fakeInputReader) ReadEvents() ([]input.Event, error) {
	f.calls.Add(1)
	return nil, nil
}

func (f *fakeInputReader) Close() error { return nil }

type fakeModel struct {
	surface SurfaceView
}

func (f *fakeModel) Init() Cmd                { return nil }
func (f *fakeModel) Update(Msg) (Model, Cmd)  { return f, nil }
func (f *fakeModel) ViewSurface() SurfaceView { return f.surface }

type fakeSurface struct {
	w          int
	h          int
	cells      []fakeCell
	dirty      bool
	dirtyStart int
	dirtyEnd   int
	dirtyRects []Rect
}

func (f fakeSurface) SurfaceWidth() int               { return f.w }
func (f fakeSurface) SurfaceHeight() int              { return f.h }
func (f fakeSurface) SurfaceCellText(x, y int) string { return f.cells[y*f.w+x].text }
func (f fakeSurface) SurfaceCellWidth(x, y int) int {
	width := f.cells[y*f.w+x].width
	if width <= 0 {
		return 1
	}
	return width
}
func (f fakeSurface) SurfaceCellContinuation(x, y int) bool { return f.cells[y*f.w+x].continuation }
func (f fakeSurface) SurfaceCellFG(x, y int) (uint8, uint8, uint8, bool) {
	cell := f.cells[y*f.w+x]
	return cell.fgr[0], cell.fgr[1], cell.fgr[2], cell.fgv
}
func (f fakeSurface) SurfaceCellBG(x, y int) (uint8, uint8, uint8, bool) {
	cell := f.cells[y*f.w+x]
	return cell.bgr[0], cell.bgr[1], cell.bgr[2], cell.bgv
}
func (f fakeSurface) SurfaceCellBold(x, y int) bool      { return f.cells[y*f.w+x].bold }
func (f fakeSurface) SurfaceCellItalic(x, y int) bool    { return f.cells[y*f.w+x].italic }
func (f fakeSurface) SurfaceCellUnderline(x, y int) bool { return f.cells[y*f.w+x].underline }
func (f fakeSurface) SurfaceCellStrikethrough(x, y int) bool {
	return f.cells[y*f.w+x].strike
}
func (f fakeSurface) DirtyRowRange() (start int, end int, ok bool) {
	if !f.dirty {
		return 0, 0, false
	}
	return f.dirtyStart, f.dirtyEnd, true
}

func (f fakeSurface) DirtyRects() ([]Rect, bool) {
	if len(f.dirtyRects) == 0 {
		return nil, false
	}
	out := make([]Rect, len(f.dirtyRects))
	copy(out, f.dirtyRects)
	return out, true
}
