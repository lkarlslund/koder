package chatinteraction

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
)

func TestTurnInstructionsFollowTheSurface(t *testing.T) {
	if TurnInstruction(SurfaceWeb) != "" {
		t.Fatal("web turns got instructions")
	}
	voice, text := TurnInstruction(SurfacePhoneVoice), TurnInstruction(SurfacePhoneText)
	if !strings.Contains(voice, "spoken aloud") || !strings.Contains(text, "short and to the point") {
		t.Fatalf("voice = %q\ntext = %q", voice, text)
	}
	// The voice style no longer claims the chat's role; any chat can be spoken to.
	if strings.Contains(voice, "orchestrator") {
		t.Fatalf("voice instructions describe a role: %q", voice)
	}
}

func TestPhoneTurnsCannotAskForWebInput(t *testing.T) {
	for _, source := range []string{domain.UserMessageSourceVoice, domain.UserMessageSourcePhone} {
		if AllowsTool(ModeForSource(source), domain.ToolKindRequestUserInput) {
			t.Fatalf("%s turn allows request_user_input", source)
		}
		if !AllowsTool(ModeForSource(source), domain.ToolKindChatStart) {
			t.Fatalf("%s turn denies chat_start", source)
		}
	}
	if !AllowsTool(ModeForSource(domain.UserMessageSourceUser), domain.ToolKindRequestUserInput) {
		t.Fatal("web turn denies request_user_input")
	}
}
