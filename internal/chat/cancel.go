package chat

import (
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
)

type CancelReason string

const (
	CancelReasonUserInterrupt         CancelReason = "UserInterrupt"
	CancelReasonUserInterruptHard     CancelReason = "UserInterruptHard"
	CancelReasonShutdownInterrupt     CancelReason = "ShutdownInterrupt"
	CancelReasonShutdownInterruptHard CancelReason = "ShutdownInterruptHard"
	CancelReasonRestartInterrupt      CancelReason = "RestartInterrupt"
	CancelReasonRestartInterruptHard  CancelReason = "RestartInterruptHard"
)

func ParseCancelReason(raw string) (CancelReason, bool) {
	switch strings.TrimSpace(raw) {
	case string(CancelReasonUserInterrupt):
		return CancelReasonUserInterrupt, true
	case string(CancelReasonUserInterruptHard):
		return CancelReasonUserInterruptHard, true
	case string(CancelReasonShutdownInterrupt):
		return CancelReasonShutdownInterrupt, true
	case string(CancelReasonShutdownInterruptHard):
		return CancelReasonShutdownInterruptHard, true
	case string(CancelReasonRestartInterrupt):
		return CancelReasonRestartInterrupt, true
	case string(CancelReasonRestartInterruptHard):
		return CancelReasonRestartInterruptHard, true
	default:
		return "", false
	}
}

func (r CancelReason) Hard() bool {
	switch r {
	case CancelReasonUserInterruptHard, CancelReasonShutdownInterruptHard, CancelReasonRestartInterruptHard:
		return true
	default:
		return false
	}
}

func (r CancelReason) Restart() bool {
	return r == CancelReasonRestartInterrupt || r == CancelReasonRestartInterruptHard
}

func (r CancelReason) NoticeReason() string {
	switch r {
	case CancelReasonUserInterrupt, CancelReasonUserInterruptHard:
		return domain.NoticeReasonUserInterrupted
	case CancelReasonShutdownInterrupt, CancelReasonShutdownInterruptHard:
		return domain.NoticeReasonProcessTerminating
	case CancelReasonRestartInterrupt, CancelReasonRestartInterruptHard:
		return domain.NoticeReasonProcessRestart
	default:
		return ""
	}
}
