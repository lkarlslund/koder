package modelruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/assets"
	"github.com/lkarlslund/koder/internal/attachment"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/environment"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/skills"
	"github.com/lkarlslund/koder/internal/tools"
)

func (r *Runtime) BuildConversationForTurn(_ context.Context, req chatpkg.TurnRequest) ([]provider.Message, error) {
	envelope, err := r.BuildPromptEnvelopeForTimeline(req.Session, req.Chat, req.Timeline, "", nil, nil, nil)
	if err != nil {
		return nil, err
	}
	return provider.SerializePromptEnvelope(envelope), nil
}

func (r *Runtime) BuildPromptEnvelopeForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem, prompt string, drafts []attachment.Draft, refs []reference.Draft, turnInstructions []provider.InstructionBlock) (provider.PromptEnvelope, error) {
	baseInstructions := r.baseInstructionsForChat(session, chat)
	envelope := provider.PromptEnvelope{Instructions: baseInstructions}
	segmentStart := 0
	for idx, item := range timeline {
		if compacted, ok := item.Content.(domain.Compaction); ok {
			if strings.TrimSpace(compacted.Summary) == "" {
				continue
			}
			if !validCompactionBoundary(timeline[segmentStart:idx], compacted.FirstKeptItemID) {
				continue
			}
			envelope.Instructions = baseInstructions
			envelope.Items = append(envelope.Items[:0], compactedHistoryMessage(compacted.Summary))
			if segmentStart < idx {
				preserved, err := r.timelineMessagesForCompactionTail(session, chat, timeline[segmentStart:idx], compacted.FirstKeptItemID)
				if err != nil {
					return provider.PromptEnvelope{}, err
				}
				envelope.Items = append(envelope.Items, preserved...)
			}
			segmentStart = idx + 1
			continue
		}
		messages, err := r.ConversationMessagesForTimelineItem(session, chat, item, r.preserveThinkingEnabled(chat))
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		envelope.Items = append(envelope.Items, messages...)
	}
	for _, msg := range previewTurnInstructionMessages(turnInstructions) {
		envelope.Items = append(envelope.Items, msg)
	}
	if strings.TrimSpace(prompt) != "" || len(drafts) > 0 {
		msg, ok, err := r.previewUserMessage(session, prompt, drafts, refs)
		if err != nil {
			return provider.PromptEnvelope{}, err
		}
		if ok {
			envelope.Items = append(envelope.Items, msg)
		}
	}
	return envelope, nil
}

func (r *Runtime) EstimateContextTokensForTimeline(session domain.Session, chat domain.Chat, timeline []domain.TimelineItem) (int, error) {
	envelope, err := r.BuildPromptEnvelopeForTimeline(session, chat, timeline, "", nil, nil, nil)
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(provider.SerializePromptEnvelope(envelope))
	if err != nil {
		return 0, err
	}
	if len(payload) == 0 {
		return 0, nil
	}
	return len(payload) / 4, nil
}

func previewTurnInstructionMessages(blocks []provider.InstructionBlock) []provider.Message {
	var out []provider.Message
	for _, block := range blocks {
		user, ok := chatpkg.TurnInstructionUserMessage(block)
		if !ok {
			continue
		}
		out = append(out, provider.Message{Role: provider.RoleUser, Content: user.Text})
	}
	return out
}

func (r *Runtime) timelineMessagesForCompactionTail(session domain.Session, chat domain.Chat, items []domain.TimelineItem, firstKeptItemID string) ([]provider.Message, error) {
	start := firstKeptTimelineIndex(items, firstKeptItemID)
	if start < 0 {
		start = preservedTimelineToolCallTailStart(items, r.compactionKeepToolCalls())
	}
	if start >= len(items) {
		return nil, nil
	}
	out := make([]provider.Message, 0, len(items)-start)
	for _, item := range items[start:] {
		messages, err := r.ConversationMessagesForTimelineItem(session, chat, item, r.preserveThinkingEnabled(chat))
		if err != nil {
			return nil, err
		}
		out = append(out, messages...)
	}
	return out, nil
}

