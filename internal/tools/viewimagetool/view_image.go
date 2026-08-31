package viewimagetool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "View image",
		Description: "Load a local or remote image into model context and preserve it on the chat timeline.",
		Usage:       "Load an image into model context so you can inspect it visually. Use this instead of file_read for screenshots, photos, diagrams, or other images. Path may be a workspace path, an absolute local path, or an HTTP(S) URL. The validated image is copied into durable session attachment storage so it remains inspectable from the timeline. Optional detail may be original or omitted.",
		Parameters:  `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute local image path, or HTTP(S) image URL"},"detail":{"type":"string","description":"Optional detail level. Use original to preserve original resolution; omit for default resized behavior.","enum":["original"]}},"required":["path"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return tools.ViewImage }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return spec, runtime.SessionID != "" && runtime.Attachments != nil
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path, err := tools.NormalizePathOrHTTPURL(args["path"])
	if err != nil {
		return nil, err
	}
	out := map[string]string{"path": path}
	if detail := strings.TrimSpace(args["detail"]); detail != "" {
		if detail != "original" {
			return nil, errors.New("detail only supports original")
		}
		out["detail"] = detail
	}
	return out, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["path"] }
func (tool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	if runtime.Attachments == nil || runtime.SessionID == "" {
		return tools.Result{}, errors.New("session attachment storage is unavailable")
	}
	label, name := req.Args["path"], "remote-image"
	var data []byte
	var mimeType string
	var err error
	if tools.IsHTTPURL(req.Args["path"]) {
		remote, fetchErr := tools.FetchRemoteMedia(ctx, runtime.HTTPClient, req.Args["path"])
		if fetchErr != nil {
			return tools.Result{}, fetchErr
		}
		data, mimeType, err = attachment.PrepareImage(remote.Data)
		name = remote.Name
		label = remote.URL
	} else {
		abs, rel, resolveErr := tools.ResolvePath(runtime, req.Args["path"], accesssettings.AccessRead)
		if resolveErr != nil {
			return tools.Result{}, resolveErr
		}
		info, statErr := os.Stat(abs)
		if statErr != nil {
			return tools.Result{}, statErr
		}
		if info.IsDir() {
			return tools.Result{}, fmt.Errorf("%s is a directory", rel)
		}
		data, mimeType, err = attachment.LoadImage(abs)
		label, name = rel, filepath.Base(abs)
	}
	if err != nil {
		return tools.Result{}, err
	}
	meta, err := runtime.Attachments.ImportSessionData(runtime.SessionID, data, name, mimeType, "view_image")
	if err != nil {
		return tools.Result{}, err
	}
	meta.Path, meta.Original = "", ""
	summary := "Viewed image " + label
	if detail := strings.TrimSpace(req.Args["detail"]); detail == "original" {
		summary += " at original detail"
	}
	return tools.Result{
		Output: summary,
		Meta: map[string]string{
			"path":          label,
			"mime_type":     mimeType,
			"detail":        strings.TrimSpace(req.Args["detail"]),
			"attachment_id": meta.ID,
		},
		Stored: tools.ViewImageStoredResult{
			Path:       label,
			MIMEType:   mimeType,
			Detail:     strings.TrimSpace(req.Args["detail"]),
			SessionID:  string(runtime.SessionID),
			Attachment: &meta,
			Summary:    summary,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Viewed image", strings.TrimSpace(result.Output)
}
