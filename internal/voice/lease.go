package voice

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrCallActive is returned when another voice call owns the process-wide lease.
var ErrCallActive = errors.New("another voice call is already active")

// ActiveCall identifies the call currently holding the process-wide lease.
type ActiveCall struct {
	CallID         string    `json:"call_id"`
	VoiceSessionID string    `json:"voice_session_id,omitempty"`
	StartedAt      time.Time `json:"started_at"`
}

// CallLease enforces the initial one-active-voice-call invariant.
type CallLease struct {
	mu         sync.Mutex
	active     ActiveCall
	generation uint64
	replaced   chan struct{}
}

// NewCallLease creates an unowned process-wide call lease.
func NewCallLease() *CallLease {
	return &CallLease{}
}

// Acquire owns the lease until the returned idempotent release function runs.
func (l *CallLease) Acquire(callID, voiceSessionID string) (func(), error) {
	release, _, err := l.AcquireConnection(callID, voiceSessionID)
	return release, err
}

// AcquireConnection owns the lease until release runs. Reconnecting with the
// same call ID atomically replaces the previous connection; replaced is closed
// so the previous handler can stop without releasing the new owner's lease.
func (l *CallLease) AcquireConnection(callID, voiceSessionID string) (release func(), replaced <-chan struct{}, err error) {
	if l == nil {
		return nil, nil, errors.New("voice call lease is unavailable")
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil, nil, errors.New("voice call id is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active.CallID != "" && l.active.CallID != callID {
		return nil, nil, ErrCallActive
	}
	if l.replaced != nil {
		close(l.replaced)
	}
	l.generation++
	generation := l.generation
	replacedSignal := make(chan struct{})
	l.replaced = replacedSignal
	l.active = ActiveCall{
		CallID:         callID,
		VoiceSessionID: strings.TrimSpace(voiceSessionID),
		StartedAt:      time.Now().UTC(),
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.generation == generation {
				l.active = ActiveCall{}
				l.replaced = nil
			}
		})
	}, replacedSignal, nil
}

// Snapshot returns the active call, if any.
func (l *CallLease) Snapshot() (ActiveCall, bool) {
	if l == nil {
		return ActiveCall{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active, l.active.CallID != ""
}
