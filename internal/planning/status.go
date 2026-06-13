package planning

//go:generate go tool enumer -type=LegacyTaskStatus,MilestoneStatus,TaskStatus -trimprefix=LegacyTaskStatus,MilestoneStatus,TaskStatus -transform=snake -json -text -values -output=status_enumer.go

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
