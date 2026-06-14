package tools

import "github.com/lkarlslund/koder/internal/domain"

type ID = domain.ToolKind

const (
	FileRead        ID = domain.ToolKindFileRead
	ViewImage       ID = domain.ToolKindViewImage
	ShowImage       ID = domain.ToolKindShowImage
	FileGlob        ID = domain.ToolKindFileGlob
	FileGrep        ID = domain.ToolKindFileGrep
	CodeSearch      ID = domain.ToolKindCodeSearch
	Lint            ID = domain.ToolKindLint
	Bash            ID = domain.ToolKindBash
	ExecCommand     ID = domain.ToolKindExecCommand
	ExecStatus      ID = domain.ToolKindExecStatus
	ExecList        ID = domain.ToolKindExecList
	ExecWriteStdin  ID = domain.ToolKindExecWriteStdin
	ExecResize      ID = domain.ToolKindExecResize
	ExecTerminate   ID = domain.ToolKindExecTerminate
	ExecCleanup     ID = domain.ToolKindExecCleanup
	FileEdit        ID = domain.ToolKindFileEdit
	FileWrite       ID = domain.ToolKindFileWrite
	Task            ID = domain.ToolKindTask
	Question        ID = domain.ToolKindQuestion
	UpdatePlan      ID = domain.ToolKindUpdatePlan
	MilestoneList   ID = domain.ToolKindMilestoneList
	MilestoneAdd    ID = domain.ToolKindMilestoneAdd
	MilestoneUpdate ID = domain.ToolKindMilestoneUpdate
	MilestonePlan   ID = domain.ToolKindMilestonePlan
	MilestoneWrite  ID = domain.ToolKindMilestoneWrite
	TaskList        ID = domain.ToolKindTaskList
	TaskAddItems    ID = domain.ToolKindTaskAddItems
	TaskUpdateItem  ID = domain.ToolKindTaskUpdateItem
	TaskFetchNext   ID = domain.ToolKindTaskFetchNext
	TasksAdd        ID = domain.ToolKindTasksAdd
	TasksUpdate     ID = domain.ToolKindTasksUpdate
	ChatList        ID = domain.ToolKindChatList
	ChatStart       ID = domain.ToolKindChatStart
	ChatSend        ID = domain.ToolKindChatSend
	ChatCancel      ID = domain.ToolKindChatCancel
	ChatArchive     ID = domain.ToolKindChatArchive
	ChatRename      ID = domain.ToolKindChatRename
	ChatCleanup     ID = domain.ToolKindChatCleanup
	Skill           ID = domain.ToolKindSkill
	WebFetch        ID = domain.ToolKindWebFetch
	WebSearch       ID = domain.ToolKindWebSearch
	MCP             ID = domain.ToolKindMCP
)

func BuiltinIDs() []ID {
	return domain.BuiltinToolKinds()
}

func IsBuiltinID(id ID) bool {
	return domain.IsBuiltinToolKind(id)
}
