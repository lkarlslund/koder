package execruntime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permissionprofile"
	"github.com/lkarlslund/koder/internal/sandbox"
)

const (
	defaultRows          = 24
	defaultCols          = 80
	defaultTailBytes     = 64 * 1024
	defaultPreviewBytes  = 4 * 1024
	defaultSubscriberCap = 32
)

type State string

const (
	StateRunning    State = "running"
	StateCompleted  State = "completed"
	StateFailed     State = "failed"
	StateTerminated State = "terminated"
	StateLost       State = "lost"
)

type EventKind string

const (
	EventKindOutput EventKind = "output"
	EventKindState  EventKind = "state"
)

type Scope string

const (
	ScopeChat    Scope = "chat"
	ScopeSession Scope = "session"
)

type TerminalSize struct {
	Rows int
	Cols int
}

type StartRequest struct {
	SessionID      domain.ID
	ChatID         domain.ID
	ToolCallID     string
	Command        string
	Workdir        string
	Shell          string
	Login          bool
	TTY            bool
	Timeout        time.Duration
	YieldTime      time.Duration
	PreviewBytes   int
	SandboxProfile permissionprofile.Profile
}

type StatusRequest struct {
	SessionID domain.ID
	ChatID    domain.ID
	ProcessID string
	MaxBytes  int
}

type ListRequest struct {
	SessionID domain.ID
	ChatID    domain.ID
	Scope     Scope
	MaxBytes  int
}

type WriteStdinRequest struct {
	SessionID  domain.ID
	ChatID     domain.ID
	ProcessID  string
	Chars      string
	CloseStdin bool
	MaxBytes   int
}

type ResizeRequest struct {
	SessionID domain.ID
	ChatID    domain.ID
	ProcessID string
	Size      TerminalSize
	MaxBytes  int
}

type TerminateRequest struct {
	SessionID domain.ID
	ChatID    domain.ID
	ProcessID string
	MaxBytes  int
}

type CleanupRequest struct {
	SessionID domain.ID
	ChatID    domain.ID
	Scope     Scope
	MaxBytes  int
}

type Snapshot struct {
	ProcessID   string
	SessionID   domain.ID
	ChatID      domain.ID
	ToolCallID  string
	Command     string
	Workdir     string
	Shell       string
	TTY         bool
	State       State
	ExitCode    *int
	StartedAt   time.Time
	EndedAt     time.Time
	TimeoutMS   int64
	Output      string
	OutputBytes int
	StdinClosed bool
	Lost        bool
}

type Event struct {
	Kind     EventKind
	Snapshot Snapshot
	Delta    string
}

type Control interface {
	Start(context.Context, StartRequest) (Snapshot, error)
	Status(context.Context, StatusRequest) (Snapshot, error)
	List(context.Context, ListRequest) ([]Snapshot, error)
	WriteStdin(context.Context, WriteStdinRequest) (Snapshot, error)
	Resize(context.Context, ResizeRequest) (Snapshot, error)
	Terminate(context.Context, TerminateRequest) (Snapshot, error)
	Cleanup(context.Context, CleanupRequest) ([]Snapshot, error)
}

type Manager struct {
	mu          sync.RWMutex
	nextID      uint64
	processes   map[string]*process
	subscribers map[domain.ID]map[chan Event]struct{}
}

type process struct {
	mu          sync.RWMutex
	processID   string
	sessionID   domain.ID
	chatID      domain.ID
	toolCallID  string
	command     string
	workdir     string
	shell       string
	tty         bool
	timeout     time.Duration
	startedAt   time.Time
	endedAt     time.Time
	state       State
	exitCode    *int
	stdinClosed bool
	lost        bool
	output      string
	outputBytes int
	proc        *exec.Cmd
	ptyHandle   *os.File
	stdin       io.WriteCloser
	done        chan struct{}
	once        sync.Once
}

func NewManager() *Manager {
	return &Manager{
		processes:   map[string]*process{},
		subscribers: map[domain.ID]map[chan Event]struct{}{},
	}
}

