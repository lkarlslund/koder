package phonetool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/tools"
)

const serviceKey = "phone_device"

// RuntimeService exposes an active phone provider to voice chats.
func RuntimeService(control phonedevice.Control) map[string]any {
	if control == nil {
		return nil
	}
	return map[string]any{serviceKey: control}
}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Use connected phone",
		Description: "Use capabilities explicitly enabled on the connected Android phone.",
		ExposeToLLM: true,
	})
	tools.Register(photoTool{kind: tools.PhonePhotosSearch, action: phonedevice.PhotosSearch}, tools.ToolSpec{
		Title: "Search phone photos", Description: "Search photo metadata on the connected phone without transferring image bytes.",
		Usage:      "Use capture-time bounds to find candidate photo IDs. This cannot recognize visual subjects; use phone_photos_thumbs to visually inspect a bounded range.",
		Parameters: `{"type":"object","properties":{"query":{"type":"string","description":"Optional file-name fragment"},"start_time":{"type":"string","description":"Inclusive RFC 3339 capture time"},"end_time":{"type":"string","description":"Exclusive RFC 3339 capture time"},"limit":{"type":"integer","minimum":1,"maximum":50}},"additionalProperties":false}`, ExposeToLLM: true,
	})
	tools.Register(photoTool{kind: tools.PhonePhotosThumbs, action: phonedevice.PhotosThumbs}, tools.ToolSpec{
		Title: "View phone photo thumbnails", Description: "Load a bounded range of phone photo thumbnails into Koder for visual inspection.",
		Usage:      "Use when the user describes a photo by what is visible rather than its file name. Narrow by capture time, request at most 12, then inspect the returned paths with view_image.",
		Parameters: `{"type":"object","properties":{"query":{"type":"string","description":"Optional file-name fragment"},"start_time":{"type":"string","description":"Inclusive RFC 3339 capture time"},"end_time":{"type":"string","description":"Exclusive RFC 3339 capture time"},"limit":{"type":"integer","minimum":1,"maximum":12}},"additionalProperties":false}`, ExposeToLLM: true,
	})
	tools.Register(photoTool{kind: tools.PhonePhotoView, action: phonedevice.PhotoView}, tools.ToolSpec{
		Title: "View phone photo", Description: "Load one selected phone photo at inspection resolution into Koder's temporary session storage.",
		Usage:      "Use a photo_id returned by phone_photos_search or phone_photos_thumbs, then inspect the returned path with view_image. This does not modify the project.",
		Parameters: `{"type":"object","properties":{"photo_id":{"type":"string"}},"required":["photo_id"],"additionalProperties":false}`, ExposeToLLM: true,
	})
	tools.Register(photoTool{kind: tools.PhonePhotoTransfer, action: phonedevice.PhotoTransfer}, tools.ToolSpec{
		Title: "Transfer phone photo", Description: "Copy one selected original phone photo into the current project or session workspace.",
		Usage:      "Use only after selecting a photo_id. Choose an explicit destination path, then use ordinary image or file tools to edit it. This is a filesystem write and follows the chat's permission policy.",
		Parameters: `{"type":"object","properties":{"photo_id":{"type":"string"},"path":{"type":"string","description":"Destination path in the current workspace"}},"required":["photo_id","path"],"additionalProperties":false}`, ExposeToLLM: true,
	})
}

type tool struct{}
type photoTool struct {
	kind   tools.ID
	action phonedevice.Action
}

func (tool) ID() tools.ID             { return tools.Phone }
func (tool) BypassesPermission() bool { return true }
func (tool) Preview(req tools.Request) string {
	return "Phone: " + strings.ReplaceAll(req.Args["action"], "_", " ")
}

