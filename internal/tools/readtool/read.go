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
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

const (
	maxReadLineChars       = 2000
	maxReadLineTruncSuffix = "... (line truncated to 2000 chars)"
)

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Read file",
		Description: "Read a text file or list a directory from the workspace.",
		Usage:       "Read a text file or list a directory from the workspace. File output is paginated and line-numbered. By default, read returns only the first page, not an entire large file. For large or known files, use start_line and end_line to read the smallest useful section. A single read returns at most 1000 lines, and reads over 100000 characters fail; use grep or code_search to locate relevant symbols before reading. If the result says more content exists, continue with the suggested start_line/end_line only when needed. Avoid repeated broad reads of the same unchanged file. Directories return direct child entries. Images and PDFs are not supported by this tool.",
		Parameters:  `{"type":"object","properties":{"path":{"type":"string","description":"Relative or absolute workspace path to a text file or directory"},"start_line":{"type":"integer","description":"Optional 1-based line number to start reading from. Defaults to 1.","minimum":1},"end_line":{"type":"integer","description":"Optional 1-based inclusive line number to stop reading at. Defaults to start_line + 999. At most 1000 lines are returned per read.","minimum":1}},"required":["path"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindRead }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	for _, key := range []string{"file", "file_path", "filepath", "start", "line", "offset", "end", "limit", "lines", "max_lines"} {
		if strings.TrimSpace(args[key]) != "" {
			return nil, fmt.Errorf("%s is no longer supported", key)
		}
	}
	path := tools.NormalizePathInput(tools.FirstArg(args, "path"))
	if path == "" {
		return nil, errors.New("path is empty")
	}
	out := map[string]string{"path": path}
	startLine, err := parsePositiveArg(tools.FirstArg(args, "start_line"), "start_line")
	if err != nil {
		return nil, err
	}
	if startLine > 0 {
		out["start_line"] = strconv.Itoa(startLine)
	}
	if endLine := tools.FirstArg(args, "end_line"); endLine != "" {
		value, err := tools.ParseFlexibleInt(endLine)
		if err != nil || value <= 0 {
			return nil, errors.New("end_line must be a positive integer")
		}
		if startLine > 0 && value < startLine {
			return nil, errors.New("end_line must be greater than or equal to start_line")
		}
		out["end_line"] = strconv.Itoa(value)
	}
	return out, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["path"] }
func (tool) Presentation(req tools.Request) tools.Presentation {
	path := strings.TrimSpace(req.Args["path"])
	startLine := strings.TrimSpace(req.Args["start_line"])
	endLine := strings.TrimSpace(req.Args["end_line"])
	return tools.Presentation{
		Title:   readPresentationTitle(path, startLine, endLine),
		Preview: path,
	}
}
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	abs, rel, err := tools.ReadablePath(runtime.Workdir, req.Args["path"])
	if err != nil {
		return tools.Result{}, err
	}
	readRange, err := readRangeFromArgs(req.Args)
	if err != nil {
		return tools.Result{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return tools.Result{}, err
	}
	if info.IsDir() {
		page, err := readDirectoryPage(abs, readRange.Start, readRange.Limit, tools.DefaultToolOutputLimit)
		if err != nil {
			return tools.Result{}, err
		}
		stored := tools.ReadStoredResult{
			Path:           rel,
			Mode:           tools.ReadStoredModeDirectory,
			Entries:        append([]string(nil), page.Entries...),
			Footer:         directoryReadFooter(page, readRange),
			StartLine:      strconv.Itoa(readRange.Start),
			EndLine:        strconv.Itoa(readRange.End),
			Offset:         strconv.Itoa(readRange.Start),
			Limit:          strconv.Itoa(readRange.Limit),
			Start:          page.Start,
			End:            page.End,
			Total:          page.Total,
			NextOffset:     page.NextOffset,
			NextStartLine:  page.NextOffset,
			EffectiveLimit: readRange.Limit,
			AutoCapped:     readRange.AutoCapped,
			RangeCapped:    readRange.RangeCapped,
			ByteCapped:     page.ByteCapped,
			HasMore:        page.HasMore,
			Truncated:      page.HasMore || page.ByteCapped,
		}
		body := tools.DisplayTextForStored(domain.ToolKindRead, stored)
		return tools.Result{
			Output: body,
			Meta: map[string]string{
				"path":            rel,
				"mode":            "dir",
				"start_line":      strconv.Itoa(readRange.Start),
				"end_line":        strconv.Itoa(readRange.End),
				"total":           strconv.Itoa(page.Total),
				"start":           strconv.Itoa(page.Start),
				"end":             strconv.Itoa(page.End),
				"next_start_line": strconv.Itoa(page.NextOffset),
				"truncated":       tools.BoolString(page.HasMore || page.ByteCapped),
				"byte_capped":     tools.BoolString(page.ByteCapped),
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
	if content, ok := fileContentForLanguageServer(abs); ok {
		runtime.TouchFile(ctx, rel, content)
	}
	page, err := readFilePage(abs, readRange.Start, readRange.Limit, 0)
	if err != nil {
		return tools.Result{}, err
	}
	stored := tools.ReadStoredResult{
		Path:           rel,
		Mode:           tools.ReadStoredModeFile,
		Lines:          append([]tools.ReadStoredLine(nil), page.Lines...),
		Footer:         fileReadFooter(page, readRange),
		StartLine:      strconv.Itoa(readRange.Start),
		EndLine:        strconv.Itoa(readRange.End),
		Offset:         strconv.Itoa(readRange.Start),
		Limit:          strconv.Itoa(readRange.Limit),
		Start:          page.Start,
		End:            page.End,
		Total:          page.Total,
		NextOffset:     page.NextOffset,
		NextStartLine:  page.NextOffset,
		EffectiveLimit: readRange.Limit,
		AutoCapped:     readRange.AutoCapped,
		RangeCapped:    readRange.RangeCapped,
		ByteCapped:     page.ByteCapped,
		HasMore:        page.HasMore,
		Truncated:      page.HasMore || page.ByteCapped,
	}
	text := tools.DisplayTextForStored(domain.ToolKindRead, stored)
	charCount := utf8.RuneCountInString(text)
	if charCount > tools.DefaultReadOutputCharLimit {
		return tools.Result{}, fmt.Errorf("read produced %d characters which exceeds the 100000 character limit; use start_line and end_line to read a smaller range", charCount)
	}
	return tools.Result{
		Output: text,
		Meta: map[string]string{
			"path":            rel,
			"mode":            "file",
			"start_line":      strconv.Itoa(readRange.Start),
			"end_line":        strconv.Itoa(readRange.End),
			"total":           strconv.Itoa(page.Total),
			"start":           strconv.Itoa(page.Start),
			"end":             strconv.Itoa(page.End),
			"next_start_line": strconv.Itoa(page.NextOffset),
			"truncated":       tools.BoolString(page.HasMore || page.ByteCapped),
			"byte_capped":     tools.BoolString(page.ByteCapped),
		},
		Stored: stored,
	}, nil
}

func fileContentForLanguageServer(path string) (string, bool) {
	const maxLSPWarmBytes = 2 * 1024 * 1024
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxLSPWarmBytes {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

type readRange struct {
	Start       int
	End         int
	Limit       int
	AutoCapped  bool
	RangeCapped bool
}

func readRangeFromArgs(args map[string]string) (readRange, error) {
	startLine, err := parsePositiveArg(args["start_line"], "start_line")
	if err != nil {
		return readRange{}, err
	}
	if startLine <= 0 {
		startLine = 1
	}
	endLine, err := parsePositiveArg(args["end_line"], "end_line")
	if err != nil {
		return readRange{}, err
	}
	autoCapped := endLine <= 0
	if endLine <= 0 {
		endLine = startLine + tools.DefaultReadLineLimit - 1
	}
	if endLine < startLine {
		return readRange{}, errors.New("end_line must be greater than or equal to start_line")
	}
	maxEnd := startLine + tools.DefaultReadLineLimit - 1
	rangeCapped := false
	if endLine > maxEnd {
		endLine = maxEnd
		rangeCapped = true
	}
	return readRange{
		Start:       startLine,
		End:         endLine,
		Limit:       endLine - startLine + 1,
		AutoCapped:  autoCapped,
		RangeCapped: rangeCapped,
	}, nil
}

func parsePositiveArg(raw, name string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, nil
	}
	parsed, err := tools.ParseFlexibleInt(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func isTextHeader(header []byte) bool {
	if len(header) == 0 {
		return true
	}
	return bytes.IndexByte(header, 0) == -1
}

func readPresentationTitle(pathValue, startLineValue, endLineValue string) string {
	title := "Read file"
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return title
	}
	title += " " + filepath.ToSlash(pathValue)
	if lineRange := readPresentationLineRange(startLineValue, endLineValue); lineRange != "" {
		title += ", " + lineRange
	}
	return title
}

func readPresentationLineRange(startLineValue, endLineValue string) string {
	startLine, err := tools.ParseFlexibleInt(startLineValue)
	if err != nil || startLine <= 0 {
		return ""
	}
	endLine, err := tools.ParseFlexibleInt(endLineValue)
	if err != nil || endLine <= 0 {
		return ""
	}
	if endLine < startLine {
		return ""
	}
	return fmt.Sprintf("lines %d-%d", startLine, endLine)
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

func readFilePage(abs string, startLine, limit, byteLimit int) (filePage, error) {
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
	start := max(1, startLine)

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
		return filePage{}, fmt.Errorf("start_line %d is out of range for this file (%d lines)", start, lineNo)
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

func readDirectoryPage(abs string, startLine, limit, byteLimit int) (directoryPage, error) {
	items, err := tools.ListDirectory(abs)
	if err != nil {
		return directoryPage{}, err
	}
	start := max(1, startLine)
	total := len(items)
	if total < start && !(total == 0 && start == 1) {
		return directoryPage{}, fmt.Errorf("start_line %d is out of range for this directory (%d entries)", start, total)
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

func fileReadFooter(page filePage, readRange readRange) string {
	return readPageFooter("lines", page.Start, page.End, page.Total, page.NextOffset, readRange, page.HasMore, page.ByteCapped)
}

func directoryReadFooter(page directoryPage, readRange readRange) string {
	return readPageFooter("entries", page.Start, page.End, page.Total, page.NextOffset, readRange, page.HasMore, page.ByteCapped)
}

func readPageFooter(label string, start, end, total, nextStartLine int, readRange readRange, hasMore, byteCapped bool) string {
	switch {
	case total == 0 && label == "lines":
		return "End of file - total 0 lines."
	case total == 0 && label == "entries":
		return "End of directory - total 0 entries."
	case byteCapped:
		return fmt.Sprintf("(showing %s %d-%d of %d, output capped; use start_line=%d end_line=%d only if you need the next section; prefer grep or a narrower range for specific code)", label, start, end, total, nextStartLine, nextStartLine+readRange.Limit-1)
	case hasMore && (readRange.AutoCapped || readRange.RangeCapped):
		return fmt.Sprintf("(showing %s %d-%d of %d, capped at 1000 lines; use start_line=%d end_line=%d only if you need the next section; prefer grep or a narrower range for specific code)", label, start, end, total, nextStartLine, nextStartLine+readRange.Limit-1)
	case hasMore:
		return fmt.Sprintf("(showing %s %d-%d of %d; use start_line=%d end_line=%d only if you need the next section; prefer grep or a narrower range for specific code)", label, start, end, total, nextStartLine, nextStartLine+readRange.Limit-1)
	case label == "entries":
		return fmt.Sprintf("End of directory - total %d entries.", total)
	default:
		return fmt.Sprintf("End of file - total %d lines.", total)
	}
}