func firstKeptTimelineIndex(items []domain.TimelineItem, firstKeptItemID string) int {
	if strings.TrimSpace(firstKeptItemID) == "" {
		return -1
	}
	for idx, item := range items {
		if item.ID == firstKeptItemID {
			return idx
		}
	}
	return -1
}

func preservedTimelineToolCallTailStart(items []domain.TimelineItem, keepCalls int) int {
	if keepCalls <= 0 || len(items) == 0 {
		return len(items)
	}
	remaining := keepCalls
	start := len(items)
	for idx := len(items) - 1; idx >= 0; idx-- {
		count := completedTimelineToolCallCount(items[idx])
		if count == 0 {
			continue
		}
		start = idx
		remaining -= count
		if remaining <= 0 {
			return idx
		}
	}
	return start
}

func completedTimelineToolCallCount(item domain.TimelineItem) int {
	message, ok := item.Content.(domain.AssistantMessage)
	if !ok {
		return 0
	}
	count := 0
	for _, tool := range message.Tools {
		if tool.Status == domain.ToolStatusDone || tool.Status == domain.ToolStatusErrored || tool.Status == domain.ToolStatusDenied || tool.Status == domain.ToolStatusCanceled {
			count++
		}
	}
	return count
}

func (r *Runtime) ConversationMessagesForTimelineItem(session domain.Session, chat domain.Chat, item domain.TimelineItem, preserveThinking bool) ([]provider.Message, error) {
	switch content := item.Content.(type) {
	case domain.UserMessage:
		parts := make([]domain.Part, 0, 1+len(content.Attachments)+len(content.References))
		if strings.TrimSpace(content.Text) != "" {
			parts = append(parts, domain.Part{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: content.Text}})
		}
		for _, attachment := range content.Attachments {
			parts = append(parts, domain.Part{Kind: domain.PartKindAttachment, Payload: domain.AttachmentPayload(attachment)})
		}
		for _, ref := range content.References {
			parts = append(parts, domain.Part{Kind: domain.PartKindReference, Payload: domain.ReferencePayload(ref)})
		}
		msg, ok, err := r.userMessageWithContext(session, parts)
		if err != nil {
			return nil, err
		}
		if ok {
			if userMessageIsSteer(content) {
				msg = wrapStructuredSteerMessage(msg)
			}
			return []provider.Message{msg}, nil
		}
		if strings.TrimSpace(content.Text) == "" {
			return nil, nil
		}
		text := content.Text
		if userMessageIsSteer(content) {
			text = steerUserMessageText(text)
		}
		return []provider.Message{{Role: provider.RoleUser, Content: text}}, nil
	case domain.AssistantMessage:
		var toolCalls []provider.ToolCall
		for _, tool := range content.Tools {
			if strings.TrimSpace(string(tool.ToolCallID)) == "" {
				return nil, fmt.Errorf("assistant item %s has tool call without id", item.ID)
			}
			toolCalls = append(toolCalls, tools.ToolCall(tools.Request{
				Tool:       tool.Tool,
				ToolCallID: string(tool.ToolCallID),
				Args:       tool.Args,
			}))
		}
		textChunks := []string{}
		reasoningChunks := []string{}
		if preserveThinking && content.Reasoning.ReplayText() != "" {
			reasoningChunks = append(reasoningChunks, content.Reasoning.ReplayText())
		}
		if strings.TrimSpace(content.Text) != "" {
			textChunks = append(textChunks, content.Text)
		}
		out := []provider.Message{{
			Role:      provider.RoleAssistant,
			Content:   assistantConversationContent(textChunks, reasoningChunks, preserveThinking),
			ToolCalls: toolCalls,
		}}
		if strings.TrimSpace(out[0].Content) == "" && len(out[0].ToolCalls) == 0 {
			out = out[:0]
		}
		for _, tool := range content.Tools {
			msg, ok := r.timelineToolResultMessage(chat, tool)
			if ok {
				out = append(out, msg)
			}
		}
		return out, nil
	case domain.Compaction:
		return nil, fmt.Errorf("compaction item %s must be handled at envelope boundary", item.ID)
	case domain.ToolExecution:
		body := ""
		if content.Result != nil {
			body = strings.TrimSpace(content.Result.Text)
		}
		if content.Error != nil {
			body = strings.TrimSpace(content.Error.Message)
		}
		if body == "" {
			return nil, nil
		}
		return []provider.Message{{Role: provider.RoleUser, Content: fmt.Sprintf("%s output:\n%s", content.Tool, body)}}, nil
	case domain.Notice:
		return nil, nil
	case domain.LintMessage:
		body := strings.TrimSpace(content.Text)
		if body == "" {
			return nil, nil
		}
		return []provider.Message{{Role: provider.RoleUser, Content: "Post-edit diagnostics:\n" + body}}, nil
	default:
		return nil, fmt.Errorf("unsupported timeline item %s content %T", item.ID, item.Content)
	}
}

