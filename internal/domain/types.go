package domain

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
)

//go:generate go tool enumer -type=MessageRole,PartKind,ApprovalStatus,TaskStatus,MilestoneStatus,TodoStatus,EventKind,QueuedInputKind,QueuedInputDelivery,QueuedInputOrigin -trimprefix=MessageRole,PartKind,ApprovalStatus,TaskStatus,MilestoneStatus,TodoStatus,EventKind,QueuedInputKind,QueuedInputDelivery,QueuedInputOrigin -transform=snake -json -text -values -output=messagerole_enumer.go
type MessageRole uint8

const (
	MessageRoleSystem MessageRole = iota
	MessageRoleUser
	MessageRoleAssistant
	MessageRoleTool
)

type PartKind uint8

const (
	PartKindText PartKind = iota
	PartKindAttachment
	PartKindReference
	PartKindReasoning
	PartKindToolCall
	PartKindToolOutput
	PartKindCompaction
	PartKindApprovalRequest
	PartKindQuestion
	PartKindTaskUpdate
	PartKindPlanUpdate
	PartKindUsage
	PartKindSystemNotice
	PartKindEventNotice
)

type ToolKind string

const (
	ToolKindFileRead        ToolKind = "file_read"
	ToolKindViewImage       ToolKind = "view_image"
	ToolKindShowImage       ToolKind = "show_image"
	ToolKindFileGlob        ToolKind = "file_glob"
	ToolKindFileGrep        ToolKind = "file_grep"
	ToolKindCodeSearch      ToolKind = "code_search"
	ToolKindLint            ToolKind = "lint"
	ToolKindBash            ToolKind = "bash"
	ToolKindExecCommand     ToolKind = "exec_command"
	ToolKindExecStatus      ToolKind = "exec_status"
	ToolKindExecList        ToolKind = "exec_list"
	ToolKindExecWriteStdin  ToolKind = "exec_write_stdin"
	ToolKindExecResize      ToolKind = "exec_resize"
	ToolKindExecTerminate   ToolKind = "exec_terminate"
	ToolKindExecCleanup     ToolKind = "exec_cleanup"
	ToolKindFileEdit        ToolKind = "file_edit"
	ToolKindFileWrite       ToolKind = "file_write"
	ToolKindTask            ToolKind = "task"
	ToolKindQuestion        ToolKind = "question"
	ToolKindUpdatePlan      ToolKind = "update_plan"
	ToolKindMilestoneList   ToolKind = "milestone_list"
	ToolKindMilestoneAdd    ToolKind = "milestone_add"
	ToolKindMilestoneUpdate ToolKind = "milestone_update"
	ToolKindMilestonePlan   ToolKind = "milestone_plan"
	ToolKindMilestoneWrite  ToolKind = "milestone_write"
	ToolKindTaskList        ToolKind = "task_list"
	ToolKindTaskAddItems    ToolKind = "task_add_items"
	ToolKindTaskUpdateItem  ToolKind = "task_update_item"
	ToolKindTaskFetchNext   ToolKind = "task_fetch_next"
	ToolKindTasksAdd        ToolKind = "tasks_add"
	ToolKindTasksUpdate     ToolKind = "tasks_update"
	ToolKindChatList        ToolKind = "chat_list"
	ToolKindChatStart       ToolKind = "chat_start"
	ToolKindChatSend        ToolKind = "chat_send"
	ToolKindChatCancel      ToolKind = "chat_cancel"
	ToolKindChatArchive     ToolKind = "chat_archive"
	ToolKindChatRename      ToolKind = "chat_rename"
	ToolKindSkill           ToolKind = "skill"
	ToolKindWebFetch        ToolKind = "web_fetch"
	ToolKindWebSearch       ToolKind = "web_search"
	ToolKindMCP             ToolKind = "mcp"
)

type PermissionOverride = accesssettings.PermissionOverride

type ToolStates map[ToolKind]bool

var builtinToolKinds = []ToolKind{
	ToolKindFileRead,
	ToolKindViewImage,
	ToolKindShowImage,
	ToolKindFileGlob,
	ToolKindFileGrep,
	ToolKindCodeSearch,
	ToolKindLint,
	ToolKindBash,
	ToolKindExecCommand,
	ToolKindExecStatus,
	ToolKindExecList,
	ToolKindExecWriteStdin,
	ToolKindExecResize,
	ToolKindExecTerminate,
	ToolKindExecCleanup,
	ToolKindFileEdit,
	ToolKindFileWrite,
	ToolKindTask,
	ToolKindQuestion,
	ToolKindUpdatePlan,
	ToolKindMilestoneList,
	ToolKindMilestoneAdd,
	ToolKindMilestoneUpdate,
	ToolKindMilestonePlan,
	ToolKindMilestoneWrite,
	ToolKindTaskList,
	ToolKindTaskAddItems,
	ToolKindTaskUpdateItem,
	ToolKindTaskFetchNext,
	ToolKindTasksAdd,
	ToolKindTasksUpdate,
	ToolKindChatList,
	ToolKindChatStart,
	ToolKindChatSend,
	ToolKindChatCancel,
	ToolKindChatArchive,
	ToolKindChatRename,
	ToolKindSkill,
	ToolKindWebFetch,
	ToolKindWebSearch,
	ToolKindMCP,
}