func (m *Manager) Subscribe(chatID domain.ID) (<-chan Event, func()) {
	ch := make(chan Event, defaultSubscriberCap)
	m.mu.Lock()
	if m.subscribers[chatID] == nil {
		m.subscribers[chatID] = map[chan Event]struct{}{}
	}
	m.subscribers[chatID][ch] = struct{}{}
	m.mu.Unlock()
	cancel := func() {
		m.mu.Lock()
		if set := m.subscribers[chatID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(m.subscribers, chatID)
			}
		}
		close(ch)
		m.mu.Unlock()
	}
	return ch, cancel
}

func (m *Manager) Start(ctx context.Context, req StartRequest) (Snapshot, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return Snapshot{}, errors.New("command is empty")
	}
	if req.SessionID == "" || req.ChatID == "" {
		return Snapshot{}, errors.New("session_id and chat_id are required")
	}
	shell, args, err := shellArgs(req.Shell, req.Login, command)
	if err != nil {
		return Snapshot{}, err
	}
	executable, wrappedArgs, err := sandbox.WrapCommand(sandbox.Command{
		Executable: shell,
		Args:       args,
		Workdir:    req.Workdir,
		Profile:    req.SandboxProfile,
	})
	if err != nil {
		return Snapshot{}, err
	}
	cmd := exec.CommandContext(context.Background(), executable, wrappedArgs...)
	cmd.Dir = strings.TrimSpace(req.Workdir)
	cmd.Env = nil
	p := &process{
		processID:  m.nextProcessID(),
		sessionID:  req.SessionID,
		chatID:     req.ChatID,
		toolCallID: strings.TrimSpace(req.ToolCallID),
		command:    command,
		workdir:    cmd.Dir,
		shell:      shell,
		tty:        req.TTY,
		timeout:    req.Timeout,
		startedAt:  time.Now().UTC(),
		state:      StateRunning,
		proc:       cmd,
		done:       make(chan struct{}),
	}
	if req.TTY {
		size := normalizeSize(TerminalSize{Rows: defaultRows, Cols: defaultCols})
		ws := &pty.Winsize{Rows: uint16(size.Rows), Cols: uint16(size.Cols)}
		ptmx, err := pty.StartWithSize(cmd, ws)
		if err != nil {
			return Snapshot{}, err
		}
		p.ptyHandle = ptmx
		p.stdin = ptmx
	} else {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return Snapshot{}, err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return Snapshot{}, err
		}
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return Snapshot{}, err
		}
		p.stdin = stdin
		if err := cmd.Start(); err != nil {
			return Snapshot{}, err
		}
		go m.readOutput(p, stdout)
		go m.readOutput(p, stderr)
	}
	if req.TTY {
		go m.readOutput(p, p.ptyHandle)
	}

	m.mu.Lock()
	m.processes[p.processID] = p
	m.mu.Unlock()
	go m.waitForExit(p)
	if req.Timeout > 0 {
		go m.enforceTimeout(p, req.Timeout)
	}
	if wait := normalizeWait(req.YieldTime); wait > 0 {
		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		case <-p.done:
		case <-time.After(wait):
		}
	}
	snap := p.snapshot(req.PreviewBytes)
	m.publish(Event{Kind: EventKindState, Snapshot: snap})
	return snap, nil
}

func (m *Manager) Status(_ context.Context, req StatusRequest) (Snapshot, error) {
	p, err := m.lookup(req.SessionID, req.ChatID, req.ProcessID)
	if err != nil {
		return Snapshot{}, err
	}
	return p.snapshot(req.MaxBytes), nil
}

