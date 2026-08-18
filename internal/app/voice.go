package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
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
	all := append(append([]domain.Session(nil), state.Sessions...), state.QuickChats...)
	out := make([]voice.Session, 0, len(all))
	for _, session := range all {
		if session.Kind != domain.SessionKindRegular && session.Kind != domain.SessionKindQuick {
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

// ResolveVoiceRoute runs the constrained voice coordination profile against
// bounded session summaries. The coordinator validates its structured output
// before it can select or create anything.
func (c *Controller) ResolveVoiceRoute(ctx context.Context, request voice.RouteRequest) (voice.RouteDecision, error) {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	providerID, modelID := cfg.ResolveModel(cfg.Defaults.ProviderID, cfg.Defaults.ModelID)
	providerCfg, ok := cfg.Provider(providerID)
	if !ok || providerCfg.Disabled || strings.TrimSpace(modelID) == "" {
		return voice.RouteDecision{}, fmt.Errorf("default model for voice routing is not configured")
	}
	client, err := provider.New(providerID, providerCfg, nil)
	if err != nil {
		return voice.RouteDecision{}, err
	}
	type routeSession struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		LastMessage string `json:"last_message,omitempty"`
	}
	sessions := make([]routeSession, 0, min(len(request.Sessions), 20))
	for _, session := range request.Sessions[:min(len(request.Sessions), 20)] {
		sessions = append(sessions, routeSession{
			ID: session.ID, Title: truncateVoiceRouteText(session.Title, 100),
			LastMessage: truncateVoiceRouteText(session.LastMessage, 240),
		})
	}
	payload, err := json.Marshal(map[string]any{
		"utterance": request.Text, "active_session_id": request.ActiveSessionID, "sessions": sessions,
	})
	if err != nil {
		return voice.RouteDecision{}, err
	}
	response, err := client.CompleteChat(ctx, provider.ChatRequest{
		Model: modelID,
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: voiceRouterPrompt},
			{Role: provider.RoleUser, Content: string(payload)},
		},
		Stream: false,
	})
	if err != nil {
		return voice.RouteDecision{}, fmt.Errorf("resolve voice route: %w", err)
	}
	decision, err := decodeVoiceRoute(response.Text)
	if err != nil {
		return voice.RouteDecision{}, fmt.Errorf("decode voice route: %w", err)
	}
	return decision, nil
}

// CreateVoiceTarget creates either a disposable one-chat managed workspace or
// a normal persistent session in the current workspace.
func (c *Controller) CreateVoiceTarget(ctx context.Context, title string, persistent bool) (voice.Session, error) {
	title = truncateVoiceRouteText(strings.TrimSpace(title), 80)
	if title == "" {
		title = "Voice task"
	}
	var created domain.Session
	var err error
	if persistent {
		created, err = c.CreateSession(ctx, title, "", false)
	} else {
		created, _, err = c.CreateQuickChat(ctx)
		if err == nil {
			err = c.RenameSession(ctx, created.ID, title)
			created.Title = title
		}
	}
	if err != nil {
		return voice.Session{}, fmt.Errorf("create voice target: %w", err)
	}
	return voice.Session{
		ID: string(created.ID), Title: voiceSessionTitle(created), LastMessage: created.LastMessage, UpdatedAt: created.UpdatedAt,
	}, nil
}

const voiceRouterPrompt = `You route a phone-style Koder voice request.
Return exactly one JSON object and no markdown:
{"action":"existing|new_temporary|new_persistent|clarify","session_id":"","title":"","question":"","delegate":true}

Rules:
- existing: use only an exact supplied session id. Prefer the active session when this clearly continues it.
- new_temporary: use for a self-contained one-off task when no supplied session is relevant.
- new_persistent: use only when the user explicitly asks to create/keep a durable session or the work is clearly an ongoing project.
- clarify: only when the choice materially changes the result; ask one short voice-friendly question.
- delegate is false only when the utterance merely selects or creates a session and contains no work request.
- Give a short descriptive title for a new target. Do not answer or perform the user's task.`

