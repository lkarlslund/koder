package readtool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindRead }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindRead, "Read a file or list a directory from the workspace", `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute workspace path to read"},"offset":{"type":"integer","description":"Optional starting line number for text files (1-indexed)"},"limit":{"type":"integer","description":"Optional maximum number of lines to return"}},"required":["path"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(tools.FirstArg(args, "path", "file", "file_path", "filepath"))
	if path == "" {
		return nil, errors.New("path is empty")
	}
	out := map[string]string{"path": path}
	if offset := tools.FirstArg(args, "offset", "start", "line"); offset != "" {
		out["offset"] = offset
	}
	if limit := tools.FirstArg(args, "limit", "lines", "max_lines"); limit != "" {
		out["limit"] = limit
	}
	return out, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"path": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["path"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: "Read file", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["path"])
}
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
		items, err := tools.ListDirectory(abs)
		if err != nil {
			return tools.Result{}, err
		}
		body := strings.Join(items, "\n")
		body, truncated := tools.TruncateText(body, tools.DefaultToolOutputLimit)
		storedEntries := append([]string(nil), items...)
		footer := ""
		if truncated {
			storedEntries, footer = splitDirectoryOutput(body)
		}
		return tools.Result{
			Output: body,
			Meta: map[string]string{
				"path":      rel,
				"mode":      "dir",
				"truncated": tools.BoolString(truncated),
			},
			Stored: tools.ReadStoredResult{
				Path:      rel,
				Mode:      tools.ReadStoredModeDirectory,
				Entries:   storedEntries,
				Footer:    footer,
				Truncated: truncated,
			},
		}, nil
	}
	header := make([]byte, 512)
	file, err := os.Open(abs)
	if err != nil {
		return tools.Result{}, err
	}
	n, readErr := file.Read(header)
	_ = file.Close()
	if readErr != nil {
		return tools.Result{}, readErr
	}
	contentType := http.DetectContentType(header[:n])
	if strings.HasPrefix(contentType, "image/") {
		return tools.Result{}, fmt.Errorf("%s is an image; image files are not readable as text", rel)
	}
	if contentType == "application/pdf" {
		return tools.Result{}, fmt.Errorf("%s is a PDF; PDFs are not readable via read", rel)
	}
	if strings.HasPrefix(contentType, "application/octet-stream") && !isTextHeader(header[:n]) {
		return tools.Result{}, fmt.Errorf("%s appears to be a binary file", rel)
	}
	text, truncated, err := tools.ReadTextFile(abs, tools.DefaultReadLineLimit, tools.DefaultReadByteLimit)
	if err != nil {
		return tools.Result{}, err
	}
	offset := parseOptionalInt(req.Args["offset"])
	limit := parseOptionalInt(req.Args["limit"])
	text = applyReadWindow(text, offset, limit)
	lines, footer := tools.ParseReadStoredLines(text)
	return tools.Result{
		Output: text,
		Meta: map[string]string{
			"path":      rel,
			"mode":      "file",
			"offset":    strings.TrimSpace(req.Args["offset"]),
			"limit":     strings.TrimSpace(req.Args["limit"]),
			"truncated": tools.BoolString(truncated),
		},
		Stored: tools.ReadStoredResult{
			Path:      rel,
			Mode:      tools.ReadStoredModeFile,
			Lines:     lines,
			Footer:    footer,
			Offset:    strings.TrimSpace(req.Args["offset"]),
			Limit:     strings.TrimSpace(req.Args["limit"]),
			Truncated: truncated,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func parseOptionalInt(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}

func applyReadWindow(text string, offset int, limit int) string {
	if offset <= 1 && limit <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	start := 0
	if offset > 1 {
		start = offset - 1
		if start > len(lines) {
			start = len(lines)
		}
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	return strings.Join(lines[start:end], "\n")
}

func isTextHeader(header []byte) bool {
	if len(header) == 0 {
		return true
	}
	return bytes.IndexByte(header, 0) == -1
}

func splitDirectoryOutput(body string) ([]string, string) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return nil, ""
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(last, "... truncated") {
		return lines, ""
	}
	return lines[:len(lines)-1], last
}
