package ui

import (
	"reflect"
	"testing"

	"github.com/lkarlslund/koder/internal/theme"
)

func assertElementRenderMatchesWrapper(t *testing.T, ctx *Context, wrapper Element, inner Element, bounds Rect) {
	t.Helper()
	got := wrapper.Render(ctx, bounds)
	want := inner.Render(ctx, bounds)
	if got.Size() != want.Size() {
		t.Fatalf("size mismatch: got %#v want %#v", got.Size(), want.Size())
	}
	if !reflect.DeepEqual(got.Lines(), want.Lines()) {
		t.Fatalf("line mismatch:\ngot=%q\nwant=%q", got.Lines(), want.Lines())
	}
	if !reflect.DeepEqual(got.Controls(), want.Controls()) {
		t.Fatalf("controls mismatch:\ngot=%#v\nwant=%#v", got.Controls(), want.Controls())
	}
}

func TestSlashMenuWrapperMatchesInnerElement(t *testing.T) {
	palette := theme.Default().Palette
	element := SlashMenu{
		Title: "Commands",
		Items: []MenuItem{
			{Title: "/help", Description: "Show help"},
			{Title: "/open", Description: "Open picker"},
		},
		Selected: 1,
	}
	width := element.panelWidth(element.contentWidth())
	assertElementRenderMatchesWrapper(t, &Context{Palette: palette, Runtime: &Runtime{}}, element, element.element(max(0, width-4)), Rect{W: width, H: 4})
}

func TestHistoryMenuWrapperMatchesInnerElement(t *testing.T) {
	palette := theme.Default().Palette
	element := HistoryMenu{
		Palette: palette,
		Query:   "draft",
		Items: []MenuItem{
			{Title: "Draft 1", Description: "alpha"},
			{Title: "Draft 2", Description: "beta"},
		},
		Selected: 1,
		Width:    40,
	}
	assertElementRenderMatchesWrapper(t, &Context{Palette: palette, Runtime: &Runtime{}}, element, element.element(), Rect{W: 40, H: 8})
}

func TestApprovalPromptWrapperMatchesInnerElement(t *testing.T) {
	palette := theme.Default().Palette
	element := NewApprovalPrompt(ApprovalPromptProps{
		Palette:      palette,
		Title:        "Approve action",
		Body:         "Apply patch to file?",
		ApproveLabel: "Approve",
		DenyLabel:    "Deny",
		ApproveFocus: true,
		Hints:        "Enter approves",
	})
	assertElementRenderMatchesWrapper(t, &Context{Palette: palette, Runtime: &Runtime{}}, element, element.element(), Rect{W: 28, H: 8})
}

func TestMenuPickerDialogWrapperMatchesInnerElement(t *testing.T) {
	palette := theme.Default().Palette
	element := NewMenuPickerDialog(PickerDialogProps{
		Palette: palette,
		Title:   "Pick option",
		Hint:    "Choose one",
		Query:   "op",
		Items: []MenuItem{
			{Title: "Open", Description: "Open file"},
			{Title: "Option", Description: "Open option"},
		},
		Index: 1,
	})
	ctx := &Context{Palette: palette, Runtime: &Runtime{}}
	got := element.Render(ctx, Rect{W: 80, H: 10})
	want := element.element().Render(&Context{Palette: palette}, Rect{W: 80, H: 10})
	if got.Size() != want.Size() {
		t.Fatalf("size mismatch: got %#v want %#v", got.Size(), want.Size())
	}
	if !reflect.DeepEqual(got.Lines(), want.Lines()) {
		t.Fatalf("line mismatch:\ngot=%q\nwant=%q", got.Lines(), want.Lines())
	}
	if len(got.Controls()) == 0 {
		t.Fatal("expected wrapper render to preserve dialog controls")
	}
}
