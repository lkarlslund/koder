package domain

import "testing"

func TestVoiceQueuedInputRetainsTranscriptSource(t *testing.T) {
	item := QueuedInput{Origin: QueuedInputOriginUser, Source: UserMessageSourceVoice}
	if got := UserMessageSourceForQueuedInput(item); got != UserMessageSourceVoice {
		t.Fatalf("source = %q, want %q", got, UserMessageSourceVoice)
	}
}
