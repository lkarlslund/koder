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

const mediaParameters = `{"type":"object","properties":{"path":{"type":"string","description":"For one item: relative or absolute local media path, or HTTP(S) media URL"},"title":{"type":"string","description":"Optional title for one item, or collection heading when items is used"},"items":{"type":"array","minItems":1,"maxItems":20,"description":"For a collection: media items shown together by one tool call","items":{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute local media path, or HTTP(S) media URL"},"title":{"type":"string","description":"Optional item title"}},"required":["path"],"additionalProperties":false}}},"oneOf":[{"required":["path"]},{"required":["items"]}],"additionalProperties":false}`

type mediaTool struct{}
type legacyImageTool struct{}

func init() {
	tools.Register(mediaTool{}, tools.ToolSpec{
		Title:       "Show media",
		Description: "Show a local or remote image, audio, or video to the user and preserve it on the chat timeline.",
		Usage:       "Show one local file or HTTP(S) media URL with path, or show up to 20 together with items: [{path,title}]. Remote media is downloaded once and all media is copied into durable session attachment storage, so timeline playback does not depend on the original path or URL. This does not load media into model context. Playback requires user interaction and never autoplays. Use view_image when you need to inspect an image yourself.",
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
func (mediaTool) Preview(req tools.Request) string { return mediaPreview(req.Args) }
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
func (legacyImageTool) Preview(req tools.Request) string { return mediaPreview(req.Args) }
func (legacyImageTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	return call(ctx, opts, true)
}
func (legacyImageTool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Showed image", strings.TrimSpace(result.Output)
}

func normalizeArgs(args map[string]string) (map[string]string, error) {
	return tools.NormalizeMediaArgs(args)
}

func mediaPreview(args map[string]string) string {
	if path := strings.TrimSpace(args["path"]); path != "" {
		return path
	}
	if title := strings.TrimSpace(args["title"]); title != "" {
		return title
	}
	items, _ := tools.MediaInputs(args)
	return fmt.Sprintf("%d media items", len(items))
}

func call(ctx context.Context, opts tools.Options, imageOnly bool) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	if runtime.Attachments == nil || runtime.SessionID == "" {
		return tools.Result{}, errors.New("session attachment storage is unavailable")
	}
	inputs, err := tools.MediaInputs(req.Args)
	if err != nil {
		return tools.Result{}, err
	}
	if len(inputs) == 1 && strings.TrimSpace(req.Args["items"]) == "" {
		item, err := storeMedia(ctx, runtime, inputs[0], imageOnly)
		if err != nil {
			return tools.Result{}, err
		}
		return singleResult(item), nil
	}
	stored := tools.ShowMediaStoredResult{Title: strings.TrimSpace(req.Args["title"])}
	for index, input := range inputs {
		item, itemErr := storeMedia(ctx, runtime, input, imageOnly)
		if itemErr != nil {
			stored.Errors = append(stored.Errors, fmt.Sprintf("item %d (%s): %v", index+1, input.Path, itemErr))
			continue
		}
		stored.Items = append(stored.Items, item)
	}
	if len(stored.Items) == 0 {
		return tools.Result{}, fmt.Errorf("none of the %d media items could be shown: %s", len(inputs), strings.Join(stored.Errors, "; "))
	}
	stored.Summary = fmt.Sprintf("Showed %d media items", len(stored.Items))
	if len(stored.Errors) > 0 {
		stored.Summary += fmt.Sprintf("; skipped %d", len(stored.Errors))
	}
	meta := map[string]string{
		"media_count":   fmt.Sprintf("%d", len(stored.Items)),
		"skipped_count": fmt.Sprintf("%d", len(stored.Errors)),
	}
	return tools.Result{Output: stored.Summary, Meta: meta, Stored: stored}, nil
}

func storeMedia(ctx context.Context, runtime tools.Runtime, input tools.MediaInput, imageOnly bool) (tools.ShowMediaStoredItem, error) {
	if tools.IsHTTPURL(input.Path) {
		return storeRemoteMedia(ctx, runtime, input, imageOnly)
	}
	abs, rel, err := tools.ResolvePath(runtime, input.Path, accesssettings.AccessRead)
	if err != nil {
		return tools.ShowMediaStoredItem{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return tools.ShowMediaStoredItem{}, err
	}
	if info.IsDir() {
		return tools.ShowMediaStoredItem{}, fmt.Errorf("%s is a directory", rel)
	}
	mimeType, kind, err := detectMediaMIME(abs)
	if err != nil {
		return tools.ShowMediaStoredItem{}, err
	}
	if imageOnly && kind != attachment.KindImage {
		return tools.ShowMediaStoredItem{}, fmt.Errorf("unsupported image type %q", mimeType)
	}
	meta, err := runtime.Attachments.ImportSessionFile(runtime.SessionID, abs, mimeType, "show_media")
	if err != nil {
		return tools.ShowMediaStoredItem{}, err
	}
	meta.Path, meta.Original = "", ""
	return tools.ShowMediaStoredItem{Path: rel, MIMEType: mimeType, MediaKind: string(kind), Title: input.Title, SessionID: string(runtime.SessionID), Attachment: &meta}, nil
}

func storeRemoteMedia(ctx context.Context, runtime tools.Runtime, input tools.MediaInput, imageOnly bool) (tools.ShowMediaStoredItem, error) {
	remote, err := tools.FetchRemoteMedia(ctx, runtime.HTTPClient, input.Path)
	if err != nil {
		return tools.ShowMediaStoredItem{}, err
	}
	mimeType, kind, err := detectMediaData(remote.Data, remote.Name, remote.MIMEType)
	if err != nil {
		return tools.ShowMediaStoredItem{}, err
	}
	data := remote.Data
	if kind == attachment.KindImage {
		data, mimeType, err = attachment.PrepareImage(data)
		if err != nil {
			return tools.ShowMediaStoredItem{}, err
		}
	}
	if imageOnly && kind != attachment.KindImage {
		return tools.ShowMediaStoredItem{}, fmt.Errorf("unsupported image type %q", mimeType)
	}
	meta, err := runtime.Attachments.ImportSessionData(runtime.SessionID, data, remote.Name, mimeType, "show_media")
	if err != nil {
		return tools.ShowMediaStoredItem{}, err
	}
	meta.Path, meta.Original = "", ""
	return tools.ShowMediaStoredItem{Path: remote.URL, MIMEType: mimeType, MediaKind: string(kind), Title: input.Title, SessionID: string(runtime.SessionID), Attachment: &meta}, nil
}

func singleResult(item tools.ShowMediaStoredItem) tools.Result {
	label := "Showed " + item.MediaKind + " " + item.Path
	stored := tools.ShowMediaStoredResult{
		Path: item.Path, MIMEType: item.MIMEType, MediaKind: item.MediaKind, Title: item.Title,
		SessionID: item.SessionID, Attachment: item.Attachment, Summary: label,
	}
	return tools.Result{
		Output: label,
		Meta:   map[string]string{"path": item.Path, "mime_type": item.MIMEType, "media_kind": item.MediaKind, "title": item.Title, "attachment_id": item.Attachment.ID},
		Stored: stored,
	}
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
