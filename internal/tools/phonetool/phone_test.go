package phonetool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestPhotoCapabilitiesUseResourceTool(t *testing.T) {
	control := &photoControl{}
	runtime := tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)}
	if _, enabled := tools.DefinitionFor(tools.Phone, runtime); enabled {
		t.Fatal("generic phone tool should not duplicate dedicated photo tools")
	}
	definition, enabled := tools.DefinitionFor(tools.PhonePhotos, runtime)
	if !enabled {
		t.Fatal("phone_photos resource is disabled")
	}
	for _, action := range []string{"search", "thumbnails", "view", "transfer"} {
		if !strings.Contains(string(definition.Function.Parameters), `"`+action+`"`) {
			t.Fatalf("phone_photos lacks %q: %s", action, definition.Function.Parameters)
		}
	}
	for _, legacy := range []tools.ID{tools.PhonePhotosSearch, tools.PhonePhotosThumbs, tools.PhonePhotoView, tools.PhonePhotoTransfer} {
		if _, enabled := tools.DefinitionFor(legacy, runtime); enabled {
			t.Fatalf("legacy photo tool remained visible: %s", legacy)
		}
	}
}

func TestPhotoThumbnailIsMaterializedWithoutPersistingBytes(t *testing.T) {
	control := &photoControl{result: phonedevice.Result{Text: "one thumbnail", Artifacts: []phonedevice.Artifact{{
		ID: "42", Name: "dog.jpg", MIMEType: "image/jpeg", Data: []byte("jpeg-data"),
	}}}}
	runtime := tools.Runtime{SessionID: id.ID("session-1"), ChatID: id.ID("chat-1"), ChatRole: chatrole.Voice, Services: RuntimeService(control)}
	t.Cleanup(func() { _ = os.RemoveAll(runtime.SessionTmpDir()) })
	result, err := tools.Call(context.Background(), tools.Options{Runtime: runtime, Request: tools.Request{Tool: tools.PhonePhotos, Args: map[string]string{"action": "thumbnails"}}})
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
		Tool: tools.PhonePhotos, Args: map[string]string{"action": "transfer", "photo_id": "42", "path": "reference/dog.jpg"},
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
	definition, enabled := tools.DefinitionFor(tools.PhoneContacts, runtime)
	if !enabled || !strings.Contains(string(definition.Function.Parameters), `"search"`) {
		t.Fatalf("definition=%#v enabled=%v", definition, enabled)
	}
	if _, enabled := tools.DefinitionFor(tools.PhoneContacts, tools.Runtime{ChatRole: chatrole.Execution, Services: RuntimeService(control)}); enabled {
		t.Fatal("phone tool offered outside the voice profile")
	}
	if _, enabled := tools.DefinitionFor(tools.PhoneContacts, tools.Runtime{ChatRole: chatrole.Voice}); enabled {
		t.Fatal("phone tool offered without a connected provider")
	}
	if _, enabled := tools.DefinitionFor(tools.Phone, runtime); enabled {
		t.Fatal("legacy generic phone dispatcher remained model-visible")
	}
}

func TestPhoneResultIncludesStructuredDataInModelOutput(t *testing.T) {
	result, err := phoneResult(tools.Runtime{}, phonedevice.Result{
		Text: "Phone status read",
		Data: map[string]any{"time_zone": "Europe/Copenhagen", "online": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Phone status read", `"time_zone":"Europe/Copenhagen"`, `"online":true`} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("phone result output %q does not contain %q", result.Output, want)
		}
	}
}

func TestPhoneResourceValidationAndFixedActions(t *testing.T) {
	cases := []struct {
		name string
		req  tools.Request
		bad  bool
	}{
		{name: "sms missing message", req: tools.Request{Tool: tools.PhoneMessages, Args: map[string]string{"action": "send", "phone_number": "+451234"}}, bad: true},
		{name: "https required", req: tools.Request{Tool: tools.PhoneOpen, Args: map[string]string{"action": "url", "url": "http://example.com"}}, bad: true},
		{name: "call recipient", req: tools.Request{Tool: tools.PhoneCalls, Args: map[string]string{"action": "place"}}, bad: true},
		{name: "calendar cancel", req: tools.Request{Tool: tools.PhoneCalendar, Args: map[string]string{"action": "cancel", "event_id": "event-1"}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := tools.Normalize(test.req)
			if test.bad {
				if err == nil {
					t.Fatalf("Normalize(%#v) succeeded", test.req)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if normalized.Args["operation"] != "cancel" {
				t.Fatalf("calendar cancel args = %#v", normalized.Args)
			}
		})
	}
}

func TestPhoneDefinitionKeepsLocalContextReadOnly(t *testing.T) {
	control := catalogControl{entries: []phonedevice.CatalogEntry{
		catalogEntry(t, phonedevice.GetLocation),
		catalogEntry(t, phonedevice.OpenMap),
	}}
	location, enabled := tools.DefinitionFor(tools.PhoneLocation, tools.Runtime{
		ChatRole: chatrole.Voice,
		Services: RuntimeService(control),
	})
	if !enabled {
		t.Fatal("phone_location definition is disabled")
	}
	if !strings.Contains(location.Function.Description, "without opening a map") || strings.Contains(location.Function.Description, "USER-FACING") {
		t.Fatalf("phone_location description = %q", location.Function.Description)
	}
	opened, enabled := tools.DefinitionFor(tools.PhoneOpen, tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)})
	if !enabled || !strings.Contains(opened.Function.Description, "USER-FACING ACTION") || !strings.Contains(opened.Function.Description, "never use it to gather knowledge") {
		t.Fatalf("phone_open description = %q enabled=%v", opened.Function.Description, enabled)
	}
}

func TestPhoneDefinitionIncludesReviewedContactEditArguments(t *testing.T) {
	control := catalogControl{entries: []phonedevice.CatalogEntry{catalogEntry(t, phonedevice.EditContact)}}
	definition, enabled := tools.DefinitionFor(tools.PhoneContacts, tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)})
	if !enabled {
		t.Fatal("phone definition is disabled")
	}
	parameters := string(definition.Function.Parameters)
	for _, required := range []string{`"edit"`, `"contact_id"`, `"address"`, `"note"`} {
		if !strings.Contains(parameters, required) {
			t.Fatalf("phone parameters lack %s: %s", required, parameters)
		}
	}
}

