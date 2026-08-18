package voice

import (
	"errors"
	"testing"
)

func TestCallLeaseAllowsExactlyOneActiveCall(t *testing.T) {
	lease := NewCallLease()
	release, err := lease.Acquire("call-1", "voice-1")
	if err != nil {
		t.Fatal(err)
	}
	active, ok := lease.Snapshot()
	if !ok || active.CallID != "call-1" || active.VoiceSessionID != "voice-1" || active.StartedAt.IsZero() {
		t.Fatalf("active call = %#v, %v", active, ok)
	}
	if _, err := lease.Acquire("call-2", "voice-2"); !errors.Is(err, ErrCallActive) {
		t.Fatalf("second acquire error = %v", err)
	}
	release()
	release()
	secondRelease, err := lease.Acquire("call-2", "voice-2")
	if err != nil {
		t.Fatal(err)
	}
	secondRelease()
	if _, ok := lease.Snapshot(); ok {
		t.Fatal("lease remained active after release")
	}
}
