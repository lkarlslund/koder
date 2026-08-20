package voice

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrCallActive is returned when another voice call is already active for the
// same connected device.
var ErrCallActive = errors.New("another voice chat is already active on this device")

// ActiveCall identifies one device's current call.
type ActiveCall struct {
	DeviceID       string    `json:"device_id,omitempty"`
	CallID         string    `json:"call_id"`
	VoiceSessionID string    `json:"voice_session_id,omitempty"`
	StartedAt      time.Time `json:"started_at"`
}

type callLeaseEntry struct {
	active     ActiveCall
	generation uint64
	replaced   chan struct{}
}

// CallLease permits one active voice call per device while allowing different
// devices to use Koder concurrently.
type CallLease struct {
	mu      sync.Mutex
	devices map[string]*callLeaseEntry
}

// NewCallLease creates an unowned per-device call lease registry.
func NewCallLease() *CallLease {
	return &CallLease{devices: make(map[string]*callLeaseEntry)}
}

// Acquire owns the legacy, unidentified-device lease until release runs.
func (l *CallLease) Acquire(callID, voiceSessionID string) (func(), error) {
	release, _, err := l.AcquireConnection(callID, voiceSessionID)
	return release, err
}

// AcquireConnection owns the legacy, unidentified-device lease. New protocol
// handlers should use AcquireDeviceConnection.
func (l *CallLease) AcquireConnection(callID, voiceSessionID string) (release func(), replaced <-chan struct{}, err error) {
	return l.AcquireDeviceConnection("legacy", callID, voiceSessionID)
}

// AcquireDeviceConnection owns one device's lease until release runs.
// Reconnecting the same logical call atomically replaces its old socket.
func (l *CallLease) AcquireDeviceConnection(deviceID, callID, voiceSessionID string) (release func(), replaced <-chan struct{}, err error) {
	if l == nil {
		return nil, nil, errors.New("voice call lease is unavailable")
	}
	deviceID = strings.TrimSpace(deviceID)
	callID = strings.TrimSpace(callID)
	if deviceID == "" {
		return nil, nil, errors.New("voice device id is required")
	}
	if callID == "" {
		return nil, nil, errors.New("voice call id is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.devices == nil {
		l.devices = make(map[string]*callLeaseEntry)
	}
	entry := l.devices[deviceID]
	if entry != nil && entry.active.CallID != callID {
		return nil, nil, ErrCallActive
	}
	if entry == nil {
		entry = &callLeaseEntry{}
		l.devices[deviceID] = entry
	}
	if entry.replaced != nil {
		close(entry.replaced)
	}
	entry.generation++
	generation := entry.generation
	replacedSignal := make(chan struct{})
	entry.replaced = replacedSignal
	entry.active = ActiveCall{
		DeviceID:       deviceID,
		CallID:         callID,
		VoiceSessionID: strings.TrimSpace(voiceSessionID),
		StartedAt:      time.Now().UTC(),
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			current := l.devices[deviceID]
			if current != nil && current.generation == generation {
				delete(l.devices, deviceID)
			}
		})
	}, replacedSignal, nil
}

// Snapshots returns every active device call, oldest first.
func (l *CallLease) Snapshots() []ActiveCall {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ActiveCall, 0, len(l.devices))
	for _, entry := range l.devices {
		out = append(out, entry.active)
	}
	slices.SortFunc(out, func(a, b ActiveCall) int { return a.StartedAt.Compare(b.StartedAt) })
	return out
}

// Snapshot returns the oldest active call for compatibility with callers that
// only need to know whether any voice connection exists.
func (l *CallLease) Snapshot() (ActiveCall, bool) {
	active := l.Snapshots()
	if len(active) == 0 {
		return ActiveCall{}, false
	}
	return active[0], true
}

// ActiveCount returns the number of devices with a live voice connection.
func (l *CallLease) ActiveCount() int { return len(l.Snapshots()) }

// Owns reports whether callID currently holds any device lease.
func (l *CallLease) Owns(callID string) bool {
	callID = strings.TrimSpace(callID)
	for _, active := range l.Snapshots() {
		if active.CallID == callID {
			return true
		}
	}
	return false
}

// OwnsDevice reports whether callID belongs to the specified device lease.
func (l *CallLease) OwnsDevice(deviceID, callID string) bool {
	if l == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	callID = strings.TrimSpace(callID)
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.devices[deviceID]
	return entry != nil && entry.active.CallID == callID
}
