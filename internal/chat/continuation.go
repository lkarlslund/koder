package chat

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type ContinuationPauseReason string

const (
	ContinuationPauseReasonRepeatedTool    ContinuationPauseReason = "repeated_tool"
	ContinuationPauseReasonTurnLimit       ContinuationPauseReason = "turn_limit"
	ContinuationPauseReasonProviderRefusal ContinuationPauseReason = "provider_refusal"
)

const RepeatedToolLoopThreshold = 3

type ContinuationPause struct {
	Reason   ContinuationPauseReason
	Tool     domain.ToolKind
	Count    int
	Limit    int
	Body     string
	Subtitle string
}

type ToolLoopTracker struct {
	lastSignature string
	lastTool      domain.ToolKind
	repeatCount   int
}

func (t *ToolLoopTracker) Reset() {
	t.lastSignature = ""
	t.lastTool = ""
	t.repeatCount = 0
}

func (t *ToolLoopTracker) TrackCalls(calls []tools.Request) (ContinuationPause, bool) {
	if len(calls) != 1 {
		t.Reset()
		return ContinuationPause{}, false
	}
	signature := toolLoopSignature(calls[0])
	if signature == "" {
		t.Reset()
		return ContinuationPause{}, false
	}
	if signature == t.lastSignature {
		t.repeatCount++
	} else {
		t.lastSignature = signature
		t.lastTool = calls[0].Tool
		t.repeatCount = 1
	}
	if t.repeatCount < RepeatedToolLoopThreshold {
		return ContinuationPause{}, false
	}
	toolName := calls[0].Tool.DisplayName()
	return ContinuationPause{
		Reason:   ContinuationPauseReasonRepeatedTool,
		Tool:     calls[0].Tool,
		Count:    t.repeatCount,
		Subtitle: fmt.Sprintf("Repeated identical %s calls", toolName),
		Body: fmt.Sprintf(
			"Paused continuation after %d identical %s calls with the same input. The model kept retrying the same tool instead of reacting to the result.",
			t.repeatCount,
			toolName,
		),
	}, true
}

func toolLoopSignature(req tools.Request) string {
	if req.Tool == domain.ToolKindExecWriteStdin && strings.TrimSpace(req.Args["chars"]) == "" && strings.TrimSpace(req.Args["close_stdin"]) == "" {
		return ""
	}
	return req.Tool.String() + "\x00" + req.ArgumentsJSON()
}

func ProviderRefusalPauseBody(reasoning string) string {
	body := "Paused continuation because the provider ended the turn without any text or tool call after tool results."
	if strings.TrimSpace(reasoning) == "" {
		return body
	}
	return body + "\n\nProvider reasoning:\n" + strings.TrimSpace(reasoning)
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