func TestPhoneDefinitionIncludesReviewedCalendarEditArguments(t *testing.T) {
	control := catalogControl{entries: []phonedevice.CatalogEntry{catalogEntry(t, phonedevice.EditCalendarEvent)}}
	definition, enabled := tools.DefinitionFor(tools.PhoneCalendar, tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)})
	if !enabled {
		t.Fatal("phone definition is disabled")
	}
	parameters := string(definition.Function.Parameters)
	for _, required := range []string{`"edit"`, `"event_id"`, `"cancel"`} {
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
	definition, enabled := tools.DefinitionFor(tools.PhoneOpen, tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)})
	if !enabled {
		t.Fatal("phone definition is disabled")
	}
	description := definition.Function.Description
	for _, required := range []string{
		"url: Open an HTTPS URL",
		"USER-FACING ACTION",
		"user explicitly requested this exact",
		"never use it to gather knowledge",
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
		Request: tools.Request{Tool: tools.PhoneContacts, Args: map[string]string{"action": " search ", "query": " Steen "}},
	})
	if err != nil || !strings.Contains(result.Output, "Steen: +45 1234") || !strings.Contains(result.Output, `"count":1`) || control.action != phonedevice.SearchContacts || control.args["query"] != "Steen" {
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

type recordingControl struct {
	catalogControl
	action phonedevice.Action
	args   map[string]string
}

func (c *recordingControl) Execute(_ context.Context, action phonedevice.Action, args map[string]string) (phonedevice.Result, error) {
	c.action, c.args = action, args
	return phonedevice.Result{Text: "ok"}, nil
}

func TestPhoneLogsAndAlertsAreRegularPhoneTools(t *testing.T) {
	control := &recordingControl{catalogControl: catalogControl{entries: []phonedevice.CatalogEntry{
		catalogEntry(t, phonedevice.ReadAppLog), catalogEntry(t, phonedevice.ScheduleAlert), catalogEntry(t, phonedevice.ListAlerts),
	}}}
	runtime := tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)}
	if definition, ok := tools.DefinitionFor(tools.PhoneLogs, runtime); !ok || !strings.Contains(string(definition.Function.Parameters), `"read"`) {
		t.Fatalf("phone_logs definition = %#v, %v", definition, ok)
	}
	definition, ok := tools.DefinitionFor(tools.PhoneAlerts, runtime)
	if params := string(definition.Function.Parameters); !ok || !strings.Contains(params, `"schedule"`) || strings.Contains(params, `"cancel"`) {
		t.Fatalf("phone_alerts offers only what the phone advertised: %s", params)
	}

	before := time.Now()
	call := func(kind tools.ID, args map[string]string) error {
		_, err := tools.Call(context.Background(), tools.Options{Runtime: runtime, Request: tools.Request{Tool: kind, Args: args}})
		return err
	}
	if err := call(tools.PhoneAlerts, map[string]string{"action": "schedule", "title": "Tea", "in": "45m"}); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, control.args["at"])
	if err != nil || control.action != phonedevice.ScheduleAlert || control.args["in"] != "" || at.Sub(before) < 44*time.Minute || at.Sub(before) > 46*time.Minute {
		t.Fatalf("schedule sent %q %v (%v)", control.action, control.args, err)
	}
	if err := call(tools.PhoneLogs, map[string]string{"action": "read", "since": "30m", "level": "warn"}); err != nil {
		t.Fatal(err)
	}
	since, err := time.Parse(time.RFC3339, control.args["since"])
	if err != nil || control.action != phonedevice.ReadAppLog || before.Sub(since) < 29*time.Minute || control.args["level"] != "warn" {
		t.Fatalf("read sent %q %v (%v)", control.action, control.args, err)
	}

	for _, bad := range []map[string]string{
		{"action": "schedule", "title": "Tea"},
		{"action": "schedule", "title": "Tea", "in": "45m", "at": "2030-01-01T00:00:00Z"},
		{"action": "schedule", "title": "Tea", "at": "2000-01-01T00:00:00Z"},
		{"action": "schedule", "in": "45m"},
	} {
		if err := call(tools.PhoneAlerts, bad); err == nil {
			t.Fatalf("schedule %v was accepted", bad)
		}
	}
}