func userMessageIsSteer(msg domain.UserMessage) bool {
	return msg.Delivery == domain.QueuedInputDeliveryTurnBoundary
}

func steerUserMessageText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return "User steering update:\n" +
		"<user_input>\n" + text + "\n</user_input>\n\n" +
		"Apply this update to the active turn before choosing the next action."
}

func wrapStructuredSteerMessage(msg provider.Message) provider.Message {
	if strings.TrimSpace(msg.Content) != "" {
		msg.Content = steerUserMessageText(msg.Content)
	}
	if len(msg.ContentParts) > 0 {
		wrapped := make([]provider.ContentPart, 0, len(msg.ContentParts)+2)
		wrapped = append(wrapped, provider.TextPart("User steering update:\n"))
		wrapped = append(wrapped, msg.ContentParts...)
		wrapped = append(wrapped, provider.TextPart("\n\nApply this update to the active turn before choosing the next action."))
		msg.ContentParts = wrapped
	}
	return msg
}

func (r *Runtime) timelineToolResultMessage(chat domain.Chat, tool domain.ToolCall) (provider.Message, bool) {
	if tool.Result == nil && tool.Error == nil {
		return provider.Message{}, false
	}
	status := domain.ToolResultStatusOK
	text := ""
	diff := ""
	var data any
	if tool.Result != nil {
		status = tool.Result.Status
		text = tool.Result.Text
		diff = tool.Result.Diff
		data = tool.Result.Data
	}
	if tool.Error != nil {
		status = domain.ToolResultStatusError
		text = tool.Error.Message
		data = tools.ErrorStoredResult{Message: tool.Error.Message}
	}
	part := domain.Part{
		Kind: domain.PartKindToolOutput,
		Payload: domain.ToolOutputPayload{
			Tool:       tool.Tool,
			ToolCallID: string(tool.ToolCallID),
			Args:       tool.Args,
			Status:     status,
			Text:       text,
			Diff:       diff,
			Result:     data,
		},
	}
	part.Body = part.Text()
	if imageMsg, ok := r.toolImageMessage(chat, part, string(tool.ToolCallID), text); ok {
		return imageMsg, true
	}
	body := strings.TrimSpace(part.Text())
	if formatted, ok := tools.ModelTextForPart(part, diff); ok {
		body = strings.TrimSpace(formatted)
	} else if diff != "" {
		if body != "" {
			body += "\n\nDiff:\n" + diff
		} else {
			body = "Diff:\n" + diff
		}
	}
	body = modelToolResultBody(tool, status, body)
	return provider.Message{Role: provider.RoleTool, Content: body, ToolCallID: string(tool.ToolCallID)}, true
}

