package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/voice"
)

// VoiceAudioConfig returns the PCM contract used by the native voice client.
func (c *Controller) VoiceAudioConfig() voice.AudioConfig {
	c.mu.RLock()
	voiceCfg := c.cfg.Voice
	c.mu.RUnlock()
	return voice.AudioConfig{
		Input: voice.AudioFormat{
			Encoding: voice.PCM16LE, SampleRate: voiceCfg.InputSampleRate, Channels: 1,
		},
		Output: voice.AudioFormat{
			Encoding: voice.PCM16LE, SampleRate: voiceCfg.OutputSampleRate, Channels: 1,
		},
		MaxUtteranceSeconds: voiceCfg.MaxUtteranceSeconds,
	}
}

// TranscribeVoice sends one VAD-finalized PCM utterance to the configured
// OpenAI-compatible speech recognition service.
func (c *Controller) TranscribeVoice(ctx context.Context, format voice.AudioFormat, pcm []byte) (string, error) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	want := c.VoiceAudioConfig().Input
	if format != want {
		return "", fmt.Errorf("unsupported voice input format %#v; expected %#v", format, want)
	}
	maxBytes := int64(cfg.Voice.MaxUtteranceSeconds) * int64(format.SampleRate) * int64(format.Channels) * 2
	if len(pcm) == 0 || int64(len(pcm)) > maxBytes || len(pcm)%2 != 0 {
		return "", fmt.Errorf("invalid voice PCM payload size %d", len(pcm))
	}
	providerID := strings.TrimSpace(cfg.Voice.STTProviderID)
	modelID := strings.TrimSpace(cfg.Voice.STTModelID)
	if providerID == "" || modelID == "" {
		return "", fmt.Errorf("voice STT provider and model are not configured")
	}
	providerCfg, ok := cfg.Provider(providerID)
	if !ok || providerCfg.Disabled {
		return "", fmt.Errorf("voice STT provider %q is not configured", providerID)
	}
	client, err := provider.New(providerID, providerCfg, nil)
	if err != nil {
		return "", err
	}
	result, err := client.TranscribeSpeech(ctx, provider.TranscriptionRequest{
		Model: modelID, Audio: wavFromPCM16(pcm, format.SampleRate, format.Channels),
		Filename: "voice-utterance.wav", Language: cfg.Voice.STTLanguage,
	})
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// StreamVoiceSpeech streams raw PCM16 from the configured remote speech
// synthesis service to the WebSocket writer.
func (c *Controller) StreamVoiceSpeech(ctx context.Context, text string, consume func([]byte) error) error {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	providerID := strings.TrimSpace(cfg.Voice.TTSProviderID)
	modelID := strings.TrimSpace(cfg.Voice.TTSModelID)
	if providerID == "" && modelID == "" {
		providerID = strings.TrimSpace(cfg.UI.TTS.ProviderID)
		modelID = strings.TrimSpace(cfg.UI.TTS.ModelID)
	}
	if providerID == "" || modelID == "" {
		return fmt.Errorf("voice TTS provider and model are not configured")
	}
	providerCfg, ok := cfg.Provider(providerID)
	if !ok || providerCfg.Disabled {
		return fmt.Errorf("voice TTS provider %q is not configured", providerID)
	}
	client, err := provider.New(providerID, providerCfg, nil)
	if err != nil {
		return err
	}
	_, err = client.StreamSpeech(ctx, provider.SpeechRequest{
		Model: modelID, Input: text, Voice: cfg.Voice.TTSVoice, Language: cfg.Voice.TTSLanguage,
		ResponseFormat: "pcm", StreamFormat: "audio",
	}, consume)
	return err
}

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

// EnsureVoiceSession resolves an explicitly requested durable voice chat or
// reuses the newest one, creating the user's first voice chat when needed.
func (c *Controller) EnsureVoiceSession(ctx context.Context, requestedID string) (voice.Session, error) {
	state, err := c.Sessions(ctx)
	if err != nil {
		return voice.Session{}, err
	}
	requestedID = strings.TrimSpace(requestedID)
	var newest domain.Session
	for _, session := range state.Sessions {
		if session.Kind != domain.SessionKindVoice {
			continue
		}
		if requestedID != "" && string(session.ID) == requestedID {
			return voice.Session{ID: string(session.ID), Title: voiceSessionTitle(session), LastMessage: session.LastMessage, UpdatedAt: session.UpdatedAt}, nil
		}
		if newest.ID == "" || session.UpdatedAt.After(newest.UpdatedAt) {
			newest = session
		}
	}
	if requestedID != "" {
		return voice.Session{}, fmt.Errorf("voice session %q was not found", requestedID)
	}
	if newest.ID == "" {
		created, _, err := c.CreateVoiceChat(ctx, "Voice Chat")
		if err != nil {
			return voice.Session{}, err
		}
		newest = created
	}
	return voice.Session{ID: string(newest.ID), Title: voiceSessionTitle(newest), LastMessage: newest.LastMessage, UpdatedAt: newest.UpdatedAt}, nil
}

// RecordVoiceExchange appends the human transcript and concise spoken result
// to the durable voice chat without invoking its model.
func (c *Controller) RecordVoiceExchange(ctx context.Context, voiceSessionID, transcript string, message voice.Message) error {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return fmt.Errorf("voice transcript is required")
	}
	_, session, _, runtime, err := c.resolveSelectedRuntimeWithoutTouch(ctx, Selection{SessionID: id.ID(strings.TrimSpace(voiceSessionID))}, true)
	if err != nil {
		return err
	}
	if session.Kind != domain.SessionKindVoice {
		return fmt.Errorf("session %s is not a voice session", session.ID)
	}
	if _, err := runtime.AppendUserMessage(ctx, domain.UserMessage{Text: transcript, Source: "voice"}); err != nil {
		return err
	}
	item, err := runtime.AppendTimelineContent(ctx, domain.AssistantMessage{Text: strings.TrimSpace(message.SpokenText)})
	if err != nil {
		return err
	}
	item.Seal(time.Now().UTC())
	if _, err := runtime.UpsertTimelineItem(ctx, item); err != nil {
		return err
	}
	return nil
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
			// Subscription updates intentionally carry a lightweight snapshot without
			// the full timeline. Read it only at a terminal boundary so streaming
			// deltas do not repeatedly clone a potentially large transcript.
			var terminalTimeline []domain.TimelineItem
			if !snapshot.Active && snapshot.Status != chat.StatusWaitingLLM && snapshot.Status != chat.StatusRunningTools {
				terminalTimeline = runtime.SnapshotTimeline()
			}
			response := latestAssistantTextAfter(terminalTimeline, initialSeq)
			if response != "" && !snapshot.Active && snapshot.Status != chat.StatusWaitingLLM && snapshot.Status != chat.StatusRunningTools {
				return voice.DelegationResult{
					SessionID:    string(session.ID),
					SessionTitle: voiceSessionTitle(session),
					ChatID:       string(chatRecord.ID),
					Text:         response,
				}, nil
			}
			if message := latestModelErrorAfter(terminalTimeline, initialSeq); message != "" {
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
