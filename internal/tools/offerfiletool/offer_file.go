package offerfiletool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/offeredfile"
	"github.com/lkarlslund/koder/internal/tools"
)

const parameters = `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute local file path to offer for download"},"title":{"type":"string","description":"Optional short title shown on the download card"}},"required":["path"],"additionalProperties":false}`

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Offer file",
		Description: "Offer a live local file to the user as a download.",
		Usage:       "Create a download card for a local file that the user should receive. The link reads the original file when downloaded; it does not copy or snapshot the file, so later changes are reflected and moved or deleted files become unavailable.",
		Parameters:  parameters,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return tools.OfferFile }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
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
func (tool) Preview(req tools.Request) string { return req.Args["path"] }
func (tool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	if runtime.OfferedFiles == nil {
		return tools.Result{}, errors.New("offered file service is unavailable")
	}
	abs, _, err := tools.ResolvePath(runtime, req.Args["path"], accesssettings.AccessRead)
	if err != nil {
		return tools.Result{}, err
	}
	file, err := os.Open(abs)
	if err != nil {
		return tools.Result{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return tools.Result{}, err
	}
	if !info.Mode().IsRegular() {
		return tools.Result{}, fmt.Errorf("%s is not a regular file", req.Args["path"])
	}
	mimeType, err := mimetype.DetectReader(file)
	if err != nil {
		return tools.Result{}, fmt.Errorf("detect file type: %w", err)
	}
	record, err := runtime.OfferedFiles.Create(ctx, offeredfile.Record{
		SessionID: runtime.SessionID,
		ChatID:    runtime.ChatID,
		Path:      abs,
		Name:      info.Name(),
		MIME:      strings.TrimSpace(strings.Split(mimeType.String(), ";")[0]),
		Size:      info.Size(),
		Title:     req.Args["title"],
	})
	if err != nil {
		return tools.Result{}, err
	}
	summary := "Offered file " + record.Name
	stored := tools.OfferFileStoredResult{
		Token: record.Token, Name: record.Name, MIMEType: record.MIME, Size: record.Size, Title: record.Title, Summary: summary,
	}
	return tools.Result{
		Output: summary,
		Meta:   map[string]string{"name": record.Name, "mime_type": record.MIME, "title": record.Title},
		Stored: stored,
	}, nil
}

func (tool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Offered file", strings.TrimSpace(result.Output)
}
