package phonetool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/tools"
)

type fakeControl struct {
	action phonedevice.Action
	args   map[string]string
}

type photoControl struct {
	action phonedevice.Action
	result phonedevice.Result
}

func (c *photoControl) Capabilities() []phonedevice.CatalogEntry {
	entries := make([]phonedevice.CatalogEntry, 0, 4)
	for _, action := range []phonedevice.Action{phonedevice.PhotosSearch, phonedevice.PhotosThumbs, phonedevice.PhotoView, phonedevice.PhotoTransfer} {
		for _, entry := range phonedevice.Catalog() {
			if entry.Action == action {
				entries = append(entries, entry)
			}
		}
	}
	return entries
}
func (c *photoControl) Execute(_ context.Context, action phonedevice.Action, _ map[string]string) (phonedevice.Result, error) {
	c.action = action
	return c.result, nil
}

func TestPhotoCapabilitiesUseDedicatedTools(t *testing.T) {
	control := &photoControl{}
	runtime := tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)}
	if _, enabled := tools.DefinitionFor(tools.Phone, runtime); enabled {
		t.Fatal("generic phone tool should not duplicate dedicated photo tools")
	}
	for _, toolID := range []tools.ID{tools.PhonePhotosSearch, tools.PhonePhotosThumbs, tools.PhonePhotoView, tools.PhonePhotoTransfer} {
		if definition, enabled := tools.DefinitionFor(toolID, runtime); !enabled || definition.Function.Name != toolID.String() {
			t.Fatalf("photo definition %q = %#v enabled=%v", toolID, definition, enabled)
		}
	}
}

func TestPhotoThumbnailIsMaterializedWithoutPersistingBytes(t *testing.T) {
	control := &photoControl{result: phonedevice.Result{Text: "one thumbnail", Artifacts: []phonedevice.Artifact{{
		ID: "42", Name: "dog.jpg", MIMEType: "image/jpeg", Data: []byte("jpeg-data"),
	}}}}
	runtime := tools.Runtime{SessionID: id.ID("session-1"), ChatID: id.ID("chat-1"), ChatRole: chatrole.Voice, Services: RuntimeService(control)}
	t.Cleanup(func() { _ = os.RemoveAll(runtime.SessionTmpDir()) })
	result, err := tools.Call(context.Background(), tools.Options{Runtime: runtime, Request: tools.Request{Tool: tools.PhonePhotosThumbs}})
	if err != nil {
		t.Fatal(err)
	}
	if control.action != phonedevice.PhotosThumbs || !strings.Contains(result.Output, "Phone artifact 42 copied to") {
		t.Fatalf("result=%#v action=%q", result, control.action)
	}
	if strings.Contains(result.Output, "jpeg-data") {
		t.Fatal("binary artifact leaked into model-facing output")
	}
	files := result.Stored.(map[string]any)["artifacts"].([]storedArtifact)
	if got, err := os.ReadFile(files[0].Path); err != nil || string(got) != "jpeg-data" {
		t.Fatalf("materialized thumbnail = %q err=%v", got, err)
	}
}

func TestPhotoTransferWritesRequestedWorkspacePath(t *testing.T) {
	workspace := t.TempDir()
	control := &photoControl{result: phonedevice.Result{Artifacts: []phonedevice.Artifact{{
		ID: "42", Name: "dog.jpg", MIMEType: "image/jpeg", Data: []byte("original-photo"),
	}}}}
	runtime := tools.Runtime{Workdir: workspace, SessionID: id.ID("session-1"), ChatID: id.ID("chat-1"), ChatRole: chatrole.Voice, Services: RuntimeService(control)}
	result, err := tools.Call(context.Background(), tools.Options{Runtime: runtime, Request: tools.Request{
		Tool: tools.PhonePhotoTransfer, Args: map[string]string{"photo_id": "42", "path": "reference/dog.jpg"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, "reference", "dog.jpg")); err != nil || string(got) != "original-photo" {
		t.Fatalf("transferred photo = %q err=%v result=%#v", got, err, result)
	}
}

func (f *fakeControl) Capabilities() []phonedevice.CatalogEntry {
	return []phonedevice.CatalogEntry{{Action: phonedevice.SearchContacts, Summary: "Search contacts", Arguments: "query"}}
}

func (f *fakeControl) Execute(_ context.Context, action phonedevice.Action, args map[string]string) (phonedevice.Result, error) {
	f.action, f.args = action, args
	return phonedevice.Result{Text: "Steen: +45 1234", Data: map[string]any{"count": 1}}, nil
}

func TestPhoneDefinitionIsDynamicAndVoiceOnly(t *testing.T) {
	control := &fakeControl{}
	runtime := tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)}
	definition, enabled := tools.DefinitionFor(tools.Phone, runtime)
	if !enabled || !strings.Contains(string(definition.Function.Parameters), `"search_contacts"`) {
		t.Fatalf("definition=%#v enabled=%v", definition, enabled)
	}
	if _, enabled := tools.DefinitionFor(tools.Phone, tools.Runtime{ChatRole: chatrole.Execution, Services: RuntimeService(control)}); enabled {
		t.Fatal("phone tool offered outside the voice profile")
	}
	if _, enabled := tools.DefinitionFor(tools.Phone, tools.Runtime{ChatRole: chatrole.Voice}); enabled {
		t.Fatal("phone tool offered without a connected provider")
	}
}