func modelToolResultBody(tool domain.ToolCall, status domain.ToolResultStatus, body string) string {
	body = strings.TrimSpace(body)
	failed := status == domain.ToolResultStatusError ||
		status == domain.ToolResultStatusDenied ||
		tool.Status == domain.ToolStatusCanceled
	if !failed {
		return body
	}
	var guidance strings.Builder
	guidance.WriteString("Tool call did not succeed.\n")
	guidance.WriteString("Do not retry the same tool call with the same arguments.\n")
	guidance.WriteString("Read the error, identify what is wrong, and either fix the arguments, use a different tool, ask the user for missing information, or provide a final answer using the information already available.\n")
	if tool.Tool != "" {
		guidance.WriteString("Failed tool: ")
		guidance.WriteString(tool.Tool.String())
		guidance.WriteString("\n")
	}
	if args := strings.TrimSpace((tools.Request{Tool: tool.Tool, Args: tool.Args}).ArgumentsJSON()); args != "" && args != "{}" {
		guidance.WriteString("Failed arguments: ")
		guidance.WriteString(args)
		guidance.WriteString("\n")
	}
	if body == "" {
		return strings.TrimSpace(guidance.String())
	}
	return strings.TrimSpace(guidance.String()) + "\n\nTool error:\n" + body
}

func (r *Runtime) baseInstructionsForChat(session domain.Session, chat domain.Chat) []provider.InstructionBlock {
	instructions := []provider.InstructionBlock{{
		Kind: provider.InstructionKindBaseSystem,
		Text: r.systemPrompt(),
	}, {
		Kind: provider.InstructionKindEnvironment,
		Text: r.sessionEnvironmentPrompt(session),
	}}
	if roleText := strings.TrimSpace(chatrole.SystemPrompt(chat.WorkflowRole)); roleText != "" {
		instructions = append(instructions, provider.InstructionBlock{
			Kind: provider.InstructionKindProjectInstructions,
			Text: roleText,
		})
	}
	if agentsText := strings.TrimSpace(session.AgentsResolved); agentsText != "" {
		instructions = append(instructions, provider.InstructionBlock{
			Kind: provider.InstructionKindProjectInstructions,
			Text: "Resolved project AGENTS.md instructions:\n" + agentsText,
		})
	}
	skillOpts := skills.DiscoverOptions{UserRoots: []string{filepath.Join(r.cfg.ManagedAssetsDir(), "skills")}}
	if skillText := strings.TrimSpace(skills.PromptContextWithOptions(sessionProjectRoot(session), skillOpts)); skillText != "" {
		instructions = append(instructions, provider.InstructionBlock{
			Kind: provider.InstructionKindSkills,
			Text: skillText,
		})
	}
	return instructions
}

func compactedHistoryMessage(summary string) provider.Message {
	return provider.Message{
		Role: provider.RoleUser,
		Content: strings.TrimSpace(
			"Compacted session summary for continuation:\n" +
				summary +
				"\n\nUse this summary as replacement history for the earlier conversation. Continue the task from the preserved context instead of restarting.",
		),
	}
}

func (r *Runtime) previewUserMessage(session domain.Session, prompt string, drafts []attachment.Draft, refs []reference.Draft) (provider.Message, bool, error) {
	parts := make([]domain.Part, 0, len(drafts)+len(refs)+1)
	if strings.TrimSpace(prompt) != "" {
		parts = append(parts, domain.Part{Kind: domain.PartKindText, Payload: domain.TextPayload{Text: prompt}})
	}
	for _, draft := range drafts {
		parts = append(parts, domain.Part{
			Kind: domain.PartKindAttachment,
			Payload: domain.AttachmentPayload{
				ID: draft.ID, Name: draft.Name, MIME: draft.MIME, Path: draft.Path, Size: draft.Size, Source: draft.Source, Original: draft.Original,
			},
		})
	}
	for _, ref := range refs {
		parts = append(parts, domain.Part{
			Kind: domain.PartKindReference,
			Payload: domain.ReferencePayload{
				Kind: string(ref.Kind), Path: ref.Path, Display: ref.Display, Start: ref.Start, End: ref.End,
			},
		})
	}
	if msg, ok, err := r.userMessageWithContext(session, parts); ok || err != nil {
		return msg, ok, err
	}
	if len(parts) == 0 {
		return provider.Message{}, false, nil
	}
	return provider.Message{Role: provider.RoleUser, Content: strings.TrimSpace(prompt)}, true, nil
}

