package viewimagetool

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

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "View image",
		Description: "Load a local image file into model context.",
		Usage:       "Load a local image file into model context so you can inspect it visually. Use this instead of file_read for screenshots, photos, diagrams, or other image files. Path may be relative to the workspace or absolute. Optional detail may be set to original or omitted.",
		Parameters:  `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute local image path"},"detail":{"type":"string","description":"Optional detail level. Use original to preserve original resolution; omit for default resized behavior.","enum":["original"]}},"required":["path"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return tools.ViewImage }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(args["path"])
	if path == "" {
		return nil, errors.New("path is empty")
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
func (tool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	abs, rel, err := tools.ReadablePath(runtime.Workdir, req.Args["path"])
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
	mimeType, err := detectImageMIME(abs)
	if err != nil {
		return tools.Result{}, err
	}
	if attachment.ClassifyMIME(mimeType) != attachment.KindImage {
		return tools.Result{}, fmt.Errorf("%s is not an image", rel)
	}
	summary := "Viewed image " + rel
	if detail := strings.TrimSpace(req.Args["detail"]); detail == "original" {
		summary += " at original detail"
	}
	return tools.Result{
		Output: summary,
		Meta: map[string]string{
			"path":      rel,
			"mime_type": mimeType,
			"detail":    strings.TrimSpace(req.Args["detail"]),
		},
		Stored: tools.ViewImageStoredResult{
			Path:       rel,
			SourcePath: abs,
			MIMEType:   mimeType,
			Detail:     strings.TrimSpace(req.Args["detail"]),
			Summary:    summary,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "Viewed image", strings.TrimSpace(result.Output)
}

func detectImageMIME(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var sniff [512]byte
	n, err := file.Read(sniff[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	mimeType := http.DetectContentType(sniff[:n])
	if mimeType == "application/octet-stream" {
		if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); byExt != "" {
			mimeType = byExt
		}
	}
	if attachment.ClassifyMIME(mimeType) != attachment.KindImage {
		return "", fmt.Errorf("unsupported image type %q", mimeType)
	}
	return mimeType, nil
}
