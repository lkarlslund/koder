package tui

import (
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
	"github.com/lkarlslund/koder/internal/ui"
)

type controlNode struct {
	ui.PassiveNode
	id string
}

func (n controlNode) Measure(_ *ui.Context, constraints ui.Constraints) ui.Size {
	return constraints.Clamp(ui.Size{W: 8, H: 1})
}

func (n controlNode) Paint(ctx *ui.Context, canvas ui.Canvas) {
	if ctx != nil && ctx.Runtime != nil {
		ctx.Runtime.Register(ui.Control{
			ID:      n.id,
			Rect:    ui.Rect{W: canvas.Width(), H: canvas.Height()},
			Enabled: true,
		})
	}
	canvas.WriteText(0, 0, "control", ui.CellStyle{})
}

func TestChatTranscriptNodeStoresRenderedControls(t *testing.T) {
	retained := ui.NewRetainedTranscript()
	retained.SetItems([]ui.TranscriptItem{{Key: "one", Node: controlNode{id: "first"}}})
	node := NewChatTranscriptNode()
	node.SetState(ChatTranscriptState{
		Retained:   retained,
		Width:      12,
		Height:     3,
		Background: ui.CellColor{},
	})

	node.Layout(&ui.Context{Palette: theme.Default().Palette}, ui.Rect{W: 12, H: 3})
	node.Prepare(&ui.Context{Palette: theme.Default().Palette})

	control, ok := node.ControlAt(ui.Point{X: 1, Y: 0})
	if !ok || control.ID != "first" {
		t.Fatalf("expected transcript control, got %#v ok=%v", control, ok)
	}
}

func TestComposerNodeCursorDirtyIsLocalized(t *testing.T) {
	palette := theme.Default().Palette
	composer := ui.AsNode(ui.NewComposer(ui.ComposerProps{
		Palette:       palette,
		Width:         20,
		Value:         "hello",
		CursorIndex:   1,
		CursorVisible: true,
	}))
	node := NewComposerNode()
	node.SetState(ComposerState{AreaElement: composer, Element: composer, Revision: 1})
	ctx := &ui.Context{Palette: palette}
	node.Layout(ctx, ui.Rect{W: 20, H: 3})
	node.Prepare(ctx)
	node.ClearDirty()

	next := ui.AsNode(ui.NewComposer(ui.ComposerProps{
		Palette:       palette,
		Width:         20,
		Value:         "hello",
		CursorIndex:   2,
		CursorVisible: true,
	}))
	node.SetState(ComposerState{AreaElement: next, Element: next, Revision: 1, CursorDirty: true})
	node.Prepare(ctx)

	rects := node.DirtyRects()
	if len(rects) == 0 {
		t.Fatal("expected cursor dirty rects")
	}
	for _, rect := range rects {
		if rect.H > 1 {
			t.Fatalf("expected cursor-local damage, got %#v", rects)
		}
	}
}

func TestComposerNodeOwnsBlinkAndInvalidatesCursor(t *testing.T) {
	palette := theme.Default().Palette
	visible := ui.AsNode(ui.NewComposer(ui.ComposerProps{
		Palette:       palette,
		Width:         20,
		Value:         "hello",
		CursorIndex:   1,
		CursorVisible: true,
	}))
	hidden := ui.AsNode(ui.NewComposer(ui.ComposerProps{
		Palette:       palette,
		Width:         20,
		Value:         "hello",
		CursorIndex:   1,
		CursorVisible: false,
	}))
	node := NewComposerNode()
	node.SetState(ComposerState{
		AreaElement:   visible,
		Element:       visible,
		ElementHidden: hidden,
		Revision:      1,
		Focused:       true,
		BlinkEnabled:  true,
	})
	ctx := &ui.Context{Palette: palette}
	node.Layout(ctx, ui.Rect{W: 20, H: 3})
	node.Prepare(ctx)
	node.ClearDirty()

	if !node.HandleTimer(ui.TimerEvent{Owner: ComposerBlinkTimerOwner}) {
		t.Fatal("expected composer node to handle its blink timer")
	}
	if rects := node.DirtyRects(); len(rects) == 0 {
		t.Fatal("expected blink to self-invalidate cursor damage")
	}
	got := node.Surface(ctx, ui.Rect{W: 20, H: 3}).Lines()
	if len(got) == 0 {
		t.Fatalf("expected hidden cursor surface to render content, got %#v", got)
	}
}
