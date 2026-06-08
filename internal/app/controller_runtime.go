package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/tools"
)

func (c *Controller) execManagerLocked() *execruntime.Manager {
	if c == nil || c.agent == nil {
		return nil
	}
	return c.agent.ExecManager()
}

func (c *Controller) snapshotWithExecProcessesLocked(snapshot chat.Snapshot) chat.Snapshot {
	manager := c.execManagerLocked()
	if manager == nil || snapshot.Session.ID == "" || snapshot.Chat.ID == "" {
		return snapshot
	}
	processes, err := manager.List(context.Background(), execruntime.ListRequest{
		SessionID: snapshot.Session.ID,
		ChatID:    snapshot.Chat.ID,
		Scope:     execruntime.ScopeChat,
		MaxBytes:  16 * 1024,
	})
	if err != nil {
		return snapshot
	}
	snapshot.ExecProcesses = execProcessesFromSnapshots(processes)
	return snapshot
}

func execProcessesFromSnapshots(snapshots []execruntime.Snapshot) []tools.ExecProcess {
	out := make([]tools.ExecProcess, 0, len(snapshots))
	for _, snapshot := range snapshots {
		out = append(out, tools.ExecProcess{
			ProcessID:   snapshot.ProcessID,
			Command:     snapshot.Command,
			Workdir:     snapshot.Workdir,
			Shell:       snapshot.Shell,
			TTY:         snapshot.TTY,
			State:       string(snapshot.State),
			ExitCode:    snapshot.ExitCode,
			TimeoutMS:   snapshot.TimeoutMS,
			Output:      snapshot.Output,
			OutputBytes: snapshot.OutputBytes,
			StdinClosed: snapshot.StdinClosed,
			Lost:        snapshot.Lost,
		})
	}
	return out
}

// TerminateExecProcessForSelection stops a running exec process owned by the selected chat.
func (c *Controller) TerminateExecProcessForSelection(ctx context.Context, selection Selection, processID string) (tools.ExecProcess, error) {
	if c == nil {
		return tools.ExecProcess{}, fmt.Errorf("controller is nil")
	}
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return tools.ExecProcess{}, fmt.Errorf("process id is required")
	}
	if selection.SessionID == "" {
		return tools.ExecProcess{}, fmt.Errorf("session id is required")
	}
	if selection.ChatID == "" {
		return tools.ExecProcess{}, fmt.Errorf("chat id is required")
	}
	c.mu.Lock()
	manager := c.execManagerLocked()
	c.mu.Unlock()
	if manager == nil {
		return tools.ExecProcess{}, fmt.Errorf("exec manager is unavailable")
	}
	snap, err := manager.Terminate(ctx, execruntime.TerminateRequest{
		SessionID: selection.SessionID,
		ChatID:    selection.ChatID,
		ProcessID: processID,
		MaxBytes:  16 * 1024,
	})
	if err != nil {
		return tools.ExecProcess{}, err
	}
	processes := execProcessesFromSnapshots([]execruntime.Snapshot{snap})
	process := tools.ExecProcess{}
	if len(processes) > 0 {
		process = processes[0]
	}
	if c.agent != nil {
		owner, err := c.agent.LoadSession(ctx, selection.SessionID)
		if err == nil {
			event, ok := c.eventForSelectedExec(ctx, owner, selection)
			if ok {
				c.broadcast(event.Type, event.Payload)
			}
		}
	}
	return process, nil
}
