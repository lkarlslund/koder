package app

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

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
func (c *Controller) TranscribeVoice(ctx context.Context, format voice.AudioFormat, pcm []byte, hints voice.TranscriptionHints) (string, error) {
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
	language, prompt := transcriptionHints(cfg.Voice.STTLanguage, hints.Languages)
	result, err := client.TranscribeSpeech(ctx, provider.TranscriptionRequest{
		Model: modelID, Audio: wavFromPCM16(pcm, format.SampleRate, format.Channels),
		Filename: "voice-utterance.wav", Language: language, Prompt: prompt,
	})
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func transcriptionHints(configured string, requested []string) (language, prompt string) {
	if len(requested) == 1 {
		return requested[0], ""
	}
	if len(requested) > 1 {
		return "", "The speaker will use only these languages: " + strings.Join(requested, ", ") + ". Do not identify the speech as another language. Transcribe it in the language spoken."
	}
	return transcriptionLanguage(configured), ""
}

func transcriptionLanguage(configured string) string {
	language := strings.TrimSpace(configured)
	if strings.EqualFold(language, "auto") {
		return ""
	}
	return language
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

// ListVoiceChats returns the durable coordination transcripts available to a
// native voice call. These are separate from work-session routing targets.
func (c *Controller) ListVoiceChats(ctx context.Context) ([]voice.Session, error) {
	state, err := c.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]voice.Session, 0)
	for _, session := range state.Sessions {
		if session.Kind != domain.SessionKindVoice {
			continue
		}
		out = append(out, voice.Session{
			ID: string(session.ID), Title: voiceSessionTitle(session),
			LastMessage: session.LastMessage, UpdatedAt: session.UpdatedAt,
		})
	}
	return out, nil
}

// CreateVoiceTarget creates either a disposable one-chat managed workspace or
// a normal persistent session in the current workspace.
func (c *Controller) CreateVoiceTarget(ctx context.Context, title string, persistent bool) (voice.Session, error) {
	title = truncateVoiceText(strings.TrimSpace(title), 80)
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

func truncateVoiceText(text string, limit int) string {
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

// CreateVoiceSession creates a durable voice chat for a native voice client.
func (c *Controller) CreateVoiceSession(ctx context.Context, title string) (voice.Session, error) {
	session, _, err := c.CreateVoiceChat(ctx, strings.TrimSpace(title))
	if err != nil {
		return voice.Session{}, err
	}
	return voice.Session{
		ID:          string(session.ID),
		Title:       voiceSessionTitle(session),
		LastMessage: session.LastMessage,
		UpdatedAt:   session.UpdatedAt,
	}, nil
}

// RenameVoiceSession changes a durable voice chat title after verifying its kind.
func (c *Controller) RenameVoiceSession(ctx context.Context, sessionID, title string) (voice.Session, error) {
	session, err := c.EnsureVoiceSession(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return voice.Session{}, err
	}
	title = truncateVoiceText(strings.TrimSpace(title), 80)
	if title == "" {
		return voice.Session{}, fmt.Errorf("voice session title is required")
	}
	if err := c.RenameSession(ctx, id.ID(session.ID), title); err != nil {
		return voice.Session{}, fmt.Errorf("rename voice session: %w", err)
	}
	session.Title = title
	return session, nil
}

// VoiceSessionHistory returns a bounded, presentation-safe transcript for a
// native client reconnecting to an existing voice conversation.
func (c *Controller) VoiceSessionHistory(ctx context.Context, voiceSessionID, beforeID string, limit int) (voice.TranscriptPage, error) {
	owner, session, chatRecord, err := c.resolveSelectedChatWithTouch(ctx, Selection{SessionID: id.ID(strings.TrimSpace(voiceSessionID))}, true, false)
	if err != nil {
		return voice.TranscriptPage{}, err
	}
	if session.Kind != domain.SessionKindVoice || chatRecord.WorkflowRole != domain.WorkflowRoleVoice {
		return voice.TranscriptPage{}, fmt.Errorf("session %s is not a voice chat", session.ID)
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	cursor := id.ID(strings.TrimSpace(beforeID))
	entries := make([]voice.TranscriptEntry, 0, limit*2)
	hasMore := false
	for {
		page, err := owner.TimelinePage(ctx, chatRecord.ID, cursor, 32, false)
		if err != nil {
			return voice.TranscriptPage{}, err
		}
		pageEntries := voiceTranscriptEntries(page.Items)
		entries = append(pageEntries, entries...)
		hasMore = page.HasMore
		if countTranscriptTurns(entries) >= limit || !page.HasMore || len(page.Items) == 0 {
			break
		}
		cursor = page.Before
	}
	start := transcriptPageStart(entries, limit)
	return voice.TranscriptPage{Entries: entries[start:], HasMore: start > 0 || hasMore}, nil
}

func voiceTranscriptEntries(items []domain.TimelineItem) []voice.TranscriptEntry {
	entries := make([]voice.TranscriptEntry, 0, len(items))
	for _, item := range items {
		var role, text string
		switch content := item.Content.(type) {
		case domain.UserMessage:
			role, text = "user", content.Text
		case domain.AssistantMessage:
			role, text = "assistant", content.Text
		}
		if text = strings.TrimSpace(text); text == "" {
			continue
		}
		entries = append(entries, voice.TranscriptEntry{
			ID: string(item.ID), Role: role, Text: text, CreatedAt: item.CreatedAt,
		})
	}
	return entries
}

func countTranscriptTurns(entries []voice.TranscriptEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.Role == "user" {
			count++
		}
	}
	return count
}

func transcriptPageStart(entries []voice.TranscriptEntry, turns int) int {
	seenTurns := 0
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].Role != "user" {
			continue
		}
		seenTurns++
		if seenTurns == turns {
			return index
		}
	}
	if seenTurns > 0 {
		return 0
	}
	return max(0, len(entries)-turns)
}

