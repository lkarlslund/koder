package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/voice"
)

// ListVoiceSessions returns bounded summaries suitable for voice routing.
func (c *Controller) ListVoiceSessions(ctx context.Context) ([]voice.Session, error) {
	state, err := c.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]voice.Session, 0, len(state.Sessions))
	for _, session := range state.Sessions {
		if session.Kind != domain.SessionKindRegular {
			continue
		}
		out = append(out, voice.Session{
			ID:          string(session.ID),
			Title:       voiceSessionTitle(session),
			LastMessage: session.LastMessage,
			UpdatedAt:   session.UpdatedAt,
		})
	}
	return out, nil
}

// DelegateVoice sends work to the existing default chat of an ordinary session
// and waits for its public response. It never creates a chat or bypasses that
// chat's permission and approval state.
func (c *Controller) DelegateVoice(ctx context.Context, sessionID, text string) (voice.DelegationResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return voice.DelegationResult{}, fmt.Errorf("voice delegation text is required")
	}
	owner, session, chatRecord, runtime, err := c.resolveSelectedRuntimeWithoutTouch(ctx, Selection{
		SessionID: id.ID(strings.TrimSpace(sessionID)),
	}, true)
	if err != nil {
		return voice.DelegationResult{}, err
	}
	_ = owner
	if err := runtime.EnsureTimeline(ctx); err != nil {
		return voice.DelegationResult{}, err
	}
	initial := runtime.Snapshot()
	if initial.Active || (initial.Status != "" && initial.Status != chat.StatusIdle && initial.Status != chat.StatusErrored) {
		return voice.DelegationResult{
			SessionID:      string(session.ID),
			SessionTitle:   voiceSessionTitle(session),
			ChatID:         string(chatRecord.ID),
			Text:           "That session's chat is already busy. Try again when its current turn is finished.",
			NeedsAttention: true,
		}, nil
	}
	initialSeq := latestTimelineSequence(initial.Timeline)
	updates, unsubscribe := runtime.Subscribe()
	defer unsubscribe()
	if err := c.enqueuePrompt(runtime, text, chat.QueueKindUser, nil); err != nil {
		return voice.DelegationResult{}, err
	}
	turnStarted := false

	for {
		select {
		case <-ctx.Done():
			return voice.DelegationResult{}, fmt.Errorf("wait for delegated chat: %w", ctx.Err())
		case update, ok := <-updates:
			if !ok {
				return voice.DelegationResult{}, fmt.Errorf("delegated chat closed before replying")
			}
			snapshot := update.Snapshot
			if voiceTurnStarted(snapshot.Status, snapshot.Active) {
				turnStarted = true
			}
			switch snapshot.Status {
			case chat.StatusWaitingApproval:
				return voiceAttentionResult(session, chatRecord, "The delegated chat needs approval. Open Koder to review it."), nil
			case chat.StatusWaitingInput:
				return voiceAttentionResult(session, chatRecord, "The delegated chat needs more information. Open Koder to answer it."), nil
			}
			response := latestAssistantTextAfter(snapshot.Timeline, initialSeq)
			if response != "" && !snapshot.Active && snapshot.Status != chat.StatusWaitingLLM && snapshot.Status != chat.StatusRunningTools {
				return voice.DelegationResult{
					SessionID:    string(session.ID),
					SessionTitle: voiceSessionTitle(session),
					ChatID:       string(chatRecord.ID),
					Text:         response,
				}, nil
			}
			if message := latestModelErrorAfter(snapshot.Timeline, initialSeq); message != "" {
				return voice.DelegationResult{}, fmt.Errorf("delegated chat stopped with an error: %s", message)
			}
			if snapshot.Status == chat.StatusErrored && !snapshot.Active && turnStarted {
				return voice.DelegationResult{}, fmt.Errorf("delegated chat stopped with an error")
			}
		}
	}
}

func voiceTurnStarted(status chat.Status, active bool) bool {
	if active {
		return true
	}
	switch status {
	case chat.StatusWaitingLLM, chat.StatusStreamingThoughts, chat.StatusStreamingResponse,
		chat.StatusRunningTools, chat.StatusWaitingApproval, chat.StatusWaitingInput:
		return true
	default:
		return false
	}
}

func voiceAttentionResult(session domain.Session, chatRecord domain.Chat, text string) voice.DelegationResult {
	return voice.DelegationResult{
		SessionID:      string(session.ID),
		SessionTitle:   voiceSessionTitle(session),
		ChatID:         string(chatRecord.ID),
		Text:           text,
		NeedsAttention: true,
	}
}

func voiceSessionTitle(session domain.Session) string {
	if title := strings.TrimSpace(session.Title); title != "" {
		return title
	}
	return "Untitled session"
}

func latestTimelineSequence(timeline []domain.TimelineItem) int64 {
	var latest int64
	for _, item := range timeline {
		if item.Seq > latest {
			latest = item.Seq
		}
	}
	return latest
}

func latestAssistantTextAfter(timeline []domain.TimelineItem, sequence int64) string {
	for index := len(timeline) - 1; index >= 0; index-- {
		item := timeline[index]
		if item.Seq <= sequence {
			break
		}
		message, ok := item.Content.(domain.AssistantMessage)
		if ok && item.Sealed() && strings.TrimSpace(message.Text) != "" {
			return strings.TrimSpace(message.Text)
		}
	}
	return ""
}

func latestModelErrorAfter(timeline []domain.TimelineItem, sequence int64) string {
	for index := len(timeline) - 1; index >= 0; index-- {
		item := timeline[index]
		if item.Seq <= sequence {
			break
		}
		switch content := item.Content.(type) {
		case domain.AssistantMessage:
			if content.Error != nil && strings.TrimSpace(content.Error.Message) != "" {
				return strings.TrimSpace(content.Error.Message)
			}
		case domain.Notice:
			if content.Kind == "model_error" && strings.TrimSpace(content.Text) != "" {
				return strings.TrimSpace(content.Text)
			}
		}
	}
	return ""
}
