package toolkind

//go:generate go tool enumer -type=Kind -trimprefix=ToolKind -transform=snake -json -text -values -output=kind_enumer.go
type Kind uint8

const (
	ToolKindFileRead Kind = iota + 1
	ToolKindViewImage
	ToolKindShowImage
	ToolKindFileGlob
	ToolKindFileGrep
	ToolKindCodeSearch
	ToolKindLint
	ToolKindBash
	ToolKindExecCommand
	ToolKindExecStatus
	ToolKindExecList
	ToolKindExecWriteStdin
	ToolKindExecResize
	ToolKindExecTerminate
	ToolKindExecCleanup
	ToolKindFileEdit
	ToolKindFileWrite
	ToolKindTask
	ToolKindQuestion
	ToolKindUpdatePlan
	ToolKindMilestoneList
	ToolKindMilestoneAdd
	ToolKindMilestoneUpdate
	ToolKindMilestonePlan
	ToolKindMilestoneWrite
	ToolKindTaskList
	ToolKindTaskAddItems
	ToolKindTaskUpdateItem
	ToolKindTaskFetchNext
	ToolKindTasksAdd
	ToolKindTasksUpdate
	ToolKindChatList
	ToolKindChatStart
	ToolKindChatSend
	ToolKindChatCancel
	ToolKindChatArchive
	ToolKindChatRename
	ToolKindSkill
	ToolKindWebFetch
	ToolKindWebSearch
	ToolKindMCP
)
