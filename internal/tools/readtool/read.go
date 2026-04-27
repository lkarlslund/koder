package readtool

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

const (
	maxReadLineChars       = 2000
	maxReadLineTruncSuffix = "... (line truncated to 2000 chars)"
)

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindRead }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindRead, "Read a text file or list a directory from the workspace. Path may be relative to the workspace or absolute. File results are returned with 1-indexed line numbers. Use offset and limit together to read a later section of a large file. Prefer grep to find specific content before reading narrow slices, and avoid many tiny repeated reads. Directories return direct child entries. Images and PDFs are not supported by this tool.", `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute workspace path to a text file or directory"},"offset":{"type":"integer","description":"Optional starting line number for text files (1-indexed). Use with limit to read a later section."},"limit":{"type":"integer","description":"Optional maximum number of lines to return for text files"}},"required":["path"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(tools.FirstArg(args, "path", "file", "file_path", "filepath"))
	if path == "" {
		return nil, errors.New("path is empty")
	}
	out := map[string]string{"path": path}
	if offset := tools.FirstArg(args, "offset", "start", "line"); offset != "" {
		value, err := tools.ParseFlexibleInt(offset)
		if err != nil || value <= 0 {
			return nil, errors.New("offset must be a positive integer")
		}
		out["offset"] = strconv.Itoa(value)
	}
	if limit := tools.FirstArg(args, "limit", "lines", "max_lines"); limit != "" {
		value, err := tools.ParseFlexibleInt(limit)
		if err != nil || value <= 0 {
			return nil, errors.New("limit must be a positive integer")
		}
		out["limit"] = strconv.Itoa(value)
	}
	return out, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"path": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["path"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: readPresentationTitle(preview, "", ""), Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	path := strings.TrimSpace(req.Args["path"])
	offset := strings.TrimSpace(req.Args["offset"])
	limit := strings.TrimSpace(req.Args["limit"])
	return tools.Presentation{
		Title:   readPresentationTitle(path, offset, limit),
		Preview: path,
	}
}
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	abs, rel, err := tools.ReadablePath(runtime.Workdir, req.Args["path"])
	if err != nil {
		return tools.Result{}, err
	}
	autoCapped := strings.TrimSpace(req.Args["limit"]) == ""
	offset, err := parseOptionalInt(req.Args["offset"])
	if err != nil {
		return tools.Result{}, errors.New("offset must be a positive integer")
	}
	if offset <= 0 {
		offset = 1
	}
	limit, err := parseOptionalInt(req.Args["limit"])
	if err != nil {
		return tools.Result{}, errors.New("limit must be a positive integer")
	}
	if limit <= 0 {
		limit = tools.DefaultReadLineLimit
	}
	info, err := os.Stat(abs)
	if err != nil {
		return tools.Result{}, err
	}
	if info.IsDir() {
		page, err := readDirectoryPage(abs, offset, limit, tools.DefaultToolOutputLimit)
		if err != nil {
			return tools.Result{}, err
		}
		stored := tools.ReadStoredResult{
			Path:           rel,
			Mode:           tools.ReadStoredModeDirectory,
			Entries:        append([]string(nil), page.Entries...),
			Footer:         directoryReadFooter(page, limit, autoCapped),
			Offset:         strconv.Itoa(offset),
			Limit:          strconv.Itoa(limit),
			Start:          page.Start,
			End:            page.End,
			Total:          page.Total,
			NextOffset:     page.NextOffset,
			EffectiveLimit: limit,
			AutoCapped:     autoCapped,
			ByteCapped:     page.ByteCapped,
			HasMore:        page.HasMore,
			Truncated:      page.HasMore || page.ByteCapped,
		}
		body := tools.DisplayTextForStored(domain.ToolKindRead, stored)
		return tools.Result{
			Output: body,
			Meta: map[string]string{
				"path":        rel,
				"mode":        "dir",
				"offset":      strconv.Itoa(offset),
				"limit":       strconv.Itoa(limit),
				"total":       strconv.Itoa(page.Total),
				"start":       strconv.Itoa(page.Start),
				"end":         strconv.Itoa(page.End),
				"next_offset": strconv.Itoa(page.NextOffset),
				"truncated":   tools.BoolString(page.HasMore || page.ByteCapped),
				"byte_capped": tools.BoolString(page.ByteCapped),
			},
			Stored: stored,
		}, nil
	}
	header := make([]byte, 512)
	file, err := os.Open(abs)
	if err != nil {
		return tools.Result{}, err
	}
	n, readErr := file.Read(header)
	_ = file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
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
	page, err := readFilePage(abs, offset, limit, tools.DefaultReadByteLimit)
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.ReadStoredResult{
		Path:           rel,
		Mode:           tools.ReadStoredModeFile,
		Lines:          append([]tools.ReadStoredLine(nil), page.Lines...),
		Footer:         fileReadFooter(page, limit, autoCapped),
		Offset:         strconv.Itoa(offset),
		Limit:          strconv.Itoa(limit),
		Start:          page.Start,
		End:            page.End,
		Total:          page.Total,
		NextOffset:     page.NextOffset,
		EffectiveLimit: limit,
		AutoCapped:     autoCapped,
		ByteCapped:     page.ByteCapped,
		HasMore:        page.HasMore,
		Truncated:      page.HasMore || page.ByteCapped,
	}
	text := tools.DisplayTextForStored(domain.ToolKindRead, stored)
	return tools.Result{
		Output: text,
		Meta: map[string]string{
			"path":        rel,
			"mode":        "file",
			"offset":      strconv.Itoa(offset),
			"limit":       strconv.Itoa(limit),
			"total":       strconv.Itoa(page.Total),
			"start":       strconv.Itoa(page.Start),
			"end":         strconv.Itoa(page.End),
			"next_offset": strconv.Itoa(page.NextOffset),
			"truncated":   tools.BoolString(page.HasMore || page.ByteCapped),
			"byte_capped": tools.BoolString(page.ByteCapped),
		},
		Stored: stored,
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}

func parseOptionalInt(raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}
	return tools.ParseFlexibleInt(value)
}

func isTextHeader(header []byte) bool {
	if len(header) == 0 {
		return true
	}
	return bytes.IndexByte(header, 0) == -1
}

func readPresentationTitle(pathValue, offsetValue, limitValue string) string {
	title := "Read file"
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return title
	}
	title += " " + filepath.ToSlash(pathValue)
	if lineRange := readPresentationLineRange(offsetValue, limitValue); lineRange != "" {
		title += ", " + lineRange
	}
	return title
}

func readPresentationLineRange(offsetValue, limitValue string) string {
	offset, err := tools.ParseFlexibleInt(offsetValue)
	if err != nil || offset <= 0 {
		return ""
	}
	limit, err := tools.ParseFlexibleInt(limitValue)
	if err != nil || limit <= 0 {
		return ""
	}
	end := offset + limit - 1
	if end < offset {
		return ""
	}
	return fmt.Sprintf("lines %d-%d", offset, end)
}

type filePage struct {
	Lines      []tools.ReadStoredLine
	Start      int
	End        int
	Total      int
	NextOffset int
	HasMore    bool
	ByteCapped bool
}

type directoryPage struct {
	Entries    []string
	Start      int
	End        int
	Total      int
	NextOffset int
	HasMore    bool
	ByteCapped bool
}

func readFilePage(abs string, offset, limit, byteLimit int) (filePage, error) {
	file, err := os.Open(abs)
	if err != nil {
		return filePage{}, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	lines := make([]tools.ReadStoredLine, 0, limit)
	var (
		lineNo    int
		bytesUsed int
		pageCut   bool
	)
	start := max(1, offset)

	for {
		raw, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return filePage{}, err
		}
		if errors.Is(err, io.EOF) && raw == "" {
			break
		}
		lineNo++
		line := strings.TrimSuffix(raw, "\n")
		line = strings.TrimSuffix(line, "\r")
		if lineNo < start {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		if len(lines) >= limit {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		line = truncateReadLine(line)
		rendered := fmt.Sprintf("%d: %s", lineNo, line)
		addition := len(rendered)
		if len(lines) > 0 {
			addition++
		}
		if byteLimit > 0 && bytesUsed+addition > byteLimit {
			pageCut = true
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		lines = append(lines, tools.ReadStoredLine{Number: lineNo, Text: line})
		bytesUsed += addition
		if errors.Is(err, io.EOF) {
			break
		}
	}

	if lineNo < start && !(lineNo == 0 && start == 1) {
		return filePage{}, fmt.Errorf("offset %d is out of range for this file (%d lines)", start, lineNo)
	}
	page := filePage{
		Lines:      lines,
		Total:      lineNo,
		ByteCapped: pageCut,
	}
	if len(lines) > 0 {
		page.Start = lines[0].Number
		page.End = lines[len(lines)-1].Number
	}
	if pageCut || len(lines) >= limit {
		page.HasMore = page.End < page.Total
	}
	if page.HasMore && page.End > 0 {
		page.NextOffset = page.End + 1
	}
	return page, nil
}

func readDirectoryPage(abs string, offset, limit, byteLimit int) (directoryPage, error) {
	items, err := tools.ListDirectory(abs)
	if err != nil {
		return directoryPage{}, err
	}
	start := max(1, offset)
	total := len(items)
	if total < start && !(total == 0 && start == 1) {
		return directoryPage{}, fmt.Errorf("offset %d is out of range for this directory (%d entries)", start, total)
	}
	page := directoryPage{Total: total}
	if total == 0 {
		return page, nil
	}
	selected := make([]string, 0, limit)
	bytesUsed := 0
	for idx := start - 1; idx < total && len(selected) < limit; idx++ {
		entry := items[idx]
		addition := len(entry)
		if len(selected) > 0 {
			addition++
		}
		if byteLimit > 0 && bytesUsed+addition > byteLimit {
			page.ByteCapped = true
			break
		}
		selected = append(selected, entry)
		bytesUsed += addition
	}
	page.Entries = selected
	if len(selected) > 0 {
		page.Start = start
		page.End = start + len(selected) - 1
	}
	page.HasMore = page.End < total
	if page.HasMore && page.End > 0 {
		page.NextOffset = page.End + 1
	}
	return page, nil
}

func truncateReadLine(line string) string {
	if utf8.RuneCountInString(line) <= maxReadLineChars {
		return line
	}
	runes := []rune(line)
	return string(runes[:maxReadLineChars]) + maxReadLineTruncSuffix
}

func fileReadFooter(page filePage, limit int, autoCapped bool) string {
	return readPageFooter("lines", page.Start, page.End, page.Total, page.NextOffset, limit, page.HasMore, autoCapped, page.ByteCapped)
}

func directoryReadFooter(page directoryPage, limit int, autoCapped bool) string {
	return readPageFooter("entries", page.Start, page.End, page.Total, page.NextOffset, limit, page.HasMore, autoCapped, page.ByteCapped)
}

func readPageFooter(label string, start, end, total, nextOffset, limit int, hasMore, autoCapped, byteCapped bool) string {
	switch {
	case total == 0 && label == "lines":
		return "End of file - total 0 lines."
	case total == 0 && label == "entries":
		return "End of directory - total 0 entries."
	case byteCapped:
		return fmt.Sprintf("(showing %s %d-%d of %d, output capped at 64 KiB; use offset=%d limit=%d to continue)", label, start, end, total, nextOffset, limit)
	case hasMore && autoCapped:
		return fmt.Sprintf("(showing %s %d-%d of %d, auto-capped; use offset=%d limit=%d to continue)", label, start, end, total, nextOffset, limit)
	case hasMore:
		return fmt.Sprintf("(showing %s %d-%d of %d; use offset=%d limit=%d to continue)", label, start, end, total, nextOffset, limit)
	case label == "entries":
		return fmt.Sprintf("End of directory - total %d entries.", total)
	default:
		return fmt.Sprintf("End of file - total %d lines.", total)
	}
}
