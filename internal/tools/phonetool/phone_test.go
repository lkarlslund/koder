package phonetool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/tools"
)

type fakeControl struct {
	action phonedevice.Action
	args   map[string]string
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
