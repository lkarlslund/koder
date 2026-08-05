package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/tools"
)

func TestBrowserToolSchemasAndRequiredArguments(t *testing.T) {
	for _, spec := range specs {
		if !json.Valid([]byte(spec.parameters)) {
			t.Errorf("%s has invalid schema: %s", spec.id, spec.parameters)
		}
	}
	if _, err := (tool{id: tools.BrowserClick}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("browser_click should require ref")
	}
	if _, err := (tool{id: tools.BrowserNavigate}).NormalizeArgs(map[string]string{"url": "file:///etc/passwd"}); err == nil {
		t.Fatal("browser_navigate should reject file URLs")
	}
}

func TestBrowserDefinitionsRequireRuntimeService(t *testing.T) {
	if _, enabled := tools.DefinitionFor(tools.BrowserStatus, tools.Runtime{}); enabled {
		t.Fatal("browser definition should be hidden without browser service")
	}
	if _, enabled := tools.DefinitionFor(tools.BrowserStatus, tools.Runtime{Browser: fakeBrowser{}}); !enabled {
		t.Fatal("browser definition should be exposed with browser service")
	}
}

func TestBrowserToolsRequireNetworkAccess(t *testing.T) {
	_, err := tools.Call(t.Context(), tools.Options{Runtime: tools.Runtime{Browser: fakeBrowser{}, AccessSettings: accesssettings.LockedDown()}, Request: tools.Request{Tool: tools.BrowserStatus, Args: map[string]string{}}})
	if err == nil || !tools.IsDenied(err) {
		t.Fatalf("expected network access denial, got %v", err)
	}
}

type fakeBrowser struct{}

func (fakeBrowser) Status(context.Context, browserapi.Chat) browserapi.Status {
	return browserapi.Status{}
}
func (fakeBrowser) Start(context.Context) error                                     { return nil }
func (fakeBrowser) Stop(context.Context) error                                      { return nil }
func (fakeBrowser) Restart(context.Context) error                                   { return nil }
func (fakeBrowser) ResetProfile(context.Context) error                              { return nil }
func (fakeBrowser) Show(context.Context, browserapi.Chat) error                     { return nil }
func (fakeBrowser) Tabs(context.Context, browserapi.Chat) ([]browserapi.Tab, error) { return nil, nil }
func (fakeBrowser) NewTab(context.Context, browserapi.Chat, string) (browserapi.Tab, error) {
	return browserapi.Tab{}, nil
}
func (fakeBrowser) ClaimTab(context.Context, browserapi.Chat, string) (browserapi.Tab, error) {
	return browserapi.Tab{}, nil
}
func (fakeBrowser) SelectTab(context.Context, browserapi.Chat, string) (browserapi.Tab, error) {
	return browserapi.Tab{}, nil
}
func (fakeBrowser) CloseTab(context.Context, browserapi.Chat, string) error { return nil }
func (fakeBrowser) Navigate(context.Context, browserapi.Chat, string, string) (browserapi.Tab, error) {
	return browserapi.Tab{}, nil
}
func (fakeBrowser) History(context.Context, browserapi.Chat, string) (browserapi.Tab, error) {
	return browserapi.Tab{}, nil
}
func (fakeBrowser) Snapshot(context.Context, browserapi.Chat, string, int, int) (browserapi.Snapshot, error) {
	return browserapi.Snapshot{}, nil
}
func (fakeBrowser) Find(context.Context, browserapi.Chat, string, string, int) (browserapi.Snapshot, error) {
	return browserapi.Snapshot{}, nil
}
func (fakeBrowser) Interact(context.Context, browserapi.Chat, string, string, string) error {
	return nil
}
func (fakeBrowser) Upload(context.Context, browserapi.Chat, string, []string) error   { return nil }
func (fakeBrowser) Evaluate(context.Context, browserapi.Chat, string) (string, error) { return "", nil }
func (fakeBrowser) Screenshot(context.Context, browserapi.Chat, string, bool, string, int) (browserapi.Binary, error) {
	return browserapi.Binary{}, errors.New("unused")
}
func (fakeBrowser) PDF(context.Context, browserapi.Chat) (browserapi.Binary, error) {
	return browserapi.Binary{}, errors.New("unused")
}
func (fakeBrowser) Console(context.Context, browserapi.Chat, string, int) ([]browserapi.ConsoleRecord, error) {
	return nil, nil
}
func (fakeBrowser) Requests(context.Context, browserapi.Chat, int) ([]browserapi.RequestRecord, error) {
	return nil, nil
}
func (fakeBrowser) ResponseBody(context.Context, browserapi.Chat, string) (browserapi.Binary, error) {
	return browserapi.Binary{}, nil
}
func (fakeBrowser) CleanupChat(context.Context, id.ID)    {}
func (fakeBrowser) CleanupSession(context.Context, id.ID) {}