func (r *Runtime) userMessageWithContext(session domain.Session, parts []domain.Part) (provider.Message, bool, error) {
	contentParts := make([]provider.ContentPart, 0, len(parts)+1)
	imageParts := make([]provider.ContentPart, 0, len(parts))
	attachmentTextParts := make([]provider.ContentPart, 0, len(parts))
	var prompt string
	var refs []reference.Metadata
	var hasStructured bool
	for _, part := range parts {
		switch part.Kind {
		case domain.PartKindText:
			if text := strings.TrimSpace(part.Text()); text != "" {
				prompt = part.Text()
			}
		case domain.PartKindReference:
			hasStructured = true
			if payload, ok := part.Payload.(domain.ReferencePayload); ok {
				refs = append(refs, reference.Metadata{
					Kind: reference.Kind(payload.Kind), Path: payload.Path, Display: payload.Display, Start: payload.Start, End: payload.End,
				})
			}
		case domain.PartKindAttachment:
			hasStructured = true
			payload, ok := part.Payload.(domain.AttachmentPayload)
			if !ok {
				return provider.Message{}, false, fmt.Errorf("attachment part has %T payload", part.Payload)
			}
			meta := attachment.Metadata{
				ID: payload.ID, Name: payload.Name, MIME: payload.MIME, Path: payload.Path, Size: payload.Size, Source: payload.Source, Original: payload.Original,
			}
			switch attachment.ClassifyMIME(meta.MIME) {
			case attachment.KindText:
				body, err := r.files.ReadText(meta)
				if err != nil {
					return provider.Message{}, false, err
				}
				attachmentTextParts = append(attachmentTextParts, provider.TextPart("Attached file "+meta.Name+":\n"+body))
			case attachment.KindImage:
				data, err := r.files.ReadBytes(meta)
				if err != nil {
					return provider.Message{}, false, err
				}
				imageParts = append(imageParts, provider.ImagePart(meta.MIME, data))
			default:
				return provider.Message{}, false, fmt.Errorf("unsupported attachment in conversation: %s", meta.MIME)
			}
		}
	}
	contentParts = append(contentParts, imageParts...)
	if len(refs) > 0 {
		slices.SortFunc(refs, func(a, b reference.Metadata) int {
			if a.Start != b.Start {
				return a.Start - b.Start
			}
			if a.End != b.End {
				return a.End - b.End
			}
			return strings.Compare(a.Path, b.Path)
		})
		cursor := 0
		for _, ref := range refs {
			start := max(0, min(ref.Start, len(prompt)))
			end := max(start, min(ref.End, len(prompt)))
			if start > cursor {
				contentParts = append(contentParts, provider.TextPart(prompt[cursor:start]))
			}
			resolved, err := r.resolveReference(session, ref)
			if err != nil {
				return provider.Message{}, false, err
			}
			contentParts = append(contentParts, provider.TextPart(resolved))
			cursor = end
		}
		if cursor < len(prompt) {
			contentParts = append(contentParts, provider.TextPart(prompt[cursor:]))
		}
	} else if strings.TrimSpace(prompt) != "" {
		contentParts = append(contentParts, provider.TextPart(prompt))
	}
	contentParts = append(contentParts, attachmentTextParts...)
	if !hasStructured {
		return provider.Message{}, false, nil
	}
	message := provider.Message{Role: provider.RoleUser, ContentParts: contentParts}
	if len(contentParts) == 0 && strings.TrimSpace(prompt) != "" {
		message.Content = prompt
	}
	return message, true, nil
}

