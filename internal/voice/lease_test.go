package voice

import (
	"errors"
	"testing"
)

func TestCallLeaseAllowsOneActiveCallPerDevice(t *testing.T) {
	lease := NewCallLease()
	release, _, err := lease.AcquireDeviceConnection("phone-1", "call-1", "voice-1")
	if err != nil {
		t.Fatal(err)
	}
	active, ok := lease.Snapshot()
	if !ok || active.DeviceID != "phone-1" || active.CallID != "call-1" || active.VoiceSessionID != "voice-1" || active.StartedAt.IsZero() {
		t.Fatalf("active call = %#v, %v", active, ok)
	}
	if _, _, err := lease.AcquireDeviceConnection("phone-1", "call-2", "voice-2"); !errors.Is(err, ErrCallActive) {
		t.Fatalf("second acquire error = %v", err)
	}
	otherRelease, _, err := lease.AcquireDeviceConnection("phone-2", "call-2", "voice-2")
	if err != nil || lease.ActiveCount() != 2 {
		t.Fatalf("parallel device acquire: count=%d err=%v", lease.ActiveCount(), err)
	}
	if !lease.OwnsDevice("phone-1", "call-1") || lease.OwnsDevice("phone-2", "call-1") {
		t.Fatal("device ownership was not isolated")
	}
	otherRelease()
	release()
	release()
	secondRelease, _, err := lease.AcquireDeviceConnection("phone-1", "call-2", "voice-2")
	if err != nil {
		t.Fatal(err)
	}
	secondRelease()
	if _, ok := lease.Snapshot(); ok {
		t.Fatal("lease remained active after release")
	}
}

func TestCallLeaseReconnectReplacesSameOwner(t *testing.T) {
	lease := NewCallLease()
	oldRelease, oldReplaced, err := lease.AcquireConnection("call-1", "voice-1")
	if err != nil {
		t.Fatal(err)
	}
	newRelease, newReplaced, err := lease.AcquireConnection("call-1", "voice-1")
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	select {
	case <-oldReplaced:
	default:
		t.Fatal("previous connection was not notified of replacement")
	}
	select {
	case <-newReplaced:
		t.Fatal("new connection was incorrectly marked replaced")
	default:
	}

	oldRelease()
	if active, ok := lease.Snapshot(); !ok || active.CallID != "call-1" {
		t.Fatalf("old release cleared replacement: %#v, %v", active, ok)
	}
	newRelease()
	if _, ok := lease.Snapshot(); ok {
		t.Fatal("lease remained active after replacement release")
	}
}
