package provider

import (
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
)

type InstructionKind string

const (
	InstructionKindBaseSystem          InstructionKind = "base_system"
	InstructionKindProjectInstructions InstructionKind = "project_instructions"
	InstructionKindSkills              InstructionKind = "skills"
	InstructionKindSessionNote         InstructionKind = "session_note"
	InstructionKindContinuation        InstructionKind = "continuation"
)

type InstructionBlock struct {
	Kind      InstructionKind
	Text      string
	Ephemeral bool
}

type PromptEnvelope struct {
	Instructions []InstructionBlock
	Items        []Message
}

func SerializePromptEnvelope(env PromptEnvelope) []Message {
	out := make([]Message, 0, len(env.Items)+1)
	if joined := joinInstructionBlocks(env.Instructions); joined != "" {
		out = append(out, Message{
			Role:    domain.MessageRoleSystem,
			Content: joined,
		})
	}
	for _, item := range env.Items {
		if item.Role == domain.MessageRoleSystem || (item.Role == "" && strings.TrimSpace(item.Content) == "" && len(item.ContentParts) == 0 && len(item.ToolCalls) == 0) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func joinInstructionBlocks(blocks []InstructionBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
