package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/theme"
)

func benchmarkPalette() theme.Palette {
	return theme.Default().Palette
}

func benchmarkContext() *Context {
	return &Context{Palette: benchmarkPalette()}
}

func benchmarkTranscript(width, items int) Transcript {
	palette := benchmarkPalette()
	entries := make([]TranscriptItem, 0, items)
	for i := 0; i < items; i++ {
		var element Element
		switch i % 4 {
		case 0:
			element = UserMessage{
				Palette:     palette,
				Body:        strings.Repeat("user text ", 8),
				Stamp:       "12:34:56",
				Width:       width / 2,
				HalfBlocks:  true,
				PromptGlyph: "┃",
			}
		case 1:
			element = AssistantMessage{
				Palette: palette,
				Body:    strings.Repeat("assistant response body ", 12),
				Stamp:   "12:34:57",
				Width:   width,
			}
		case 2:
			element = AssistantMessage{
				Palette: palette,
				Body:    strings.Repeat("tool narration and output summary ", 8),
				Stamp:   "12:34:58",
				Width:   width,
			}
		default:
			element = ReasoningBlock{
				Palette: palette,
				Body:    strings.Repeat("reasoning line ", 14),
				Width:   width,
			}
		}
		entries = append(entries, TranscriptItem{Element: element, GapBefore: 2})
	}
	return Transcript{Items: entries}
}

func BenchmarkRenderElementTranscriptLarge(b *testing.B) {
	ctx := benchmarkContext()
	transcript := benchmarkTranscript(100, 120)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RenderElement(ctx, transcript, 100, 0)
	}
}

func BenchmarkRenderElementComposer(b *testing.B) {
	ctx := benchmarkContext()
	composer := Composer{
		Palette:       benchmarkPalette(),
		Width:         100,
		HalfBlocks:    true,
		PromptGlyph:   "┃",
		Value:         "benchmark composer text",
		ContentBefore: strings.Repeat("composed text ", 4),
		ContentCursor: "x",
		ContentAfter:  strings.Repeat(" trailing", 3),
		CursorVisible: true,
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RenderElement(ctx, composer, 100, 3)
	}
}

func BenchmarkRenderElementTableAndList(b *testing.B) {
	ctx := benchmarkContext()
	rows := make([]TableRow, 0, 40)
	items := make([]ListItem, 0, 40)
	for i := 0; i < 40; i++ {
		rows = append(rows, TableRow{
			ControlID: fmt.Sprintf("row-%d", i),
			Cells:     []string{fmt.Sprintf("model-%02d-long-name", i), "provider", "img,pdf"},
			Selected:  i == 12,
			Focused:   i == 12,
		})
		items = append(items, ListItem{
			ControlID: fmt.Sprintf("item-%d", i),
			Primary:   fmt.Sprintf("title-%02d", i),
			Secondary: "secondary description",
			Tertiary:  "meta",
		})
	}
	element := FlexBox{Direction: DirectionVertical, Children: []Child{
		Fixed(Table{
			Columns: []TableColumn{
				{Title: "Model", Width: 32},
				{Title: "Owner", Width: 18},
				{Title: "Caps", Width: 12},
			},
			Rows:       rows,
			Width:      72,
			ShowHeader: true,
		}),
		Fixed(Spacer{H: 1}),
		Fixed(List{Items: items, Width: 72, Selected: 12, Focused: true}),
	}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RenderElement(ctx, element, 72, 0)
	}
}

func BenchmarkSurfaceNormalizeLarge(b *testing.B) {
	surface := SurfaceFromString(strings.Repeat("0123456789abcdef", 16) + "\n" + strings.Repeat("line\n", 120))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = surface.normalize(96, 80)
	}
}

func BenchmarkSurfacePlaceAtLarge(b *testing.B) {
	base := BlankSurface(120, 60)
	child := SurfaceFromString(strings.Repeat("overlay-content ", 4) + "\n" + strings.Repeat("more ", 8))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = base.placeAt(8, 10, child)
	}
}

func BenchmarkCellSurfaceWriteLine(b *testing.B) {
	surface := BlankSurface(120, 8)
	style := CellStyle{FG: cellColor(benchmarkPalette().MarkdownText)}
	text := strings.Repeat("cell-write ", 8)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := surface
		s.WriteText(4, 3, text, style)
	}
}

func BenchmarkCellSurfaceBlitLarge(b *testing.B) {
	base := BlankSurface(120, 60)
	child := BlankSurface(48, 8)
	style := CellStyle{FG: cellColor(benchmarkPalette().MarkdownText)}
	for y := 0; y < 8; y++ {
		child.WriteText(0, y, strings.Repeat("overlay ", 6), style)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = base.placeAt(8, 10, child)
	}
}

