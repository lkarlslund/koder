package presenttool

import (
	"context"
	"encoding/json"
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

func TestPresentNormalizesVersionedGenericCard(t *testing.T) {
	card := `{"version":1,"blocks":[{"kind":"text","text":"Today in Aarhus","style":"heading"},{"kind":"key_value","items":[{"key":"Event","value":"DHL Stafet"}]},{"kind":"list","items":[{"title":"First item","detail":"Nearby"}]},{"kind":"progress","label":"Walking","value":2,"max":5},{"kind":"image","uri":"/artifacts/map.png","alt":"Map"},{"kind":"action","label":"Open details","uri":"https://example.com/event"},{"kind":"file","name":"event.ics","uri":"/artifacts/event.ics","mime_type":"text/calendar"}]}`
	args, err := (tool{}).NormalizeArgs(map[string]string{"title": "Nearby", "card": card})
	if err != nil {
		t.Fatal(err)
	}
	if args["mime_type"] != presentationMIME || args["title"] != "Nearby" {
		t.Fatalf("normalized args = %#v", args)
	}
	var decoded document
	if err := json.Unmarshal([]byte(args["content"]), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != 1 || len(decoded.Blocks) != 7 || decoded.Blocks[1].Items[0].Value != "DHL Stafet" {
		t.Fatalf("card = %#v", decoded)
	}
}

func TestPresentAcceptsProviderStringifiedCardAndTextTitleAlias(t *testing.T) {
	card := `{"version":1,"blocks":[{"kind":"text","title":"Fairphone 6","style":"heading"},{"kind":"text","text":"Available now."}]}`
	stringified, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	args, err := (tool{}).NormalizeArgs(map[string]string{"card": string(stringified)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded document
	if err := json.Unmarshal([]byte(args["content"]), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Blocks[0].Text != "Fairphone 6" || decoded.Blocks[0].Title != "" {
		t.Fatalf("normalized heading = %#v", decoded.Blocks[0])
	}
}

func TestPresentAcceptsPresentationDocumentMIME(t *testing.T) {
	content := `{"version":1,"blocks":[{"kind":"text","text":"Fairphone 6"}]}`
	args, err := (tool{}).NormalizeArgs(map[string]string{"mime_type": presentationMIME, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	if args["mime_type"] != presentationMIME || !strings.Contains(args["content"], "Fairphone 6") {
		t.Fatalf("normalized args = %#v", args)
	}
}

func TestPresentRejectsInvalidGenericCards(t *testing.T) {
	tests := []string{
		`{"version":2,"blocks":[{"kind":"text","text":"Hi"}]}`,
		`{"version":1,"blocks":[]}`,
		`{"version":1,"blocks":[{"kind":"table"}]}`,
		`{"version":1,"blocks":[{"kind":"progress","value":7,"max":5}]}`,
		`{"version":1,"blocks":[{"kind":"action","label":"Call","uri":"tel:+4512345678"}]}`,
		`{"version":1,"blocks":[{"kind":"image","uri":"//evil.example/image.png"}]}`,
		`{"version":1,"blocks":[{"kind":"text","text":"Hi"}]} {"version":1,"blocks":[]}`,
	}
	for _, card := range tests {
		if _, err := (tool{}).NormalizeArgs(map[string]string{"card": card}); err == nil {
			t.Errorf("NormalizeArgs(%s) succeeded", card)
		}
	}
	if _, err := (tool{}).NormalizeArgs(map[string]string{"card": `{"version":1,"blocks":[{"kind":"text","text":"Hi"}]}`, "mime_type": "text/plain", "content": "Hi"}); err == nil {
		t.Fatal("expected mixed legacy and card input rejection")
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
