package chat

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

const AfterToolResultContinuationPrompt = "Continue from the latest tool result. If you learned a meaningful fact or changed direction, include one short visible progress sentence before the next tool call. Do not expose hidden reasoning. Either produce a visible answer for the user or make the next tool call."

type modelTurnLoop struct {
	model                    ModelRuntime
	session                  domain.Session
	tracker                  ToolLoopTracker
	autoContinuedBadStop     bool
	skipAutoCompactOnce      bool
	consecutiveReasoningOnly int
}

func (l *modelTurnLoop) maxSteps() int {
	return l.model.MaxToolLoopSteps()
}

func (l *modelTurnLoop) pauseLimit(ctx context.Context, rt *Chat, out chan<- domain.Event) {
	snapshot := rt.Snapshot()
	session := snapshot.Session
	pause := ContinuationPause{
		Reason: ContinuationPauseReasonTurnLimit,
		Limit:  l.model.MaxToolLoopSteps(),
		Body:   fmt.Sprintf("Paused continuation after reaching the model tool-turn limit (%d).", l.model.MaxToolLoopSteps()),
	}
	l.pauseContinuation(ctx, rt, session.ID, pause, out)
}

func (l *modelTurnLoop) step(ctx context.Context, rt *Chat, step int, turnInstructions []provider.InstructionBlock, out chan<- domain.Event) (TurnStepResult, error) {
	if rt == nil {
		return TurnStepResult{}, fmt.Errorf("chat runtime is required")
	}
	session := l.session
	if session.ID == "" {
		session = rt.Snapshot().Session
	}
	chat := rt.Snapshot().Chat
	client, err := l.model.ClientForChat(chat)
	if err != nil {
		return TurnStepResult{}, err
	}
	if err := l.model.BeginModelTurn(ctx, session.ID, chat.ID, step+1, out); err != nil {
		return TurnStepResult{}, err
	}
	if err := rt.MaterializeTurnInstructions(ctx, turnInstructions, out); err != nil {
		return TurnStepResult{}, err
	}
	turnRequest, err := rt.BuildTurnRequest()
	if err != nil {
		return TurnStepResult{}, err
	}
	session = turnRequest.Session
	chat = turnRequest.Chat
	messages, buildErr := l.model.BuildConversationForTurn(ctx, turnRequest)
	if buildErr != nil {
		return TurnStepResult{}, buildErr
	}
	if l.skipAutoCompactOnce {
		l.skipAutoCompactOnce = false
	} else {
		compacted, compactErr := l.model.AutoCompactAtTurnBoundary(ctx, rt, client, messages, out)
		if compactErr != nil {
			return TurnStepResult{}, compactErr
		}
		if compacted {
			session = rt.Snapshot().Session
			l.session = session
			l.skipAutoCompactOnce = true
			return TurnStepResult{
				Continue:         true,
				TurnInstructions: TurnInstructionBlocks("", "Continue from the compacted session summary. Do not restart, greet, or restate the summary. Continue the pending task from the latest tool result."),
			}, nil
		}
	}

	stream := l.model.ProviderStreamingEnabled(chat)
	req := l.model.ChatRequest(session, chat, messages, stream)
	assistantItem, itemErr := l.model.NextAssistantTimelineItemForTurn(ctx, rt)
	if itemErr != nil {
		return TurnStepResult{}, itemErr
	}
	resp, completeErr := l.model.CompleteModelRequest(ctx, session, chat, client, out, req, assistantItem)
	if completeErr != nil {
		return TurnStepResult{}, completeErr
	}

	text, reasoningContent, usage := resp.Text, resp.Reasoning, resp.Usage
	if len(resp.ToolCalls) > 0 {
		parsed := l.model.ParseProviderToolCallsForTranscript(resp.ToolCalls, session.ID)
		for _, callErr := range resp.ToolCallErrors {
			parsed.ToolCalls = append(parsed.ToolCalls, l.model.FailedStreamedProviderToolCall(callErr))
		}
		calls := parsed.Requests
		if len(parsed.ToolCalls) == 0 && parsed.Err != nil {
			if strings.TrimSpace(text) == "" && strings.TrimSpace(resp.RawReasoning) == "" {
				return TurnStepResult{}, parsed.Err
			}
			l.model.RecordLifecycle(session.ID, "provider_tool_call_parse_ignored", parsed.Err.Error(), map[string]string{
				"tool_calls": strconv.Itoa(len(resp.ToolCalls)),
			})
		} else if len(parsed.ToolCalls) > 0 {
			l.consecutiveReasoningOnly = 0
			assistantItem, err := rt.AppendAssistantToolCalls(ctx, assistantItem, parsed.ToolCalls, strings.TrimSpace(resp.Text), reasoningContent, resp.Usage)
			if err != nil {
				return TurnStepResult{}, err
			}
			out <- domain.Event{Kind: domain.EventKindToolCallDelta, Text: "tool calls persisted", Item: assistantItem}
			if resp.Usage.HasAnyTokens() {
				if err := rt.SetContextUsage(ctx, resp.Usage); err != nil {
					return TurnStepResult{}, err
				}
				out <- domain.Event{Kind: domain.EventKindUsage, Usage: resp.Usage}
			}
			if ShouldStop(ctx) {
				return TurnStepResult{Done: true}, nil
			}
			if len(calls) == 0 {
				if result, handled, err := l.handleRepeatedStoredToolCalls(ctx, rt, session.ID, parsed.ToolCalls, out); handled || err != nil {
					return result, err
				}
				return TurnStepResult{Continue: true}, nil
			}
			if rt.shouldStopAfterCurrentLLMTurn() {
				return TurnStepResult{Done: true}, nil
			}
			if result, handled, err := l.handleRepeatedToolCall(ctx, rt, session.ID, calls, out); handled || err != nil {
				return result, err
			}
			needsApproval, handledErr := rt.RunToolCalls(ctx, calls, out)
			if handledErr != nil {
				return TurnStepResult{}, handledErr
			}
			if needsApproval {
				return TurnStepResult{WaitingApproval: true}, nil
			}
			if ShouldStop(ctx) {
				return TurnStepResult{Done: true}, nil
			}
			return TurnStepResult{Continue: true}, nil
		}
	}
	if len(resp.ToolCallErrors) > 0 {
		l.consecutiveReasoningOnly = 0
		toolCalls := make([]domain.ToolCall, 0, len(resp.ToolCallErrors))
		for _, callErr := range resp.ToolCallErrors {
			toolCalls = append(toolCalls, l.model.FailedStreamedProviderToolCall(callErr))
		}
		assistantItem, err := rt.AppendAssistantToolCalls(ctx, assistantItem, toolCalls, strings.TrimSpace(resp.Text), reasoningContent, resp.Usage)
		if err != nil {
			return TurnStepResult{}, err
		}
		out <- domain.Event{Kind: domain.EventKindToolCallDelta, Text: "tool calls persisted", Item: assistantItem}
		return TurnStepResult{Continue: true}, nil
	}

	call, plain := ParseToolCall(text)
	if call != nil {
		l.consecutiveReasoningOnly = 0
		l.model.RecordLifecycle(session.ID, "tool_call_parsed", call.ContextString(), map[string]string{"tool": call.Tool.String(), "tool_call_id": call.ToolCallID})
		assistantItem, err := rt.AppendAssistantToolRequests(ctx, assistantItem, []tools.Request{*call}, strings.TrimSpace(plain), reasoningContent, domain.Usage{})
		if err != nil {
			return TurnStepResult{}, err
		}
		out <- domain.Event{Kind: domain.EventKindToolCallDelta, Text: "tool call persisted", Item: assistantItem}
		if result, handled, err := l.handleRepeatedToolCall(ctx, rt, session.ID, []tools.Request{*call}, out); handled || err != nil {
			return result, err
		}
		if ShouldStop(ctx) {
			return TurnStepResult{Done: true}, nil
		}
		if rt.shouldStopAfterCurrentLLMTurn() {
			return TurnStepResult{Done: true}, nil
		}
		needsApproval, handledErr := rt.RunToolCalls(ctx, []tools.Request{*call}, out)
		if handledErr != nil {
			return TurnStepResult{}, handledErr
		}
		if needsApproval {
			return TurnStepResult{WaitingApproval: true}, nil
		}
		if ShouldStop(ctx) {
			return TurnStepResult{Done: true}, nil
		}
		return TurnStepResult{Continue: true}, nil
	}
	l.tracker.Reset()

	if strings.TrimSpace(text) == "" && len(resp.ToolCalls) == 0 {
		if strings.TrimSpace(resp.RawReasoning) != "" {
			l.consecutiveReasoningOnly++
			if step > 0 && l.consecutiveReasoningOnly == 1 {
				return TurnStepResult{
					Continue:         true,
					TurnInstructions: TurnInstructionBlocks("", AfterToolResultContinuationPrompt),
				}, nil
			}
			l.pauseContinuation(ctx, rt, session.ID, ContinuationPause{
				Reason: ContinuationPauseReasonProviderRefusal,
				Body:   ProviderRefusalPauseBody(resp.RawReasoning),
			}, out)
			return TurnStepResult{Done: true}, nil
		}
		l.pauseContinuation(ctx, rt, session.ID, ContinuationPause{
			Reason: ContinuationPauseReasonProviderRefusal,
			Body:   ProviderRefusalPauseBody(resp.RawReasoning),
		}, out)
		return TurnStepResult{Done: true}, nil
	}
	l.consecutiveReasoningOnly = 0
	if step > 0 && l.model.AutoContinueBadStopEnabled() && !l.autoContinuedBadStop && len(resp.ToolCalls) == 0 && shouldAutoContinueBadStop(text) {
		l.autoContinuedBadStop = true
		l.model.RecordLifecycle(session.ID, "auto_continue_bad_stop", strings.TrimSpace(text), map[string]string{"step": strconv.Itoa(step + 1)})
		return TurnStepResult{
			Continue:         true,
			TurnInstructions: TurnInstructionBlocks("", "Continue by issuing the tool call now. Do not describe intent. If no tool call is needed, provide the final user-facing answer instead."),
		}, nil
	}
	assistant := domain.AssistantMessage{Text: text}
	if strings.TrimSpace(reasoningContent.Text) != "" {
		assistant.Reasoning = reasoningContent
	}
	usage = usage.Normalized()
	if usage.HasAnyTokens() {
		assistant.Usage = &usage
		if err := rt.SetContextUsage(ctx, usage); err != nil {
			return TurnStepResult{}, err
		}
		if !resp.Streamed {
			out <- domain.Event{Kind: domain.EventKindUsage, Usage: usage}
		}
	}
	if !resp.Streamed && strings.TrimSpace(text) != "" {
		out <- domain.Event{Kind: domain.EventKindMessageDelta, Text: text, Item: assistantItem}
	}
	if !resp.Streamed && strings.TrimSpace(resp.RawReasoning) != "" {
		out <- domain.Event{Kind: domain.EventKindReasoning, Text: resp.RawReasoning, Item: assistantItem}
	}
	updated, updateErr := rt.AppendAssistantMessage(ctx, assistantItem, assistant)
	if updateErr != nil {
		return TurnStepResult{}, updateErr
	}
	assistantItem = updated
	l.model.RecordLifecycle(session.ID, "assistant_message_persisted", strings.TrimSpace(text), map[string]string{"item_id": assistantItem.ID})
	chatTitle, chatTitleErr := l.model.MaybeUpdateChatTitle(ctx, chat.ID)
	if chatTitleErr != nil {
		l.model.RecordLifecycle(session.ID, "chat_title_update_failed", chatTitleErr.Error(), map[string]string{"chat_id": chat.ID})
	}
	if strings.TrimSpace(chatTitle) != "" {
		l.model.RecordLifecycle(session.ID, "chat_title_updated", chatTitle, map[string]string{"chat_id": chat.ID})
		out <- domain.Event{
			Kind: domain.EventKindChatTitle,
			Text: chatTitle,
			Meta: map[string]string{"chat_id": chat.ID},
		}
	}
	title, titleErr := l.model.MaybeUpdateSessionTitle(ctx, session, chat, client)
	if titleErr != nil {
		l.model.RecordLifecycle(session.ID, "session_title_update_failed", titleErr.Error(), nil)
	}
	if strings.TrimSpace(title) != "" {
		l.model.RecordLifecycle(session.ID, "session_title_updated", title, nil)
		out <- domain.Event{
			Kind: domain.EventKindSessionTitle,
			Text: title,
			Meta: map[string]string{"session_id": session.ID},
		}
	}
	out <- domain.Event{Kind: domain.EventKindMessageDone, Item: assistantItem}
	return TurnStepResult{Done: true}, nil
}

