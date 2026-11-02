package tools

import (
	"time"

	"github.com/lkarlslund/koder/internal/execruntime"
)

// ExecListFromSnapshots keeps agent-facing listings and live context consistent.
func ExecListFromSnapshots(snaps []execruntime.Snapshot, scope, message string) ExecListStoredResult {
	items := make([]ExecListStoredItem, 0, len(snaps))
	for _, snap := range snaps {
		started := ""
		if !snap.StartedAt.IsZero() {
			started = snap.StartedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, ExecListStoredItem{
			ProcessID: snap.ProcessID, Command: snap.Command, State: string(snap.State), TTY: snap.TTY, ExitCode: snap.ExitCode,
			PID: snap.PID, ChatID: snap.ChatID, Description: snap.Label, Workdir: snap.Workdir, StartedAt: started, ElapsedMS: snap.Elapsed.Milliseconds(), Resources: snap.Resources,
		})
	}
	return ExecListStoredResult{Scope: scope, Message: message, Items: items}
}
