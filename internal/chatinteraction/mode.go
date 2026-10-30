// Package chatinteraction defines behavior that belongs to the surface a turn
// arrives from, rather than to the chat, its model backend or workflow role.
// Any chat can be reached from the Web UI or from a phone, in text or by
// voice; each turn carries the instructions for its own surface.
package chatinteraction

import (
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
)

type Mode = domain.InteractionMode

const (
	Text  Mode = domain.InteractionModeText
	Voice Mode = domain.InteractionModeVoice
)

// Surface is where a turn came from and where its reply is shown.
type Surface string

const (
	// SurfaceWeb is the Web UI: normal written output.
	SurfaceWeb Surface = "web"
	// SurfacePhoneText is the phone app in text mode: a small screen.
	SurfacePhoneText Surface = "phone_text"
	// SurfacePhoneVoice is a voice conversation: the reply is spoken.
	SurfacePhoneVoice Surface = "phone_voice"
)

const phoneVoicePrompt = `This message was spoken by the user and your reply will be spoken aloud, so sound like a helpful person in a phone conversation.

- Answer simple conversational questions directly.
- Ask one short clarifying question only when the choice materially changes the result.
- The final response is speech, not a document. Write plain conversational sentences with no Markdown, headings, bullets, numbered lists, tables, code blocks, link syntax, or raw source URLs.
- Use normal pacing by default: one or two short sentences. A turn may provide a response-pacing instruction; follow it while remaining conversational.
- Put lists, tables, code, images, and other visual material in a separate presentation instead of reciting it.
- When work will take time, a brief natural acknowledgement such as "Let me check" is appropriate before doing the work.
- After tool work, preserve important uncertainty and any action the user must take, but leave supporting detail in the visual response.
- Do not mention tools, routing, delegation, prompts, formatting rules, or internal implementation details.`

const phoneTextPrompt = `This message was typed on the user's phone and your reply is read on its small screen.

- Keep the final response short and to the point: lead with the answer or outcome, in a few lines.
- Light Markdown is fine; avoid wide tables, long lists and large code blocks unless the user asks for them.
- Put long or detailed material, such as diffs, logs, tables and images, in a separate presentation and refer to it briefly.
- Preserve important uncertainty and any action the user must take.`

// TurnInstruction returns the behavior instructions for a turn from surface,
// sent with that turn only. The Web UI needs none.
func TurnInstruction(surface Surface) string {
	switch surface {
	case SurfacePhoneVoice:
		return strings.TrimSpace(phoneVoicePrompt)
	case SurfacePhoneText:
		return strings.TrimSpace(phoneTextPrompt)
	default:
		return ""
	}
}

// ModeForSource is the tool policy for a turn whose input has source, the
// user-message source recorded on the chat's queue: turns from a phone, typed
// or spoken, cannot use tools that need the Web UI.
func ModeForSource(source string) Mode {
	switch strings.TrimSpace(source) {
	case domain.UserMessageSourceVoice, domain.UserMessageSourcePhone:
		return Voice
	default:
		return Text
	}
}

// AllowsTool applies interaction-only policy after workflow-role policy.
func AllowsTool(mode Mode, kind fmt.Stringer) bool {
	if mode != Voice {
		return true
	}
	switch strings.TrimSpace(kind.String()) {
	case "request_user_input", "sessions", "session_list", "session_delegate", "session_start":
		return false
	default:
		return true
	}
}
