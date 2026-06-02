package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
)

type StoredResultStatus string

const (
	StoredResultStatusOK     StoredResultStatus = "ok"
	StoredResultStatusDenied StoredResultStatus = "denied"
	StoredResultStatusError  StoredResultStatus = "error"
)

type StoredResultPayload interface {
	storedResultPayload()
}

type storedResultEnvelope struct {
	Version  int                `json:"version"`
	PartKind domain.PartKind    `json:"part_kind"`
	Tool     domain.ToolKind    `json:"tool,omitempty"`
	Status   StoredResultStatus `json:"status"`
	Payload  json.RawMessage    `json:"payload,omitempty"`
}

type ReadStoredMode string

const (
	ReadStoredModeFile      ReadStoredMode = "file"
	ReadStoredModeDirectory ReadStoredMode = "dir"
)

type ReadStoredLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

type ReadStoredResult struct {
	Path           string           `json:"path"`
	Mode           ReadStoredMode   `json:"mode"`
	Lines          []ReadStoredLine `json:"lines,omitempty"`
	Entries        []string         `json:"entries,omitempty"`
	Footer         string           `json:"footer,omitempty"`
	StartLine      string           `json:"start_line,omitempty"`
	EndLine        string           `json:"end_line,omitempty"`
	Offset         string           `json:"offset,omitempty"`
	Limit          string           `json:"limit,omitempty"`
	Start          int              `json:"start,omitempty"`
	End            int              `json:"end,omitempty"`
	Total          int              `json:"total,omitempty"`
	NextStartLine  int              `json:"next_start_line,omitempty"`
	NextOffset     int              `json:"next_offset,omitempty"`
	EffectiveLimit int              `json:"effective_limit,omitempty"`
	AutoCapped     bool             `json:"auto_capped,omitempty"`
	RangeCapped    bool             `json:"range_capped,omitempty"`
	ByteCapped     bool             `json:"byte_capped,omitempty"`
	HasMore        bool             `json:"has_more,omitempty"`
	Truncated      bool             `json:"truncated,omitempty"`
}

type BashStoredResult struct {
	Command   string `json:"command"`
	Workdir   string `json:"workdir"`
	TimeoutMS int64  `json:"timeout_ms"`
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output,omitempty"`
}

type ExecStoredResult struct {
	ProcessID   string `json:"process_id"`
	Command     string `json:"command"`
	Workdir     string `json:"workdir,omitempty"`
	Shell       string `json:"shell,omitempty"`
	TTY         bool   `json:"tty,omitempty"`
	State       string `json:"state"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	TimeoutMS   int64  `json:"timeout_ms,omitempty"`
	Output      string `json:"output,omitempty"`
	OutputBytes int    `json:"output_bytes,omitempty"`
	StdinClosed bool   `json:"stdin_closed,omitempty"`
	Message     string `json:"message,omitempty"`
}

type ExecListStoredItem struct {
	ProcessID string `json:"process_id"`
	Command   string `json:"command"`
	State     string `json:"state"`
	TTY       bool   `json:"tty,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Output    string `json:"output,omitempty"`
}

type ExecListStoredResult struct {
	Scope   string               `json:"scope,omitempty"`
	Message string               `json:"message,omitempty"`
	Items   []ExecListStoredItem `json:"items,omitempty"`
}

type EditStoredResult struct {
	Path             string                 `json:"path"`
	ReplaceAll       bool                   `json:"replace_all,omitempty"`
	Occurrences      int                    `json:"occurrences,omitempty"`
	Summary          string                 `json:"summary,omitempty"`
	Matcher          string                 `json:"matcher,omitempty"`
	Verification     string                 `json:"verification,omitempty"`
	Diagnostics      string                 `json:"diagnostics,omitempty"`
	DiagnosticReport DiagnosticReportStored `json:"diagnostic_report,omitempty"`
	Diff             string                 `json:"diff,omitempty"`
	Hunks            []EditStoredHunk       `json:"hunks,omitempty"`
	Truncated        bool                   `json:"truncated,omitempty"`
}

type DiagnosticReportStored struct {
	Diagnostics []DiagnosticStored `json:"diagnostics,omitempty"`
	Skipped     []string           `json:"skipped,omitempty"`
}

type DiagnosticStored struct {
	Source   string `json:"source,omitempty"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
}

type EditStoredHunk struct {
	OldStart int      `json:"old_start"`
	NewStart int      `json:"new_start"`
	OldLines []string `json:"old_lines,omitempty"`
	NewLines []string `json:"new_lines,omitempty"`
}

type WriteStoredResult struct {
	Path             string                 `json:"path"`
	Action           string                 `json:"action,omitempty"`
	Summary          string                 `json:"summary,omitempty"`
	Content          string                 `json:"content,omitempty"`
	Diagnostics      string                 `json:"diagnostics,omitempty"`
	DiagnosticReport DiagnosticReportStored `json:"diagnostic_report,omitempty"`
	Truncated        bool                   `json:"truncated,omitempty"`
}

type LintStoredResult struct {
	Path             string                 `json:"path"`
	Mode             string                 `json:"mode,omitempty"`
	Summary          string                 `json:"summary,omitempty"`
	Diagnostics      string                 `json:"diagnostics,omitempty"`
	DiagnosticReport DiagnosticReportStored `json:"diagnostic_report,omitempty"`
}

type GlobStoredResult struct {
	Pattern   string   `json:"pattern"`
	BasePath  string   `json:"base_path,omitempty"`
	Matches   []string `json:"matches,omitempty"`
	Footer    string   `json:"footer,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
}

