package domain

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
)

//go:generate go tool enumer -type=MessageRole,PartKind,ApprovalStatus,LegacyTaskStatus,MilestoneStatus,TaskStatus,EventKind,QueuedInputKind,QueuedInputDelivery,QueuedInputOrigin,SessionKind -trimprefix=MessageRole,PartKind,ApprovalStatus,LegacyTaskStatus,MilestoneStatus,TaskStatus,EventKind,QueuedInputKind,QueuedInputDelivery,QueuedInputOrigin,SessionKind -transform=snake -json -text -values -output=messagerole_enumer.go
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
	ToolKindFileRead            ToolKind = "file_read"
	ToolKindViewImage           ToolKind = "view_image"
	ToolKindShowImage           ToolKind = "show_image"
	ToolKindShowMedia           ToolKind = "show_media"
	ToolKindOfferFile           ToolKind = "offer_file"
	ToolKindFileGlob            ToolKind = "file_glob"
	ToolKindFileGrep            ToolKind = "file_grep"
	ToolKindCodeSearch          ToolKind = "code_search"
	ToolKindLint                ToolKind = "lint"
	ToolKindBash                ToolKind = "bash"
	ToolKindExecCommand         ToolKind = "exec_command"
	ToolKindExecSession         ToolKind = "exec_session"
	ToolKindExecStatus          ToolKind = "exec_status"
	ToolKindExecList            ToolKind = "exec_list"
	ToolKindExecWriteStdin      ToolKind = "exec_write_stdin"
	ToolKindExecResize          ToolKind = "exec_resize"
	ToolKindExecTerminate       ToolKind = "exec_terminate"
	ToolKindExecCleanup         ToolKind = "exec_cleanup"
	ToolKindFileEdit            ToolKind = "file_edit"
	ToolKindFileWrite           ToolKind = "file_write"
	ToolKindTask                ToolKind = "task"
	ToolKindRequestUserInput    ToolKind = "request_user_input"
	ToolKindQuestion            ToolKind = "question" // Legacy stored tool kind.
	ToolKindUpdatePlan          ToolKind = "update_plan"
	ToolKindMilestones          ToolKind = "milestones"
	ToolKindMilestoneList       ToolKind = "milestone_list"
	ToolKindMilestoneAdd        ToolKind = "milestone_add"
	ToolKindMilestoneUpdate     ToolKind = "milestone_update"
	ToolKindMilestoneDepend     ToolKind = "milestone_depend"
	ToolKindMilestoneArchive    ToolKind = "milestone_archive"
	ToolKindMilestoneDelete     ToolKind = "milestone_delete"
	ToolKindMilestonePlan       ToolKind = "milestone_plan"
	ToolKindMilestoneWrite      ToolKind = "milestone_write"
	ToolKindTasks               ToolKind = "tasks"
	ToolKindTaskList            ToolKind = "task_list"
	ToolKindTaskAddItems        ToolKind = "task_add_items"
	ToolKindTaskUpdateItem      ToolKind = "task_update_item"
	ToolKindTaskFetchNext       ToolKind = "task_fetch_next"
	ToolKindTaskArchive         ToolKind = "task_archive"
	ToolKindTaskDelete          ToolKind = "task_delete"
	ToolKindTasksAdd            ToolKind = "tasks_add"
	ToolKindTasksUpdate         ToolKind = "tasks_update"
	ToolKindChatList            ToolKind = "chat_list"
	ToolKindChatStart           ToolKind = "chat_start"
	ToolKindChatSend            ToolKind = "chat_send"
	ToolKindChatCancel          ToolKind = "chat_cancel"
	ToolKindChatArchive         ToolKind = "chat_archive"
	ToolKindChatRename          ToolKind = "chat_rename"
	ToolKindChatCleanup         ToolKind = "chat_cleanup"
	ToolKindChatStatus          ToolKind = "chat_status"
	ToolKindSessionList         ToolKind = "session_list"
	ToolKindSessionDelegate     ToolKind = "session_delegate"
	ToolKindSessionStart        ToolKind = "session_start"
	ToolKindPhone               ToolKind = "phone"
	ToolKindPhonePhotosSearch   ToolKind = "phone_photos_search"
	ToolKindPhonePhotosThumbs   ToolKind = "phone_photos_thumbs"
	ToolKindPhonePhotoView      ToolKind = "phone_photo_view"
	ToolKindPhonePhotoTransfer  ToolKind = "phone_photo_transfer"
	ToolKindPresent             ToolKind = "present"
	ToolKindSkill               ToolKind = "skill"
	ToolKindWebFetch            ToolKind = "web_fetch"
	ToolKindWebSearch           ToolKind = "web_search"
	ToolKindMCP                 ToolKind = "mcp"
	ToolKindBrowserStatus       ToolKind = "browser_status"
	ToolKindBrowserTabList      ToolKind = "browser_tab_list"
	ToolKindBrowserTabNew       ToolKind = "browser_tab_new"
	ToolKindBrowserTabClaim     ToolKind = "browser_tab_claim"
	ToolKindBrowserTabSelect    ToolKind = "browser_tab_select"
	ToolKindBrowserTabClose     ToolKind = "browser_tab_close"
	ToolKindBrowserNavigate     ToolKind = "browser_navigate"
	ToolKindBrowserBack         ToolKind = "browser_back"
	ToolKindBrowserForward      ToolKind = "browser_forward"
	ToolKindBrowserReload       ToolKind = "browser_reload"
	ToolKindBrowserSnapshot     ToolKind = "browser_snapshot"
	ToolKindBrowserFind         ToolKind = "browser_find"
	ToolKindBrowserClick        ToolKind = "browser_click"
	ToolKindBrowserFill         ToolKind = "browser_fill"
	ToolKindBrowserType         ToolKind = "browser_type"
	ToolKindBrowserPress        ToolKind = "browser_press"
	ToolKindBrowserSelect       ToolKind = "browser_select"
	ToolKindBrowserCheck        ToolKind = "browser_check"
	ToolKindBrowserUncheck      ToolKind = "browser_uncheck"
	ToolKindBrowserHover        ToolKind = "browser_hover"
	ToolKindBrowserDrag         ToolKind = "browser_drag"
	ToolKindBrowserScroll       ToolKind = "browser_scroll"
	ToolKindBrowserWait         ToolKind = "browser_wait"
	ToolKindBrowserUpload       ToolKind = "browser_upload"
	ToolKindBrowserEvaluate     ToolKind = "browser_evaluate"
	ToolKindBrowserScreenshot   ToolKind = "browser_screenshot"
	ToolKindBrowserImage        ToolKind = "browser_image"
	ToolKindBrowserPDF          ToolKind = "browser_pdf"
	ToolKindBrowserConsole      ToolKind = "browser_console"
	ToolKindBrowserRequests     ToolKind = "browser_requests"
	ToolKindBrowserRequest      ToolKind = "browser_request"
	ToolKindBrowserResponseBody ToolKind = "browser_response_body"
	ToolKindBrowserDownloads    ToolKind = "browser_downloads"
	ToolKindBrowserDownload     ToolKind = "browser_download"
)