func decodeVoiceRoute(text string) (voice.RouteDecision, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return voice.RouteDecision{}, fmt.Errorf("model did not return a JSON object")
	}
	var wire struct {
		Action    voice.RouteAction `json:"action"`
		SessionID string            `json:"session_id"`
		Title     string            `json:"title"`
		Question  string            `json:"question"`
		Delegate  bool              `json:"delegate"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &wire); err != nil {
		return voice.RouteDecision{}, err
	}
	switch wire.Action {
	case voice.RouteExisting, voice.RouteNewTemporary, voice.RouteNewPersistent, voice.RouteClarify:
	default:
		return voice.RouteDecision{}, fmt.Errorf("unsupported action %q", wire.Action)
	}
	return voice.RouteDecision{
		Action: wire.Action, SessionID: strings.TrimSpace(wire.SessionID),
		Title: truncateVoiceRouteText(wire.Title, 80), Question: truncateVoiceRouteText(wire.Question, 240),
		Delegate: wire.Delegate,
	}, nil
}

func truncateVoiceRouteText(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
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
					Parts:        voicePresentationParts(terminalTimeline, initialSeq),
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

// VoiceSessionArtifact resolves a durable session attachment previously
// produced by a tool in a delegated chat.
func (c *Controller) VoiceSessionArtifact(sessionID, attachmentID string) (voice.ArtifactFile, error) {
	path, err := c.SessionAttachmentPath(id.ID(strings.TrimSpace(sessionID)), strings.TrimSpace(attachmentID))
	if err != nil {
		return voice.ArtifactFile{}, err
	}
	return voice.ArtifactFile{Path: path}, nil
}

// VoiceOfferedArtifact resolves an offered-file token through its owning
// manager while retaining the voice endpoint's bearer-token boundary.
func (c *Controller) VoiceOfferedArtifact(ctx context.Context, token string) (voice.ArtifactFile, error) {
	record, err := c.ResolveOfferedFile(ctx, strings.TrimSpace(token))
	if err != nil {
		return voice.ArtifactFile{}, err
	}
	return voice.ArtifactFile{Path: record.Path, Name: record.Name, MIMEType: record.MIME}, nil
}

func voicePresentationParts(timeline []domain.TimelineItem, sequence int64) []voice.Part {
	parts := make([]voice.Part, 0)
	seen := map[string]struct{}{}
	addAttachment := func(sessionID string, metadata *attachment.Metadata, title string) {
		if metadata == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(metadata.ID) == "" {
			return
		}
		uri := "/voice/v1/artifacts/session/" + url.PathEscape(sessionID) + "/" + url.PathEscape(metadata.ID)
		if _, ok := seen[uri]; ok {
			return
		}
		seen[uri] = struct{}{}
		partMetadata := map[string]string{"name": metadata.Name}
		if strings.TrimSpace(title) != "" {
			partMetadata["title"] = strings.TrimSpace(title)
		}
		parts = append(parts, voice.Part{ID: metadata.ID, MIMEType: metadata.MIME, URI: uri, Metadata: partMetadata})
	}
	for _, item := range timeline {
		if item.Seq <= sequence {
			continue
		}
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range assistant.Tools {
			if call.Result == nil {
				continue
			}
			switch result := call.Result.Data.(type) {
			case tools.ShowMediaStoredResult:
				addAttachment(result.SessionID, result.Attachment, result.Title)
			case *tools.ShowMediaStoredResult:
				if result != nil {
					addAttachment(result.SessionID, result.Attachment, result.Title)
				}
			case tools.BrowserStoredResult:
				addAttachment(result.SessionID, result.Attachment, result.Summary)
			case *tools.BrowserStoredResult:
				if result != nil {
					addAttachment(result.SessionID, result.Attachment, result.Summary)
				}
			case tools.OfferFileStoredResult:
				uri := "/voice/v1/artifacts/offered/" + url.PathEscape(result.Token)
				if result.Token != "" {
					if _, ok := seen[uri]; !ok {
						seen[uri] = struct{}{}
						parts = append(parts, voice.Part{MIMEType: result.MIMEType, URI: uri, Metadata: map[string]string{"name": result.Name, "title": result.Title}})
					}
				}
			}
		}
	}
	return parts
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
