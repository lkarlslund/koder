package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/browserapi"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct {
	id          tools.ID
	title       string
	description string
	parameters  string
}

var specs = []tool{
	{tools.BrowserStatus, "Browser status", "Inspect the managed browser's health and this chat's tab count.", object(saveToFileProperty)},
	{tools.BrowserTabList, "List browser tabs", "List this chat's tabs and unowned manual tabs without starting Chrome. Tabs owned by other chats are hidden.", object(saveToFileProperty)},
	{tools.BrowserTabNew, "New browser tab", "Create and select a browser tab owned by this chat.", object(`"url":{"type":"string"}`)},
	{tools.BrowserTabClaim, "Claim browser tab", "Atomically claim an unowned manual browser tab.", required(object(`"tab_id":{"type":"string"}`), "tab_id")},
	{tools.BrowserTabSelect, "Select browser tab", "Select one of this chat's browser tabs.", required(object(`"tab_id":{"type":"string"}`), "tab_id")},
	{tools.BrowserTabClose, "Close browser tab", "Close one of this chat's browser tabs.", required(object(`"tab_id":{"type":"string"}`), "tab_id")},
	{tools.BrowserNavigate, "Navigate browser", "Navigate the selected tab to an HTTP, HTTPS, or permitted local file URL.", required(object(`"url":{"type":"string"},"wait_until":{"type":"string","enum":["domcontentloaded","load","networkidle"]}`), "url")},
	{tools.BrowserBack, "Browser back", "Navigate the selected tab back.", object(``)},
	{tools.BrowserForward, "Browser forward", "Navigate the selected tab forward.", object(``)},
	{tools.BrowserReload, "Reload browser", "Reload the selected tab.", object(``)},
	{tools.BrowserSnapshot, "Browser snapshot", "Return an informational compact view of the current visible DOM. Interactions resolve their own targets and do not depend on snapshots.", object(`"depth":{"type":"integer"},"max_chars":{"type":"integer"},` + saveToFileProperty)},
	{tools.BrowserFind, "Find in browser", "Return up to 10 current visible semantic candidates as a flat list, with complete locator arguments ready for a subsequent interaction. Locators are resolved against the current DOM and are not stored references. Refine query or role when more candidates are omitted.", required(object(`"query":{"type":"string"},"role":{"type":"string"},"max_chars":{"type":"integer"},`+saveToFileProperty), "query")},
	{tools.BrowserClick, "Click browser element", "Find a current visible element by accessible name, label, or text and click it.", locatorObject("", true)},
	{tools.BrowserFill, "Fill browser element", "Find a current editable control and replace its value.", locatorObject(`"value":{"type":"string"}`, true, "value")},
	{tools.BrowserType, "Type in browser element", "Find a current editable control and type text into it.", locatorObject(`"value":{"type":"string"}`, true, "value")},
	{tools.BrowserPress, "Press browser key", "Send a key or key chord to a current semantic target, or to the focused element when target is omitted.", locatorObject(`"key":{"type":"string","description":"Key or chord such as Enter, Tab, Control+a, or Shift+Tab."}`, false, "key")},
	{tools.BrowserSelect, "Select browser option", "Find a current select control and set its value.", locatorObject(`"value":{"type":"string"}`, true, "value")},
	{tools.BrowserCheck, "Check browser element", "Find and check a current checkbox, radio button, or switch.", locatorObject("", true)},
	{tools.BrowserUncheck, "Uncheck browser element", "Find and uncheck a current checkbox or switch.", locatorObject("", true)},
	{tools.BrowserHover, "Hover browser element", "Find a current visible element and hover it.", locatorObject("", true)},
	{tools.BrowserDrag, "Drag browser element", "Find current source and destination elements and drag the source onto the destination.", dragLocatorObject()},
	{tools.BrowserScroll, "Scroll browser", "Scroll the page, or find and scroll a current element when a semantic target or selector is provided.", locatorObject(`"x":{"type":"integer"},"y":{"type":"integer"}`, false)},
	{tools.BrowserWait, "Wait in browser", "Wait for text to appear in the selected page.", required(object(`"text":{"type":"string"},"timeout_ms":{"type":"integer","minimum":1,"maximum":120000},`+saveToFileProperty), "text")},
	{tools.BrowserUpload, "Upload browser files", "Find a current file input and upload authorized workspace files through it.", locatorObject(`"paths":{"type":"array","items":{"type":"string"}}`, true, "paths")},
	{tools.BrowserEvaluate, "Evaluate browser JavaScript", "Evaluate JavaScript in the selected tab and return bounded JSON.", required(object(`"expression":{"type":"string"},`+saveToFileProperty), "expression")},
	{tools.BrowserScreenshot, "Screenshot browser", "Capture the viewport, full page, or a current semantic target. Return it as a session attachment unless save_to_file persists it to disk instead.", locatorObject(`"full_page":{"type":"boolean"},"format":{"type":"string","enum":["png","jpeg"]},"quality":{"type":"integer"},`+saveToFileProperty, false)},
	{tools.BrowserImage, "Extract browser image", "Extract the original bytes of a current image, or the transparent content of a canvas or inline SVG, without capturing the surrounding page. Return it as a session attachment unless save_to_file persists it to disk instead. Use browser_screenshot for rendered page elements.", locatorObject(saveToFileProperty, true)},
	{tools.BrowserPDF, "Save browser PDF", "Print the selected page as a PDF. Return it as a session attachment unless save_to_file persists it to disk instead.", object(saveToFileProperty)},
	{tools.BrowserConsole, "Browser console", "Read bounded console records for the selected tab.", object(`"level":{"type":"string"},"limit":{"type":"integer"},` + saveToFileProperty)},
	{tools.BrowserRequests, "Browser requests", "List bounded network records for the selected tab.", object(`"limit":{"type":"integer"},` + saveToFileProperty)},
	{tools.BrowserRequest, "Browser request", "Inspect one opaque browser request record.", required(object(`"request_id":{"type":"string"},`+saveToFileProperty), "request_id")},
	{tools.BrowserResponseBody, "Browser response body", "Read a response body by opaque request ID.", required(object(`"request_id":{"type":"string"},`+saveToFileProperty), "request_id")},
	{tools.BrowserDownloadsOld, "Browser downloads", "List downloads owned by this chat.", object(saveToFileProperty)},
	{tools.BrowserDownload, "Browser download", "Read a completed browser download. Return it as a session attachment unless save_to_file persists it to disk instead.", required(object(`"download_id":{"type":"string"},`+saveToFileProperty), "download_id")},
}

