package voiceapi

import (
	"strings"
	"testing"
)

func TestTurnRegistryAllowsParallelDevicesButOneUtterancePerCall(t *testing.T) {
	registry := newTurnRegistry()
	release := make(chan struct{})
	first, created, err := registry.start("phone-call-1", "utterance-1", "one", "", "session-1", "voice-1", func(turn *cachedTurn) {
		<-release
		turn.finish(nil)
	})
	if err != nil || !created || first == nil {
		t.Fatalf("first turn = %#v, created=%v err=%v", first, created, err)
	}
	second, created, err := registry.start("phone-call-2", "utterance-2", "two", "", "session-2", "voice-2", func(turn *cachedTurn) {
		<-release
		turn.finish(nil)
	})
	if err != nil || !created || second == nil || second == first {
		t.Fatalf("parallel turn = %#v, created=%v err=%v", second, created, err)
	}
	if _, _, err := registry.start("phone-call-1", "utterance-new", "three", "", "session-1", "voice-1", func(*cachedTurn) {}); err == nil || !strings.Contains(err.Error(), "still active") {
		t.Fatalf("same-call concurrent turn error = %v", err)
	}
	close(release)
}
