package toolruntime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/tools"
)

// RunningExecContext is regenerated for each model request, outside persisted
// history and compaction. Append it after history so prompt prefixes stay stable.
func (r *Runtime) RunningExecContext(ctx context.Context, session domain.Session, chat domain.Chat) string {
	if r == nil || r.exec == nil || session.ID == "" {
		return ""
	}
	snaps, err := r.exec.List(ctx, execruntime.ListRequest{SessionID: session.ID, ChatID: chat.ID, Scope: execruntime.ScopeSession, MaxBytes: 1})
	if err != nil {
		return "Live running-exec inventory is unavailable. Check exec_session list before starting expensive work."
	}
	running := make([]execruntime.Snapshot, 0, len(snaps))
	for _, snap := range snaps {
		if snap.State == execruntime.StateRunning {
			running = append(running, snap)
		}
	}
	if len(running) == 0 {
		return ""
	}
	sort.SliceStable(running, func(i, j int) bool {
		var left, right float64
		if running[i].Resources != nil {
			left = running[i].Resources.CPUPercent
		}
		if running[j].Resources != nil {
			right = running[j].Resources.CPUPercent
		}
		return left > right
	})
	total := len(running)
	if len(running) > 16 {
		running = running[:16]
	}
	listing := tools.DisplayTextForStored(tools.ExecList, tools.ExecListFromSnapshots(running, "session", ""))
	const limit = 12000
	if len(listing) > limit {
		listing = strings.ToValidUTF8(listing[:limit], "") + "\n(inventory truncated; use exec_session list scope=session)"
	}
	return fmt.Sprintf("<running_exec_sessions>\nLive runtime state, not a new user request. %d command(s) are still running in this session (showing up to 16). These jobs survive the end of a turn; do not start them again because their output is empty or history was compacted. Inspect, reuse, wait for, or terminate existing work before starting a replacement. Avoid overlapping CPU-heavy work. Only the owning chat can directly inspect or terminate a job; coordinate with that chat for work owned by a sibling. Use exec_session list scope=session for the complete inventory. CPU is a lifetime average, not an instantaneous sample.\n%s\n</running_exec_sessions>", total, strings.TrimSpace(listing))
}