// RunVoiceTurn executes an utterance through the durable voice chat's normal
// model loop, history, role prompt, and profile-scoped tools.
func (c *Controller) RunVoiceTurn(ctx context.Context, voiceSessionID, text string, onWorking func(voice.Session) error) (voice.Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return voice.Message{}, fmt.Errorf("voice transcript is required")
	}
	_, session, chatRecord, runtime, err := c.resolveSelectedRuntimeWithoutTouch(ctx, Selection{SessionID: id.ID(strings.TrimSpace(voiceSessionID))}, true)
	if err != nil {
		return voice.Message{}, err
	}
	if session.Kind != domain.SessionKindVoice || chatRecord.WorkflowRole != domain.WorkflowRoleVoice {
		return voice.Message{}, fmt.Errorf("session %s is not a voice chat", session.ID)
	}
	if err := runtime.EnsureTimeline(ctx); err != nil {
		return voice.Message{}, err
	}
	initial := runtime.Snapshot()
	if initial.Active || (initial.Status != "" && initial.Status != chat.StatusIdle && initial.Status != chat.StatusErrored) {
		return voice.Message{}, fmt.Errorf("voice chat is already busy")
	}
	initialSeq := latestTimelineSequence(initial.Timeline)
	updates, unsubscribe := runtime.Subscribe()
	defer unsubscribe()
	runtime.Enqueue(chat.QueueItem{Kind: chat.QueueKindUser, Source: domain.UserMessageSourceVoice, Text: text})
	started := false
	workingSent := false
	for {
		select {
		case <-ctx.Done():
			return voice.Message{}, fmt.Errorf("wait for voice chat: %w", ctx.Err())
		case update, ok := <-updates:
			if !ok {
				return voice.Message{}, fmt.Errorf("voice chat closed before replying")
			}
			snapshot := update.Snapshot
			if voiceTurnStarted(snapshot.Status, snapshot.Active) {
				started = true
			}
			if !workingSent && snapshot.Status == chat.StatusRunningTools && onWorking != nil {
				if target, found := voiceWorkingTarget(runtime.SnapshotTimeline(), initialSeq); found {
					target = c.describeVoiceTarget(ctx, target)
					if err := onWorking(target); err != nil {
						return voice.Message{}, err
					}
					workingSent = true
				}
			}
			if snapshot.Status == chat.StatusWaitingApproval {
				return voice.Message{}, fmt.Errorf("voice chat needs approval in Koder")
			}
			if snapshot.Status == chat.StatusWaitingInput {
				return voice.Message{}, fmt.Errorf("voice chat needs text input in Koder")
			}
			if snapshot.Active || snapshot.Status == chat.StatusWaitingLLM || snapshot.Status == chat.StatusRunningTools {
				continue
			}
			timeline := runtime.SnapshotTimeline()
			if response := latestAssistantTextAfter(timeline, initialSeq); response != "" {
				spoken := conciseSpokenResponse(response)
				parts := []voice.Part{{MIMEType: "text/plain", Data: spoken}}
				parts = append(parts, voicePresentationParts(timeline, initialSeq)...)
				return voice.Message{SpokenText: spoken, Parts: parts}, nil
			}
			if message := latestModelErrorAfter(timeline, initialSeq); message != "" {
				return voice.Message{}, fmt.Errorf("voice chat stopped with an error: %s", message)
			}
			if snapshot.Status == chat.StatusErrored && started {
				return voice.Message{}, fmt.Errorf("voice chat stopped with an error")
			}
		}
	}
}

