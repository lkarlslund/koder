package planning

//go:generate go tool enumer -type=TaskStatus,MilestoneStatus,TodoStatus -trimprefix=TaskStatus,MilestoneStatus,TodoStatus -transform=snake -json -text -values -output=status_enumer.go

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
