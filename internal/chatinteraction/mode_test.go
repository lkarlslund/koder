package chatinteraction

import (
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
)

func TestVoiceInteractionPromptAndPolicy(t *testing.T) {
	if SystemPrompt(Voice) == "" {
		t.Fatal("voice prompt is empty")
	}
	if AllowsTool(Voice, domain.ToolKindRequestUserInput) {
		t.Fatal("voice interaction allows request_user_input")
	}
	if !AllowsTool(Voice, domain.ToolKindChatStart) {
		t.Fatal("voice interaction denies chat_start")
	}
	if !AllowsTool(Text, domain.ToolKindRequestUserInput) {
		t.Fatal("text interaction unexpectedly denies request_user_input")
	}
}