func (tool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	if runtime.ChatRole != chatrole.Voice {
		return tools.ToolSpec{}, false
	}
	control, err := service(runtime)
	if err != nil {
		return tools.ToolSpec{}, false
	}
	capabilities := control.Capabilities()
	if len(capabilities) == 0 {
		return tools.ToolSpec{}, false
	}
	actions := make([]string, 0, len(capabilities))
	lines := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if isPhotoAction(capability.Action) {
			continue
		}
		action := string(capability.Action)
		actions = append(actions, action)
		confirmation := ""
		if capability.Confirmation {
			confirmation = " The phone asks the user to confirm."
		}
		visibility := " Reads phone data without opening another app."
		if capability.UserFacing {
			visibility = " USER-FACING ACTION: visibly or audibly affects the phone; call only when the user explicitly requested this exact action, never to gather knowledge."
		}
		lines = append(lines, fmt.Sprintf("%s: %s. Arguments: %s.%s%s", action, capability.Summary, capability.Arguments, visibility, confirmation))
	}
	if len(actions) == 0 {
		return tools.ToolSpec{}, false
	}
	slices.Sort(actions)
	schema, err := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":           map[string]any{"type": "string", "enum": actions},
			"query":            map[string]any{"type": "string"},
			"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
			"phone_number":     map[string]any{"type": "string"},
			"contact_name":     map[string]any{"type": "string"},
			"contact_id":       map[string]any{"type": "string"},
			"event_id":         map[string]any{"type": "string"},
			"operation":        map[string]any{"type": "string", "enum": []string{"edit", "cancel"}},
			"message":          map[string]any{"type": "string"},
			"to":               map[string]any{"type": "string"},
			"subject":          map[string]any{"type": "string"},
			"body":             map[string]any{"type": "string"},
			"name":             map[string]any{"type": "string"},
			"email":            map[string]any{"type": "string"},
			"title":            map[string]any{"type": "string"},
			"start_time":       map[string]any{"type": "string", "description": "RFC 3339 timestamp"},
			"end_time":         map[string]any{"type": "string", "description": "RFC 3339 timestamp"},
			"since_time":       map[string]any{"type": "string", "description": "RFC 3339 timestamp"},
			"location":         map[string]any{"type": "string"},
			"description":      map[string]any{"type": "string"},
			"address":          map[string]any{"type": "string"},
			"note":             map[string]any{"type": "string"},
			"latitude":         map[string]any{"type": "number"},
			"longitude":        map[string]any{"type": "number"},
			"hour":             map[string]any{"type": "integer", "minimum": 0, "maximum": 23},
			"minute":           map[string]any{"type": "integer", "minimum": 0, "maximum": 59},
			"duration_seconds": map[string]any{"type": "integer", "minimum": 1},
			"label":            map[string]any{"type": "string"},
			"text":             map[string]any{"type": "string"},
			"url":              map[string]any{"type": "string"},
			"app":              map[string]any{"type": "string"},
			"package_name":     map[string]any{"type": "string"},
			"media_action":     map[string]any{"type": "string", "enum": []string{"play", "pause", "toggle", "next", "previous"}},
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	})
	if err != nil {
		return tools.ToolSpec{}, false
	}
	spec.Usage = "Use only for an action on the currently connected phone. Actions marked USER-FACING ACTION open UI, cause sound, communicate, or change phone state. Call one only when the current user utterance explicitly requests that exact action; never call one to gain knowledge, perform research, or inspect its result. For current local information, first read get_location; if outside research is needed, use the standard research tools or coordinate a sibling chat with the resolved place name. Enabled actions:\n- " + strings.Join(lines, "\n- ")
	spec.Parameters = string(schema)
	return spec, true
}