const saveToFileProperty = `"save_to_file":{"type":"string","description":"Optional destination file. Omit to return the extracted data to the model; set to persist it to disk instead."}`

func init() {
	for _, spec := range specs {
		tools.Register(spec, tools.ToolSpec{Title: spec.title, Description: spec.description, Usage: spec.description, Parameters: spec.parameters, ExposeToLLM: true, Legacy: legacyBrowserOperation(spec.id)})
	}
	registerResourceTools()
}

func legacyBrowserOperation(kind tools.ID) bool {
	switch kind {
	case tools.BrowserStatus, tools.BrowserConsole, tools.BrowserEvaluate:
		return false
	default:
		return true
	}
}

func registerResourceTools() {
	registerActionTool(tools.BrowserTabs, "Browser tabs", "Manage tabs owned by this chat. list returns owned and claimable manual tabs; create opens and selects a new tab; claim takes ownership of a manual tab; select changes the active tab; close closes an owned tab.",
		`{"type":"object","properties":{"action":{"type":"string"},"tab_id":{"type":"string"},"url":{"type":"string"},"save_to_file":{"type":"string"}},"required":["action"],"additionalProperties":false}`,
		[]tools.ActionRoute{{Action: "list", Tool: tools.BrowserTabList}, {Action: "create", Tool: tools.BrowserTabNew}, {Action: "claim", Tool: tools.BrowserTabClaim}, {Action: "select", Tool: tools.BrowserTabSelect}, {Action: "close", Tool: tools.BrowserTabClose}})
	registerActionTool(tools.BrowserNavigation, "Browser navigation", "Navigate the selected tab. goto requires an HTTP, HTTPS, or permitted local file URL; back and forward move through history; reload refreshes the current page.",
		`{"type":"object","properties":{"action":{"type":"string"},"url":{"type":"string"},"wait_until":{"type":"string","enum":["domcontentloaded","load","networkidle"]}},"required":["action"],"additionalProperties":false}`,
		[]tools.ActionRoute{{Action: "goto", Tool: tools.BrowserNavigate}, {Action: "back", Tool: tools.BrowserBack}, {Action: "forward", Tool: tools.BrowserForward}, {Action: "reload", Tool: tools.BrowserReload}})
	registerActionTool(tools.BrowserPage, "Browser page", "Inspect the selected page. snapshot returns a compact visible DOM; find returns semantic locator arguments for matching elements; wait blocks until specified visible text appears or timeout_ms expires.",
		`{"type":"object","properties":{"action":{"type":"string"},"query":{"type":"string"},"role":{"type":"string"},"text":{"type":"string"},"depth":{"type":"integer"},"max_chars":{"type":"integer"},"timeout_ms":{"type":"integer","minimum":1,"maximum":120000},"save_to_file":{"type":"string"}},"required":["action"],"additionalProperties":false}`,
		[]tools.ActionRoute{{Action: "snapshot", Tool: tools.BrowserSnapshot}, {Action: "find", Tool: tools.BrowserFind}, {Action: "wait", Tool: tools.BrowserWait}})
	registerActionTool(tools.BrowserInteract, "Browser interaction", "Interact with the selected page using current semantic targets. click, fill, type, press, select, check, uncheck, hover, drag, scroll, and upload map to their ordinary browser meanings. fill replaces a value while type appends keystrokes; upload requires authorized workspace paths.",
		required(object(`"action":{"type":"string"},`+locatorProperties("")+`,`+locatorProperties("source")+`,"value":{"type":"string"},"key":{"type":"string"},"x":{"type":"integer"},"y":{"type":"integer"},"paths":{"type":"array","items":{"type":"string"}}`), "action"),
		[]tools.ActionRoute{{Action: "click", Tool: tools.BrowserClick}, {Action: "fill", Tool: tools.BrowserFill}, {Action: "type", Tool: tools.BrowserType}, {Action: "press", Tool: tools.BrowserPress}, {Action: "select", Tool: tools.BrowserSelect}, {Action: "check", Tool: tools.BrowserCheck}, {Action: "uncheck", Tool: tools.BrowserUncheck}, {Action: "hover", Tool: tools.BrowserHover}, {Action: "drag", Tool: tools.BrowserDrag}, {Action: "scroll", Tool: tools.BrowserScroll}, {Action: "upload", Tool: tools.BrowserUpload}})
	registerActionTool(tools.BrowserCapture, "Browser capture", "Capture page output. screenshot captures rendered pixels; image extracts original image, canvas, or SVG bytes; pdf prints the selected page. Omit save_to_file for a session attachment or set it to persist the result.",
		required(object(`"action":{"type":"string"},`+locatorProperties("")+`,"full_page":{"type":"boolean"},"format":{"type":"string","enum":["png","jpeg"]},"quality":{"type":"integer"},`+saveToFileProperty), "action"),
		[]tools.ActionRoute{{Action: "screenshot", Tool: tools.BrowserScreenshot}, {Action: "image", Tool: tools.BrowserImage}, {Action: "pdf", Tool: tools.BrowserPDF}})
	registerActionTool(tools.BrowserNetwork, "Browser network", "Inspect selected-page network activity. list returns bounded request records; get_request returns one opaque request record; get_response_body reads its response bytes. Obtain request_id from list and use save_to_file for large or binary bodies.",
		`{"type":"object","properties":{"action":{"type":"string"},"request_id":{"type":"string"},"limit":{"type":"integer"},"save_to_file":{"type":"string"}},"required":["action"],"additionalProperties":false}`,
		[]tools.ActionRoute{{Action: "list", Tool: tools.BrowserRequests}, {Action: "get_request", Tool: tools.BrowserRequest}, {Action: "get_response_body", Tool: tools.BrowserResponseBody}})
	tools.Register(tools.ActionTool{
		Kind:          tools.BrowserDownloads,
		Routes:        []tools.ActionRoute{{Action: "list", Tool: tools.BrowserDownloadsOld}, {Action: "get", Tool: tools.BrowserDownload}},
		DefaultAction: "list",
	}, tools.ToolSpec{
		Title: "Browser downloads", Description: "List or retrieve downloads owned by this chat.",
		Usage:      "Use action=list to inspect downloads and action=get with download_id to retrieve a completed download. Omit save_to_file to return an attachment; set it to persist the bytes in the workspace.",
		Parameters: `{"type":"object","properties":{"action":{"type":"string","enum":["list","get"]},"download_id":{"type":"string"},"save_to_file":{"type":"string"}},"required":["action"],"additionalProperties":false}`, ExposeToLLM: true,
	})
}

