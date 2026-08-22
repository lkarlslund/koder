package tools

import "github.com/lkarlslund/koder/internal/domain"

type ID = domain.ToolKind

const (
	FileRead            ID = domain.ToolKindFileRead
	ViewImage           ID = domain.ToolKindViewImage
	ShowImage           ID = domain.ToolKindShowImage
	ShowMedia           ID = domain.ToolKindShowMedia
	OfferFile           ID = domain.ToolKindOfferFile
	FileGlob            ID = domain.ToolKindFileGlob
	FileGrep            ID = domain.ToolKindFileGrep
	CodeSearch          ID = domain.ToolKindCodeSearch
	Lint                ID = domain.ToolKindLint
	Bash                ID = domain.ToolKindBash
	ExecCommand         ID = domain.ToolKindExecCommand
	ExecSession         ID = domain.ToolKindExecSession
	ExecStatus          ID = domain.ToolKindExecStatus
	ExecList            ID = domain.ToolKindExecList
	ExecWriteStdin      ID = domain.ToolKindExecWriteStdin
	ExecResize          ID = domain.ToolKindExecResize
	ExecTerminate       ID = domain.ToolKindExecTerminate
	ExecCleanup         ID = domain.ToolKindExecCleanup
	FileEdit            ID = domain.ToolKindFileEdit
	FileWrite           ID = domain.ToolKindFileWrite
	Task                ID = domain.ToolKindTask
	Question            ID = domain.ToolKindQuestion
	RequestUserInput    ID = domain.ToolKindRequestUserInput
	UpdatePlan          ID = domain.ToolKindUpdatePlan
	Milestones          ID = domain.ToolKindMilestones
	MilestoneList       ID = domain.ToolKindMilestoneList
	MilestoneAdd        ID = domain.ToolKindMilestoneAdd
	MilestoneUpdate     ID = domain.ToolKindMilestoneUpdate
	MilestoneDepend     ID = domain.ToolKindMilestoneDepend
	MilestoneArchive    ID = domain.ToolKindMilestoneArchive
	MilestoneDelete     ID = domain.ToolKindMilestoneDelete
	MilestonePlan       ID = domain.ToolKindMilestonePlan
	MilestoneWrite      ID = domain.ToolKindMilestoneWrite
	Tasks               ID = domain.ToolKindTasks
	TaskList            ID = domain.ToolKindTaskList
	TaskAddItems        ID = domain.ToolKindTaskAddItems
	TaskUpdateItem      ID = domain.ToolKindTaskUpdateItem
	TaskFetchNext       ID = domain.ToolKindTaskFetchNext
	TaskArchive         ID = domain.ToolKindTaskArchive
	TaskDelete          ID = domain.ToolKindTaskDelete
	TasksAdd            ID = domain.ToolKindTasksAdd
	TasksUpdate         ID = domain.ToolKindTasksUpdate
	ChatList            ID = domain.ToolKindChatList
	ChatStart           ID = domain.ToolKindChatStart
	ChatSend            ID = domain.ToolKindChatSend
	ChatCancel          ID = domain.ToolKindChatCancel
	ChatArchive         ID = domain.ToolKindChatArchive
	ChatRename          ID = domain.ToolKindChatRename
	ChatCleanup         ID = domain.ToolKindChatCleanup
	ChatStatus          ID = domain.ToolKindChatStatus
	SessionList         ID = domain.ToolKindSessionList
	SessionDelegate     ID = domain.ToolKindSessionDelegate
	SessionStart        ID = domain.ToolKindSessionStart
	Phone               ID = domain.ToolKindPhone
	PhonePhotosSearch   ID = domain.ToolKindPhonePhotosSearch
	PhonePhotosThumbs   ID = domain.ToolKindPhonePhotosThumbs
	PhonePhotoView      ID = domain.ToolKindPhonePhotoView
	PhonePhotoTransfer  ID = domain.ToolKindPhonePhotoTransfer
	Present             ID = domain.ToolKindPresent
	Skill               ID = domain.ToolKindSkill
	WebFetch            ID = domain.ToolKindWebFetch
	WebSearch           ID = domain.ToolKindWebSearch
	MCP                 ID = domain.ToolKindMCP
	BrowserStatus       ID = domain.ToolKindBrowserStatus
	BrowserTabs         ID = domain.ToolKindBrowserTabs
	BrowserNavigation   ID = domain.ToolKindBrowserNavigation
	BrowserPage         ID = domain.ToolKindBrowserPage
	BrowserInteract     ID = domain.ToolKindBrowserInteract
	BrowserCapture      ID = domain.ToolKindBrowserCapture
	BrowserNetwork      ID = domain.ToolKindBrowserNetwork
	BrowserTabList      ID = domain.ToolKindBrowserTabList
	BrowserTabNew       ID = domain.ToolKindBrowserTabNew
	BrowserTabClaim     ID = domain.ToolKindBrowserTabClaim
	BrowserTabSelect    ID = domain.ToolKindBrowserTabSelect
	BrowserTabClose     ID = domain.ToolKindBrowserTabClose
	BrowserNavigate     ID = domain.ToolKindBrowserNavigate
	BrowserBack         ID = domain.ToolKindBrowserBack
	BrowserForward      ID = domain.ToolKindBrowserForward
	BrowserReload       ID = domain.ToolKindBrowserReload
	BrowserSnapshot     ID = domain.ToolKindBrowserSnapshot
	BrowserFind         ID = domain.ToolKindBrowserFind
	BrowserClick        ID = domain.ToolKindBrowserClick
	BrowserFill         ID = domain.ToolKindBrowserFill
	BrowserType         ID = domain.ToolKindBrowserType
	BrowserPress        ID = domain.ToolKindBrowserPress
	BrowserSelect       ID = domain.ToolKindBrowserSelect
	BrowserCheck        ID = domain.ToolKindBrowserCheck
	BrowserUncheck      ID = domain.ToolKindBrowserUncheck
	BrowserHover        ID = domain.ToolKindBrowserHover
	BrowserDrag         ID = domain.ToolKindBrowserDrag
	BrowserScroll       ID = domain.ToolKindBrowserScroll
	BrowserWait         ID = domain.ToolKindBrowserWait
	BrowserUpload       ID = domain.ToolKindBrowserUpload
	BrowserEvaluate     ID = domain.ToolKindBrowserEvaluate
	BrowserScreenshot   ID = domain.ToolKindBrowserScreenshot
	BrowserImage        ID = domain.ToolKindBrowserImage
	BrowserPDF          ID = domain.ToolKindBrowserPDF
	BrowserConsole      ID = domain.ToolKindBrowserConsole
	BrowserRequests     ID = domain.ToolKindBrowserRequests
	BrowserRequest      ID = domain.ToolKindBrowserRequest
	BrowserResponseBody ID = domain.ToolKindBrowserResponseBody
	BrowserDownloads    ID = domain.ToolKindBrowserDownloads
	BrowserDownloadsOld ID = domain.ToolKindBrowserDownloadsOld
	BrowserDownload     ID = domain.ToolKindBrowserDownload
)

func BuiltinIDs() []ID {
	return domain.BuiltinToolKinds()
}

func IsBuiltinID(id ID) bool {
	return domain.IsBuiltinToolKind(id)
}