func BenchmarkDiffSurfaceDamageCursorBlink(b *testing.B) {
	previous := BlankSurface(120, 40)
	current := previous
	previous.WriteText(10, 38, "x", CellStyle{})
	current.WriteText(10, 38, "x", CellStyle{FG: NewCellColorRGB(255, 255, 255), BG: NewCellColorRGB(0, 0, 0)})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = DiffSurfaceDamage(previous, current)
	}
}

func BenchmarkDiffSurfaceDamageFooterRow(b *testing.B) {
	previous := BlankSurface(120, 40)
	current := previous
	previous.WriteText(0, 39, strings.Repeat("ready ", 10), CellStyle{})
	current.WriteText(0, 39, strings.Repeat("busy ", 10), CellStyle{})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = DiffSurfaceDamage(previous, current)
	}
}

func BenchmarkCanvasWriteTextComposite(b *testing.B) {
	surface := BlankSurface(120, 4)
	base := CellStyle{BG: cellColor(benchmarkPalette().SidebarBackground)}
	for y := 0; y < surface.SurfaceHeight(); y++ {
		for x := 0; x < surface.SurfaceWidth(); x++ {
			surface.setCell(x, y, blankCell(base))
		}
	}
	textStyle := CellStyle{FG: cellColor(benchmarkPalette().MarkdownText)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := surface
		canvas := NewCanvas(&s, Rect{W: s.SurfaceWidth(), H: s.SurfaceHeight()})
		canvas.WriteText(8, 2, strings.Repeat("overlay ", 8), textStyle)
	}
}

func BenchmarkSurfaceNodePaintDiff(b *testing.B) {
	toggle := false
	node := &SurfaceNode{
		MeasureFn: func(_ *Context, constraints Constraints) Size {
			return constraints.Clamp(Size{W: 40, H: 1})
		},
		RenderFn: func(_ *Context, bounds Rect) Surface {
			surface := BlankSurface(bounds.W, bounds.H)
			text := "ready"
			if toggle {
				text = "busy "
			}
			surface.WriteText(2, 0, text, CellStyle{FG: cellColor(benchmarkPalette().MarkdownText)})
			return surface
		},
	}
	node.Layout(nil, Rect{W: 40, H: 1})
	root := BlankSurface(40, 1)
	canvas := NewCanvas(&root, Rect{W: 40, H: 1})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		toggle = !toggle
		node.Paint(nil, canvas)
		node.ClearDirty()
	}
}

func BenchmarkButtonRowRender(b *testing.B) {
	row := ButtonRow{
		Buttons: []Button{
			{Label: "Approve", Primary: true, Hotkey: 'a'},
			{Label: "Permissions", Hotkey: 'p'},
			{Label: "Deny", Hotkey: 'd'},
		},
		Index: 1,
		Gap:   2,
		Width: 64,
		Align: HorizontalAlignRight,
	}
	palette := benchmarkPalette()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = row.render(palette)
	}
}

func BenchmarkMenuRenderCell(b *testing.B) {
	ctx := benchmarkContext()
	menu := HistoryMenu{
		Palette: benchmarkPalette(),
		Query:   "needle",
		Width:   72,
		Items: []MenuItem{
			{Title: "first-entry", Description: "first description"},
			{Title: "second-entry", Description: "second description"},
			{Title: "third-entry", Description: "third description"},
			{Title: "fourth-entry", Description: "fourth description"},
		},
		Selected: 2,
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = menu.Render(ctx, Rect{W: 72})
	}
}

func BenchmarkBorderRenderCell(b *testing.B) {
	ctx := benchmarkContext()
	panel := Border{
		Width:        72,
		Padding:      SymmetricInsets(1, 1),
		Background:   benchmarkPalette().SidebarBackground,
		Foreground:   benchmarkPalette().SidebarForeground,
		BorderLeft:   true,
		BorderRight:  true,
		BorderTop:    true,
		BorderBottom: true,
		BorderColor:  benchmarkPalette().SidebarBorder,
		Child: Paragraph{
			Text: strings.Repeat("panel paragraph content ", 12),
			Style: lipgloss.NewStyle().
				Foreground(benchmarkPalette().MarkdownText),
		},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = panel.Render(ctx, Rect{W: 72, H: panel.Measure(ctx, NewConstraints(72, 0)).H})
	}
}

func BenchmarkToolRunCardRender(b *testing.B) {
	run := ToolRun{
		ID:       "grep-1",
		Tool:     domain.ToolKindGrep,
		Title:    "Search text",
		Subtitle: "needle in internal (*.go)",
		Status:   ToolRunStatusCompleted,
		Output:   strings.Repeat("internal/tui/model.go:123:matching line output\n", 10),
	}
	palette := benchmarkPalette()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = run.CardSurface(palette, 92, false, false)
	}
}
