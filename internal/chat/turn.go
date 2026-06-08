package chat

//go:generate go tool enumer -type=Turn -trimprefix=Turn -transform=snake -json -text -values -output=turn_enumer.go

// Turn describes the chat-owned turn phase.
type Turn uint8

const (
	TurnIdle Turn = iota
	TurnQueued
	TurnPreparing
	TurnCompacting
	TurnWaitingLLM
	TurnStreaming
	TurnRunningTools
	TurnWaitingApproval
	TurnCancelling
	TurnErrored
)

func turnForStatus(status Status, active bool, cancelState CancelState) Turn {
	if cancelState == CancelStateCancelling {
		return TurnCancelling
	}
	switch status {
	case StatusWaitingLLM:
		if active {
			return TurnWaitingLLM
		}
	case StatusStreamingThoughts, StatusStreamingResponse:
		return TurnStreaming
	case StatusRunningTools:
		return TurnRunningTools
	case StatusWaitingApproval:
		return TurnWaitingApproval
	case StatusErrored:
		return TurnErrored
	}
	if active {
		return TurnPreparing
	}
	return TurnIdle
}