type GrepStoredResult struct {
	Pattern   string `json:"pattern"`
	BasePath  string `json:"base_path,omitempty"`
	Include   string `json:"include,omitempty"`
	Output    string `json:"output,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type QuestionStoredResult struct {
	Question string `json:"question"`
}

type TaskStoredResult struct {
	Body   string              `json:"body"`
	Status planning.TaskStatus `json:"status"`
}

type PlanStoredStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type UpdatePlanStoredResult struct {
	Explanation string           `json:"explanation,omitempty"`
	Steps       []PlanStoredStep `json:"steps"`
}

type MilestoneStoredItem struct {
	Ref         string `json:"ref"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Notes       string `json:"notes,omitempty"`
	OwnerChatID string `json:"owner_chat_id,omitempty"`
}

type MilestonePlanStoredResult struct {
	Summary    string                `json:"summary,omitempty"`
	Milestones []MilestoneStoredItem `json:"milestones,omitempty"`
}

type ChatStoredItem struct {
	ID                 id.ID  `json:"id"`
	Title              string `json:"title"`
	Role               string `json:"role,omitempty"`
	State              string `json:"state,omitempty"`
	Archived           bool   `json:"archived,omitempty"`
	ActiveMilestoneRef string `json:"active_milestone_ref,omitempty"`
	AssignedTodoRef    id.ID  `json:"assigned_todo_ref,omitempty"`
	StatusText         string `json:"status_text,omitempty"`
}

type ChatListStoredResult struct {
	Items []ChatStoredItem `json:"items,omitempty"`
}

type TodoStoredItem struct {
	ID      id.ID  `json:"id"`
	Content string `json:"content"`
	Note    string `json:"note,omitempty"`
	Status  string `json:"status"`
}

type TodoListStoredResult struct {
	MilestoneRef   string           `json:"milestone_ref,omitempty"`
	MilestoneTitle string           `json:"milestone_title,omitempty"`
	Message        string           `json:"message,omitempty"`
	Items          []TodoStoredItem `json:"items,omitempty"`
}