func BuiltinToolKinds() []ToolKind {
	return slices.Clone(builtinToolKinds)
}

func IsBuiltinToolKind(kind ToolKind) bool {
	for _, known := range builtinToolKinds {
		if known == kind {
			return true
		}
	}
	return false
}

func (k ToolKind) String() string {
	return string(k)
}

func (k ToolKind) DisplayName() string {
	name := strings.TrimSpace(k.String())
	if name == "" {
		return ""
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-'
	})
	for idx, part := range parts {
		if part == "" {
			continue
		}
		parts[idx] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}

func (s *ToolStates) UnmarshalJSON(data []byte) error {
	var raw map[string]bool
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	states := make(ToolStates, len(raw))
	for name, enabled := range raw {
		kind := ToolKind(strings.TrimSpace(name))
		if kind == "" {
			continue
		}
		if !IsBuiltinToolKind(kind) {
			continue
		}
		states[kind] = enabled
	}
	*s = states
	return nil
}

type ApprovalStatus uint8

const (
	ApprovalStatusPending ApprovalStatus = iota
	ApprovalStatusApproved
	ApprovalStatusDenied
)

type TaskStatus uint8

const (
	TaskStatusPending TaskStatus = iota
	TaskStatusInProgress
	TaskStatusCompleted
	TaskStatusCancelled
)

type MilestoneStatus uint8

const (
	MilestoneStatusPending MilestoneStatus = iota
	MilestoneStatusDecomposing
	MilestoneStatusReady
	MilestoneStatusExecuting
	MilestoneStatusCompleted
	MilestoneStatusBlocked
	MilestoneStatusCancelled
)

type TodoStatus uint8

const (
	TodoStatusPending TodoStatus = iota
	TodoStatusInProgress
	TodoStatusCompleted
)

type EventKind uint8

const (
	EventKindMessageDelta EventKind = iota
	EventKindMessageDone
	EventKindReasoning
	EventKindToolCallDelta
	EventKindUsage
	EventKindToolStart
	EventKindToolResult
	EventKindApprovalAsk
	EventKindApprovalReply
	EventKindTaskUpdate
	EventKindSessionTitle
	EventKindChatTitle
	EventKindError
	EventKindStatus
)

type Session struct {
	ID                ID
	ParentID          *ID
	Title             string
	TitleGeneratedAt  time.Time
	TitleRefreshCount int
	PermissionProfile string
	PermissionRules   []PermissionOverride
	ToolStates        ToolStates
	AccessSettings    accesssettings.Settings
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
	WorkflowRoleGeneral      WorkflowRole = "general"
	WorkflowRoleOrchestrator WorkflowRole = "orchestrator"
	WorkflowRolePlanning     WorkflowRole = "planning"
	WorkflowRoleExecution    WorkflowRole = "execution"
	WorkflowRoleCompaction   WorkflowRole = "compaction"
)

func (r WorkflowRole) String() string {
	return string(r)
}

type Chat struct {
	ID                     ID
	SessionID              ID
	ParentChatID           *ID
	Title                  string
	WorkflowRole           WorkflowRole
	ProviderID             string
	ModelID                string
	PermissionProfile      string
	ToolStates             ToolStates
	ActiveMilestoneRef     string
	AssignedTodoBucketRef  string
	AssignedTodoRef        ID
	LastKnownContextTokens int
	ContextTokensKnown     bool
	TokenUsage             Usage
	Position               int
	Archived               bool
	AutoRestart            bool
	QueuedInputs           []QueuedInput
	CreatedAt              time.Time
	UpdatedAt              time.Time
	LastMessage            string
}

type ContextUsage struct {
	AnchorTokens int
	TotalTokens  int
}

type QueuedInputKind uint8

const (
	QueuedInputKindSteer QueuedInputKind = iota
	QueuedInputKindQueued
	QueuedInputKindContinue
	QueuedInputKindRejectedSteer
)

type QueuedInputDelivery uint8

const (
	QueuedInputDeliveryNextTurn QueuedInputDelivery = iota
	QueuedInputDeliveryTurnBoundary
	QueuedInputDeliveryContinue
)

