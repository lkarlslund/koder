package viewpdftool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/tools"
)

const parameters = `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute local PDF path"},"page":{"type":"integer","description":"1-based page number to view; defaults to 1","minimum":1}},"required":["path"],"additionalProperties":false}`

type pdfBackend interface {
	Available() bool
	PageCount(context.Context, string) (int, error)
	RenderPage(context.Context, string, int, string) error
}

type tool struct{ backend pdfBackend }

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "View PDF",
		Description: "Render one page of a local PDF into model context.",
		Usage:       "Inspect a local PDF one page at a time. Page numbers are 1-based and page defaults to 1. The result reports the total page count; call again with another page when needed. Use this instead of file_read for PDFs.",
		Parameters:  parameters,
		ExposeToLLM: true,
	})
}

func (t tool) ID() tools.ID             { return tools.ViewPDF }
func (t tool) BypassesPermission() bool { return false }
func (t tool) Definition(_ tools.Runtime, spec tools.ToolSpec) (tools.ToolSpec, bool) {
	return spec, t.pdfBackend().Available()
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(args["path"])
	if path == "" {
		return nil, errors.New("path is empty")
	}
	page := 1
	if raw := strings.TrimSpace(args["page"]); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			return nil, errors.New("page must be a positive integer")
		}
		page = parsed
	}
	return map[string]string{"path": path, "page": strconv.Itoa(page)}, nil
}
func (tool) Preview(req tools.Request) string {
	return req.Args["path"] + " page " + req.Args["page"]
}
func (t tool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	abs, rel, err := tools.ResolvePath(runtime, req.Args["path"], accesssettings.AccessRead)
	if err != nil {
		return tools.Result{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return tools.Result{}, err
	}
	if !info.Mode().IsRegular() {
		return tools.Result{}, fmt.Errorf("%s is not a regular file", rel)
	}
	page, err := strconv.Atoi(req.Args["page"])
	if err != nil || page < 1 {
		return tools.Result{}, errors.New("page must be a positive integer")
	}
	backend := t.pdfBackend()
	if !backend.Available() {
		return tools.Result{}, errors.New("PDF rendering is unavailable; install poppler-utils")
	}
	pageCount, err := backend.PageCount(ctx, abs)
	if err != nil {
		return tools.Result{}, fmt.Errorf("inspect PDF %s: %w", rel, err)
	}
	if page > pageCount {
		return tools.Result{}, fmt.Errorf("page %d is out of range; %s has %d pages", page, rel, pageCount)
	}
	root := runtime.SessionTmpDir()
	if root == "" {
		root = filepath.Join(os.TempDir(), "koder-pdf-pages")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return tools.Result{}, fmt.Errorf("prepare PDF page storage: %w", err)
	}
	prefixFile, err := os.CreateTemp(root, "pdf-page-*")
	if err != nil {
		return tools.Result{}, fmt.Errorf("prepare PDF page: %w", err)
	}
	prefix := prefixFile.Name()
	if err := prefixFile.Close(); err != nil {
		return tools.Result{}, err
	}
	_ = os.Remove(prefix)
	pagePath := prefix + ".png"
	if err := backend.RenderPage(ctx, abs, page, prefix); err != nil {
		_ = os.Remove(pagePath)
		return tools.Result{}, fmt.Errorf("render PDF %s page %d: %w", rel, page, err)
	}
	data, mimeType, err := attachment.LoadImage(pagePath)
	if err != nil || len(data) == 0 {
		_ = os.Remove(pagePath)
		if err == nil {
			err = errors.New("renderer produced an empty image")
		}
		return tools.Result{}, fmt.Errorf("validate rendered PDF page: %w", err)
	}
	summary := fmt.Sprintf("Viewed PDF %s page %d of %d", rel, page, pageCount)
	return tools.Result{
		Output: summary,
		Meta: map[string]string{
			"path": rel, "page": strconv.Itoa(page), "page_count": strconv.Itoa(pageCount), "mime_type": mimeType,
		},
		Stored: tools.ViewImageStoredResult{
			Path: rel, SourcePath: pagePath, MIMEType: mimeType, Summary: summary, Page: page, PageCount: pageCount,
		},
	}, nil
}
func (tool) SummarizeResult(_ tools.Request, result tools.Result) (string, string) {
	return "Viewed PDF page", strings.TrimSpace(result.Output)
}
func (t tool) pdfBackend() pdfBackend {
	if t.backend != nil {
		return t.backend
	}
	return popplerBackend{}
}

type popplerBackend struct{}

func (popplerBackend) Available() bool {
	_, infoErr := exec.LookPath("pdfinfo")
	_, renderErr := exec.LookPath("pdftoppm")
	return infoErr == nil && renderErr == nil
}
func (popplerBackend) PageCount(ctx context.Context, path string) (int, error) {
	cmd := exec.CommandContext(ctx, "pdfinfo", path)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, commandError(err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "pages") {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && count > 0 {
			return count, nil
		}
	}
	return 0, errors.New("PDF page count is missing")
}
func (popplerBackend) RenderPage(ctx context.Context, path string, page int, prefix string) error {
	cmd := exec.CommandContext(ctx, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-singlefile", "-png", "-scale-to", "2048", path, prefix)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return commandError(err, output)
	}
	return nil
}
func commandError(err error, output []byte) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, message)
}
