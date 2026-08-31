package presenttool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/offeredfile"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/offerfiletool"
	_ "github.com/lkarlslund/koder/internal/tools/showimagetool"
)

func TestPresentIsCrossModeResourceAndStoresGenericVisual(t *testing.T) {
	persisted := tools.Runtime{
		SessionID: "session-1", ChatID: "chat-1", ChatRole: chatrole.General,
		Attachments: attachment.NewManager(t.TempDir()), OfferedFiles: offeredfile.NewManager(nil),
	}
	definition, enabled := tools.DefinitionFor(tools.Present, persisted)
	if !enabled || !strings.Contains(string(definition.Function.Parameters), "text/markdown") || !strings.Contains(string(definition.Function.Parameters), `"media"`) || !strings.Contains(string(definition.Function.Parameters), `"file"`) || !strings.Contains(string(definition.Function.Parameters), `"maxItems":20`) {
		t.Fatalf("definition=%#v enabled=%v", definition, enabled)
	}
	if _, enabled := tools.DefinitionFor(tools.PresentContentOld, tools.Runtime{ChatRole: chatrole.Voice}); enabled {
		t.Fatal("legacy presentation content tool remained model-visible")
	}
	result, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{SessionID: "session-1", ChatID: "chat-1", ChatRole: chatrole.Voice},
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

func TestPresentShowsMultipleMediaItemsInOneCall(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{"one.png", "two.png"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := tools.Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{
			Workdir: workspace, SessionID: "session-1", ChatID: "chat-1", ChatRole: chatrole.General,
			Attachments: attachment.NewManager(t.TempDir()),
		},
		Request: tools.Request{Tool: tools.Present, Args: map[string]string{
			"action": "media", "title": "Two examples",
			"items": `[{"path":"one.png","title":"One"},{"path":"two.png","title":"Two"}]`,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result.Stored.(tools.ShowMediaStoredResult)
	if !ok || stored.Title != "Two examples" || len(stored.Items) != 2 || stored.Items[0].Attachment == nil || stored.Items[1].Attachment == nil {
		t.Fatalf("multi-media present result = %#v", result.Stored)
	}
}

func TestPresentActionsFollowDestinationCapabilities(t *testing.T) {
	persisted := tools.Runtime{SessionID: "session-1", ChatID: "chat-1", ChatRole: chatrole.General}
	definition, enabled := tools.DefinitionFor(tools.Present, persisted)
	parameters := string(definition.Function.Parameters)
	if !enabled || !strings.Contains(parameters, `"enum":["content"]`) {
		t.Fatalf("content-only definition=%#v enabled=%v", definition, enabled)
	}

	persisted.Attachments = attachment.NewManager(t.TempDir())
	definition, enabled = tools.DefinitionFor(tools.Present, persisted)
	parameters = string(definition.Function.Parameters)
	if !enabled || !strings.Contains(parameters, `"enum":["content","media"]`) {
		t.Fatalf("media-capable definition=%#v enabled=%v", definition, enabled)
	}
}

func TestPresentIsUnavailableWithoutDurableDestination(t *testing.T) {
	if _, enabled := tools.DefinitionFor(tools.Present, tools.Runtime{ChatRole: chatrole.General}); enabled {
		t.Fatal("stateless request was offered present")
	}
	_, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{ChatRole: chatrole.General},
		Request: tools.Request{Tool: tools.Present, Args: map[string]string{
			"action": "content", "mime_type": "text/plain", "content": "nowhere",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "active persisted chat") {
		t.Fatalf("stateless present error = %v", err)
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