func (m *Manager) List(_ context.Context, req ListRequest) ([]Snapshot, error) {
	scope := req.Scope
	if scope == "" {
		scope = ScopeChat
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var snaps []Snapshot
	for _, p := range m.processes {
		if !matchesScope(p, req.SessionID, req.ChatID, scope) {
			continue
		}
		snaps = append(snaps, p.snapshot(req.MaxBytes))
	}
	sort.Slice(snaps, func(i, j int) bool {
		if snaps[i].StartedAt.Equal(snaps[j].StartedAt) {
			return snaps[i].ProcessID < snaps[j].ProcessID
		}
		return snaps[i].StartedAt.Before(snaps[j].StartedAt)
	})
	return snaps, nil
}

func (m *Manager) WriteStdin(_ context.Context, req WriteStdinRequest) (Snapshot, error) {
	p, err := m.lookup(req.SessionID, req.ChatID, req.ProcessID)
	if err != nil {
		return Snapshot{}, err
	}
	p.mu.Lock()
	if p.state != StateRunning {
		p.mu.Unlock()
		return Snapshot{}, errors.New("process is not running")
	}
	if p.stdin == nil {
		p.mu.Unlock()
		return Snapshot{}, errors.New("stdin is not available for this process")
	}
	if req.Chars != "" {
		if _, err := io.WriteString(p.stdin, req.Chars); err != nil {
			p.mu.Unlock()
			return Snapshot{}, err
		}
	}
	if req.CloseStdin && !p.stdinClosed {
		p.stdinClosed = true
		if err := p.stdin.Close(); err != nil {
			p.mu.Unlock()
			return Snapshot{}, err
		}
	}
	p.mu.Unlock()
	return p.snapshot(req.MaxBytes), nil
}

func (m *Manager) Resize(_ context.Context, req ResizeRequest) (Snapshot, error) {
	p, err := m.lookup(req.SessionID, req.ChatID, req.ProcessID)
	if err != nil {
		return Snapshot{}, err
	}
	p.mu.Lock()
	if !p.tty || p.ptyHandle == nil {
		p.mu.Unlock()
		return Snapshot{}, errors.New("process is not running in a tty")
	}
	size := normalizeSize(req.Size)
	if err := pty.Setsize(p.ptyHandle, &pty.Winsize{Rows: uint16(size.Rows), Cols: uint16(size.Cols)}); err != nil {
		p.mu.Unlock()
		return Snapshot{}, err
	}
	p.mu.Unlock()
	return p.snapshot(req.MaxBytes), nil
}

func (m *Manager) Terminate(_ context.Context, req TerminateRequest) (Snapshot, error) {
	p, err := m.lookup(req.SessionID, req.ChatID, req.ProcessID)
	if err != nil {
		return Snapshot{}, err
	}
	if err := terminateProcess(p); err != nil {
		return Snapshot{}, err
	}
	return p.snapshot(req.MaxBytes), nil
}

func (m *Manager) Cleanup(_ context.Context, req CleanupRequest) ([]Snapshot, error) {
	scope := req.Scope
	if scope == "" {
		scope = ScopeChat
	}
	m.mu.RLock()
	var matches []*process
	for _, p := range m.processes {
		if matchesScope(p, req.SessionID, req.ChatID, scope) {
			matches = append(matches, p)
		}
	}
	m.mu.RUnlock()
	var snaps []Snapshot
	for _, p := range matches {
		if p.snapshot(0).State == StateRunning {
			_ = terminateProcess(p)
		}
		snaps = append(snaps, p.snapshot(req.MaxBytes))
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].ProcessID < snaps[j].ProcessID })
	return snaps, nil
}

func (m *Manager) readOutput(p *process, r io.Reader) {
	reader := bufio.NewReader(r)
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			delta := string(buf[:n])
			p.appendOutput(delta)
			m.publish(Event{Kind: EventKindOutput, Snapshot: p.snapshot(defaultPreviewBytes), Delta: delta})
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) waitForExit(p *process) {
	err := p.proc.Wait()
	p.mu.Lock()
	if p.state == StateRunning {
		exitCode := 0
		state := StateCompleted
		if err != nil {
			state = StateFailed
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		if p.state == StateTerminated {
			state = StateTerminated
		}
		p.exitCode = intPtr(exitCode)
		p.state = state
		p.endedAt = time.Now().UTC()
	}
	p.closeDone()
	p.mu.Unlock()
	m.publish(Event{Kind: EventKindState, Snapshot: p.snapshot(defaultPreviewBytes)})
}

func (m *Manager) enforceTimeout(p *process, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-timer.C:
		_ = terminateProcess(p)
	case <-p.done:
	}
}

