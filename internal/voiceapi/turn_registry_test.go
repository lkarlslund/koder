package voiceapi

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/voice"
)

type codedTurnError struct{}

func (codedTurnError) Error() string           { return "choose another model" }
func (codedTurnError) ClientErrorCode() string { return "model_unavailable" }

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

func TestCachedTurnStreamsGenericRenderEvent(t *testing.T) {
	turn := newCachedTurn("call", "utterance", "fingerprint", "voice")
	turn.appendRender([]voice.Part{{MIMEType: "image/png", URI: "/artifact.png"}})
	events := turn.snapshot().events
	if len(events) != 1 || events[0].frame == nil || events[0].frame.Type != "render" || len(events[0].frame.Parts) != 1 {
		t.Fatalf("render events = %#v", events)
	}
}

func TestCachedTurnPreservesClientErrorCode(t *testing.T) {
	turn := newCachedTurn("call", "utterance", "fingerprint", "voice")
	turn.finish(codedTurnError{})
	events := turn.snapshot().events
	if len(events) != 1 || events[0].frame == nil {
		t.Fatalf("error events = %#v", events)
	}
	frame := events[0].frame
	if frame.Type != "error" || frame.ErrorCode != "model_unavailable" || frame.Error != "choose another model" {
		t.Fatalf("error frame = %#v", frame)
	}
}
