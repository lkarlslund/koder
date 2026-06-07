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

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Show image",
		Description: "Show a local image file to the user.",
		Usage:       "Show a local image file to the user in the browser UI to illustrate an explanation or result. This does not load the image into model vision context. Path may be relative to the workspace or absolute. Use this only for local image files that should be displayed to the user; use view_image when you need to inspect the image yourself.",
		Parameters:  `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute local image path to show to the user"}},"required":["path"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return domain.ToolKindShowImage }
func (tool) BypassesPermission() bool { return false }

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(tools.FirstArg(args, "path", "file", "file_path", "filepath"))
	if path == "" {
		return nil, errors.New("path is empty")
	}
	return map[string]string{"path": path}, nil
}

func (tool) Preview(req tools.Request) string { return req.Args["path"] }

func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
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
	summary := "Showed image " + rel
	return tools.Result{
		Output: summary,
		Meta: map[string]string{
			"path":      rel,
			"mime_type": mimeType,
		},
		Stored: tools.ShowImageStoredResult{
			Path:       rel,
			SourcePath: abs,
			MIMEType:   mimeType,
			Summary:    summary,
		},
	}, nil
}

func (tool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Showed image", strings.TrimSpace(result.Output)
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