func (m *Manager) lookup(sessionID, chatID domain.ID, processID string) (*process, error) {
	m.mu.RLock()
	p := m.processes[strings.TrimSpace(processID)]
	m.mu.RUnlock()
	if p == nil {
		return nil, fmt.Errorf("process %q not found", processID)
	}
	if p.sessionID != sessionID || p.chatID != chatID {
		return nil, errors.New("process is not available in this chat")
	}
	return p, nil
}

func (m *Manager) nextProcessID() string {
	id := atomic.AddUint64(&m.nextID, 1)
	return "exec_" + strconv.FormatUint(id, 10)
}

func (m *Manager) publish(evt Event) {
	m.mu.Lock()
	subs := m.subscribers[evt.Snapshot.ChatID]
	for ch := range subs {
		select {
		case ch <- evt:
		default:
		}
	}
	m.mu.Unlock()
}

func (p *process) appendOutput(delta string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.output += delta
	p.outputBytes += len(delta)
	if len(p.output) > defaultTailBytes {
		p.output = p.output[len(p.output)-defaultTailBytes:]
	}
}

func (p *process) snapshot(maxBytes int) Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	output := p.output
	if maxBytes <= 0 {
		maxBytes = defaultPreviewBytes
	}
	if len(output) > maxBytes {
		output = output[len(output)-maxBytes:]
	}
	return Snapshot{
		ProcessID:   p.processID,
		SessionID:   p.sessionID,
		ChatID:      p.chatID,
		ToolCallID:  p.toolCallID,
		Command:     p.command,
		Workdir:     p.workdir,
		Shell:       p.shell,
		TTY:         p.tty,
		State:       p.state,
		ExitCode:    cloneIntPtr(p.exitCode),
		StartedAt:   p.startedAt,
		EndedAt:     p.endedAt,
		TimeoutMS:   p.timeout.Milliseconds(),
		Output:      output,
		OutputBytes: p.outputBytes,
		StdinClosed: p.stdinClosed,
		Lost:        p.lost,
	}
}

func (p *process) closeDone() {
	p.once.Do(func() {
		if p.stdin != nil && !p.stdinClosed {
			_ = p.stdin.Close()
			p.stdinClosed = true
		}
		if p.ptyHandle != nil {
			_ = p.ptyHandle.Close()
		}
		close(p.done)
	})
}

func shellArgs(requested string, login bool, command string) (string, []string, error) {
	shell := strings.TrimSpace(requested)
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "cmd"
		} else {
			shell = "bash"
		}
	}
	path, err := exec.LookPath(shell)
	if err != nil {
		return "", nil, fmt.Errorf("resolve shell %q: %w", shell, err)
	}
	if runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(path), "cmd.exe") {
		return path, []string{"/C", command}, nil
	}
	if login {
		return path, []string{"-lc", command}, nil
	}
	return path, []string{"-c", command}, nil
}

func terminateProcess(p *process) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateRunning || p.proc == nil || p.proc.Process == nil {
		return nil
	}
	p.state = StateTerminated
	p.endedAt = time.Now().UTC()
	p.exitCode = intPtr(-1)
	if runtime.GOOS != "windows" {
		if err := p.proc.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		go func() {
			select {
			case <-time.After(3 * time.Second):
				_ = p.proc.Process.Kill()
			case <-p.done:
			}
		}()
		return nil
	}
	return p.proc.Process.Kill()
}

func matchesScope(p *process, sessionID, chatID domain.ID, scope Scope) bool {
	switch scope {
	case ScopeSession:
		return p.sessionID == sessionID
	default:
		return p.sessionID == sessionID && p.chatID == chatID
	}
}

func normalizeWait(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func normalizeSize(size TerminalSize) TerminalSize {
	if size.Rows <= 0 {
		size.Rows = defaultRows
	}
	if size.Cols <= 0 {
		size.Cols = defaultCols
	}
	return size
}

func intPtr(v int) *int {
	return &v
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}
