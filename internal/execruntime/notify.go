package execruntime

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/id"
)

// NotifyMode says what an exec session reports to its chat on its own after
// exec_command has returned.
type NotifyMode string

const (
	// NotifyExit reports the exit once, unless a tool result already showed it.
	NotifyExit NotifyMode = "exit"
	// NotifyLines reports each batch of new output lines, then the exit.
	NotifyLines NotifyMode = "lines"
)

// ParseNotifyMode reads a notify argument; empty means NotifyExit.
func ParseNotifyMode(value string) (NotifyMode, error) {
	switch NotifyMode(strings.TrimSpace(value)) {
	case "", NotifyExit:
		return NotifyExit, nil
	case NotifyLines:
		return NotifyLines, nil
	default:
		return "", fmt.Errorf("notify must be %q or %q", NotifyExit, NotifyLines)
	}
}

// NotificationKind distinguishes output events from the final exit report.
type NotificationKind string

const (
	NotificationExit  NotificationKind = "exit"
	NotificationLines NotificationKind = "lines"
)

// Notification is a report an exec session sends to its chat.
type Notification struct {
	SessionID id.ID
	ChatID    id.ID
	ProcessID string
	Kind      NotificationKind
	Text      string
}

const (
	// Lines-mode sessions expire so a forgotten watch does not run forever.
	DefaultLinesTimeout = 5 * time.Minute
	MaxLinesTimeout     = 30 * time.Minute

	exitReportBytes = 4 * 1024
)

// SetNotifier sets where exec sessions send their reports.
func (m *Manager) SetNotifier(notify func(Notification)) {
	m.mu.Lock()
	m.notifier = notify
	m.mu.Unlock()
}

func (m *Manager) notify(n Notification) {
	m.mu.RLock()
	notify := m.notifier
	m.mu.RUnlock()
	if notify != nil {
		notify(n)
	}
}

// beginModelWait marks a tool call that may show the process's exit, so the
// exit is not also reported on its own while the call is in flight.
func (p *process) beginModelWait() {
	p.mu.Lock()
	p.modelWaits++
	p.mu.Unlock()
}

// endModelWait records whether the tool result showed the exit.
func (p *process) endModelWait(snap Snapshot) {
	p.mu.Lock()
	p.modelWaits--
	if snap.State != StateRunning {
		p.exitShown = true
	}
	p.mu.Unlock()
}

// reportExitIfDue sends the exit report once the process has ended, unless a
// tool result already showed the exit, a tool call that may show it is still
// in flight, or the process was stopped on request.
func (m *Manager) reportExitIfDue(p *process) {
	p.mu.Lock()
	due := p.state != StateRunning && p.startReturned && !p.exitShown && !p.exitReported &&
		!p.stopRequested && p.modelWaits == 0 && !p.lost
	if due {
		p.exitReported = true
	}
	p.mu.Unlock()
	if !due {
		return
	}
	// Exit happens after the output readers finish, so every line has been
	// delivered or is waiting in the monitor.
	var finalLines string
	if p.monitor != nil {
		finalLines = p.monitor.close()
	}
	snap := p.drainSnapshot(exitReportBytes)
	m.notify(Notification{
		SessionID: p.sessionID,
		ChatID:    p.chatID,
		ProcessID: p.processID,
		Kind:      NotificationExit,
		Text:      exitReportText(p, snap, finalLines),
	})
}

// ExitReportsPending returns nil when no session in the chat may still report
// its exit. Otherwise the channel receives once all of them have ended: true
// when at least one sent a report, false when each ended without one, for
// example because a tool result showed the exit or it was stopped on request.
func (m *Manager) ExitReportsPending(sessionID, chatID id.ID) <-chan bool {
	m.mu.RLock()
	var pending []*process
	for _, p := range m.processes {
		if p.sessionID == sessionID && p.chatID == chatID && p.mayReportExit() {
			pending = append(pending, p)
		}
	}
	m.mu.RUnlock()
	if len(pending) == 0 {
		return nil
	}
	result := make(chan bool, 1)
	go func() {
		reported := false
		for _, p := range pending {
			<-p.settled
			p.mu.RLock()
			reported = reported || p.exitReported
			p.mu.RUnlock()
		}
		result <- reported
	}()
	return result
}

func (p *process) mayReportExit() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state == StateRunning && p.startReturned && !p.exitShown && !p.stopRequested && !p.lost
}

func exitReportText(p *process, snap Snapshot, finalLines string) string {
	p.mu.RLock()
	label, endReason, monitor := p.label, p.endReason, p.monitor
	p.mu.RUnlock()
	var b strings.Builder
	b.WriteString("event: exec session ended\n")
	writeSessionFields(&b, snap.ProcessID, label, snap.Command)
	state := string(snap.State)
	if endReason != "" {
		state += " (" + endReason + ")"
	}
	b.WriteString("state: " + state + "\n")
	if snap.ExitCode != nil {
		b.WriteString("exit_code: " + strconv.Itoa(*snap.ExitCode) + "\n")
	}
	if !snap.StartedAt.IsZero() && !snap.EndedAt.IsZero() {
		b.WriteString("runtime: " + snap.EndedAt.Sub(snap.StartedAt).Round(time.Second).String() + "\n")
	}
	output := snap.Output
	if monitor != nil {
		delivered, suppressed := monitor.counts()
		b.WriteString("events_delivered: " + strconv.Itoa(delivered) + "\n")
		if suppressed > 0 {
			b.WriteString("events_suppressed: " + strconv.Itoa(suppressed) + "\n")
		}
		output = finalLines
	}
	b.WriteString("output:\n")
	if strings.TrimSpace(output) == "" {
		b.WriteString("(none)")
	} else {
		b.WriteString(strings.TrimRight(output, "\n"))
	}
	return b.String()
}

func linesEventText(p *process, lines string, suppressedBefore int) string {
	p.mu.RLock()
	label, processID, command := p.label, p.processID, p.command
	p.mu.RUnlock()
	var b strings.Builder
	b.WriteString("event: exec session output\n")
	writeSessionFields(&b, processID, label, command)
	if suppressedBefore > 0 {
		b.WriteString("events_suppressed_before: " + strconv.Itoa(suppressedBefore) + " (output rate too high)\n")
	}
	b.WriteString("output:\n")
	b.WriteString(lines)
	return b.String()
}

func writeSessionFields(b *strings.Builder, processID, label, command string) {
	b.WriteString("process_id: " + processID + "\n")
	if label != "" {
		b.WriteString("description: " + label + "\n")
	} else {
		b.WriteString("command: " + firstLine(command) + "\n")
	}
}

func firstLine(text string) string {
	line, rest, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if rest != "" {
		return line + " …"
	}
	return line
}
