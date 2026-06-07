package domain

import (
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/toolkind"
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

type ToolKind = toolkind.Kind

const (
	ToolKindFileRead        = toolkind.ToolKindFileRead
	ToolKindViewImage       = toolkind.ToolKindViewImage
	ToolKindShowImage       = toolkind.ToolKindShowImage
	ToolKindFileGlob        = toolkind.ToolKindFileGlob
	ToolKindFileGrep        = toolkind.ToolKindFileGrep
	ToolKindCodeSearch      = toolkind.ToolKindCodeSearch
	ToolKindLint            = toolkind.ToolKindLint
	ToolKindBash            = toolkind.ToolKindBash
	ToolKindExecCommand     = toolkind.ToolKindExecCommand
	ToolKindExecStatus      = toolkind.ToolKindExecStatus
	ToolKindExecList        = toolkind.ToolKindExecList
	ToolKindExecWriteStdin  = toolkind.ToolKindExecWriteStdin
	ToolKindExecResize      = toolkind.ToolKindExecResize
	ToolKindExecTerminate   = toolkind.ToolKindExecTerminate
	ToolKindExecCleanup     = toolkind.ToolKindExecCleanup
	ToolKindFileEdit        = toolkind.ToolKindFileEdit
	ToolKindFileWrite       = toolkind.ToolKindFileWrite
	ToolKindTask            = toolkind.ToolKindTask
	ToolKindQuestion        = toolkind.ToolKindQuestion
	ToolKindUpdatePlan      = toolkind.ToolKindUpdatePlan
	ToolKindMilestoneList   = toolkind.ToolKindMilestoneList
	ToolKindMilestoneAdd    = toolkind.ToolKindMilestoneAdd
	ToolKindMilestoneUpdate = toolkind.ToolKindMilestoneUpdate
	ToolKindMilestonePlan   = toolkind.ToolKindMilestonePlan
	ToolKindMilestoneWrite  = toolkind.ToolKindMilestoneWrite
	ToolKindTaskList        = toolkind.ToolKindTaskList
	ToolKindTaskAddItems    = toolkind.ToolKindTaskAddItems
	ToolKindTaskUpdateItem  = toolkind.ToolKindTaskUpdateItem
	ToolKindTaskFetchNext   = toolkind.ToolKindTaskFetchNext
	ToolKindTasksAdd        = toolkind.ToolKindTasksAdd
	ToolKindTasksUpdate     = toolkind.ToolKindTasksUpdate
	ToolKindChatList        = toolkind.ToolKindChatList
	ToolKindChatStart       = toolkind.ToolKindChatStart
	ToolKindChatSend        = toolkind.ToolKindChatSend
	ToolKindChatCancel      = toolkind.ToolKindChatCancel
	ToolKindChatArchive     = toolkind.ToolKindChatArchive
	ToolKindChatRename      = toolkind.ToolKindChatRename
	ToolKindSkill           = toolkind.ToolKindSkill
	ToolKindWebFetch        = toolkind.ToolKindWebFetch
	ToolKindWebSearch       = toolkind.ToolKindWebSearch
	ToolKindMCP             = toolkind.ToolKindMCP
)

type PermissionOverride = accesssettings.PermissionOverride

type ToolStates = toolkind.States

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

type WorkflowRole = chatrole.Role

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