func registerActionTool(kind tools.ID, title, description, parameters string, routes []tools.ActionRoute) {
	tools.Register(tools.ActionTool{Kind: kind, Routes: routes}, tools.ToolSpec{
		Title: title, Description: description,
		Usage:      description + " Choose the specific action and provide only fields used by that action. Semantic targets are resolved against the current DOM; do not invent stored element references.",
		Parameters: parameters, ExposeToLLM: true,
	})
}

func (t tool) ID() tools.ID             { return t.id }
func (t tool) BypassesPermission() bool { return false }
func (t tool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return spec, runtime.Browser != nil
}

func (t tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(args))
	for key, value := range args {
		out[key] = strings.TrimSpace(value)
	}
	if path := out["save_to_file"]; path != "" {
		out["save_to_file"] = tools.NormalizePathInput(path)
	}
	for _, key := range requiredArgs(t.id) {
		if out[key] == "" {
			return nil, fmt.Errorf("%s is required", key)
		}
	}
	if usesLocator(t.id) {
		required := t.id != tools.BrowserPress && t.id != tools.BrowserScroll && t.id != tools.BrowserScreenshot
		if _, err := locatorFromArgs(out, "", required); err != nil {
			return nil, err
		}
	}
	if t.id == tools.BrowserDrag {
		if _, err := locatorFromArgs(out, "source", true); err != nil {
			return nil, fmt.Errorf("source: %w", err)
		}
		if _, err := locatorFromArgs(out, "", true); err != nil {
			return nil, fmt.Errorf("target: %w", err)
		}
	}
	if t.id == tools.BrowserNavigate || t.id == tools.BrowserTabNew {
		if raw := out["url"]; raw != "" {
			parsed, err := url.Parse(raw)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "about" && parsed.Scheme != "file") {
				return nil, errors.New("url must use http, https, about, or file")
			}
		}
	}
	if t.id == tools.BrowserUpload {
		var paths []string
		if err := json.Unmarshal([]byte(out["paths"]), &paths); err != nil || len(paths) == 0 {
			return nil, errors.New("paths must be a non-empty array")
		}
	}
	return out, nil
}

