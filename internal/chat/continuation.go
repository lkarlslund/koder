package chat

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

type ContinuationPauseReason string

const (
	ContinuationPauseReasonRepeatedTool    ContinuationPauseReason = "repeated_tool"
	ContinuationPauseReasonTurnLimit       ContinuationPauseReason = "turn_limit"
	ContinuationPauseReasonProviderRefusal ContinuationPauseReason = "provider_refusal"
)

const RepeatedToolLoopThreshold = 3

type ToolLoopAction uint8

const (
	ToolLoopAllow ToolLoopAction = iota
	ToolLoopDeny
	ToolLoopStop
)

type ContinuationPause struct {
	Reason   ContinuationPauseReason
	Tool     domain.ToolKind
	Count    int
	Limit    int
	Args     string
	Body     string
	Subtitle string
}

type ToolLoopTracker struct {
	lastCall    toolLoopComparableCall
	repeatCount int
}

type toolLoopComparableCall struct {
	Tool domain.ToolKind
	Args map[string]string
}

func (t *ToolLoopTracker) Reset() {
	t.lastCall = toolLoopComparableCall{}
	t.repeatCount = 0
}

func (t *ToolLoopTracker) TrackCalls(calls []tools.Request) (ToolLoopAction, ContinuationPause) {
	if len(calls) != 1 {
		t.Reset()
		return ToolLoopAllow, ContinuationPause{}
	}
	call, ok := comparableToolLoopCall(calls[0])
	if !ok {
		t.Reset()
		return ToolLoopAllow, ContinuationPause{}
	}
	if reflect.DeepEqual(call, t.lastCall) {
		t.repeatCount++
	} else {
		t.lastCall = call
		t.repeatCount = 1
	}
	if t.repeatCount < RepeatedToolLoopThreshold {
		return ToolLoopAllow, ContinuationPause{}
	}
	toolName := calls[0].Tool.DisplayName()
	args := calls[0].ArgumentsJSON()
	pause := ContinuationPause{
		Reason:   ContinuationPauseReasonRepeatedTool,
		Tool:     calls[0].Tool,
		Count:    t.repeatCount,
		Args:     args,
		Subtitle: fmt.Sprintf("Repeated identical %s calls", toolName),
		Body: fmt.Sprintf(
			"Stopped continuation after %d identical %s calls with the same input. The model kept retrying the same tool instead of reacting to prior results.\n\nRepeated input: %s",
			t.repeatCount,
			toolName,
			args,
		),
	}
	if t.repeatCount == RepeatedToolLoopThreshold {
		return ToolLoopDeny, pause
	}
	return ToolLoopStop, pause
}

func RepeatedToolDeniedMessage(pause ContinuationPause) string {
	toolName := strings.TrimSpace(pause.Tool.DisplayName())
	if toolName == "" {
		toolName = "tool"
	}
	count := pause.Count
	if count <= 0 {
		count = RepeatedToolLoopThreshold
	}
	msg := fmt.Sprintf(
		"Denied repeated %s call: this is identical call %d with the same input.",
		toolName,
		count,
	)
	if args := strings.TrimSpace(pause.Args); args != "" {
		msg += " Repeated input: " + args + "."
	}
	return msg + " Read the previous tool result, then choose a different tool or pass different arguments that include the field you intended to change."
}

func comparableToolLoopCall(req tools.Request) (toolLoopComparableCall, bool) {
	if req.Tool == domain.ToolKindExecWriteStdin && strings.TrimSpace(req.Args["chars"]) == "" && strings.TrimSpace(req.Args["close_stdin"]) == "" {
		return toolLoopComparableCall{}, false
	}
	return toolLoopComparableCall{Tool: req.Tool, Args: cloneToolLoopArgs(req.Args)}, true
}

func cloneToolLoopArgs(args map[string]string) map[string]string {
	if args == nil {
		return nil
	}
	out := make(map[string]string, len(args))
	for key, value := range args {
		out[key] = value
	}
	return out
}

func ProviderRefusalPauseBody(reasoning string) string {
	body := "Paused continuation because the provider ended the turn without any text or tool call after tool results."
	if strings.TrimSpace(reasoning) == "" {
		return body
	}
	return body + "\n\nProvider reasoning:\n" + strings.TrimSpace(reasoning)
}

func (r *Chat) MaterializeTurnInstructions(ctx context.Context, blocks []provider.InstructionBlock, out chan<- domain.Event) error {
	if r == nil {
		return fmt.Errorf("chat runtime is required")
	}
	for _, block := range blocks {
		user, ok := TurnInstructionUserMessage(block)
		if !ok {
			continue
		}
		item, err := r.AppendUserMessage(ctx, user)
		if err != nil {
			return err
		}
		out <- domain.Event{Kind: domain.EventKindStatus, Text: "Turn instruction added", Item: item}
	}
	return nil
}

func TurnInstructionUserMessage(block provider.InstructionBlock) (domain.UserMessage, bool) {
	text := strings.TrimSpace(block.Text)
	if text == "" {
		return domain.UserMessage{}, false
	}
	source := domain.UserMessageSourceTurnInstruction
	if block.Kind == provider.InstructionKindContinuation && text == "Continue from where you left off." {
		source = domain.UserMessageSourceAutoResume
	}
	return domain.UserMessage{Text: text, Source: source}, true
}

func (r *Chat) PauseContinuation(ctx context.Context, pause ContinuationPause, out chan<- domain.Event) (domain.TimelineItem, bool) {
	body := strings.TrimSpace(pause.Body)
	if body == "" {
		body = "Paused continuation."
	}
	subtitle := strings.TrimSpace(pause.Subtitle)
	if subtitle == "" {
		subtitle = continuationPauseSubtitle(pause)
	}
	item, err := r.AppendTimelineContent(ctx, domain.Notice{
		Kind:     "loop_pause",
		Level:    "warning",
		Reason:   string(pause.Reason),
		Text:     body,
		Title:    "Continuation paused",
		Subtitle: subtitle,
		Tool:     pause.Tool,
		Count:    pause.Count,
		Limit:    pause.Limit,
	})
	ok := err == nil
	if ok {
		item.Seal(item.UpdatedAt)
		item, err = r.UpsertTimelineItem(ctx, item)
		ok = err == nil
	}
	if out != nil {
		evt := domain.Event{Kind: domain.EventKindStatus, Text: body}
		if ok {
			evt.Item = item
		}
		out <- evt
		out <- domain.Event{Kind: domain.EventKindMessageDone}
	}
	return item, ok
}

func continuationPauseSubtitle(pause ContinuationPause) string {
	switch pause.Reason {
	case ContinuationPauseReasonRepeatedTool:
		if pause.Tool != "" {
			return fmt.Sprintf("Repeated identical %s calls", pause.Tool.DisplayName())
		}
		return "Repeated identical tool calls"
	case ContinuationPauseReasonTurnLimit:
		if pause.Limit > 0 {
			return fmt.Sprintf("Turn limit reached (%d)", pause.Limit)
		}
		return "Turn limit reached"
	case ContinuationPauseReasonProviderRefusal:
		return "Provider stopped continuation"
	default:
		return "Continuation stopped"
	}
}

func ContinuationPauseMeta(pause ContinuationPause) map[string]string {
	return map[string]string{
		"reason": string(pause.Reason),
		"tool":   pause.Tool.String(),
		"count":  strconv.Itoa(pause.Count),
		"limit":  strconv.Itoa(pause.Limit),
	}
}
