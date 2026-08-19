package presenttool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestPresentIsVoiceOnlyAndStoresGenericVisual(t *testing.T) {
	if _, enabled := tools.DefinitionFor(tools.Present, tools.Runtime{ChatRole: chatrole.General}); enabled {
		t.Fatal("present tool offered outside voice chat")
	}
	definition, enabled := tools.DefinitionFor(tools.Present, tools.Runtime{ChatRole: chatrole.Voice})
	if !enabled || !strings.Contains(string(definition.Function.Parameters), "text/markdown") {
		t.Fatalf("definition=%#v enabled=%v", definition, enabled)
	}
	result, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{ChatRole: chatrole.Voice},
		Request: tools.Request{Tool: tools.Present, Args: map[string]string{
			"title": "Appointments", "mime_type": "text/markdown", "content": "| Time | Person |\n|---|---|\n| 10:00 | Steen |",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result.Stored.(tools.PresentationStoredResult)
	if !ok || stored.Title != "Appointments" || stored.MIMEType != "text/markdown" || !strings.Contains(stored.Content, "Steen") {
		t.Fatalf("stored result = %#v", result.Stored)
	}
}

func TestPresentRejectsUnsupportedOrOversizedContent(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{"mime_type": "text/html", "content": "hello"}); err == nil {
		t.Fatal("expected unsafe MIME type rejection")
	}
	if _, err := (tool{}).NormalizeArgs(map[string]string{"mime_type": "text/plain", "content": strings.Repeat("x", 64*1024+1)}); err == nil {
		t.Fatal("expected size rejection")
	}
}