func (t tool) Preview(req tools.Request) string {
	for _, key := range []string{"url", "query", "target", "source", "tab_id", "expression", "request_id", "download_id"} {
		if value := strings.TrimSpace(req.Args[key]); value != "" {
			return value
		}
	}
	return ""
}

func (t tool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	service := opts.Runtime.Browser
	if service == nil {
		return tools.Result{}, errors.New("browser service is unavailable")
	}
	chat := browserapi.Chat{SessionID: opts.Runtime.SessionID, ChatID: opts.Runtime.ChatID}
	args := opts.Request.Args
	var err error
	if t.id == tools.BrowserNavigate || t.id == tools.BrowserTabNew {
		if args["url"] != "" {
			args = maps.Clone(args)
			args["url"], err = permittedBrowserURL(opts.Runtime, args["url"])
			if err != nil {
				return tools.Result{}, err
			}
		}
	}
	result := tools.BrowserStoredResult{Kind: t.id.String()}
	var value any
	switch t.id {
	case tools.BrowserStatus:
		value = service.Status(ctx, chat)
	case tools.BrowserTabList:
		value, err = service.Tabs(ctx, chat)
	case tools.BrowserTabNew:
		value, err = service.NewTab(ctx, chat, args["url"])
	case tools.BrowserTabClaim:
		value, err = service.ClaimTab(ctx, chat, args["tab_id"])
	case tools.BrowserTabSelect:
		value, err = service.SelectTab(ctx, chat, args["tab_id"])
	case tools.BrowserTabClose:
		err = service.CloseTab(ctx, chat, args["tab_id"])
		value = map[string]string{"closed": args["tab_id"]}
	case tools.BrowserNavigate:
		value, err = service.Navigate(ctx, chat, args["url"], args["wait_until"])
	case tools.BrowserBack, tools.BrowserForward, tools.BrowserReload:
		value, err = service.History(ctx, chat, strings.TrimPrefix(t.id.String(), "browser_"))
	case tools.BrowserSnapshot:
		value, err = service.Snapshot(ctx, chat, "", intArg(args, "depth", -1), intArg(args, "max_chars", 32*1024))
	case tools.BrowserFind:
		value, err = service.Find(ctx, chat, args["query"], args["role"], intArg(args, "max_chars", 32*1024))
	case tools.BrowserClick, tools.BrowserFill, tools.BrowserType, tools.BrowserPress, tools.BrowserSelect, tools.BrowserCheck, tools.BrowserUncheck, tools.BrowserHover:
		action := strings.TrimPrefix(t.id.String(), "browser_")
		input := args["value"]
		if t.id == tools.BrowserPress {
			input = args["key"]
		}
		locator, _ := locatorFromArgs(args, "", false)
		err = service.Interact(ctx, chat, action, locator, input)
		value = map[string]any{"action": action, "locator": locator}
	case tools.BrowserDrag:
		source, _ := locatorFromArgs(args, "source", true)
		target, _ := locatorFromArgs(args, "", true)
		err = service.Drag(ctx, chat, source, target)
		value = map[string]browserapi.Locator{"source": source, "target": target}
	case tools.BrowserScroll:
		locator, _ := locatorFromArgs(args, "", false)
		err = service.Scroll(ctx, chat, locator, intArg(args, "x", 0), intArg(args, "y", 600))
		value = map[string]any{"locator": locator, "x": intArg(args, "x", 0), "y": intArg(args, "y", 600)}
	case tools.BrowserWait:
		wait := time.Duration(intArg(args, "timeout_ms", 30000)) * time.Millisecond
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if wait > 120*time.Second {
			wait = 120 * time.Second
		}
		waitCtx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		for {
			value, err = service.Evaluate(waitCtx, chat, fmt.Sprintf(`(document.body?.innerText || '').toLowerCase().includes(%s)`, jsonString(strings.ToLower(args["text"]))))
			if err == nil && value == "true" {
				value = map[string]string{"found": args["text"]}
				break
			}
			if waitCtx.Err() != nil {
				err = fmt.Errorf("timed out waiting for %q after %s", args["text"], wait)
				break
			}
			if err != nil {
				break
			}
			select {
			case <-waitCtx.Done():
				err = fmt.Errorf("timed out waiting for %q after %s", args["text"], wait)
			case <-time.After(100 * time.Millisecond):
			}
			if err != nil {
				break
			}
		}
	case tools.BrowserUpload:
		var paths []string
		_ = json.Unmarshal([]byte(args["paths"]), &paths)
		for index, path := range paths {
			abs, _, pathErr := tools.ResolvePath(opts.Runtime, path, accesssettings.AccessRead)
			if pathErr != nil {
				return tools.Result{}, pathErr
			}
			paths[index] = abs
		}
		locator, _ := locatorFromArgs(args, "", true)
		err = service.Upload(ctx, chat, locator, paths)
		value = map[string]any{"uploaded": len(paths)}
	case tools.BrowserEvaluate:
		value, err = service.Evaluate(ctx, chat, args["expression"])
	case tools.BrowserScreenshot:
		locator, _ := locatorFromArgs(args, "", false)
		binary, binaryErr := service.Screenshot(ctx, chat, locator, boolArg(args, "full_page"), args["format"], intArg(args, "quality", 90))
		return binaryResult(opts, t.id.String(), args["save_to_file"], binary, binaryErr)
	case tools.BrowserImage:
		locator, _ := locatorFromArgs(args, "", true)
		if locator.Selector == "" && locator.Role == "" {
			locator.Role = "image"
		}
		binary, binaryErr := service.Image(ctx, chat, locator)
		return binaryResult(opts, t.id.String(), args["save_to_file"], binary, binaryErr)
	case tools.BrowserPDF:
		binary, binaryErr := service.PDF(ctx, chat)
		return binaryResult(opts, t.id.String(), args["save_to_file"], binary, binaryErr)
	case tools.BrowserConsole:
		value, err = service.Console(ctx, chat, args["level"], intArg(args, "limit", 100))
	case tools.BrowserRequests:
		value, err = service.Requests(ctx, chat, intArg(args, "limit", 100))
	case tools.BrowserRequest:
		var records []browserapi.RequestRecord
		records, err = service.Requests(ctx, chat, 500)
		if err == nil {
			err = errors.New("browser request not found")
			for _, record := range records {
				if record.ID == args["request_id"] {
					value, err = record, nil
					break
				}
			}
		}
	case tools.BrowserResponseBody:
		binary, binaryErr := service.ResponseBody(ctx, chat, args["request_id"])
		return binaryResult(opts, t.id.String(), args["save_to_file"], binary, binaryErr)
	case tools.BrowserDownloadsOld:
		value, err = service.Downloads(ctx, chat)
	case tools.BrowserDownload:
		binary, binaryErr := service.Download(ctx, chat, args["download_id"])
		return binaryResult(opts, t.id.String(), args["save_to_file"], binary, binaryErr)
	}
	if err != nil {
		return tools.Result{}, err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return tools.Result{}, err
	}
	result.Text = string(data)
	result.Summary = t.title
	output := result.Text
	meta := map[string]string{}
	if path := args["save_to_file"]; path != "" {
		saved, saveErr := saveOutput(opts.Runtime, path, extractedData(t.id, value, data))
		if saveErr != nil {
			return tools.Result{}, saveErr
		}
		result.Path = saved
		result.Summary = fmt.Sprintf("%s saved to %s", t.title, saved)
		output = fmt.Sprintf("Saved to %s", saved)
		result.Text = output
		meta["path"] = saved
	}
	return tools.Result{Output: output, Meta: meta, Stored: result}, nil
}

