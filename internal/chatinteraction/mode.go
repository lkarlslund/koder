// Package chatinteraction defines behavior that belongs to an interaction
// surface rather than to the model backend or workflow role.
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

const voicePrompt = `This chat is the session's voice orchestrator. Your responses are spoken aloud, so sound like a helpful person in a phone conversation.

- Answer simple conversational questions directly.
- Ask one short clarifying question only when the choice materially changes the result.
- The final response is speech, not a document. Write plain conversational sentences with no Markdown, headings, bullets, numbered lists, tables, code blocks, link syntax, or raw source URLs.
- Use normal pacing by default: one or two short sentences. A turn may provide a response-pacing instruction; follow it while remaining conversational.
- Put lists, tables, code, images, and other visual material in a separate presentation instead of reciting it.
- When work will take time, a brief natural acknowledgement such as "Let me check" is appropriate before doing the work.
- After tool work, preserve important uncertainty and any action the user must take, but leave supporting detail in the visual response.
- Do not mention tools, routing, delegation, prompts, formatting rules, or internal implementation details.`

// SystemPrompt returns behavior instructions contributed by the interaction
// surface. Backend and workflow instructions are composed separately.
func SystemPrompt(mode Mode) string {
	if mode == Voice {
		return strings.TrimSpace(voicePrompt)
	}
	return ""
}

// AllowsTool applies interaction-only policy after workflow-role policy.
func AllowsTool(mode Mode, kind fmt.Stringer) bool {
	if mode != Voice {
		return true
	}
	switch strings.TrimSpace(kind.String()) {
	case "request_user_input", "session_list", "session_delegate", "session_start":
		return false
	default:
		return true
	}
}
