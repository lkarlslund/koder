package domain

import "time"

type MessageRole string

const (
	MessageRoleSystem    MessageRole = "system"
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
)

type PartKind string

const (
	PartKindText            PartKind = "text"
	PartKindAttachment      PartKind = "attachment"
	PartKindReasoning       PartKind = "reasoning"
	PartKindToolCall        PartKind = "tool_call"
	PartKindToolOutput      PartKind = "tool_output"
	PartKindDiff            PartKind = "diff"
	PartKindCompaction      PartKind = "compaction"
	PartKindApprovalRequest PartKind = "approval_request"
	PartKindQuestion        PartKind = "question"
	PartKindTaskUpdate      PartKind = "task_update"
	PartKindSystemNotice    PartKind = "system_notice"
)

type ToolKind string

const (
	ToolKindRead       ToolKind = "read"
	ToolKindGlob       ToolKind = "glob"
	ToolKindGrep       ToolKind = "grep"
	ToolKindBash       ToolKind = "bash"
	ToolKindApplyPatch ToolKind = "apply_patch"
	ToolKindTask       ToolKind = "task"
	ToolKindQuestion   ToolKind = "question"
	ToolKindWebFetch   ToolKind = "webfetch"
	ToolKindWebSearch  ToolKind = "websearch"
)

type PermissionMode string

const (
	PermissionModeAllow PermissionMode = "allow"
	PermissionModeAsk   PermissionMode = "ask"
	PermissionModeDeny  PermissionMode = "deny"
)

type ApprovalStatus string

const (
	ApprovalStatusPending  ApprovalStatus = "pending"
	ApprovalStatusApproved ApprovalStatus = "approved"
	ApprovalStatusDenied   ApprovalStatus = "denied"
)

type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusCancelled  TaskStatus = "cancelled"
)

type EventKind string

const (
	EventKindMessageDelta  EventKind = "message_delta"
	EventKindMessageDone   EventKind = "message_done"
	EventKindReasoning     EventKind = "reasoning"
	EventKindUsage         EventKind = "usage"
	EventKindToolStart     EventKind = "tool_start"
	EventKindToolResult    EventKind = "tool_result"
	EventKindApprovalAsk   EventKind = "approval_ask"
	EventKindApprovalReply EventKind = "approval_reply"
	EventKindTaskUpdate    EventKind = "task_update"
	EventKindSessionTitle  EventKind = "session_title"
	EventKindError         EventKind = "error"
	EventKindStatus        EventKind = "status"
)

type Session struct {
	ID                int64
	ParentID          *int64
	Title             string
	ProviderID        string
	ModelID           string
	PermissionProfile string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastMessage       string
}

type Message struct {
	ID        int64
	SessionID int64
	Role      MessageRole
	Summary   string
	CreatedAt time.Time
}

type Part struct {
	ID        int64
	MessageID int64
	Kind      PartKind
	Body      string
	MetaJSON  string
	CreatedAt time.Time
}

type Model struct {
	ID                string
	OwnedBy           string
	SupportsImages    bool
	SupportsPDFs      bool
	CapabilitySource  string
	CapabilitiesKnown bool
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type Event struct {
	Kind    EventKind
	Text    string
	Tool    ToolKind
	Meta    map[string]string
	Usage   Usage
	Err     error
	RawJSON string
}
