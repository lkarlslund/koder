package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/attachment"
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

func TestBrowserSnapshotSavesExtractedResult(t *testing.T) {
	workdir := t.TempDir()
	result, err := (tool{id: tools.BrowserSnapshot, title: "Browser snapshot"}).Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Workdir: workdir, Browser: savingBrowser{}, SessionID: "session-1", ChatID: "chat-1"},
		Request: tools.Request{Tool: tools.BrowserSnapshot, Args: map[string]string{"save_to_file": "captures/page.json"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(workdir, "captures", "page.json")
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "extracted page text" {
		t.Fatalf("unexpected saved snapshot: %s", data)
	}
	if result.Meta["path"] != "captures/page.json" || !strings.Contains(result.Output, "Saved to captures/page.json") {
		t.Fatalf("unexpected result: %#v", result)
	}
	stored, ok := result.Stored.(tools.BrowserStoredResult)
	if !ok || stored.Path != "captures/page.json" {
		t.Fatalf("unexpected stored result: %#v", result.Stored)
	}
}

func TestBrowserScreenshotSavesBinaryAndAttachment(t *testing.T) {
	workdir := t.TempDir()
	attachments := attachment.NewManager(t.TempDir())
	result, err := (tool{id: tools.BrowserScreenshot}).Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Workdir: workdir, Browser: savingBrowser{}, Attachments: attachments, SessionID: "session-1", ChatID: "chat-1"},
		Request: tools.Request{Tool: tools.BrowserScreenshot, Args: map[string]string{"save_to_file": "captures/page.png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "captures", "page.png"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(savedPNG) {
		t.Fatalf("saved screenshot differs: %q", data)
	}
	if result.Meta["path"] != "captures/page.png" || result.Meta["attachment_id"] == "" {
		t.Fatalf("unexpected result metadata: %#v", result.Meta)
	}
	stored, ok := result.Stored.(tools.BrowserStoredResult)
	if !ok || stored.Path != "captures/page.png" || stored.Attachment == nil {
		t.Fatalf("unexpected stored result: %#v", result.Stored)
	}
}

func TestBrowserBinaryCanSaveWithoutAttachmentStorage(t *testing.T) {
	workdir := t.TempDir()
	result, err := (tool{id: tools.BrowserResponseBody}).Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Workdir: workdir, Browser: savingBrowser{}, SessionID: "session-1", ChatID: "chat-1"},
		Request: tools.Request{Tool: tools.BrowserResponseBody, Args: map[string]string{"request_id": "request-1", "save_to_file": "captures/body.bin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "captures", "body.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "raw response bytes" || result.Meta["path"] != "captures/body.bin" || result.Meta["attachment_id"] != "" {
		t.Fatalf("unexpected saved response: data=%q meta=%#v", data, result.Meta)
	}
}

func TestBrowserExtractionPathRequiresWriteAccess(t *testing.T) {
	settings := accesssettings.Default()
	settings.Project = accesssettings.ModeReadOnly
	_, err := tools.Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Workdir: t.TempDir(), Browser: savingBrowser{}, AccessSettings: settings},
		Request: tools.Request{Tool: tools.BrowserSnapshot, Args: map[string]string{"save_to_file": "capture.json"}},
	})
	if err == nil || !tools.IsDenied(err) || !strings.Contains(err.Error(), "write access") {
		t.Fatalf("expected write access denial, got %v", err)
	}
}

var savedPNG = []byte("\x89PNG\r\n\x1a\nimage")

type savingBrowser struct{ fakeBrowser }

func (savingBrowser) Snapshot(context.Context, browserapi.Chat, string, int, int) (browserapi.Snapshot, error) {
	return browserapi.Snapshot{TabID: "tab-1", Generation: 1, Text: "extracted page text"}, nil
}

func (savingBrowser) Screenshot(context.Context, browserapi.Chat, string, bool, string, int) (browserapi.Binary, error) {
	return browserapi.Binary{Name: "browser-screenshot.png", MIME: "image/png", Data: savedPNG}, nil
}

func (savingBrowser) ResponseBody(context.Context, browserapi.Chat, string) (browserapi.Binary, error) {
	return browserapi.Binary{Name: "response.bin", MIME: "application/octet-stream", Data: []byte("raw response bytes")}, nil
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
func (fakeBrowser) Downloads(context.Context, browserapi.Chat) ([]browserapi.DownloadRecord, error) {
	return nil, nil
}
func (fakeBrowser) Download(context.Context, browserapi.Chat, string) (browserapi.Binary, error) {
	return browserapi.Binary{}, nil
}
func (fakeBrowser) CleanupChat(context.Context, id.ID)    {}
func (fakeBrowser) CleanupSession(context.Context, id.ID) {}
