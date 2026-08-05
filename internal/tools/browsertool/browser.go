package browsertool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

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
	{tools.BrowserNavigate, "Navigate browser", "Navigate the selected tab to an HTTP or HTTPS URL.", required(object(`"url":{"type":"string"},"wait_until":{"type":"string","enum":["domcontentloaded","load","networkidle"]}`), "url")},
	{tools.BrowserBack, "Browser back", "Navigate the selected tab back.", object(``)},
	{tools.BrowserForward, "Browser forward", "Navigate the selected tab forward.", object(``)},
	{tools.BrowserReload, "Reload browser", "Reload the selected tab.", object(``)},
	{tools.BrowserSnapshot, "Browser snapshot", "Return a compact visible DOM snapshot with ephemeral element refs.", object(`"depth":{"type":"integer"},"max_chars":{"type":"integer"},` + saveToFileProperty)},
	{tools.BrowserFind, "Find in browser", "Find visible page elements by text and return a fresh referenced snapshot.", required(object(`"query":{"type":"string"},"role":{"type":"string"},"max_chars":{"type":"integer"},`+saveToFileProperty), "query")},
	{tools.BrowserClick, "Click browser element", "Click an element ref from the latest snapshot.", refSchema(false)},
	{tools.BrowserFill, "Fill browser element", "Replace an input's value.", refValueSchema()},
	{tools.BrowserType, "Type in browser element", "Type text into an element.", refValueSchema()},
	{tools.BrowserPress, "Press browser key", "Send a key to an element.", refValueSchema()},
	{tools.BrowserSelect, "Select browser option", "Set a select element's value.", refValueSchema()},
	{tools.BrowserCheck, "Check browser element", "Check a checkbox or radio element.", refSchema(false)},
	{tools.BrowserUncheck, "Uncheck browser element", "Uncheck a checkbox element.", refSchema(false)},
	{tools.BrowserHover, "Hover browser element", "Hover an element.", refSchema(false)},
	{tools.BrowserDrag, "Drag browser element", "Drag one referenced element onto another.", required(object(`"source_ref":{"type":"string"},"target_ref":{"type":"string"}`), "source_ref", "target_ref")},
	{tools.BrowserScroll, "Scroll browser", "Scroll the selected page or a referenced element.", object(`"ref":{"type":"string"},"x":{"type":"integer"},"y":{"type":"integer"}`)},
	{tools.BrowserWait, "Wait in browser", "Wait for text to appear in the selected page.", required(object(`"text":{"type":"string"},"timeout_ms":{"type":"integer"},`+saveToFileProperty), "text")},
	{tools.BrowserUpload, "Upload browser files", "Upload workspace files through a referenced file input.", required(object(`"ref":{"type":"string"},"paths":{"type":"array","items":{"type":"string"}}`), "ref", "paths")},
	{tools.BrowserEvaluate, "Evaluate browser JavaScript", "Evaluate JavaScript in the selected tab and return bounded JSON.", required(object(`"expression":{"type":"string"},`+saveToFileProperty), "expression")},
	{tools.BrowserScreenshot, "Screenshot browser", "Capture the viewport, full page, or a referenced element. Return it as a session attachment unless save_to_file persists it to disk instead.", object(`"ref":{"type":"string"},"full_page":{"type":"boolean"},"format":{"type":"string","enum":["png","jpeg"]},"quality":{"type":"integer"},` + saveToFileProperty)},
	{tools.BrowserImage, "Capture browser image", "Capture a referenced image or canvas. Return it as a session attachment unless save_to_file persists it to disk instead.", required(object(`"ref":{"type":"string"},`+saveToFileProperty), "ref")},
	{tools.BrowserPDF, "Save browser PDF", "Print the selected page as a PDF. Return it as a session attachment unless save_to_file persists it to disk instead.", object(saveToFileProperty)},
	{tools.BrowserConsole, "Browser console", "Read bounded console records for the selected tab.", object(`"level":{"type":"string"},"limit":{"type":"integer"},` + saveToFileProperty)},
	{tools.BrowserRequests, "Browser requests", "List bounded network records for the selected tab.", object(`"limit":{"type":"integer"},` + saveToFileProperty)},
	{tools.BrowserRequest, "Browser request", "Inspect one opaque browser request record.", required(object(`"request_id":{"type":"string"},`+saveToFileProperty), "request_id")},
	{tools.BrowserResponseBody, "Browser response body", "Read a response body by opaque request ID.", required(object(`"request_id":{"type":"string"},`+saveToFileProperty), "request_id")},
	{tools.BrowserDownloads, "Browser downloads", "List downloads owned by this chat.", object(saveToFileProperty)},
	{tools.BrowserDownload, "Browser download", "Read a completed browser download. Return it as a session attachment unless save_to_file persists it to disk instead.", required(object(`"download_id":{"type":"string"},`+saveToFileProperty), "download_id")},
}

const saveToFileProperty = `"save_to_file":{"type":"string","description":"Optional destination file. Omit to return the extracted data to the model; set to persist it to disk instead."}`

