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
	PartKindReference       PartKind = "reference"
	PartKindReasoning       PartKind = "reasoning"
	PartKindToolCall        PartKind = "tool_call"
	PartKindToolOutput      PartKind = "tool_output"
	PartKindDiff            PartKind = "diff"
	PartKindCompaction      PartKind = "compaction"
	PartKindApprovalRequest PartKind = "approval_request"
	PartKindQuestion        PartKind = "question"
	PartKindTaskUpdate      PartKind = "task_update"
	PartKindPlanUpdate      PartKind = "plan_update"
	PartKindSystemNotice    PartKind = "system_notice"
	PartKindEventNotice     PartKind = "event_notice"
)

type ToolKind string

const (
	ToolKindRead            ToolKind = "read"
	ToolKindGlob            ToolKind = "glob"
	ToolKindGrep            ToolKind = "grep"
	ToolKindBash            ToolKind = "bash"
	ToolKindApplyPatch      ToolKind = "apply_patch"
	ToolKindEdit            ToolKind = "edit"
	ToolKindWrite           ToolKind = "write"
	ToolKindTask            ToolKind = "task"
	ToolKindQuestion        ToolKind = "question"
	ToolKindUpdatePlan      ToolKind = "update_plan"
	ToolKindMilestoneList   ToolKind = "milestone_list"
	ToolKindMilestoneAdd    ToolKind = "milestone_add_items"
	ToolKindMilestoneUpdate ToolKind = "milestone_update_item"
	ToolKindMilestonePlan   ToolKind = "milestone_plan_and_decompose"
	ToolKindMilestoneWrite  ToolKind = "milestone_write"
	ToolKindTodoList        ToolKind = "todo_list"
	ToolKindTodoAddItems    ToolKind = "todo_add_items"
	ToolKindTodoUpdateItem  ToolKind = "todo_update_item"
	ToolKindTodoFetchNext   ToolKind = "todo_fetch_next"
	ToolKindChatList        ToolKind = "chat_list"
	ToolKindChatStartDecomp ToolKind = "chat_start_decomposition"
	ToolKindChatStartExec   ToolKind = "chat_start_execution"
	ToolKindChatPoll        ToolKind = "chat_poll"
	ToolKindSkill           ToolKind = "skill"
	ToolKindWebFetch        ToolKind = "webfetch"
	ToolKindWebSearch       ToolKind = "websearch"
	ToolKindMCP             ToolKind = "mcp"
)

func AllToolKinds() []ToolKind {
	return []ToolKind{
		ToolKindRead,
		ToolKindGlob,
		ToolKindGrep,
		ToolKindBash,
		ToolKindApplyPatch,
		ToolKindEdit,
		ToolKindWrite,
		ToolKindTask,
		ToolKindQuestion,
		ToolKindUpdatePlan,
		ToolKindMilestoneList,
		ToolKindMilestoneAdd,
		ToolKindMilestoneUpdate,
		ToolKindMilestonePlan,
		ToolKindMilestoneWrite,
		ToolKindTodoList,
		ToolKindTodoAddItems,
		ToolKindTodoUpdateItem,
		ToolKindTodoFetchNext,
		ToolKindChatList,
		ToolKindChatStartDecomp,
		ToolKindChatStartExec,
		ToolKindChatPoll,
		ToolKindSkill,
		ToolKindWebFetch,
		ToolKindWebSearch,
		ToolKindMCP,
	}
}

type PermissionMode string

const (
	PermissionModeAllow PermissionMode = "allow"
	PermissionModeAsk   PermissionMode = "ask"
	PermissionModeDeny  PermissionMode = "deny"
)

type PermissionOverride struct {
	Tool    ToolKind
	Pattern string
	Action  PermissionMode
}

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

type MilestoneStatus string

const (
	MilestoneStatusPending     MilestoneStatus = "pending"
	MilestoneStatusInProgress  MilestoneStatus = "in_progress"
	MilestoneStatusDecomposing MilestoneStatus = "decomposing"
	MilestoneStatusExecuting   MilestoneStatus = "executing"
	MilestoneStatusCompleted   MilestoneStatus = "completed"
	MilestoneStatusBlocked     MilestoneStatus = "blocked"
)

type TodoStatus string

const (
	TodoStatusPending    TodoStatus = "pending"
	TodoStatusInProgress TodoStatus = "in_progress"
	TodoStatusCompleted  TodoStatus = "completed"
)

type EventKind string

const (
	EventKindMessageDelta  EventKind = "message_delta"
	EventKindMessageDone   EventKind = "message_done"
	EventKindReasoning     EventKind = "reasoning"
	EventKindToolCallDelta EventKind = "tool_call_delta"
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
	TitleGeneratedAt  time.Time
	TitleRefreshCount int
	ProviderID        string
	ModelID           string
	PermissionProfile string
	PermissionRules   []PermissionOverride
	ToolStates        map[ToolKind]bool
	CWD               string
	ProjectRoot       string
	ProjectChecksum   string
	AgentsResolved    string
	AgentsSummary     string
	AgentsFiles       []AgentsFile
	AgentsGeneratedAt time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastMessage       string
}

type WorkflowRole string

const (
	WorkflowRoleGeneral       WorkflowRole = "general"
	WorkflowRoleOrchestrator  WorkflowRole = "orchestrator"
	WorkflowRolePlanning      WorkflowRole = "planning"
	WorkflowRoleDecomposition WorkflowRole = "decomposition"
	WorkflowRoleExecution     WorkflowRole = "execution"
)

type Chat struct {
	ID                    int64
	SessionID             int64
	ParentChatID          *int64
	Title                 string
	WorkflowRole          WorkflowRole
	ProviderID            string
	ModelID               string
	PermissionProfile     string
	ToolStates            map[ToolKind]bool
	ActiveMilestoneRef    string
	AssignedTodoBucketRef string
	QueuedInputs          []QueuedInput
	CreatedAt             time.Time
	UpdatedAt             time.Time
	LastMessage           string
}

type QueuedInputKind string

const (
	QueuedInputKindSteer         QueuedInputKind = "steer"
	QueuedInputKindQueued        QueuedInputKind = "queued"
	QueuedInputKindContinue      QueuedInputKind = "continue"
	QueuedInputKindRejectedSteer QueuedInputKind = "rejected_steer"
)

type QueuedInput struct {
	ID          int64
	Kind        QueuedInputKind
	Text        string
	Held        bool
	Attachments []QueuedAttachment
	References  []QueuedReference
	CreatedAt   time.Time
}

type QueuedAttachment struct {
	ID       string
	Name     string
	MIME     string
	Path     string
	Size     int64
	Source   string
	Original string
}

type QueuedReference struct {
	Kind    string
	Path    string
	Display string
	Start   int
	End     int
}

type AgentsFile struct {
	Path         string
	Kind         string
	Priority     int
	ModTime      time.Time
	Checksum     string
	Size         int64
	DiscoveredBy string
}

type Message struct {
	ID        int64
	SessionID int64
	ChatID    int64
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