type SkillStoredResult struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type WebFetchStoredResult struct {
	URL         string `json:"url"`
	FinalURL    string `json:"final_url,omitempty"`
	Format      string `json:"format,omitempty"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

type WebSearchStoredItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type WebSearchStoredResult struct {
	Query          string                `json:"query"`
	AllowedDomains string                `json:"allowed_domains,omitempty"`
	BlockedDomains string                `json:"blocked_domains,omitempty"`
	Items          []WebSearchStoredItem `json:"items,omitempty"`
}

type ViewImageStoredResult struct {
	Path       string `json:"path"`
	SourcePath string `json:"source_path"`
	MIMEType   string `json:"mime_type"`
	Detail     string `json:"detail,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

type ShowImageStoredResult struct {
	Path       string `json:"path"`
	SourcePath string `json:"source_path"`
	MIMEType   string `json:"mime_type"`
	Summary    string `json:"summary,omitempty"`
}

type MCPStoredContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	URI      string `json:"uri,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

type MCPStoredResult struct {
	ServerID          string                 `json:"server_id"`
	ServerName        string                 `json:"server_name,omitempty"`
	ToolName          string                 `json:"tool_name"`
	StructuredContent string                 `json:"structured_content,omitempty"`
	IsError           bool                   `json:"is_error,omitempty"`
	Content           []MCPStoredContentItem `json:"content,omitempty"`
}

type DeniedStoredResult struct {
	Message string `json:"message"`
}

type ErrorStoredResult struct {
	Message string `json:"message"`
}

func (ReadStoredResult) storedResultPayload()          {}
func (BashStoredResult) storedResultPayload()          {}
func (ExecStoredResult) storedResultPayload()          {}
func (ExecListStoredResult) storedResultPayload()      {}
func (EditStoredResult) storedResultPayload()          {}
func (WriteStoredResult) storedResultPayload()         {}
func (LintStoredResult) storedResultPayload()          {}
func (GlobStoredResult) storedResultPayload()          {}
func (GrepStoredResult) storedResultPayload()          {}
func (QuestionStoredResult) storedResultPayload()      {}
func (TaskStoredResult) storedResultPayload()          {}
func (UpdatePlanStoredResult) storedResultPayload()    {}
func (MilestonePlanStoredResult) storedResultPayload() {}
func (ChatListStoredResult) storedResultPayload()      {}
func (TodoListStoredResult) storedResultPayload()      {}
func (SkillStoredResult) storedResultPayload()         {}
func (WebFetchStoredResult) storedResultPayload()      {}
func (WebSearchStoredResult) storedResultPayload()     {}
func (ViewImageStoredResult) storedResultPayload()     {}
func (ShowImageStoredResult) storedResultPayload()     {}
func (MCPStoredResult) storedResultPayload()           {}
func (DeniedStoredResult) storedResultPayload()        {}
func (ErrorStoredResult) storedResultPayload()         {}

func ModelTextForPart(part domain.Part, diff string) (string, bool) {
	env, ok := storedResultFromPart(part)
	if !ok {
		return "", false
	}
	text, ok := formatStoredResultForPart(env)
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	if shouldAppendDiffToModelText(env) && strings.TrimSpace(diff) != "" {
		text += "\n\nDiff:\n" + diff
	}
	return text, true
}

// CompactFormatLimits controls lossy text rendering for compaction prompts.
type CompactFormatLimits struct {
	MaxBytes      int
	HeadLines     int
	TailLines     int
	ReadMaxLines  int
	ExecHeadLines int
	ExecTailLines int
}

// DefaultCompactFormatLimits returns conservative limits for compaction input.
func DefaultCompactFormatLimits() CompactFormatLimits {
	return CompactFormatLimits{
		MaxBytes:      12 * 1024,
		HeadLines:     80,
		TailLines:     80,
		ReadMaxLines:  120,
		ExecHeadLines: 40,
		ExecTailLines: 80,
	}
}

// CompactModelTextForPart returns a bounded text-only rendering of a stored result for compaction.
func CompactModelTextForPart(part domain.Part, diff string, limits CompactFormatLimits) (string, bool) {
	env, ok := storedResultFromPart(part)
	if !ok {
		return "", false
	}
	text, ok := compactStoredResultForPart(env, diff, normalizeCompactFormatLimits(limits))
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

func normalizeCompactFormatLimits(limits CompactFormatLimits) CompactFormatLimits {
	defaults := DefaultCompactFormatLimits()
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.HeadLines <= 0 {
		limits.HeadLines = defaults.HeadLines
	}
	if limits.TailLines <= 0 {
		limits.TailLines = defaults.TailLines
	}
	if limits.ReadMaxLines <= 0 {
		limits.ReadMaxLines = defaults.ReadMaxLines
	}
	if limits.ExecHeadLines <= 0 {
		limits.ExecHeadLines = defaults.ExecHeadLines
	}
	if limits.ExecTailLines <= 0 {
		limits.ExecTailLines = defaults.ExecTailLines
	}
	return limits
}

func compactStoredResultForPart(env storedResultEnvelope, diff string, limits CompactFormatLimits) (string, bool) {
	if env.PartKind != domain.PartKindToolOutput {
		text, ok := formatStoredResultForPart(env)
		return compactTextForCompaction(text, limits.HeadLines, limits.TailLines, limits.MaxBytes, "stored result"), ok
	}
	if env.Status == StoredResultStatusDenied || env.Status == StoredResultStatusError {
		text, ok := formatStoredToolOutput(env)
		return compactTextForCompaction(text, limits.HeadLines, limits.TailLines, limits.MaxBytes, env.Tool.String()+" result"), ok
	}
	switch env.Tool {
	case domain.ToolKindFileRead:
		return decodeAndFormat[ReadStoredResult](env.Payload, func(result ReadStoredResult) string {
			return compactReadStoredResult(result, limits)
		})
	case domain.ToolKindBash:
		return decodeAndFormat[BashStoredResult](env.Payload, func(result BashStoredResult) string {
			return compactBashStoredResult(result, limits)
		})
	case domain.ToolKindExecCommand, domain.ToolKindExecStatus, domain.ToolKindExecWriteStdin, domain.ToolKindExecResize, domain.ToolKindExecTerminate:
		return decodeAndFormat[ExecStoredResult](env.Payload, func(result ExecStoredResult) string {
			return compactExecStoredResult(result, limits)
		})
	case domain.ToolKindViewImage:
		return decodeAndFormat[ViewImageStoredResult](env.Payload, compactViewImageStoredResult)
	case domain.ToolKindShowImage:
		return decodeAndFormat[ShowImageStoredResult](env.Payload, compactShowImageStoredResult)
	case domain.ToolKindFileEdit, domain.ToolKindFileWrite, domain.ToolKindLint:
		text, ok := formatStoredToolOutput(env)
		if !ok {
			return "", false
		}
		return compactTextForCompaction(text, limits.HeadLines, limits.TailLines, limits.MaxBytes, env.Tool.String()+" result"), true
	default:
		text, ok := formatStoredToolOutput(env)
		if !ok {
			return "", false
		}
		if shouldAppendDiffToModelText(env) && strings.TrimSpace(diff) != "" {
			text += "\n\nDiff:\n" + diff
		}
		return compactTextForCompaction(text, limits.HeadLines, limits.TailLines, limits.MaxBytes, env.Tool.String()+" result"), true
	}
}

func compactReadStoredResult(result ReadStoredResult, limits CompactFormatLimits) string {
	lines := make([]string, 0, min(len(result.Lines), limits.ReadMaxLines)+5)
	if path := strings.TrimSpace(result.Path); path != "" {
		lines = append(lines, "path: "+path)
	}
	if result.Mode != "" {
		lines = append(lines, "mode: "+string(result.Mode))
	}
	if result.Start > 0 || result.End > 0 || result.Total > 0 {
		lines = append(lines, fmt.Sprintf("range: %d-%d of %d", result.Start, result.End, result.Total))
	}
	switch result.Mode {
	case ReadStoredModeDirectory:
		for _, entry := range result.Entries {
			lines = append(lines, entry)
		}
	default:
		for _, line := range result.Lines {
			lines = append(lines, strconv.Itoa(line.Number)+": "+line.Text)
		}
	}
	if footer := strings.TrimSpace(result.Footer); footer != "" {
		lines = append(lines, footer)
	} else if footer := readStoredFooter(result); footer != "" {
		lines = append(lines, footer)
	}
	return compactTextForCompaction(strings.Join(lines, "\n"), limits.ReadMaxLines, 0, limits.MaxBytes, "read result")
}

func compactBashStoredResult(result BashStoredResult, limits CompactFormatLimits) string {
	lines := make([]string, 0, 5)
	if command := strings.TrimSpace(result.Command); command != "" {
		lines = append(lines, "command: "+command)
	}
	if workdir := strings.TrimSpace(result.Workdir); workdir != "" {
		lines = append(lines, "workdir: "+workdir)
	}
	if result.TimeoutMS > 0 {
		lines = append(lines, fmt.Sprintf("timeout_ms: %d", result.TimeoutMS))
	}
	lines = append(lines, fmt.Sprintf("exit_code: %d", result.ExitCode))
	if output := strings.TrimSpace(result.Output); output != "" {
		lines = append(lines, "output:")
		lines = append(lines, compactTextForCompaction(output, limits.ExecHeadLines, limits.ExecTailLines, limits.MaxBytes, "bash output"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func compactExecStoredResult(result ExecStoredResult, limits CompactFormatLimits) string {
	lines := make([]string, 0, 8)
	if msg := strings.TrimSpace(result.Message); msg != "" {
		lines = append(lines, msg)
	}
	if id := strings.TrimSpace(result.ProcessID); id != "" {
		lines = append(lines, "process_id: "+id)
	}
	if command := strings.TrimSpace(result.Command); command != "" {
		lines = append(lines, "command: "+command)
	}
	if workdir := strings.TrimSpace(result.Workdir); workdir != "" {
		lines = append(lines, "workdir: "+workdir)
	}
	if state := strings.TrimSpace(result.State); state != "" {
		lines = append(lines, "state: "+state)
	}
	if result.ExitCode != nil {
		lines = append(lines, fmt.Sprintf("exit_code: %d", *result.ExitCode))
	}
	if result.OutputBytes > 0 {
		lines = append(lines, fmt.Sprintf("output_bytes: %d", result.OutputBytes))
	}
	if output := strings.TrimSpace(result.Output); output != "" {
		lines = append(lines, "output:")
		lines = append(lines, compactTextForCompaction(output, limits.ExecHeadLines, limits.ExecTailLines, limits.MaxBytes, "exec output"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func compactViewImageStoredResult(result ViewImageStoredResult) string {
	lines := []string{"Viewed image; image bytes omitted for text-only compaction."}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		lines = append(lines, "summary: "+summary)
	}
	if path := strings.TrimSpace(result.Path); path != "" {
		lines = append(lines, "path: "+path)
	}
	if source := strings.TrimSpace(result.SourcePath); source != "" {
		lines = append(lines, "source_path: "+source)
	}
	if mime := strings.TrimSpace(result.MIMEType); mime != "" {
		lines = append(lines, "mime: "+mime)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func compactShowImageStoredResult(result ShowImageStoredResult) string {
	lines := []string{"Showed image; image bytes omitted for text-only compaction."}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		lines = append(lines, "summary: "+summary)
	}
	if path := strings.TrimSpace(result.Path); path != "" {
		lines = append(lines, "path: "+path)
	}
	if source := strings.TrimSpace(result.SourcePath); source != "" {
		lines = append(lines, "source_path: "+source)
	}
	if mime := strings.TrimSpace(result.MIMEType); mime != "" {
		lines = append(lines, "mime: "+mime)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func compactTextForCompaction(text string, headLines int, tailLines int, maxBytes int, label string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len([]byte(text)) <= maxBytes && (tailLines <= 0 || len(lines) <= headLines+tailLines) && (tailLines > 0 || len(lines) <= headLines) {
		return text
	}
	if tailLines <= 0 {
		kept := min(len(lines), headLines)
		out := strings.Join(lines[:kept], "\n")
		out += fmt.Sprintf("\n[%s truncated for compaction: kept first %d of %d lines]", label, kept, len(lines))
		return trimBytesForCompaction(out, maxBytes, label)
	}
	if len(lines) <= headLines+tailLines {
		return trimBytesForCompaction(text, maxBytes, label)
	}
	out := strings.Join(lines[:headLines], "\n")
	out += fmt.Sprintf("\n[%s truncated for compaction: kept first %d and last %d lines of %d lines]\n", label, headLines, tailLines, len(lines))
	out += strings.Join(lines[len(lines)-tailLines:], "\n")
	return trimBytesForCompaction(out, maxBytes, label)
}

func trimBytesForCompaction(text string, maxBytes int, label string) string {
	if maxBytes <= 0 || len([]byte(text)) <= maxBytes {
		return text
	}
	data := []byte(text)
	if maxBytes < 256 {
		maxBytes = 256
	}
	suffix := []byte(fmt.Sprintf("\n[%s truncated for compaction: kept %d bytes]", label, maxBytes))
	limit := maxBytes - len(suffix)
	if limit < 1 {
		limit = maxBytes
	}
	return strings.TrimSpace(string(data[:limit])) + string(suffix)
}

func shouldAppendDiffToModelText(env storedResultEnvelope) bool {
	if env.PartKind != domain.PartKindToolOutput {
		return false
	}
	switch env.Tool {
	case domain.ToolKindFileEdit, domain.ToolKindFileWrite:
		return false
	default:
		return true
	}
}

func DisplayTextForPart(part domain.Part) (string, bool) {
	env, ok := storedResultFromPart(part)
	if !ok {
		return "", false
	}
	text, ok := formatStoredResultForDisplay(env)
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

func StoredResultInfoForPart(part domain.Part) (domain.ToolKind, StoredResultStatus, bool) {
	env, ok := storedResultFromPart(part)
	if !ok {
		return 0, "", false
	}
	return env.Tool, env.Status, true
}

func DisplayTextForStored(tool domain.ToolKind, payload any) string {
	raw, err := marshalStoredResult(domain.PartKindToolOutput, tool, StoredResultStatusOK, payload)
	if err != nil {
		return ""
	}
	var env storedResultEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return ""
	}
	text, _ := formatStoredResultForDisplay(env)
	return text
}

func ViewImageStoredResultForPart(part domain.Part) (ViewImageStoredResult, bool) {
	env, ok := storedResultFromPart(part)
	if !ok || env.PartKind != domain.PartKindToolOutput || env.Tool != domain.ToolKindViewImage {
		return ViewImageStoredResult{}, false
	}
	var result ViewImageStoredResult
	if err := json.Unmarshal(env.Payload, &result); err != nil {
		return ViewImageStoredResult{}, false
	}
	return result, true
}

func ShowImageStoredResultForPart(part domain.Part) (ShowImageStoredResult, bool) {
	env, ok := storedResultFromPart(part)
	if !ok || env.PartKind != domain.PartKindToolOutput || env.Tool != domain.ToolKindShowImage {
		return ShowImageStoredResult{}, false
	}
	var result ShowImageStoredResult
	if err := json.Unmarshal(env.Payload, &result); err != nil {
		return ShowImageStoredResult{}, false
	}
	return result, true
}

func EditStoredResultForPart(part domain.Part) (EditStoredResult, bool) {
	env, ok := storedResultFromPart(part)
	if !ok || env.PartKind != domain.PartKindToolOutput || env.Tool != domain.ToolKindFileEdit {
		return EditStoredResult{}, false
	}
	var result EditStoredResult
	if err := json.Unmarshal(env.Payload, &result); err != nil {
		return EditStoredResult{}, false
	}
	return result, true
}

func marshalStoredResult(partKind domain.PartKind, tool domain.ToolKind, status StoredResultStatus, payload any) (string, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	rawEnvelope, err := json.Marshal(storedResultEnvelope{
		Version:  2,
		PartKind: partKind,
		Tool:     tool,
		Status:   status,
		Payload:  rawPayload,
	})
	if err != nil {
		return "", err
	}
	return string(rawEnvelope), nil
}

func storedResultFromPart(part domain.Part) (storedResultEnvelope, bool) {
	switch payload := part.Payload.(type) {
	case domain.ToolOutputPayload:
		raw, err := json.Marshal(payload.Result)
		if err != nil {
			return storedResultEnvelope{}, false
		}
		return storedResultEnvelope{
			Version:  2,
			PartKind: domain.PartKindToolOutput,
			Tool:     payload.Tool,
			Status:   StoredResultStatus(payload.Status),
			Payload:  raw,
		}, true
	case domain.TaskUpdatePayload:
		raw, err := json.Marshal(TaskStoredResult{Body: payload.Body, Status: planning.TaskStatus(payload.Status)})
		if err != nil {
			return storedResultEnvelope{}, false
		}
		return storedResultEnvelope{
			Version:  2,
			PartKind: domain.PartKindTaskUpdate,
			Tool:     domain.ToolKindTask,
			Status:   StoredResultStatusOK,
			Payload:  raw,
		}, true
	case domain.PlanUpdatePayload:
		steps := make([]PlanStoredStep, 0, len(payload.Steps))
		for _, step := range payload.Steps {
			steps = append(steps, PlanStoredStep{Step: step.Step, Status: step.Status})
		}
		raw, err := json.Marshal(UpdatePlanStoredResult{Explanation: payload.Explanation, Steps: steps})
		if err != nil {
			return storedResultEnvelope{}, false
		}
		return storedResultEnvelope{
			Version:  2,
			PartKind: domain.PartKindPlanUpdate,
			Tool:     domain.ToolKindUpdatePlan,
			Status:   StoredResultStatusOK,
			Payload:  raw,
		}, true
	default:
		return storedResultEnvelope{}, false
	}
}

func formatStoredResultForPart(env storedResultEnvelope) (string, bool) {
	switch env.PartKind {
	case domain.PartKindToolOutput:
		return formatStoredToolOutput(env)
	case domain.PartKindTaskUpdate:
		return decodeAndFormat[TaskStoredResult](env.Payload, formatTaskStoredResult)
	case domain.PartKindPlanUpdate:
		return decodeAndFormat[UpdatePlanStoredResult](env.Payload, formatUpdatePlanStoredResult)
	default:
		return "", false
	}
}

func formatStoredToolOutput(env storedResultEnvelope) (string, bool) {
	if env.Status == StoredResultStatusDenied {
		return decodeAndFormat[DeniedStoredResult](env.Payload, func(result DeniedStoredResult) string {
			return strings.TrimSpace(result.Message)
		})
	}
	if env.Status == StoredResultStatusError {
		return decodeAndFormat[ErrorStoredResult](env.Payload, func(result ErrorStoredResult) string {
			return formatErrorStoredResult(env.Tool, result.Message)
		})
	}
	switch env.Tool {
	case domain.ToolKindFileRead:
		return decodeAndFormat[ReadStoredResult](env.Payload, formatReadStoredResult)
	case domain.ToolKindBash:
		return decodeAndFormat[BashStoredResult](env.Payload, func(result BashStoredResult) string {
			return strings.TrimSpace(result.Output)
		})
	case domain.ToolKindExecCommand, domain.ToolKindExecStatus, domain.ToolKindExecWriteStdin, domain.ToolKindExecResize, domain.ToolKindExecTerminate:
		return decodeAndFormat[ExecStoredResult](env.Payload, formatExecStoredResult)
	case domain.ToolKindExecList, domain.ToolKindExecCleanup:
		return decodeAndFormat[ExecListStoredResult](env.Payload, formatExecListStoredResult)
	case domain.ToolKindFileEdit:
		return decodeAndFormat[EditStoredResult](env.Payload, func(result EditStoredResult) string {
			return strings.TrimSpace(result.Summary)
		})
	case domain.ToolKindFileWrite:
		return decodeAndFormat[WriteStoredResult](env.Payload, formatWriteStoredResultForModel)
	case domain.ToolKindLint:
		return decodeAndFormat[LintStoredResult](env.Payload, func(result LintStoredResult) string {
			if diagnostics := strings.TrimSpace(result.Diagnostics); diagnostics != "" {
				return diagnostics
			}
			return strings.TrimSpace(result.Summary)
		})
	case domain.ToolKindFileGlob:
		return decodeAndFormat[GlobStoredResult](env.Payload, formatGlobStoredResult)
	case domain.ToolKindFileGrep:
		return decodeAndFormat[GrepStoredResult](env.Payload, func(result GrepStoredResult) string {
			return strings.TrimSpace(result.Output)
		})
	case domain.ToolKindQuestion:
		return decodeAndFormat[QuestionStoredResult](env.Payload, func(result QuestionStoredResult) string {
			return strings.TrimSpace(result.Question)
		})
	case domain.ToolKindSkill:
		return decodeAndFormat[SkillStoredResult](env.Payload, func(result SkillStoredResult) string {
			return strings.TrimSpace(result.Content)
		})
	case domain.ToolKindWebFetch:
		return decodeAndFormat[WebFetchStoredResult](env.Payload, func(result WebFetchStoredResult) string {
			return strings.TrimSpace(result.Body)
		})
	case domain.ToolKindWebSearch:
		return decodeAndFormat[WebSearchStoredResult](env.Payload, formatWebSearchStoredResult)
	case domain.ToolKindViewImage:
		return decodeAndFormat[ViewImageStoredResult](env.Payload, formatViewImageStoredResult)
	case domain.ToolKindShowImage:
		return decodeAndFormat[ShowImageStoredResult](env.Payload, formatShowImageStoredResult)
	case domain.ToolKindMilestoneList, domain.ToolKindMilestoneAdd, domain.ToolKindMilestoneUpdate, domain.ToolKindMilestoneWrite, domain.ToolKindMilestonePlan:
		return decodeAndFormat[MilestonePlanStoredResult](env.Payload, formatMilestonePlanStoredResult)
	case domain.ToolKindChatList, domain.ToolKindChatStart, domain.ToolKindChatStartDecomposition, domain.ToolKindChatStartExecution, domain.ToolKindChatPoll, domain.ToolKindChatUpdate:
		return decodeAndFormat[ChatListStoredResult](env.Payload, formatChatListStoredResult)
	case domain.ToolKindTodoList, domain.ToolKindTodoAddItems, domain.ToolKindTodoUpdateItem, domain.ToolKindTodoFetchNext, domain.ToolKindTodosAdd, domain.ToolKindTodosUpdate:
		return decodeAndFormat[TodoListStoredResult](env.Payload, formatTodoListStoredResult)
	default:
		return "", false
	}
}

func formatErrorStoredResult(tool domain.ToolKind, message string) string {
	message = strings.TrimSpace(message)
	if tool == 0 || message == "" {
		return message
	}
	messageLower := strings.ToLower(message)
	name := tool.String()
	snake := toSnakeCase(name)
	for _, candidate := range []string{name, snake} {
		prefix := candidate + " failed:"
		if strings.HasPrefix(messageLower, strings.ToLower(prefix)) {
			return strings.TrimSpace(message[len(prefix):])
		}
	}
	return message
}

func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func formatStoredResultForDisplay(env storedResultEnvelope) (string, bool) {
	if env.Status == StoredResultStatusDenied || env.Status == StoredResultStatusError {
		return formatStoredToolOutput(env)
	}
	switch env.PartKind {
	case domain.PartKindToolOutput:
		if env.Tool == domain.ToolKindFileEdit {
			return decodeAndFormat[EditStoredResult](env.Payload, formatEditStoredResultForDisplay)
		}
		if env.Tool == domain.ToolKindFileWrite {
			return decodeAndFormat[WriteStoredResult](env.Payload, formatWriteStoredResultForDisplay)
		}
		if env.Tool == domain.ToolKindLint {
			return decodeAndFormat[LintStoredResult](env.Payload, formatLintStoredResultForDisplay)
		}
		return formatStoredToolOutput(env)
	default:
		return "", false
	}
}

func decodeAndFormat[T any](payload json.RawMessage, format func(T) string) (string, bool) {
	if len(payload) == 0 {
		return "", false
	}
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		return "", false
	}
	return format(value), true
}

func formatReadStoredResult(result ReadStoredResult) string {
	lines := make([]string, 0, max(len(result.Entries), len(result.Lines))+1)
	switch result.Mode {
	case ReadStoredModeDirectory:
		lines = append(lines, result.Entries...)
	default:
		for _, line := range result.Lines {
			lines = append(lines, strconv.Itoa(line.Number)+": "+line.Text)
		}
	}
	footer := strings.TrimSpace(result.Footer)
	if footer == "" {
		footer = readStoredFooter(result)
	}
	if footer != "" {
		lines = append(lines, footer)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatExecStoredResult(result ExecStoredResult) string {
	lines := make([]string, 0, 4)
	if msg := strings.TrimSpace(result.Message); msg != "" {
		lines = append(lines, msg)
	}
	if id := strings.TrimSpace(result.ProcessID); id != "" {
		lines = append(lines, "process_id: "+id)
	}
	if state := strings.TrimSpace(result.State); state != "" {
		lines = append(lines, "state: "+state)
	}
	if result.ExitCode != nil {
		lines = append(lines, fmt.Sprintf("exit_code: %d", *result.ExitCode))
	}
	if output := strings.TrimSpace(result.Output); output != "" {
		lines = append(lines, output)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatExecListStoredResult(result ExecListStoredResult) string {
	lines := make([]string, 0, len(result.Items)+1)
	if msg := strings.TrimSpace(result.Message); msg != "" {
		lines = append(lines, msg)
	}
	for _, item := range result.Items {
		line := fmt.Sprintf("%s [%s] %s", strings.TrimSpace(item.ProcessID), strings.TrimSpace(item.State), strings.TrimSpace(item.Command))
		if item.ExitCode != nil {
			line += fmt.Sprintf(" (exit %d)", *item.ExitCode)
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func readStoredFooter(result ReadStoredResult) string {
	if result.Total == 0 && result.Mode == ReadStoredModeFile {
		return "End of file - total 0 lines."
	}
	if result.Total == 0 && result.Mode == ReadStoredModeDirectory {
		return "End of directory - total 0 entries."
	}
	if result.Start == 0 || result.End == 0 {
		return ""
	}
	label := "lines"
	if result.Mode == ReadStoredModeDirectory {
		label = "entries"
	}
	if result.ByteCapped {
		if result.Total > 0 {
			return fmt.Sprintf("(showing %s %d-%d of %d, output capped; use start_line=%d end_line=%d only if you need the next section; prefer file_grep or a narrower range for specific code)", label, result.Start, result.End, result.Total, nextReadStart(result), nextReadEnd(result))
		}
		return fmt.Sprintf("(showing %s %d-%d, output capped; use start_line=%d end_line=%d only if you need the next section; prefer file_grep or a narrower range for specific code)", label, result.Start, result.End, nextReadStart(result), nextReadEnd(result))
	}
	if result.HasMore {
		if result.AutoCapped || result.RangeCapped {
			return fmt.Sprintf("(showing %s %d-%d of %d, capped at 1000 lines; use start_line=%d end_line=%d only if you need the next section; prefer file_grep or a narrower range for specific code)", label, result.Start, result.End, result.Total, nextReadStart(result), nextReadEnd(result))
		}
		return fmt.Sprintf("(showing %s %d-%d of %d; use start_line=%d end_line=%d only if you need the next section; prefer file_grep or a narrower range for specific code)", label, result.Start, result.End, result.Total, nextReadStart(result), nextReadEnd(result))
	}
	if result.Mode == ReadStoredModeDirectory {
		return fmt.Sprintf("End of directory - total %d entries.", result.Total)
	}
	return fmt.Sprintf("End of file - total %d lines.", result.Total)
}

func nextReadStart(result ReadStoredResult) int {
	if result.NextStartLine > 0 {
		return result.NextStartLine
	}
	return result.NextOffset
}

func nextReadEnd(result ReadStoredResult) int {
	start := nextReadStart(result)
	if start <= 0 {
		return 0
	}
	return start + effectiveReadLimit(result) - 1
}

func effectiveReadLimit(result ReadStoredResult) int {
	if result.EffectiveLimit > 0 {
		return result.EffectiveLimit
	}
	if limit, err := strconv.Atoi(strings.TrimSpace(result.Limit)); err == nil && limit > 0 {
		return limit
	}
	return DefaultReadLineLimit
}

func formatGlobStoredResult(result GlobStoredResult) string {
	lines := append([]string(nil), result.Matches...)
	if footer := strings.TrimSpace(result.Footer); footer != "" {
		lines = append(lines, footer)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatTaskStoredResult(result TaskStoredResult) string {
	return strings.TrimSpace(result.Body)
}

func formatUpdatePlanStoredResult(result UpdatePlanStoredResult) string {
	lines := make([]string, 0, len(result.Steps)+1)
	if explanation := strings.TrimSpace(result.Explanation); explanation != "" {
		lines = append(lines, explanation)
	}
	for _, step := range result.Steps {
		if strings.TrimSpace(step.Step) == "" {
			continue
		}
		lines = append(lines, "["+strings.TrimSpace(step.Status)+"] "+strings.TrimSpace(step.Step))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatMilestonePlanStoredResult(result MilestonePlanStoredResult) string {
	lines := make([]string, 0, len(result.Milestones)+1)
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		lines = append(lines, summary)
	}
	for _, item := range result.Milestones {
		if strings.TrimSpace(item.Title) == "" {
			continue
		}
		line := "[" + strings.TrimSpace(item.Status) + "] " + strings.TrimSpace(item.Title)
		if ref := strings.TrimSpace(item.Ref); ref != "" {
			line += " (" + ref + ")"
		}
		lines = append(lines, line)
		if notes := strings.TrimSpace(item.Notes); notes != "" {
			lines = append(lines, notes)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatTodoListStoredResult(result TodoListStoredResult) string {
	lines := make([]string, 0, len(result.Items)+2)
	if title := strings.TrimSpace(result.MilestoneTitle); title != "" {
		lines = append(lines, "Milestone: "+title)
	}
	if ref := strings.TrimSpace(result.MilestoneRef); ref != "" {
		lines = append(lines, "Ref: "+ref)
	}
	for _, item := range result.Items {
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s] #%s %s", strings.TrimSpace(item.Status), item.ID, strings.TrimSpace(item.Content)))
		if note := strings.TrimSpace(item.Note); note != "" {
			lines = append(lines, "note: "+note)
		}
	}
	if message := strings.TrimSpace(result.Message); message != "" {
		lines = append(lines, message)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatChatListStoredResult(result ChatListStoredResult) string {
	lines := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		if item.ID == "" {
			continue
		}
		line := fmt.Sprintf("#%s %s", item.ID, strings.TrimSpace(item.Title))
		if role := strings.TrimSpace(item.Role); role != "" {
			line += " [" + role + "]"
		}
		if state := strings.TrimSpace(item.State); state != "" {
			line += " {" + state + "}"
		}
		if item.Archived {
			line += " {archived}"
		}
		lines = append(lines, line)
		if ref := strings.TrimSpace(item.ActiveMilestoneRef); ref != "" {
			lines = append(lines, "milestone: "+ref)
		}
		if status := strings.TrimSpace(item.StatusText); status != "" {
			lines = append(lines, status)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatWriteStoredResultForDisplay(result WriteStoredResult) string {
	diagnostics := strings.TrimSpace(result.Diagnostics)
	if strings.TrimSpace(result.Content) == "" {
		if diagnostics != "" {
			return strings.TrimSpace(result.Summary + "\n\nDiagnostics:\n" + diagnostics)
		}
		return strings.TrimSpace(result.Summary)
	}
	text := strings.TrimSpace(result.Content)
	if result.Truncated {
		text += "\n... truncated ..."
	}
	if diagnostics != "" {
		text += "\n\nDiagnostics:\n" + diagnostics
	}
	return text
}

func formatWriteStoredResultForModel(result WriteStoredResult) string {
	lines := []string{}
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		lines = append(lines, summary)
	} else if path := strings.TrimSpace(result.Path); path != "" {
		action := strings.TrimSpace(result.Action)
		if action == "" {
			action = "wrote"
		}
		lines = append(lines, fmt.Sprintf("%s %s", action, path))
	}
	if content := strings.TrimSpace(result.Content); content != "" {
		lineCount := strings.Count(content, "\n")
		if content[len(content)-1] != '\n' {
			lineCount++
		}
		lines = append(lines, fmt.Sprintf("Wrote %d lines.", lineCount))
	}
	if result.Truncated {
		lines = append(lines, "Stored written content is truncated in the UI transcript.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatLintStoredResultForDisplay(result LintStoredResult) string {
	if diagnostics := strings.TrimSpace(result.Diagnostics); diagnostics != "" {
		return diagnostics
	}
	return strings.TrimSpace(result.Summary)
}

func formatEditStoredResultForDisplay(result EditStoredResult) string {
	var footer []string
	if diagnostics := strings.TrimSpace(result.Diagnostics); diagnostics != "" {
		footer = append(footer, "Diagnostics:\n"+diagnostics)
	}
	if diff := strings.TrimSpace(result.Diff); diff != "" {
		if len(footer) == 0 {
			return diff
		}
		return strings.TrimSpace(diff + "\n\n" + strings.Join(footer, "\n\n"))
	}
	text := formatLegacyEditStoredResultForDisplay(result)
	if len(footer) > 0 {
		text = strings.TrimSpace(text + "\n\n" + strings.Join(footer, "\n\n"))
	}
	return text
}

func formatLegacyEditStoredResultForDisplay(result EditStoredResult) string {
	lines := []string{strings.TrimSpace(result.Summary)}
	for _, hunk := range result.Hunks {
		oldCount := max(1, len(hunk.OldLines))
		newCount := max(1, len(hunk.NewLines))
		lines = append(lines, fmt.Sprintf("@@ -%d,%d +%d,%d @@", hunk.OldStart, oldCount, hunk.NewStart, newCount))
		for idx, line := range hunk.OldLines {
			lines = append(lines, fmt.Sprintf("-%d %s", hunk.OldStart+idx, line))
		}
		for idx, line := range hunk.NewLines {
			lines = append(lines, fmt.Sprintf("+%d %s", hunk.NewStart+idx, line))
		}
	}
	if result.Truncated {
		lines = append(lines, "... additional replacements omitted ...")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatWebSearchStoredResult(result WebSearchStoredResult) string {
	lines := make([]string, 0, len(result.Items)*4)
	for idx, item := range result.Items {
		if strings.TrimSpace(item.Title) == "" && strings.TrimSpace(item.URL) == "" {
			continue
		}
		lines = append(lines, strconv.Itoa(idx+1)+". "+strings.TrimSpace(item.Title))
		lines = append(lines, strings.TrimSpace(item.URL))
		if snippet := strings.TrimSpace(item.Snippet); snippet != "" {
			lines = append(lines, snippet)
		}
		lines = append(lines, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatViewImageStoredResult(result ViewImageStoredResult) string {
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		return summary
	}
	path := strings.TrimSpace(result.Path)
	if path == "" {
		path = strings.TrimSpace(result.SourcePath)
	}
	if path == "" {
		return "Viewed image"
	}
	return "Viewed image " + path
}

func formatShowImageStoredResult(result ShowImageStoredResult) string {
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		return summary
	}
	path := strings.TrimSpace(result.Path)
	if path == "" {
		path = strings.TrimSpace(result.SourcePath)
	}
	if path == "" {
		return "Showed image"
	}
	return "Showed image " + path
}

func ParseReadStoredLines(output string) ([]ReadStoredLine, string) {
	rawLines := strings.Split(strings.TrimSpace(output), "\n")
	lines := make([]ReadStoredLine, 0, len(rawLines))
	var footer []string
	for _, raw := range rawLines {
		raw = strings.TrimRight(raw, "\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		number, textPart, ok := parseReadStoredLine(raw)
		if !ok {
			footer = append(footer, strings.TrimSpace(raw))
			continue
		}
		lines = append(lines, ReadStoredLine{Number: number, Text: textPart})
	}
	return lines, strings.TrimSpace(strings.Join(footer, "\n"))
}

func parseReadStoredLine(raw string) (int, string, bool) {
	if numberPart, textPart, ok := strings.Cut(raw, "\t"); ok {
		number, err := strconv.Atoi(strings.TrimSpace(numberPart))
		if err == nil {
			return number, textPart, true
		}
	}
	return 0, "", false
}