func conciseSpokenResponse(text string) string {
	const maxWords = 60
	text = conversationalVoiceText(text)
	if text == "" {
		return "I've put the details in the conversation."
	}
	words := strings.Fields(text)
	if len(words) <= maxWords {
		return text
	}
	cut := strings.Join(words[:maxWords], " ")
	for index := len(cut) - 1; index >= 0; index-- {
		if cut[index] == '.' || cut[index] == '!' || cut[index] == '?' {
			if index >= len(cut)/2 {
				return strings.TrimSpace(cut[:index+1])
			}
			break
		}
	}
	return strings.TrimRight(cut, ",;:-") + "…"
}

var (
	voiceMarkdownImage = regexp.MustCompile(`!\[([^]]*)\]\([^)]+\)`)
	voiceMarkdownLink  = regexp.MustCompile(`\[([^]]+)\]\([^)]+\)`)
	voiceNumberedItem  = regexp.MustCompile(`^\d+[.)]\s+`)
	voiceRawURL        = regexp.MustCompile(`https?://\S+`)
	voiceTableDivider  = regexp.MustCompile(`^\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?$`)
)

// conversationalVoiceText is a defensive last mile for TTS. The voice role
// prompt should already produce plain speech, but provider output must never
// make a synthesizer recite Markdown punctuation or raw table delimiters.
func conversationalVoiceText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	spoken := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || line == "" || voiceTableDivider.MatchString(line) {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "#>"))
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "), "+ "))
		line = voiceNumberedItem.ReplaceAllString(line, "")
		if strings.Count(line, "|") >= 2 {
			cells := strings.Split(strings.Trim(line, "|"), "|")
			values := cells[:0]
			for _, cell := range cells {
				if cell = strings.TrimSpace(cell); cell != "" {
					values = append(values, cell)
				}
			}
			line = strings.Join(values, ", ")
		}
		line = voiceMarkdownImage.ReplaceAllString(line, "$1")
		line = voiceMarkdownLink.ReplaceAllString(line, "$1")
		line = voiceRawURL.ReplaceAllString(line, "the link")
		line = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "", "*", "").Replace(line)
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		last := line[len(line)-1]
		if !strings.ContainsRune(".!?;:", rune(last)) {
			line += "."
		}
		spoken = append(spoken, line)
	}
	return strings.TrimSpace(strings.Join(spoken, " "))
}

func (c *Controller) describeVoiceTarget(ctx context.Context, target voice.Session) voice.Session {
	sessions, err := c.ListVoiceSessions(ctx)
	if err != nil {
		return target
	}
	for _, session := range sessions {
		if session.ID == target.ID {
			return session
		}
	}
	return target
}

func voiceWorkingTarget(timeline []domain.TimelineItem, after int64) (voice.Session, bool) {
	for index := len(timeline) - 1; index >= 0; index-- {
		item := timeline[index]
		if item.Seq <= after {
			break
		}
		message, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range message.Tools {
			if call.Tool != domain.ToolKindSessionDelegate || (call.Status != domain.ToolStatusPending && call.Status != domain.ToolStatusRunning) {
				continue
			}
			return voice.Session{ID: strings.TrimSpace(call.Args["session_id"]), Title: "Work session"}, true
		}
	}
	return voice.Session{}, false
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
			case tools.PresentationStoredResult:
				parts = append(parts, voice.Part{MIMEType: result.MIMEType, Data: result.Content, Metadata: map[string]string{
					"title": result.Title, "presentation": "true",
				}})
			case *tools.PresentationStoredResult:
				if result != nil {
					parts = append(parts, voice.Part{MIMEType: result.MIMEType, Data: result.Content, Metadata: map[string]string{
						"title": result.Title, "presentation": "true",
					}})
				}
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
