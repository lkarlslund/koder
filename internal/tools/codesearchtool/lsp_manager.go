package codesearchtool

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type lspManager struct {
	mu              sync.Mutex
	clients         map[lspKey]*managedLSP
	idleTimeout     time.Duration
	sweepInterval   time.Duration
	shutdownTimeout time.Duration
	wake            chan struct{}
	done            chan struct{}
	closed          bool
	started         bool
}

type lspKey struct {
	Root     string
	Language string
}

type managedLSP struct {
	key      lspKey
	server   languageServer
	client   *lspClient
	refs     int
	lastUsed time.Time
	starting bool
	ready    chan struct{}
	err      error
}

type lspLease struct {
	manager *lspManager
	entry   *managedLSP
}

func newLSPManager(idleTimeout, sweepInterval, shutdownTimeout time.Duration) *lspManager {
	return &lspManager{
		clients:         map[lspKey]*managedLSP{},
		idleTimeout:     idleTimeout,
		sweepInterval:   sweepInterval,
		shutdownTimeout: shutdownTimeout,
		wake:            make(chan struct{}, 1),
		done:            make(chan struct{}),
	}
}

func (m *lspManager) query(ctx context.Context, rootAbs string, server languageServer, options searchOptions) (lspResult, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		lease, err := m.acquire(ctx, rootAbs, server)
		if err != nil {
			return lspResult{}, err
		}
		result, err := queryClient(ctx, lease.client(), rootAbs, server, options)
		bad := err != nil && !isLSPServerError(err)
		lease.release(bad)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !bad {
			break
		}
	}
	return lspResult{}, lastErr
}

func (m *lspManager) acquire(ctx context.Context, rootAbs string, server languageServer) (*lspLease, error) {
	key := lspKey{Root: rootAbs, Language: server.ID}
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errors.New("language server manager is closed")
		}
		m.startReaperLocked()
		if entry := m.clients[key]; entry != nil {
			if entry.starting {
				ready := entry.ready
				m.mu.Unlock()
				select {
				case <-ready:
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if entry.err != nil {
				delete(m.clients, key)
				m.mu.Unlock()
				continue
			}
			entry.refs++
			entry.lastUsed = time.Now()
			m.mu.Unlock()
			return &lspLease{manager: m, entry: entry}, nil
		}

		entry := &managedLSP{
			key:      key,
			server:   server,
			refs:     1,
			lastUsed: time.Now(),
			starting: true,
			ready:    make(chan struct{}),
		}
		m.clients[key] = entry
		m.mu.Unlock()

		client, err := startLSP(rootAbs, server)
		if err == nil {
			err = client.initialize(ctx, rootAbs)
		}
		m.mu.Lock()
		if err != nil {
			delete(m.clients, key)
			entry.err = err
		} else {
			entry.client = client
		}
		entry.starting = false
		close(entry.ready)
		m.mu.Unlock()
		if err != nil {
			if client != nil {
				client.closeWithTimeout(m.shutdownTimeout)
			}
			return nil, err
		}
		return &lspLease{manager: m, entry: entry}, nil
	}
}

func (m *lspManager) startReaperLocked() {
	if m.started {
		return
	}
	m.started = true
	go m.reapLoop()
}

func (m *lspManager) reapLoop() {
	ticker := time.NewTicker(m.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.reapIdle()
		case <-m.wake:
			m.reapIdle()
		case <-m.done:
			return
		}
	}
}

func (m *lspManager) reapIdle() {
	now := time.Now()
	var expired []*lspClient
	m.mu.Lock()
	for key, entry := range m.clients {
		if entry.starting || entry.client == nil || entry.refs > 0 {
			continue
		}
		if now.Sub(entry.lastUsed) >= m.idleTimeout {
			expired = append(expired, entry.client)
			delete(m.clients, key)
		}
	}
	m.mu.Unlock()
	for _, client := range expired {
		client.closeWithTimeout(m.shutdownTimeout)
	}
}

func (m *lspManager) close() {
	var clients []*lspClient
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	for key, entry := range m.clients {
		if entry.client != nil {
			clients = append(clients, entry.client)
		}
		delete(m.clients, key)
	}
	started := m.started
	m.mu.Unlock()
	if started {
		close(m.done)
	}
	for _, client := range clients {
		client.closeWithTimeout(m.shutdownTimeout)
	}
}

func (l *lspLease) client() *lspClient {
	return l.entry.client
}

func (l *lspLease) release(discard bool) {
	var closeClient *lspClient
	l.manager.mu.Lock()
	if l.entry.refs > 0 {
		l.entry.refs--
	}
	l.entry.lastUsed = time.Now()
	if discard {
		delete(l.manager.clients, l.entry.key)
		closeClient = l.entry.client
	} else if l.entry.refs == 0 {
		l.manager.signalReaperLocked()
	}
	l.manager.mu.Unlock()
	if closeClient != nil {
		closeClient.closeWithTimeout(l.manager.shutdownTimeout)
	}
}

func (m *lspManager) signalReaperLocked() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

type lspServerError struct {
	message string
}

func (e lspServerError) Error() string {
	return e.message
}

func isLSPServerError(err error) bool {
	var serverErr lspServerError
	return errors.As(err, &serverErr)
}

func missingCommand(server languageServer) string {
	if len(server.Command) == 0 {
		return ""
	}
	if _, err := exec.LookPath(server.Command[0]); err != nil {
		return fmt.Sprintf("%s: %s requires command %q", server.ID, server.Title, server.Command[0])
	}
	return ""
}

func commandString(server languageServer) string {
	return strings.Join(server.Command, " ")
}
