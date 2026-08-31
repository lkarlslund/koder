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

const mediaParameters = `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute local media path, or HTTP(S) media URL"},"title":{"type":"string","description":"Optional short title shown above the media"}},"required":["path"],"additionalProperties":false}`

type mediaTool struct{}
type legacyImageTool struct{}

func init() {
	tools.Register(mediaTool{}, tools.ToolSpec{
		Title:       "Show media",
		Description: "Show a local or remote image, audio, or video to the user and preserve it on the chat timeline.",
		Usage:       "Show a local file or HTTP(S) media URL in the browser UI. Remote media is downloaded once and all media is copied into durable session attachment storage, so timeline playback does not depend on the original path or URL. This does not load media into model context. Playback requires user interaction and never autoplays. Use view_image when you need to inspect an image yourself.",
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
func (mediaTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	return call(ctx, opts, false)
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
func (legacyImageTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	return call(ctx, opts, true)
}
func (legacyImageTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Showed image", strings.TrimSpace(result.Output)
}

func normalizeArgs(args map[string]string) (map[string]string, error) {
	path, err := tools.NormalizePathOrHTTPURL(args["path"])
	if err != nil {
		return nil, err
	}
	out := map[string]string{"path": path}
	if title := strings.TrimSpace(args["title"]); title != "" {
		out["title"] = title
	}
	return out, nil
}

func call(ctx context.Context, opts tools.Options, imageOnly bool) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	if runtime.Attachments == nil || runtime.SessionID == "" {
		return tools.Result{}, errors.New("session attachment storage is unavailable")
	}
	if tools.IsHTTPURL(req.Args["path"]) {
		return callRemote(ctx, opts, imageOnly)
	}
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
	meta, importErr := runtime.Attachments.ImportSessionFile(runtime.SessionID, abs, mimeType, "show_media")
	if importErr != nil {
		return tools.Result{}, importErr
	}
	meta.Path, meta.Original = "", ""
	stored.SessionID = string(runtime.SessionID)
	stored.Attachment = &meta
	stored.SourcePath = ""
	return tools.Result{
		Output: label,
		Meta:   map[string]string{"path": rel, "mime_type": mimeType, "media_kind": string(kind), "title": title, "attachment_id": meta.ID},
		Stored: stored,
	}, nil
}

func callRemote(ctx context.Context, opts tools.Options, imageOnly bool) (tools.Result, error) {
	remote, err := tools.FetchRemoteMedia(ctx, opts.Runtime.HTTPClient, opts.Request.Args["path"])
	if err != nil {
		return tools.Result{}, err
	}
	mimeType, kind, err := detectMediaData(remote.Data, remote.Name, remote.MIMEType)
	if err != nil {
		return tools.Result{}, err
	}
	data := remote.Data
	if kind == attachment.KindImage {
		data, mimeType, err = attachment.PrepareImage(data)
		if err != nil {
			return tools.Result{}, err
		}
	}
	if imageOnly && kind != attachment.KindImage {
		return tools.Result{}, fmt.Errorf("unsupported image type %q", mimeType)
	}
	meta, err := opts.Runtime.Attachments.ImportSessionData(opts.Runtime.SessionID, data, remote.Name, mimeType, "show_media")
	if err != nil {
		return tools.Result{}, err
	}
	meta.Path, meta.Original = "", ""
	title := strings.TrimSpace(opts.Request.Args["title"])
	label := "Showed " + string(kind) + " " + remote.URL
	stored := tools.ShowMediaStoredResult{Path: remote.URL, MIMEType: mimeType, MediaKind: string(kind), Title: title, SessionID: string(opts.Runtime.SessionID), Attachment: &meta, Summary: label}
	return tools.Result{
		Output: label,
		Meta:   map[string]string{"path": remote.URL, "mime_type": mimeType, "media_kind": string(kind), "title": title, "attachment_id": meta.ID},
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
	return detectMediaData(sniff[:n], path, "")
}

func detectMediaData(data []byte, name, declaredMIME string) (string, attachment.Kind, error) {
	mimeType := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	if attachment.ClassifyMIME(mimeType) != attachment.KindImage && attachment.ClassifyMIME(mimeType) != attachment.KindAudio && attachment.ClassifyMIME(mimeType) != attachment.KindVideo {
		if declared := strings.TrimSpace(strings.Split(declaredMIME, ";")[0]); declared != "" {
			declaredKind := attachment.ClassifyMIME(declared)
			if declaredKind == attachment.KindImage || declaredKind == attachment.KindAudio || declaredKind == attachment.KindVideo {
				mimeType = declared
			}
		}
	}
	if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); byExt != "" {
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
