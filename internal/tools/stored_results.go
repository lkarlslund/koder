package tools

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
)

const storedResultMetaKey = "_stored_result"

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
	Path      string           `json:"path"`
	Mode      ReadStoredMode   `json:"mode"`
	Lines     []ReadStoredLine `json:"lines,omitempty"`
	Entries   []string         `json:"entries,omitempty"`
	Footer    string           `json:"footer,omitempty"`
	Offset    string           `json:"offset,omitempty"`
	Limit     string           `json:"limit,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
}

type BashStoredResult struct {
	Command   string `json:"command"`
	Workdir   string `json:"workdir"`
	TimeoutMS int64  `json:"timeout_ms"`
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output,omitempty"`
}

type ApplyPatchStoredResult struct {
	Summary      string   `json:"summary,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	FileCount    int      `json:"file_count,omitempty"`
}

type EditStoredResult struct {
	Path        string `json:"path"`
	ReplaceAll  bool   `json:"replace_all,omitempty"`
	Occurrences int    `json:"occurrences,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

type WriteStoredResult struct {
	Path      string `json:"path"`
	Action    string `json:"action,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
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
	Body   string            `json:"body"`
	Status domain.TaskStatus `json:"status"`
}

type PlanStoredStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type UpdatePlanStoredResult struct {
	Explanation string           `json:"explanation,omitempty"`
	Steps       []PlanStoredStep `json:"steps"`
}

type SkillStoredResult struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type WebFetchStoredResult struct {
	URL         string `json:"url"`
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
	Query string                `json:"query"`
	Items []WebSearchStoredItem `json:"items,omitempty"`
}

type DeniedStoredResult struct {
	Message string `json:"message"`
}

type ErrorStoredResult struct {
	Message string `json:"message"`
}

func (ReadStoredResult) storedResultPayload()       {}
func (BashStoredResult) storedResultPayload()       {}
func (ApplyPatchStoredResult) storedResultPayload() {}
func (EditStoredResult) storedResultPayload()       {}
func (WriteStoredResult) storedResultPayload()      {}
func (GlobStoredResult) storedResultPayload()       {}
func (GrepStoredResult) storedResultPayload()       {}
func (QuestionStoredResult) storedResultPayload()   {}
func (TaskStoredResult) storedResultPayload()       {}
func (UpdatePlanStoredResult) storedResultPayload() {}
func (SkillStoredResult) storedResultPayload()      {}
func (WebFetchStoredResult) storedResultPayload()   {}
func (WebSearchStoredResult) storedResultPayload()  {}
func (DeniedStoredResult) storedResultPayload()     {}
func (ErrorStoredResult) storedResultPayload()      {}

func MetaWithStoredResult(meta map[string]string, partKind domain.PartKind, tool domain.ToolKind, status StoredResultStatus, payload StoredResultPayload) map[string]string {
	if payload == nil {
		return meta
	}
	body, err := marshalStoredResult(partKind, tool, status, payload)
	if err != nil {
		return meta
	}
	if meta == nil {
		meta = map[string]string{}
	}
	meta[storedResultMetaKey] = body
	return meta
}

func ModelTextForPart(part domain.Part, diff string) (string, bool) {
	env, ok := storedResultFromPart(part)
	if !ok {
		return "", false
	}
	text, ok := formatStoredResultForPart(env)
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	if part.Kind == domain.PartKindToolOutput && strings.TrimSpace(diff) != "" {
		text += "\n\nDiff:\n" + diff
	}
	return text, true
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

func marshalStoredResult(partKind domain.PartKind, tool domain.ToolKind, status StoredResultStatus, payload StoredResultPayload) (string, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	rawEnvelope, err := json.Marshal(storedResultEnvelope{
		Version:  1,
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
	if strings.TrimSpace(part.MetaJSON) == "" {
		return storedResultEnvelope{}, false
	}
	meta, err := decodeStringMap([]byte(part.MetaJSON))
	if err != nil {
		return storedResultEnvelope{}, false
	}
	raw := strings.TrimSpace(meta[storedResultMetaKey])
	if raw == "" {
		return storedResultEnvelope{}, false
	}
	var env storedResultEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return storedResultEnvelope{}, false
	}
	return env, true
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
			return strings.TrimSpace(result.Message)
		})
	}
	switch env.Tool {
	case domain.ToolKindRead:
		return decodeAndFormat[ReadStoredResult](env.Payload, formatReadStoredResult)
	case domain.ToolKindBash:
		return decodeAndFormat[BashStoredResult](env.Payload, func(result BashStoredResult) string {
			return strings.TrimSpace(result.Output)
		})
	case domain.ToolKindApplyPatch:
		return decodeAndFormat[ApplyPatchStoredResult](env.Payload, func(result ApplyPatchStoredResult) string {
			return strings.TrimSpace(result.Summary)
		})
	case domain.ToolKindEdit:
		return decodeAndFormat[EditStoredResult](env.Payload, func(result EditStoredResult) string {
			return strings.TrimSpace(result.Summary)
		})
	case domain.ToolKindWrite:
		return decodeAndFormat[WriteStoredResult](env.Payload, func(result WriteStoredResult) string {
			return strings.TrimSpace(result.Summary)
		})
	case domain.ToolKindGlob:
		return decodeAndFormat[GlobStoredResult](env.Payload, formatGlobStoredResult)
	case domain.ToolKindGrep:
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
	default:
		return "", false
	}
}

func formatStoredResultForDisplay(env storedResultEnvelope) (string, bool) {
	if env.Status == StoredResultStatusDenied || env.Status == StoredResultStatusError {
		return formatStoredToolOutput(env)
	}
	switch env.PartKind {
	case domain.PartKindToolOutput:
		if env.Tool == domain.ToolKindWrite {
			return decodeAndFormat[WriteStoredResult](env.Payload, formatWriteStoredResultForDisplay)
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
	if footer := strings.TrimSpace(result.Footer); footer != "" {
		lines = append(lines, footer)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
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

func formatWriteStoredResultForDisplay(result WriteStoredResult) string {
	if strings.TrimSpace(result.Content) == "" {
		return strings.TrimSpace(result.Summary)
	}
	text := strings.TrimSpace(result.Content)
	if result.Truncated {
		text += "\n... truncated ..."
	}
	return text
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

func ParseReadStoredLines(output string) ([]ReadStoredLine, string) {
	rawLines := strings.Split(strings.TrimSpace(output), "\n")
	lines := make([]ReadStoredLine, 0, len(rawLines))
	var footer []string
	for _, raw := range rawLines {
		raw = strings.TrimRight(raw, "\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		numberPart, textPart, ok := strings.Cut(raw, "\t")
		if !ok {
			footer = append(footer, strings.TrimSpace(raw))
			continue
		}
		number, err := strconv.Atoi(strings.TrimSpace(numberPart))
		if err != nil {
			footer = append(footer, strings.TrimSpace(raw))
			continue
		}
		lines = append(lines, ReadStoredLine{Number: number, Text: textPart})
	}
	return lines, strings.TrimSpace(strings.Join(footer, "\n"))
}