type QueuedInputOrigin uint8

const (
	QueuedInputOriginUser QueuedInputOrigin = iota
	QueuedInputOriginSubchat
	QueuedInputOriginAutoGenerated
	QueuedInputOriginAutoResume
	QueuedInputOriginRejectedSteer
)

const (
	UserMessageSourceUser            = "user"
	UserMessageSourceSteer           = "steer"
	UserMessageSourceQueued          = "queued"
	UserMessageSourceRejectedSteer   = "rejected_steer"
	UserMessageSourceAutoGenerated   = "auto_generated"
	UserMessageSourceAutoResume      = "auto_resume"
	UserMessageSourceSubchat         = "subchat"
	UserMessageSourceTurnInstruction = "turn_instruction"
)

type QueuedInput struct {
	ID          ID
	Kind        QueuedInputKind
	Delivery    QueuedInputDelivery
	Origin      QueuedInputOrigin
	Text        string
	Source      string
	Held        bool
	Attachments []QueuedAttachment
	References  []QueuedReference
	CreatedAt   time.Time
}

// UserMessageSourceForQueuedInput returns the transcript source label for a queued input.
func UserMessageSourceForQueuedInput(item QueuedInput) string {
	switch item.Origin {
	case QueuedInputOriginUser:
		return UserMessageSourceUser
	case QueuedInputOriginSubchat:
		return UserMessageSourceSubchat
	case QueuedInputOriginAutoGenerated:
		return UserMessageSourceAutoGenerated
	case QueuedInputOriginAutoResume:
		return UserMessageSourceAutoResume
	case QueuedInputOriginRejectedSteer:
		return UserMessageSourceRejectedSteer
	}
	if source := strings.TrimSpace(item.Source); source != "" {
		switch source {
		case UserMessageSourceSteer, UserMessageSourceQueued:
			return UserMessageSourceUser
		default:
			return source
		}
	}
	switch item.Kind {
	case QueuedInputKindSteer:
		return UserMessageSourceUser
	case QueuedInputKindQueued:
		return UserMessageSourceUser
	case QueuedInputKindRejectedSteer:
		return UserMessageSourceRejectedSteer
	default:
		return UserMessageSourceUser
	}
}

func DeliveryForQueuedInput(item QueuedInput) QueuedInputDelivery {
	if item.Delivery.IsAQueuedInputDelivery() {
		return item.Delivery
	}
	switch item.Kind {
	case QueuedInputKindSteer:
		return QueuedInputDeliveryTurnBoundary
	case QueuedInputKindContinue:
		return QueuedInputDeliveryContinue
	default:
		return QueuedInputDeliveryNextTurn
	}
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
	ID        ID
	SessionID ID
	ChatID    ID
	Role      MessageRole
	Summary   string
	CreatedAt time.Time
}

type Part struct {
	ID        ID
	MessageID ID
	Kind      PartKind
	Payload   PartPayload
	Body      string `json:"-"`
	MetaJSON  string `json:"-"`
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
	CachedTokens     int
	TotalTokens      int
}

func (u Usage) HasAnyTokens() bool {
	return u.PromptTokens > 0 || u.CompletionTokens > 0 || u.CachedTokens > 0 || u.TotalTokens > 0
}

func (u Usage) Normalized() Usage {
	if u.TotalTokens <= 0 && (u.PromptTokens > 0 || u.CompletionTokens > 0) {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	return u
}

func (u Usage) Add(other Usage) Usage {
	u = u.Normalized()
	other = other.Normalized()
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.CachedTokens += other.CachedTokens
	u.TotalTokens += other.TotalTokens
	return u.Normalized()
}

// ContextTokens returns the prompt/input token count represented by the usage.
func (u Usage) ContextTokens() (int, bool) {
	u = u.Normalized()
	if u.PromptTokens > 0 {
		return u.PromptTokens, true
	}
	if u.TotalTokens > 0 && u.CompletionTokens >= 0 && u.TotalTokens > u.CompletionTokens {
		return u.TotalTokens - u.CompletionTokens, true
	}
	return 0, false
}

type Event struct {
	Kind       EventKind
	Text       string
	Tool       ToolKind
	ToolCallID string
	ApprovalID ID
	Item       TimelineItem
	Meta       map[string]string
	Usage      Usage
	Err        error
	RawJSON    string
}

const (
	// EventMetaRefresh names a refresh target requested by an event.
	EventMetaRefresh = "refresh"

	// EventMetaPromptProgress marks provider prompt preprocessing progress.
	EventMetaPromptProgress = "prompt_progress"

	// EventRefreshQueue asks chat runtimes to reload queued inputs from storage.
	EventRefreshQueue = "queue"
)
