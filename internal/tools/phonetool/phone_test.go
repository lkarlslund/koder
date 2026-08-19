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