func init() {
	for _, spec := range specs {
		tools.Register(spec, tools.ToolSpec{Title: spec.title, Description: spec.description, Usage: spec.description, Parameters: spec.parameters, ExposeToLLM: true})
	}
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
	if t.id == tools.BrowserNavigate || t.id == tools.BrowserTabNew {
		if raw := out["url"]; raw != "" {
			parsed, err := url.Parse(raw)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "about") {
				return nil, errors.New("url must use http, https, or about")
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
	for _, key := range []string{"url", "query", "ref", "tab_id", "expression", "request_id", "download_id"} {
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
	result := tools.BrowserStoredResult{Kind: t.id.String()}
	var value any
	var err error
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
		value, err = service.Snapshot(ctx, chat, "", intArg(args, "depth", 0), intArg(args, "max_chars", 32*1024))
	case tools.BrowserFind:
		value, err = service.Find(ctx, chat, args["query"], args["role"], intArg(args, "max_chars", 32*1024))
	case tools.BrowserClick, tools.BrowserFill, tools.BrowserType, tools.BrowserPress, tools.BrowserSelect, tools.BrowserCheck, tools.BrowserUncheck, tools.BrowserHover:
		action := strings.TrimPrefix(t.id.String(), "browser_")
		err = service.Interact(ctx, chat, action, args["ref"], args["value"])
		value = map[string]string{"action": action, "ref": args["ref"]}
	case tools.BrowserDrag:
		sourceSelector := fmt.Sprintf(`[data-koder-ref=%q]`, args["source_ref"])
		targetSelector := fmt.Sprintf(`[data-koder-ref=%q]`, args["target_ref"])
		expression := fmt.Sprintf(`(()=>{const a=document.querySelector(%s),b=document.querySelector(%s);if(!a||!b)throw new Error('stale element reference');const d=new DataTransfer();a.dispatchEvent(new DragEvent('dragstart',{bubbles:true,dataTransfer:d}));for(const t of ['dragenter','dragover','drop'])b.dispatchEvent(new DragEvent(t,{bubbles:true,cancelable:true,dataTransfer:d}));a.dispatchEvent(new DragEvent('dragend',{bubbles:true,dataTransfer:d}));return true})()`, jsonString(sourceSelector), jsonString(targetSelector))
		value, err = service.Evaluate(ctx, chat, expression)
	case tools.BrowserScroll:
		target := "window"
		if args["ref"] != "" {
			target = fmt.Sprintf(`document.querySelector(%s)`, jsonString(fmt.Sprintf(`[data-koder-ref=%q]`, args["ref"])))
		}
		value, err = service.Evaluate(ctx, chat, fmt.Sprintf(`(()=>{const e=%s;if(!e)throw new Error('stale element reference');e.scrollBy(%d,%d);return true})()`, target, intArg(args, "x", 0), intArg(args, "y", 600)))
	case tools.BrowserWait:
		wait := time.Duration(intArg(args, "timeout_ms", 30000)) * time.Millisecond
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		if wait > 120*time.Second {
			wait = 120 * time.Second
		}
		deadline := time.Now().Add(wait)
		for {
			value, err = service.Find(ctx, chat, args["text"], "", 8*1024)
			if err == nil && strings.TrimSpace(value.(browserapi.Snapshot).Text) != "" {
				break
			}
			if err != nil || time.Now().After(deadline) {
				if err == nil {
					err = fmt.Errorf("timed out waiting for %q", args["text"])
				}
				break
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
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
			abs, _, pathErr := tools.ReadablePath(opts.Runtime.Workdir, path)
			if pathErr != nil {
				return tools.Result{}, pathErr
			}
			paths[index] = abs
		}
		err = service.Upload(ctx, chat, args["ref"], paths)
		value = map[string]any{"uploaded": len(paths)}
	case tools.BrowserEvaluate:
		value, err = service.Evaluate(ctx, chat, args["expression"])
	case tools.BrowserScreenshot, tools.BrowserImage:
		ref := args["ref"]
		binary, binaryErr := service.Screenshot(ctx, chat, ref, boolArg(args, "full_page"), args["format"], intArg(args, "quality", 90))
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
	case tools.BrowserDownloads:
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
	abs, label, err := tools.WritablePath(runtime, path)
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

func refSchema(_ bool) string { return required(object(`"ref":{"type":"string"}`), "ref") }
func refValueSchema() string {
	return required(object(`"ref":{"type":"string"},"value":{"type":"string"}`), "ref", "value")
}

func requiredArgs(kind tools.ID) []string {
	switch kind {
	case tools.BrowserTabClaim, tools.BrowserTabSelect, tools.BrowserTabClose:
		return []string{"tab_id"}
	case tools.BrowserNavigate:
		return []string{"url"}
	case tools.BrowserFind:
		return []string{"query"}
	case tools.BrowserClick, tools.BrowserCheck, tools.BrowserUncheck, tools.BrowserHover, tools.BrowserImage:
		return []string{"ref"}
	case tools.BrowserFill, tools.BrowserType, tools.BrowserPress, tools.BrowserSelect:
		return []string{"ref", "value"}
	case tools.BrowserDrag:
		return []string{"source_ref", "target_ref"}
	case tools.BrowserWait:
		return []string{"text"}
	case tools.BrowserUpload:
		return []string{"ref", "paths"}
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
	value, err := strconv.Atoi(args[key])
	if err != nil {
		return fallback
	}
	return value
}

func boolArg(args map[string]string, key string) bool {
	value, _ := strconv.ParseBool(args[key])
	return value
}

func jsonString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