func binaryResult(opts tools.Options, kind, path string, binary browserapi.Binary, err error) (tools.Result, error) {
	if err != nil {
		return tools.Result{}, err
	}
	saved := ""
	if strings.TrimSpace(path) != "" {
		var saveErr error
		saved, saveErr = saveOutput(opts.Runtime, path, binary.Data)
		if saveErr != nil {
			return tools.Result{}, saveErr
		}
		summary := fmt.Sprintf("Saved %s to %s (%s, %d bytes)", binary.Name, saved, binary.MIME, len(binary.Data))
		stored := tools.BrowserStoredResult{Kind: kind, SessionID: string(opts.Runtime.SessionID), Path: saved, Summary: summary, Text: summary}
		return tools.Result{Output: summary, Meta: map[string]string{"mime_type": binary.MIME, "path": saved}, Stored: stored}, nil
	}
	if opts.Runtime.Attachments == nil {
		return tools.Result{}, errors.New("attachment storage is unavailable")
	}
	meta, err := opts.Runtime.Attachments.ImportSessionData(opts.Runtime.SessionID, binary.Data, binary.Name, binary.MIME, attachment.SourceBrowser)
	if err != nil {
		return tools.Result{}, err
	}
	summary := fmt.Sprintf("Captured %s (%s, %d bytes)", binary.Name, binary.MIME, len(binary.Data))
	stored := tools.BrowserStoredResult{Kind: kind, SessionID: string(opts.Runtime.SessionID), Summary: summary, Text: summary, Attachment: &meta}
	return tools.Result{Output: summary, Meta: map[string]string{"attachment_id": meta.ID, "mime_type": meta.MIME}, Stored: stored}, nil
}