func TestPhoneDefinitionKeepsLocalContextReadOnly(t *testing.T) {
	control := catalogControl{entries: []phonedevice.CatalogEntry{
		catalogEntry(t, phonedevice.GetLocation),
		catalogEntry(t, phonedevice.OpenMap),
	}}
	definition, enabled := tools.DefinitionFor(tools.Phone, tools.Runtime{
		ChatRole: chatrole.Voice,
		Services: RuntimeService(control),
	})
	if !enabled {
		t.Fatal("phone definition is disabled")
	}
	description := definition.Function.Description
	for _, required := range []string{
		"first read get_location",
		"coordinate a sibling chat with the resolved place name",
		"only when the user explicitly asks",
		"never use this merely to determine or describe where the user is",
	} {
		if !strings.Contains(description, required) {
			t.Fatalf("phone definition does not contain %q: %s", required, description)
		}
	}
}

func TestPhoneDefinitionIncludesReviewedContactEditArguments(t *testing.T) {
	control := catalogControl{entries: []phonedevice.CatalogEntry{catalogEntry(t, phonedevice.EditContact)}}
	definition, enabled := tools.DefinitionFor(tools.Phone, tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)})
	if !enabled {
		t.Fatal("phone definition is disabled")
	}
	parameters := string(definition.Function.Parameters)
	for _, required := range []string{`"edit_contact"`, `"contact_id"`, `"address"`, `"note"`} {
		if !strings.Contains(parameters, required) {
			t.Fatalf("phone parameters lack %s: %s", required, parameters)
		}
	}
}

func TestPhoneDefinitionIncludesReviewedCalendarEditArguments(t *testing.T) {
	control := catalogControl{entries: []phonedevice.CatalogEntry{catalogEntry(t, phonedevice.EditCalendarEvent)}}
	definition, enabled := tools.DefinitionFor(tools.Phone, tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)})
	if !enabled {
		t.Fatal("phone definition is disabled")
	}
	parameters := string(definition.Function.Parameters)
	for _, required := range []string{`"edit_calendar_event"`, `"event_id"`, `"operation"`, `"cancel"`} {
		if !strings.Contains(parameters, required) {
			t.Fatalf("phone parameters lack %s: %s", required, parameters)
		}
	}
}

func TestPhoneDefinitionLabelsUserFacingActions(t *testing.T) {
	control := catalogControl{entries: []phonedevice.CatalogEntry{
		catalogEntry(t, phonedevice.GetLocation),
		catalogEntry(t, phonedevice.OpenURL),
	}}
	definition, enabled := tools.DefinitionFor(tools.Phone, tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)})
	if !enabled {
		t.Fatal("phone definition is disabled")
	}
	description := definition.Function.Description
	for _, required := range []string{
		"open_url: Open an HTTPS URL",
		"USER-FACING ACTION",
		"current user utterance explicitly requests that exact action",
		"never call one to gain knowledge",
		"get_location: Read and resolve",
		"without opening another app",
	} {
		if !strings.Contains(description, required) {
			t.Fatalf("phone definition does not contain %q: %s", required, description)
		}
	}
}

func TestPhoneToolCallsInjectedControl(t *testing.T) {
	control := &fakeControl{}
	result, err := tools.Call(context.Background(), tools.Options{
		Runtime: tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)},
		Request: tools.Request{Tool: tools.Phone, Args: map[string]string{"action": " search_contacts ", "query": " Steen "}},
	})
	if err != nil || result.Output != "Steen: +45 1234" || control.action != phonedevice.SearchContacts || control.args["query"] != "Steen" {
		t.Fatalf("result=%#v action=%q args=%v err=%v", result, control.action, control.args, err)
	}
}

type catalogControl struct {
	entries []phonedevice.CatalogEntry
}

func (c catalogControl) Capabilities() []phonedevice.CatalogEntry { return c.entries }
func (catalogControl) Execute(context.Context, phonedevice.Action, map[string]string) (phonedevice.Result, error) {
	return phonedevice.Result{}, nil
}

func catalogEntry(t *testing.T, action phonedevice.Action) phonedevice.CatalogEntry {
	t.Helper()
	for _, entry := range phonedevice.Catalog() {
		if entry.Action == action {
			return entry
		}
	}
	t.Fatalf("catalog action %q was not found", action)
	return phonedevice.CatalogEntry{}
}
