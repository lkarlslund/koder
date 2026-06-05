package domain

// ReadStoredMode describes whether a read result came from a file or directory.
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
	OutputMode  string `json:"output_mode,omitempty"`
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

type ExecProcess struct {
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
	Lost        bool   `json:"lost,omitempty"`
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
	Body   string     `json:"body"`
	Status TaskStatus `json:"status"`
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
	Ref          string `json:"ref"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	Notes        string `json:"notes,omitempty"`
	DependsOnRef string `json:"depends_on_ref,omitempty"`
	OwnerChatID  string `json:"owner_chat_id,omitempty"`
	TodoSummary  string `json:"todo_summary,omitempty"`
}

type MilestonePlanStoredResult struct {
	Summary    string                `json:"summary,omitempty"`
	Milestones []MilestoneStoredItem `json:"milestones,omitempty"`
}

type ChatStoredItem struct {
	ID                 ID     `json:"id"`
	Title              string `json:"title"`
	Role               string `json:"role,omitempty"`
	State              string `json:"state,omitempty"`
	QueuedInputs       int    `json:"queued_inputs,omitempty"`
	ActiveMilestoneRef string `json:"active_milestone_ref,omitempty"`
	AssignedTodoRef    ID     `json:"assigned_todo_ref,omitempty"`
	StatusText         string `json:"status_text,omitempty"`
}

type ChatListStoredResult struct {
	Items []ChatStoredItem `json:"items,omitempty"`
}

type TodoStoredItem struct {
	ID      ID     `json:"id"`
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

func (ReadStoredResult) ToolResultPayload()          {}
func (BashStoredResult) ToolResultPayload()          {}
func (ExecStoredResult) ToolResultPayload()          {}
func (ExecListStoredResult) ToolResultPayload()      {}
func (EditStoredResult) ToolResultPayload()          {}
func (WriteStoredResult) ToolResultPayload()         {}
func (LintStoredResult) ToolResultPayload()          {}
func (GlobStoredResult) ToolResultPayload()          {}
func (GrepStoredResult) ToolResultPayload()          {}
func (QuestionStoredResult) ToolResultPayload()      {}
func (TaskStoredResult) ToolResultPayload()          {}
func (UpdatePlanStoredResult) ToolResultPayload()    {}
func (MilestonePlanStoredResult) ToolResultPayload() {}
func (ChatListStoredResult) ToolResultPayload()      {}
func (TodoListStoredResult) ToolResultPayload()      {}
func (SkillStoredResult) ToolResultPayload()         {}
func (WebFetchStoredResult) ToolResultPayload()      {}
func (WebSearchStoredResult) ToolResultPayload()     {}
func (ViewImageStoredResult) ToolResultPayload()     {}
func (ShowImageStoredResult) ToolResultPayload()     {}
func (MCPStoredResult) ToolResultPayload()           {}
func (DeniedStoredResult) ToolResultPayload()        {}
func (ErrorStoredResult) ToolResultPayload()         {}