func (r *Runtime) resolveReference(session domain.Session, meta reference.Metadata) (string, error) {
	root := sessionProjectRoot(session)
	switch meta.Kind {
	case reference.KindFile:
		return reference.ResolveFile(root, meta)
	case reference.KindDirectory:
		return reference.ResolveDirectory(root, meta)
	default:
		return "", fmt.Errorf("unsupported reference kind %q", meta.Kind)
	}
}

func (r *Runtime) toolImageMessage(chat domain.Chat, part domain.Part, toolCallID string, body string) (provider.Message, bool) {
	stored, ok := tools.ViewImageStoredResultForPart(part)
	if !ok {
		return provider.Message{}, false
	}
	if !r.chatSupportsImageAttachments(chat) {
		return provider.Message{}, false
	}
	sourcePath := strings.TrimSpace(stored.SourcePath)
	mimeType := strings.TrimSpace(stored.MIMEType)
	if sourcePath == "" || mimeType == "" {
		return provider.Message{}, false
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil || len(data) == 0 {
		return provider.Message{}, false
	}
	contentParts := make([]provider.ContentPart, 0, 2)
	if strings.TrimSpace(body) != "" {
		contentParts = append(contentParts, provider.TextPart(body))
	}
	contentParts = append(contentParts, provider.ImagePart(mimeType, data))
	return provider.Message{Role: provider.RoleTool, ContentParts: contentParts, ToolCallID: toolCallID}, true
}

func (r *Runtime) chatSupportsImageAttachments(chat domain.Chat) bool {
	supported, err := r.caps.SupportsAttachment(chat.ProviderID, providerCfgForChat(r.cfg, chat), chat.ModelID, attachment.KindImage)
	return err == nil && supported
}

func providerCfgForChat(cfg config.Config, chat domain.Chat) config.Provider {
	if providerCfg, ok := cfg.Provider(chat.ProviderID); ok {
		return providerCfg
	}
	return config.Provider{}
}

func (r *Runtime) preserveThinkingEnabled(chat domain.Chat) bool {
	thinking, err := r.settings.Thinking(chat, r.cfg.Thinking.CavemanPrompt, true)
	if err != nil {
		return true
	}
	return thinking.PreserveThinking
}

func (r *Runtime) compactionKeepToolCalls() int {
	return config.NormalizeCompactionKeepToolCalls(r.settings.Snapshot().Compaction.KeepToolCalls)
}

func assistantConversationContent(textChunks, reasoningChunks []string, preserveThinking bool) string {
	body := strings.TrimSpace(strings.Join(textChunks, "\n\n"))
	if !preserveThinking || len(reasoningChunks) == 0 {
		return body
	}
	thinking := formatThinkingBlock(strings.Join(reasoningChunks, "\n\n"))
	if body == "" {
		return thinking
	}
	return thinking + "\n\n" + body
}

func formatThinkingBlock(reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return ""
	}
	return "<think>\n" + reasoning + "\n</think>"
}

func validCompactionBoundary(items []domain.TimelineItem, firstKeptItemID string) bool {
	if strings.TrimSpace(firstKeptItemID) == "" {
		return true
	}
	return firstKeptTimelineIndex(items, firstKeptItemID) >= 0
}

func (r *Runtime) systemPrompt() string {
	return managedPrompt(r.cfg.ManagedAssetsDir(), "system-prompt.md")
}

func managedPrompt(root string, name string) string {
	if root = strings.TrimSpace(root); root != "" {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	data, err := assets.DefaultContent(name)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(data))
}

func (r *Runtime) sessionEnvironmentPrompt(session domain.Session) string {
	r.envMu.Lock()
	defer r.envMu.Unlock()
	if r.envCache == nil {
		r.envCache = map[string]string{}
	}
	key := string(session.ID)
	if text := r.envCache[key]; text != "" {
		return text
	}
	text := environment.Prompt(sessionProjectRoot(session))
	r.envCache[key] = text
	return text
}

func sessionProjectRoot(session domain.Session) string {
	return strings.TrimSpace(session.ProjectRoot)
}
