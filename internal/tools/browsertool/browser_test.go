package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		if strings.Contains(spec.parameters, `"ref"`) || strings.Contains(spec.parameters, `"source_ref"`) {
			t.Errorf("%s still exposes element refs: %s", spec.id, spec.parameters)
		}
	}
	var selectSchema string
	for _, spec := range specs {
		if spec.id == tools.BrowserSelect {
			selectSchema = spec.parameters
			break
		}
	}
	for _, forbidden := range []string{`"selector"`, `"anyOf"`, `"allOf"`} {
		if strings.Contains(selectSchema, forbidden) {
			t.Fatalf("browser_select exposes llama.cpp-incompatible schema keyword %s: %s", forbidden, selectSchema)
		}
	}
	if !strings.Contains(selectSchema, `"required":["value","target"]`) {
		t.Fatalf("browser_select does not directly require value and target: %s", selectSchema)
	}
	if _, err := (tool{id: tools.BrowserClick}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("browser_click should require target")
	}
	if args, err := (tool{id: tools.BrowserClick}).NormalizeArgs(map[string]string{"target": " Submit "}); err != nil || args["target"] != "Submit" {
		t.Fatalf("browser_click semantic target normalization failed: %#v, %v", args, err)
	}
	for _, target := range []string{"css=#submit", "xpath=//button[@type='submit']"} {
		if _, err := (tool{id: tools.BrowserClick}).NormalizeArgs(map[string]string{"target": target}); err != nil {
			t.Fatalf("browser_click rejected advanced target %q: %v", target, err)
		}
	}
	if _, err := (tool{id: tools.BrowserNavigate}).NormalizeArgs(map[string]string{"url": "ftp://example.com"}); err == nil {
		t.Fatal("browser_navigate should reject unsupported URL schemes")
	}
}