func (l *modelTurnLoop) pauseContinuation(ctx context.Context, rt *Chat, sessionID id.ID, pause ContinuationPause, out chan<- domain.Event) {
	body := strings.TrimSpace(pause.Body)
	if body == "" {
		body = "Paused continuation."
	}
	l.model.RecordLifecycle(sessionID, "model_turn_paused", body, ContinuationPauseMeta(pause))
	rt.PauseContinuation(ctx, pause, out)
}

func (l *modelTurnLoop) handleRepeatedToolCall(ctx context.Context, rt *Chat, sessionID id.ID, calls []tools.Request, out chan<- domain.Event) (TurnStepResult, bool, error) {
	action, pause := l.tracker.TrackCalls(calls)
	switch action {
	case ToolLoopAllow:
		return TurnStepResult{}, false, nil
	case ToolLoopDeny, ToolLoopStop:
		if len(calls) != 1 {
			return TurnStepResult{}, true, fmt.Errorf("repeated tool guard expected one call, got %d", len(calls))
		}
		evt, err := rt.RecordToolDenied(ctx, calls[0], RepeatedToolDeniedMessage(pause))
		if err != nil {
			return TurnStepResult{}, true, err
		}
		if out != nil {
			out <- evt
		}
		if action == ToolLoopDeny {
			return TurnStepResult{Continue: true}, true, nil
		}
		l.pauseContinuation(ctx, rt, sessionID, pause, out)
		return TurnStepResult{Done: true}, true, nil
	default:
		return TurnStepResult{}, true, fmt.Errorf("unsupported repeated tool action %d", action)
	}
}