func extractedData(kind tools.ID, value any, fallback []byte) []byte {
	switch kind {
	case tools.BrowserSnapshot, tools.BrowserFind, tools.BrowserWait:
		if snapshot, ok := value.(browserapi.Snapshot); ok {
			return []byte(snapshot.Text)
		}
	case tools.BrowserEvaluate:
		if text, ok := value.(string); ok {
			return []byte(text)
		}
	}
	return fallback
}

func saveOutput(runtime tools.Runtime, path string, data []byte) (string, error) {
	abs, label, err := tools.ResolvePath(runtime, path, accesssettings.AccessWrite)
	if err != nil {
		return "", err
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(abs); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := tools.WriteFile(abs, data, mode); err != nil {
		return "", fmt.Errorf("save browser output to %s: %w", label, err)
	}
	return label, nil
}

func object(properties string) string {
	return `{"type":"object","properties":{` + properties + `},"additionalProperties":false}`
}

func required(schema string, names ...string) string {
	suffix, _ := json.Marshal(names)
	return strings.TrimSuffix(schema, "}") + `,"required":` + string(suffix) + `}`
}

func locatorObject(extra string, locatorRequired bool, requiredExtra ...string) string {
	properties := locatorProperties("")
	if extra = strings.Trim(extra, ","); extra != "" {
		properties += "," + extra
	}
	schema := object(properties)
	requiredNames := append([]string(nil), requiredExtra...)
	if locatorRequired {
		requiredNames = append(requiredNames, "target")
	}
	if len(requiredNames) > 0 {
		schema = required(schema, requiredNames...)
	}
	return schema
}

func dragLocatorObject() string {
	properties := locatorProperties("") + "," + locatorProperties("source")
	return required(object(properties), "source", "target")
}

func locatorProperties(prefix string) string {
	name := func(field string) string {
		if prefix == "" {
			return field
		}
		if field == "target" {
			return prefix
		}
		return prefix + "_" + field
	}
	return fmt.Sprintf(`%q:{"type":"string","description":"Accessible name, associated label, or visible text. For advanced targeting, use css= followed by a CSS selector or xpath= followed by an XPath expression."},%q:{"type":"string","description":"Optional semantic role such as button, textbox, link, checkbox, or image."},%q:{"type":"string","description":"Optional text or accessible label from the containing list item, row, option, article, section, form, dialog, or fieldset."},%q:{"type":"boolean","description":"Require an exact semantic name match. Defaults to false; ambiguous partial matches fail safely."},%q:{"type":"integer","minimum":1,"description":"One-based occurrence used only to disambiguate multiple matches."}`, name("target"), name("role"), name("within"), name("exact"), name("occurrence"))
}

func usesLocator(kind tools.ID) bool {
	switch kind {
	case tools.BrowserClick, tools.BrowserFill, tools.BrowserType, tools.BrowserPress, tools.BrowserSelect,
		tools.BrowserCheck, tools.BrowserUncheck, tools.BrowserHover, tools.BrowserScroll,
		tools.BrowserUpload, tools.BrowserScreenshot, tools.BrowserImage:
		return true
	default:
		return false
	}
}

func locatorFromArgs(args map[string]string, prefix string, required bool) (browserapi.Locator, error) {
	name := func(field string) string {
		if prefix == "" {
			return field
		}
		if field == "target" {
			return prefix
		}
		return prefix + "_" + field
	}
	locator := browserapi.Locator{
		Target:     strings.TrimSpace(args[name("target")]),
		Role:       strings.TrimSpace(args[name("role")]),
		Within:     strings.TrimSpace(args[name("within")]),
		Exact:      boolArgDefault(args, name("exact"), false),
		Occurrence: intArg(args, name("occurrence"), 0),
	}
	switch {
	case strings.HasPrefix(locator.Target, "css="):
		locator.Selector = strings.TrimSpace(strings.TrimPrefix(locator.Target, "css="))
		locator.Target = ""
		if locator.Selector == "" {
			return browserapi.Locator{}, errors.New("css target requires a selector")
		}
	case strings.HasPrefix(locator.Target, "xpath="):
		if strings.TrimSpace(strings.TrimPrefix(locator.Target, "xpath=")) == "" {
			return browserapi.Locator{}, errors.New("xpath target requires an expression")
		}
		locator.Selector = locator.Target
		locator.Target = ""
	}
	if required && locator.Empty() {
		return browserapi.Locator{}, errors.New("target is required")
	}
	if locator.Empty() && (locator.Role != "" || locator.Within != "" || locator.Occurrence > 0) {
		return browserapi.Locator{}, errors.New("role, within, and occurrence require target")
	}
	if locator.Occurrence < 0 {
		return browserapi.Locator{}, errors.New("occurrence must be one or greater")
	}
	return locator, nil
}

func requiredArgs(kind tools.ID) []string {
	switch kind {
	case tools.BrowserTabClaim, tools.BrowserTabSelect, tools.BrowserTabClose:
		return []string{"tab_id"}
	case tools.BrowserNavigate:
		return []string{"url"}
	case tools.BrowserFind:
		return []string{"query"}
	case tools.BrowserFill, tools.BrowserType, tools.BrowserSelect:
		return []string{"value"}
	case tools.BrowserPress:
		return []string{"key"}
	case tools.BrowserWait:
		return []string{"text"}
	case tools.BrowserUpload:
		return []string{"paths"}
	case tools.BrowserEvaluate:
		return []string{"expression"}
	case tools.BrowserRequest, tools.BrowserResponseBody:
		return []string{"request_id"}
	case tools.BrowserDownload:
		return []string{"download_id"}
	default:
		return nil
	}
}

func intArg(args map[string]string, key string, fallback int) int {
	value := strings.TrimSpace(args[key])
	if value == "" {
		return fallback
	}
	parsed, err := tools.ParseToolInt(value)
	if err != nil {
		return fallback
	}
	return parsed.Int()
}

func boolArg(args map[string]string, key string) bool {
	value, _ := strconv.ParseBool(args[key])
	return value
}

func boolArgDefault(args map[string]string, key string, fallback bool) bool {
	value := strings.TrimSpace(args[key])
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func jsonString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func permittedBrowserURL(runtime tools.Runtime, raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "file" {
		return raw, err
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return "", errors.New("file URL host must be empty or localhost")
	}
	abs, _, err := tools.ResolvePath(runtime, parsed.Path, accesssettings.AccessRead)
	if err != nil {
		return "", fmt.Errorf("resolve browser file URL: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("open browser file URL: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("browser file URL must reference a regular file")
	}
	return (&url.URL{Scheme: "file", Path: abs, Fragment: parsed.Fragment}).String(), nil
}