func (t photoTool) ID() tools.ID             { return t.kind }
func (t photoTool) BypassesPermission() bool { return t.action != phonedevice.PhotoTransfer }
func (t photoTool) Preview(req tools.Request) string {
	if id := strings.TrimSpace(req.Args["photo_id"]); id != "" {
		return strings.ReplaceAll(t.kind.String(), "_", " ") + " " + id
	}
	return strings.ReplaceAll(t.kind.String(), "_", " ")
}
func (t photoTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	if runtime.ChatRole != chatrole.Voice {
		return tools.ToolSpec{}, false
	}
	control, err := service(runtime)
	if err != nil || !hasCapability(control.Capabilities(), t.action) {
		return tools.ToolSpec{}, false
	}
	return spec, true
}
func (t photoTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(args))
	for key, value := range args {
		if value = strings.TrimSpace(value); value != "" {
			out[strings.TrimSpace(key)] = value
		}
	}
	if (t.action == phonedevice.PhotoView || t.action == phonedevice.PhotoTransfer) && out["photo_id"] == "" {
		return nil, errors.New("photo_id is required")
	}
	if t.action == phonedevice.PhotoTransfer {
		out["path"] = tools.NormalizePathInput(out["path"])
		if out["path"] == "" {
			return nil, errors.New("path is required")
		}
	}
	return out, nil
}
func (t photoTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	control, err := service(opts.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	args := make(map[string]string, len(opts.Request.Args))
	for key, value := range opts.Request.Args {
		if key != "path" {
			args[key] = value
		}
	}
	result, err := control.Execute(ctx, t.action, args)
	if err != nil {
		return tools.Result{}, err
	}
	if t.action == phonedevice.PhotoTransfer {
		return transferPhoto(opts.Runtime, opts.Request.Args["path"], result)
	}
	return phoneResult(opts.Runtime, result)
}

func isPhotoAction(action phonedevice.Action) bool {
	switch action {
	case phonedevice.PhotosSearch, phonedevice.PhotosThumbs, phonedevice.PhotoView, phonedevice.PhotoTransfer:
		return true
	default:
		return false
	}
}

func hasCapability(capabilities []phonedevice.CatalogEntry, action phonedevice.Action) bool {
	return slices.ContainsFunc(capabilities, func(entry phonedevice.CatalogEntry) bool { return entry.Action == action })
}

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	action := strings.TrimSpace(args["action"])
	if action == "" {
		return nil, errors.New("action is required")
	}
	out := make(map[string]string, len(args))
	for key, value := range args {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	out["action"] = action
	return out, nil
}

func (tool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	control, err := service(opts.Runtime)
	if err != nil {
		return tools.Result{}, err
	}
	action := phonedevice.Action(opts.Request.Args["action"])
	args := make(map[string]string, len(opts.Request.Args)-1)
	for key, value := range opts.Request.Args {
		if key != "action" {
			args[key] = value
		}
	}
	result, err := control.Execute(ctx, action, args)
	if err != nil {
		return tools.Result{}, err
	}
	return phoneResult(opts.Runtime, result)
}

func phoneResult(runtime tools.Runtime, result phonedevice.Result) (tools.Result, error) {
	stored := map[string]any{"data": result.Data}
	output := strings.TrimSpace(result.Text)
	if len(result.Artifacts) != 0 {
		files, err := materializeArtifacts(runtime, result.Artifacts)
		if err != nil {
			return tools.Result{}, err
		}
		stored["artifacts"] = files
		for _, file := range files {
			output += fmt.Sprintf("\nPhone artifact %s copied to %s (%s). Use view_image to inspect images and show_media to present a result to the user.", file.ID, file.Path, file.MIMEType)
		}
	}
	return tools.Result{Output: strings.TrimSpace(output), Stored: stored}, nil
}

func transferPhoto(runtime tools.Runtime, requestedPath string, result phonedevice.Result) (tools.Result, error) {
	if len(result.Artifacts) != 1 {
		return tools.Result{}, errors.New("phone photo transfer must return exactly one image")
	}
	artifact := result.Artifacts[0]
	if attachment.ClassifyMIME(artifact.MIMEType) != attachment.KindImage {
		return tools.Result{}, fmt.Errorf("phone returned unsupported photo type %q", artifact.MIMEType)
	}
	abs, label, err := tools.ResolvePath(runtime, requestedPath, accesssettings.AccessWrite)
	if err != nil {
		return tools.Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return tools.Result{}, fmt.Errorf("create photo destination: %w", err)
	}
	if err := os.WriteFile(abs, artifact.Data, 0o644); err != nil {
		return tools.Result{}, fmt.Errorf("write transferred photo: %w", err)
	}
	stored := storedArtifact{ID: artifact.ID, Name: artifact.Name, Path: label, MIMEType: artifact.MIMEType, Size: len(artifact.Data)}
	return tools.Result{Output: "Transferred phone photo to " + label, Stored: stored}, nil
}

type storedArtifact struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	MIMEType string `json:"mime_type"`
	Size     int    `json:"size"`
}

func materializeArtifacts(runtime tools.Runtime, artifacts []phonedevice.Artifact) ([]storedArtifact, error) {
	root := runtime.SessionTmpDir()
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("phone artifacts require a persistent session")
	}
	directory := filepath.Join(root, "phone-artifacts", string(runtime.ChatID))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create phone artifact directory: %w", err)
	}
	files := make([]storedArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		name := filepath.Base(strings.TrimSpace(artifact.Name))
		if name == "" || name == "." {
			name = "phone-artifact" + extensionForMIME(artifact.MIMEType)
		}
		path := filepath.Join(directory, string(id.New())+extensionForMIME(artifact.MIMEType))
		if err := os.WriteFile(path, artifact.Data, 0o600); err != nil {
			return nil, fmt.Errorf("write phone artifact: %w", err)
		}
		files = append(files, storedArtifact{ID: artifact.ID, Name: name, Path: path, MIMEType: artifact.MIMEType, Size: len(artifact.Data)})
	}
	return files, nil
}

func extensionForMIME(mimeType string) string {
	exts, _ := mime.ExtensionsByType(strings.TrimSpace(mimeType))
	if len(exts) == 0 {
		return ""
	}
	return exts[0]
}

func service(runtime tools.Runtime) (phonedevice.Control, error) {
	control, err := tools.RequireService[phonedevice.Control](runtime, serviceKey)
	if err != nil {
		return nil, errors.New("connected phone is unavailable")
	}
	if resolver, ok := control.(interface {
		ForChat(string) phonedevice.Control
	}); ok {
		control = resolver.ForChat(string(runtime.ChatID))
	}
	return control, nil
}
