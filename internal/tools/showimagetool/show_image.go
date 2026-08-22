package showimagetool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/tools"
)

const mediaParameters = `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute local media path to show to the user"},"title":{"type":"string","description":"Optional short title shown above the media"}},"required":["path"],"additionalProperties":false}`

type mediaTool struct{}
type legacyImageTool struct{}

func init() {
	tools.Register(mediaTool{}, tools.ToolSpec{
		Title:       "Show media",
		Description: "Show a local image, audio, or video file to the user.",
		Usage:       "Show a local image, audio, or video file in the browser UI to illustrate an explanation or result. This does not load media into model context. Playback always requires user interaction and never autoplays. Use view_image when you need to inspect an image yourself.",
		Parameters:  mediaParameters,
		ExposeToLLM: true,
		Legacy:      true,
	})
	tools.Register(legacyImageTool{}, tools.ToolSpec{
		Title:       "Show image (legacy)",
		Description: "Backward-compatible alias for old show_image calls.",
		Parameters:  mediaParameters,
		ExposeToLLM: false,
	})
}

func (mediaTool) ID() tools.ID             { return tools.ShowMedia }
func (mediaTool) BypassesPermission() bool { return false }
func (mediaTool) Definition(runtime tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return spec, runtime.SessionID != "" && runtime.ChatID != "" && runtime.Attachments != nil
}
func (mediaTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return normalizeArgs(args)
}
func (mediaTool) Preview(req tools.Request) string { return req.Args["path"] }
func (mediaTool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	return call(opts, false)
}
func (mediaTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Showed media", strings.TrimSpace(result.Output)
}

func (legacyImageTool) ID() tools.ID             { return tools.ShowImage }
func (legacyImageTool) BypassesPermission() bool { return false }
func (legacyImageTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return normalizeArgs(args)
}
func (legacyImageTool) Preview(req tools.Request) string { return req.Args["path"] }
func (legacyImageTool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	return call(opts, true)
}
func (legacyImageTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Showed image", strings.TrimSpace(result.Output)
}

func normalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(args["path"])
	if path == "" {
		return nil, errors.New("path is empty")
	}
	out := map[string]string{"path": path}
	if title := strings.TrimSpace(args["title"]); title != "" {
		out["title"] = title
	}
	return out, nil
}

func call(opts tools.Options, imageOnly bool) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	abs, rel, err := tools.ResolvePath(runtime, req.Args["path"], accesssettings.AccessRead)
	if err != nil {
		return tools.Result{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return tools.Result{}, err
	}
	if info.IsDir() {
		return tools.Result{}, fmt.Errorf("%s is a directory", rel)
	}
	mimeType, kind, err := detectMediaMIME(abs)
	if err != nil {
		return tools.Result{}, err
	}
	if imageOnly && kind != attachment.KindImage {
		return tools.Result{}, fmt.Errorf("unsupported image type %q", mimeType)
	}
	title := strings.TrimSpace(req.Args["title"])
	label := "Showed " + string(kind) + " " + rel
	stored := tools.ShowMediaStoredResult{Path: rel, SourcePath: abs, MIMEType: mimeType, MediaKind: string(kind), Title: title, Summary: label}
	if runtime.Attachments != nil && runtime.SessionID != "" {
		meta, importErr := runtime.Attachments.ImportSessionFile(runtime.SessionID, abs, mimeType, "show_media")
		if importErr != nil {
			return tools.Result{}, importErr
		}
		meta.Path = ""
		meta.Original = ""
		stored.SessionID = string(runtime.SessionID)
		stored.Attachment = &meta
		stored.SourcePath = ""
	}
	return tools.Result{
		Output: label,
		Meta:   map[string]string{"path": rel, "mime_type": mimeType, "media_kind": string(kind), "title": title},
		Stored: stored,
	}, nil
}

func detectMediaMIME(path string) (string, attachment.Kind, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", attachment.KindUnsupported, err
	}
	defer func() { _ = file.Close() }()
	var sniff [512]byte
	n, err := file.Read(sniff[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return "", attachment.KindUnsupported, err
	}
	mimeType := strings.TrimSpace(strings.Split(http.DetectContentType(sniff[:n]), ";")[0])
	if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); byExt != "" {
		byExt = strings.TrimSpace(strings.Split(byExt, ";")[0])
		detectedKind := attachment.ClassifyMIME(mimeType)
		extensionKind := attachment.ClassifyMIME(byExt)
		if (detectedKind != attachment.KindImage && detectedKind != attachment.KindAudio && detectedKind != attachment.KindVideo) &&
			(extensionKind == attachment.KindImage || extensionKind == attachment.KindAudio || extensionKind == attachment.KindVideo) {
			mimeType = byExt
		}
	}
	kind := attachment.ClassifyMIME(mimeType)
	if kind != attachment.KindImage && kind != attachment.KindAudio && kind != attachment.KindVideo {
		return "", kind, fmt.Errorf("unsupported media type %q", mimeType)
	}
	return mimeType, kind, nil
}