func (l *modelTurnLoop) handleRepeatedStoredToolCalls(ctx context.Context, rt *Chat, sessionID id.ID, calls []domain.ToolCall, out chan<- domain.Event) (TurnStepResult, bool, error) {
	action, pause := l.tracker.TrackToolCalls(calls)
	switch action {
	case ToolLoopAllow:
		return TurnStepResult{}, false, nil
	case ToolLoopDeny, ToolLoopStop:
		l.pauseContinuation(ctx, rt, sessionID, pause, out)
		return TurnStepResult{Done: true}, true, nil
	default:
		return TurnStepResult{}, true, fmt.Errorf("unsupported repeated tool action %d", action)
	}
}

func TurnInstructionBlocks(note string, continuePrompt string) []provider.InstructionBlock {
	var out []provider.InstructionBlock
	if strings.TrimSpace(note) != "" {
		out = append(out, provider.InstructionBlock{
			Kind: provider.InstructionKindSessionNote,
			Text: "Session update:\n" + strings.TrimSpace(note),
		})
	}
	if strings.TrimSpace(continuePrompt) != "" {
		out = append(out, provider.InstructionBlock{
			Kind: provider.InstructionKindContinuation,
			Text: strings.TrimSpace(continuePrompt),
		})
	}
	return out
}

func ParseToolCall(text string) (*tools.Request, string) {
	re := regexp.MustCompile(`(?s)<koder_tool>\s*(\{.*?\})\s*</koder_tool>`)
	match := re.FindStringSubmatch(text)
	if len(match) != 2 {
		return nil, text
	}
	call, err := tools.RequestFromMeta(match[1])
	if err != nil || call.Tool == "" {
		return nil, text
	}
	plain := strings.TrimSpace(re.ReplaceAllString(text, ""))
	return &call, plain
}

func shouldAutoContinueBadStop(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.HasSuffix(trimmed, ":") {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{
		"let me ",
		"i need to ",
		"i'll ",
		"i will ",
		"i am going to ",
		"i'm going to ",
		"i’m going to ",
		"next i ",
		"now i ",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
