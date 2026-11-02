package execruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/fsutil"
	"github.com/lkarlslund/koder/internal/id"
)

// RestartJournal records jobs owned by the previous Koder process, never handles
// that can be reattached to the new manager.
type RestartJournal struct {
	RecordedAt time.Time
	Terminated bool
	Jobs       []Snapshot
}

// SetRestartJournal configures durable lifecycle tracking before any jobs start.
func (m *Manager) SetRestartJournal(path string) { m.journalPath = path }

func (m *Manager) PreviousJobs() (RestartJournal, error) {
	var journal RestartJournal
	if m.journalPath == "" {
		return journal, nil
	}
	data, err := os.ReadFile(m.journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return journal, nil
	}
	if err != nil {
		return journal, err
	}
	err = json.Unmarshal(data, &journal)
	return journal, err
}

func (m *Manager) ClearPreviousJobs() error {
	if m.journalPath == "" {
		return nil
	}
	err := os.Remove(m.journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (m *Manager) writeJournalLocked(journal RestartJournal) error {
	if m.journalPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.journalPath), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(m.journalPath, data, 0600)
}

func (m *Manager) runningJobs() ([]*process, []Snapshot) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var processes []*process
	var jobs []Snapshot
	for _, p := range m.processes {
		snap := p.snapshot(1)
		if snap.State != StateRunning {
			continue
		}
		snap.Output = ""
		snap.Stream = nil
		processes = append(processes, p)
		jobs = append(jobs, snap)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ProcessID < jobs[j].ProcessID })
	return processes, jobs
}

func (m *Manager) recordRunningJobs() error {
	m.journalMu.Lock()
	defer m.journalMu.Unlock()
	if m.journalFrozen || m.journalPath == "" {
		return nil
	}
	_, jobs := m.runningJobs()
	return m.writeJournalLocked(RestartJournal{RecordedAt: time.Now().UTC(), Jobs: jobs})
}

// Shutdown records and stops jobs after chat runtimes have drained. Keeping the
// final journal lets the next process explain exactly what shutdown interrupted.
func (m *Manager) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	m.startMu.Lock()
	defer m.startMu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.journalMu.Lock()
	defer m.journalMu.Unlock()
	m.journalFrozen = true
	processes, jobs := m.runningJobs()
	journal := RestartJournal{RecordedAt: time.Now().UTC(), Jobs: jobs}
	writeErr := m.writeJournalLocked(journal)
	var stopErr error
	for _, p := range processes {
		p.requestStop()
		stopErr = errors.Join(stopErr, terminateProcess(p))
	}
	for _, p := range processes {
		select {
		case <-p.done:
		case <-ctx.Done():
			return errors.Join(writeErr, stopErr, ctx.Err())
		}
	}
	journal.Terminated = stopErr == nil
	return errors.Join(writeErr, stopErr, m.writeJournalLocked(journal))
}

func (j RestartJournal) ChatNotice(chatID id.ID) string {
	var jobs []Snapshot
	for _, job := range j.Jobs {
		if job.ChatID == chatID {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return ""
	}
	var text strings.Builder
	text.WriteString("Koder was restarted. ")
	if j.Terminated {
		text.WriteString("The following running processes owned by this chat were terminated during shutdown:\n")
	} else {
		text.WriteString("The following processes were recorded as running in the previous Koder instance. Their exec sessions were lost during restart; their OS process exit could not be confirmed:\n")
	}
	text.WriteString("Previous runtime recorded at: " + j.RecordedAt.Format(time.RFC3339) + "\n")
	for _, job := range jobs {
		fmt.Fprintf(&text, "- %s", job.ProcessID)
		if job.Label != "" {
			fmt.Fprintf(&text, " — %s", job.Label)
		}
		fmt.Fprintf(&text, "\n  command: %s\n  workdir: %s\n  started_at: %s\n", job.Command, job.Workdir, job.StartedAt.UTC().Format(time.RFC3339))
	}
	text.WriteString("These are old exec IDs from the previous Koder instance, not live handles. Check exec_session list before starting replacements, and restart only work that is still needed. Continue the original task with this interruption accounted for.")
	return text.String()
}