func TestPermittedBrowserURLHonorsReadAccess(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "page.html")
	if err := os.WriteFile(path, []byte("<title>local</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{Workdir: root, AccessSettings: accesssettings.AllowAll()}
	want := (&url.URL{Scheme: "file", Path: path}).String()
	got, err := permittedBrowserURL(runtime, want)
	if err != nil || got != want {
		t.Fatalf("permittedBrowserURL() = %q, %v", got, err)
	}
	locked := runtime
	locked.AccessSettings.Project = accesssettings.ModeNone
	if _, err := permittedBrowserURL(locked, want); err == nil {
		t.Fatal("expected file URL read access rejection")
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
	if strings.Contains(result.Output, "extracted page text") {
		t.Fatalf("saved result should not repeat extracted data to the model: %q", result.Output)
	}
	stored, ok := result.Stored.(tools.BrowserStoredResult)
	if !ok || stored.Path != "captures/page.json" {
		t.Fatalf("unexpected stored result: %#v", result.Stored)
	}
}

func TestBrowserScreenshotSavesBinaryInsteadOfAttachment(t *testing.T) {
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
	if result.Meta["path"] != "captures/page.png" || result.Meta["attachment_id"] != "" {
		t.Fatalf("unexpected result metadata: %#v", result.Meta)
	}
	stored, ok := result.Stored.(tools.BrowserStoredResult)
	if !ok || stored.Path != "captures/page.png" || stored.Attachment != nil {
		t.Fatalf("unexpected stored result: %#v", result.Stored)
	}
}

func TestBrowserScreenshotWithoutFileReturnsAttachment(t *testing.T) {
	attachments := attachment.NewManager(t.TempDir())
	result, err := (tool{id: tools.BrowserScreenshot}).Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Workdir: t.TempDir(), Browser: savingBrowser{}, Attachments: attachments, SessionID: "session-1", ChatID: "chat-1"},
		Request: tools.Request{Tool: tools.BrowserScreenshot, Args: map[string]string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result.Stored.(tools.BrowserStoredResult)
	if !ok || stored.Path != "" || stored.Attachment == nil || result.Meta["attachment_id"] == "" {
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

func TestBrowserWaitUsesDirectEvaluation(t *testing.T) {
	browser := &waitBrowser{}
	result, err := (tool{id: tools.BrowserWait, title: "Browser wait"}).Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Browser: browser, SessionID: "session-1", ChatID: "chat-1"},
		Request: tools.Request{Tool: tools.BrowserWait, Args: map[string]string{"text": "ready", "timeout_ms": "100"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if browser.findCalls != 0 || browser.evaluateCalls != 1 {
		t.Fatalf("wait used find %d times and evaluate %d times", browser.findCalls, browser.evaluateCalls)
	}
	if !strings.Contains(result.Output, `"found": "ready"`) {
		t.Fatalf("unexpected wait output: %s", result.Output)
	}
}

func TestBrowserWaitBoundsBlockedEvaluation(t *testing.T) {
	started := time.Now()
	_, err := (tool{id: tools.BrowserWait, title: "Browser wait"}).Call(t.Context(), tools.Options{
		Runtime: tools.Runtime{Browser: blockingBrowser{}, SessionID: "session-1", ChatID: "chat-1"},
		Request: tools.Request{Tool: tools.BrowserWait, Args: map[string]string{"text": "missing", "timeout_ms": "20.00000"}},
	})
	if err == nil || !strings.Contains(err.Error(), `timed out waiting for "missing" after 20ms`) {
		t.Fatalf("unexpected wait error: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("browser wait exceeded deadline: %s", elapsed)
	}
}

func TestIntArgAcceptsIntegralFloatEncoding(t *testing.T) {
	if got := intArg(map[string]string{"value": "2000.00000"}, "value", 30_000); got != 2000 {
		t.Fatalf("intArg() = %d, want 2000", got)
	}
	if got := intArg(map[string]string{"value": "2.5"}, "value", 30_000); got != 30_000 {
		t.Fatalf("intArg() = %d for fractional value, want fallback", got)
	}
}

func TestSemanticLocatorArguments(t *testing.T) {
	locator, err := locatorFromArgs(map[string]string{
		"target": "Submit order", "role": "button", "within": "Checkout", "exact": "true", "occurrence": "2.00000",
	}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	want := browserapi.Locator{Target: "Submit order", Role: "button", Within: "Checkout", Exact: true, Occurrence: 2}
	if locator != want {
		t.Fatalf("locatorFromArgs() = %#v, want %#v", locator, want)
	}
	partial, err := locatorFromArgs(map[string]string{"target": "Submit"}, "", true)
	if err != nil || partial.Exact {
		t.Fatalf("default partial locator = %#v, %v", partial, err)
	}
	css, err := locatorFromArgs(map[string]string{"target": "css=#submit"}, "", true)
	if err != nil || css.Target != "" || css.Selector != "#submit" {
		t.Fatalf("CSS locator = %#v, %v", css, err)
	}
	xpath, err := locatorFromArgs(map[string]string{"target": "xpath=//button"}, "", true)
	if err != nil || xpath.Target != "" || xpath.Selector != "xpath=//button" {
		t.Fatalf("XPath locator = %#v, %v", xpath, err)
	}
	if _, err := (tool{id: tools.BrowserPress}).NormalizeArgs(map[string]string{"key": "Enter"}); err != nil {
		t.Fatalf("browser_press should allow the focused element: %v", err)
	}
	if _, err := (tool{id: tools.BrowserDrag}).NormalizeArgs(map[string]string{"source": "Card", "target": "Column"}); err != nil {
		t.Fatalf("browser_drag semantic locators rejected: %v", err)
	}
}

var savedPNG = []byte("\x89PNG\r\n\x1a\nimage")

type savingBrowser struct{ fakeBrowser }

type waitBrowser struct {
	fakeBrowser
	findCalls     int
	evaluateCalls int
}

type blockingBrowser struct{ fakeBrowser }

func (blockingBrowser) Evaluate(ctx context.Context, _ browserapi.Chat, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (b *waitBrowser) Find(context.Context, browserapi.Chat, string, string, int) (browserapi.Snapshot, error) {
	b.findCalls++
	return browserapi.Snapshot{}, nil
}

func (b *waitBrowser) Evaluate(context.Context, browserapi.Chat, string) (string, error) {
	b.evaluateCalls++
	return "true", nil
}

func (savingBrowser) Snapshot(context.Context, browserapi.Chat, string, int, int) (browserapi.Snapshot, error) {
	return browserapi.Snapshot{TabID: "tab-1", Text: "extracted page text"}, nil
}

func (savingBrowser) Screenshot(context.Context, browserapi.Chat, browserapi.Locator, bool, string, int) (browserapi.Binary, error) {
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
func (fakeBrowser) Interact(context.Context, browserapi.Chat, string, browserapi.Locator, string) error {
	return nil
}
func (fakeBrowser) Drag(context.Context, browserapi.Chat, browserapi.Locator, browserapi.Locator) error {
	return nil
}
func (fakeBrowser) Scroll(context.Context, browserapi.Chat, browserapi.Locator, int, int) error {
	return nil
}
func (fakeBrowser) Upload(context.Context, browserapi.Chat, browserapi.Locator, []string) error {
	return nil
}
func (fakeBrowser) Evaluate(context.Context, browserapi.Chat, string) (string, error) { return "", nil }
func (fakeBrowser) Screenshot(context.Context, browserapi.Chat, browserapi.Locator, bool, string, int) (browserapi.Binary, error) {
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