type PermissionOverride = accesssettings.PermissionOverride

type ToolStates map[ToolKind]bool

var builtinToolKinds = []ToolKind{
	ToolKindFileRead,
	ToolKindViewImage,
	ToolKindShowMedia,
	ToolKindOfferFile,
	ToolKindFileGlob,
	ToolKindFileGrep,
	ToolKindCodeSearch,
	ToolKindLint,
	ToolKindBash,
	ToolKindExecCommand,
	ToolKindExecSession,
	ToolKindExecStatus,
	ToolKindExecList,
	ToolKindExecWriteStdin,
	ToolKindExecResize,
	ToolKindExecTerminate,
	ToolKindExecCleanup,
	ToolKindFileEdit,
	ToolKindFileWrite,
	ToolKindTask,
	ToolKindRequestUserInput,
	ToolKindUpdatePlan,
	ToolKindMilestones,
	ToolKindMilestoneList,
	ToolKindMilestoneAdd,
	ToolKindMilestoneUpdate,
	ToolKindMilestoneDepend,
	ToolKindMilestoneArchive,
	ToolKindMilestoneDelete,
	ToolKindMilestonePlan,
	ToolKindMilestoneWrite,
	ToolKindTasks,
	ToolKindTaskList,
	ToolKindTaskAddItems,
	ToolKindTaskUpdateItem,
	ToolKindTaskFetchNext,
	ToolKindTaskArchive,
	ToolKindTaskDelete,
	ToolKindTasksAdd,
	ToolKindTasksUpdate,
	ToolKindChatList,
	ToolKindChatStart,
	ToolKindChatSend,
	ToolKindChatCancel,
	ToolKindChatArchive,
	ToolKindChatRename,
	ToolKindChatCleanup,
	ToolKindChatStatus,
	ToolKindSessionList,
	ToolKindSessionDelegate,
	ToolKindSessionStart,
	ToolKindPhone,
	ToolKindPhonePhotosSearch,
	ToolKindPhonePhotosThumbs,
	ToolKindPhonePhotoView,
	ToolKindPhonePhotoTransfer,
	ToolKindPresent,
	ToolKindSkill,
	ToolKindWebFetch,
	ToolKindWebSearch,
	ToolKindMCP,
	ToolKindBrowserStatus, ToolKindBrowserTabList, ToolKindBrowserTabNew, ToolKindBrowserTabClaim,
	ToolKindBrowserTabSelect, ToolKindBrowserTabClose, ToolKindBrowserNavigate, ToolKindBrowserBack,
	ToolKindBrowserForward, ToolKindBrowserReload, ToolKindBrowserSnapshot, ToolKindBrowserFind,
	ToolKindBrowserClick, ToolKindBrowserFill, ToolKindBrowserType, ToolKindBrowserPress,
	ToolKindBrowserSelect, ToolKindBrowserCheck, ToolKindBrowserUncheck, ToolKindBrowserHover,
	ToolKindBrowserDrag, ToolKindBrowserScroll, ToolKindBrowserWait, ToolKindBrowserUpload,
	ToolKindBrowserEvaluate, ToolKindBrowserScreenshot, ToolKindBrowserImage, ToolKindBrowserPDF,
	ToolKindBrowserConsole, ToolKindBrowserRequests, ToolKindBrowserRequest, ToolKindBrowserResponseBody,
	ToolKindBrowserDownloads, ToolKindBrowserDownload,
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

type LegacyTaskStatus uint8

const (
	LegacyTaskStatusPending LegacyTaskStatus = iota
	LegacyTaskStatusInProgress
	LegacyTaskStatusCompleted
	LegacyTaskStatusCancelled
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

type TaskStatus uint8

const (
	TaskStatusPending TaskStatus = iota
	TaskStatusInProgress
	TaskStatusCompleted
	TaskStatusCancelled
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
	EventKindUserInputAsk
	EventKindUserInputReply
	EventKindTaskUpdate
	EventKindSessionTitle
	EventKindChatTitle
	EventKindError
	EventKindStatus
)

type Session struct {
	ID                 ID
	ParentID           *ID
	Kind               SessionKind
	Title              string
	TitleUserDefined   bool // Prevents automatic title generation from replacing a user-supplied title.
	TitleGeneratedAt   time.Time
	TitleRefreshCount  int
	PermissionProfile  string
	PermissionRules    []PermissionOverride
	ToolStates         ToolStates
	AccessSettings     accesssettings.Settings
	ProjectRoot        string
	ProjectRootManaged bool
	ProjectChecksum    string
	AgentsResolved     string
	AgentsSummary      string
	AgentsFiles        []AgentsFile
	AgentsGeneratedAt  time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	LastMessage        string
	Archived           bool
	Pinned             bool
	Favorite           bool
	DeletedAt          time.Time
	VoiceResultCount   uint64
}

type SessionKind uint8

const (
	SessionKindRegular SessionKind = iota
	SessionKindQuick
	SessionKindVoice
)

type WorkflowRole string

const (
	WorkflowRoleGeneral      WorkflowRole = "general"
	WorkflowRoleOrchestrator WorkflowRole = "orchestrator"
	WorkflowRolePlanning     WorkflowRole = "planning"
	WorkflowRoleExecution    WorkflowRole = "execution"
	WorkflowRoleCompaction   WorkflowRole = "compaction"
	WorkflowRoleStandalone   WorkflowRole = "standalone"
	WorkflowRoleVoice        WorkflowRole = "voice"
)

func (r WorkflowRole) String() string {
	return string(r)
}

// ChatBackend identifies the implementation that processes turns for a chat.
// It is independent from the chat's workflow role and interaction mode.
type ChatBackend string

const (
	ChatBackendKoder ChatBackend = "koder"
	ChatBackendCodex ChatBackend = "codex"
)

func (b ChatBackend) String() string { return string(b) }

// InteractionMode describes how a chat is presented to and controlled by the
// user. Voice remains a durable chat; only its input/output adaptation differs.
type InteractionMode string

const (
	InteractionModeText  InteractionMode = "text"
	InteractionModeVoice InteractionMode = "voice"
)

// ChatCreateSpec is the shared creation contract used by the web UI, voice
// clients, and orchestration tools. Backend, workflow role, and interaction
// mode are deliberately independent dimensions.
type ChatCreateSpec struct {
	Title             string          `json:"title,omitempty"`
	Backend           ChatBackend     `json:"backend,omitempty"`
	WorkflowRole      WorkflowRole    `json:"workflow_role,omitempty"`
	InteractionMode   InteractionMode `json:"interaction_mode,omitempty"`
	ProviderID        string          `json:"provider_id,omitempty"`
	ModelID           string          `json:"model_id,omitempty"`
	PermissionProfile string          `json:"permission_profile,omitempty"`
	MilestoneKey      string          `json:"milestone_key,omitempty"`
	TaskRef           string          `json:"task_ref,omitempty"`
	ToolStates        ToolStates      `json:"tool_states,omitempty"`
}

func (s ChatCreateSpec) Normalized() ChatCreateSpec {
	s.Title = strings.TrimSpace(s.Title)
	s.ProviderID = strings.TrimSpace(s.ProviderID)
	s.ModelID = strings.TrimSpace(s.ModelID)
	s.PermissionProfile = strings.TrimSpace(s.PermissionProfile)
	s.MilestoneKey = strings.TrimSpace(s.MilestoneKey)
	s.TaskRef = strings.TrimSpace(s.TaskRef)
	if s.Backend == "" {
		s.Backend = ChatBackendKoder
	}
	if s.WorkflowRole == "" || s.WorkflowRole == WorkflowRoleVoice {
		if s.WorkflowRole == WorkflowRoleVoice {
			s.InteractionMode = InteractionModeVoice
		}
		s.WorkflowRole = WorkflowRoleOrchestrator
	}
	if s.InteractionMode == "" {
		s.InteractionMode = InteractionModeText
	}
	return s
}

func (m InteractionMode) String() string { return string(m) }

type Chat struct {
	ID                     ID
	SessionID              ID
	ParentChatID           *ID
	Title                  string
	TitleUserDefined       bool // Prevents automatic title generation from replacing a user-supplied title.
	WorkflowRole           WorkflowRole
	Backend                ChatBackend
	InteractionMode        InteractionMode
	ProviderID             string
	ModelID                string
	PermissionProfile      string
	ToolStates             ToolStates
	ActiveMilestoneKey     string
	AssignedTaskBucketKey  string
	AssignedTaskRef        string
	LastKnownContextTokens int
	ContextTokensKnown     bool
	TokenUsage             Usage
	RequiresImages         bool
	Position               int
	Archived               bool
	AutoRestart            bool
	QueuedInputs           []QueuedInput
	CreatedAt              time.Time
	UpdatedAt              time.Time
	LastMessage            string
	Activity               ChatActivity
}

// EffectiveBackend returns the backend used by old and new chat records.
func (c Chat) EffectiveBackend() ChatBackend {
	if c.Backend == "" {
		return ChatBackendKoder
	}
	return c.Backend
}

// EffectiveInteractionMode preserves compatibility with legacy voice-role
// records while new records store voice independently from workflow role.
func (c Chat) EffectiveInteractionMode() InteractionMode {
	if c.WorkflowRole == WorkflowRoleVoice {
		return InteractionModeVoice
	}
	if c.InteractionMode != "" {
		return c.InteractionMode
	}
	return InteractionModeText
}

// EffectiveWorkflowRole maps the retired composite voice role to its actual
// orchestration role. New records should store this normalized value directly.
func (c Chat) EffectiveWorkflowRole() WorkflowRole {
	if c.WorkflowRole == WorkflowRoleVoice {
		return WorkflowRoleOrchestrator
	}
	if c.WorkflowRole == "" {
		return WorkflowRoleGeneral
	}
	return c.WorkflowRole
}

// NormalizeChatDimensions fills compatibility defaults before persistence.
func NormalizeChatDimensions(c Chat) Chat {
	c.Backend = c.EffectiveBackend()
	c.InteractionMode = c.EffectiveInteractionMode()
	c.WorkflowRole = c.EffectiveWorkflowRole()
	return c
}

// ChatActivity is durable model-authored work context. It complements, but
// never replaces, the runtime execution state of a chat.
type ChatActivity struct {
	Summary         string
	Phase           string
	ProgressPercent *int
	Blocked         bool
	UpdatedAt       time.Time
}

func (c *Chat) UnmarshalJSON(data []byte) error {
	type chatJSON struct {
		ID                     ID
		SessionID              ID
		ParentChatID           *ID
		Title                  string
		WorkflowRole           WorkflowRole
		Backend                ChatBackend
		InteractionMode        InteractionMode
		ProviderID             string
		ModelID                string
		PermissionProfile      string
		ToolStates             ToolStates
		ActiveMilestoneKey     string
		ActiveMilestoneRef     string
		AssignedTaskBucketKey  string
		AssignedTaskBucketRef  string
		AssignedTaskRef        string
		LastKnownContextTokens int
		ContextTokensKnown     bool
		TokenUsage             Usage
		RequiresImages         bool
		Position               int
		Archived               bool
		AutoRestart            bool
		QueuedInputs           []QueuedInput
		CreatedAt              time.Time
		UpdatedAt              time.Time
		LastMessage            string
		Activity               ChatActivity
	}
	var raw chatJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	activeMilestoneKey := raw.ActiveMilestoneKey
	if activeMilestoneKey == "" {
		activeMilestoneKey = raw.ActiveMilestoneRef
	}
	assignedTaskBucketKey := raw.AssignedTaskBucketKey
	if assignedTaskBucketKey == "" {
		assignedTaskBucketKey = raw.AssignedTaskBucketRef
	}
	role := raw.WorkflowRole
	backend := raw.Backend
	interactionMode := raw.InteractionMode
	if backend == "" {
		backend = ChatBackendKoder
	}
	if interactionMode == "" {
		interactionMode = InteractionModeText
	}
	// Voice used to be encoded as a workflow role. Keep old records readable
	// while normalizing the in-memory representation to orthogonal dimensions.
	if role == WorkflowRoleVoice {
		role = WorkflowRoleOrchestrator
		interactionMode = InteractionModeVoice
	}
	*c = Chat{
		ID:                     raw.ID,
		SessionID:              raw.SessionID,
		ParentChatID:           raw.ParentChatID,
		Title:                  raw.Title,
		WorkflowRole:           role,
		Backend:                backend,
		InteractionMode:        interactionMode,
		ProviderID:             raw.ProviderID,
		ModelID:                raw.ModelID,
		PermissionProfile:      raw.PermissionProfile,
		ToolStates:             raw.ToolStates,
		ActiveMilestoneKey:     activeMilestoneKey,
		AssignedTaskBucketKey:  assignedTaskBucketKey,
		AssignedTaskRef:        raw.AssignedTaskRef,
		LastKnownContextTokens: raw.LastKnownContextTokens,
		ContextTokensKnown:     raw.ContextTokensKnown,
		TokenUsage:             raw.TokenUsage,
		RequiresImages:         raw.RequiresImages,
		Position:               raw.Position,
		Archived:               raw.Archived,
		AutoRestart:            raw.AutoRestart,
		QueuedInputs:           raw.QueuedInputs,
		CreatedAt:              raw.CreatedAt,
		UpdatedAt:              raw.UpdatedAt,
		LastMessage:            raw.LastMessage,
		Activity:               raw.Activity,
	}
	return nil
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
	UserMessageSourceVoice           = "voice"
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
	if strings.TrimSpace(item.Source) == UserMessageSourceVoice {
		return UserMessageSourceVoice
	}
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
	ContextWindow     int
	MaxContextWindow  int
	MaxOutputTokens   int
	MetadataSource    string
	SupportsChat      bool
	SupportsSTT       bool
	SupportsTTS       bool
	SupportsImages    bool
	ImagesKnown       bool
	SupportsPDFs      bool
	SupportsTools     bool
	ToolsKnown        bool
	SupportsJSON      bool
	SupportsReasoning bool
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
