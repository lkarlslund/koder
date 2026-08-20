package app

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/provider"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/chattool"
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
	client, err := provider.New(providerID, providerCfg, nil, c.providerHealth)
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
	client, err := provider.New(providerID, providerCfg, nil, c.providerHealth)
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
	all, err := c.workspaceSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]voice.Session, 0, len(all))
	for _, session := range all {
		if session.Kind != domain.SessionKindRegular && session.Kind != domain.SessionKindQuick && session.Kind != domain.SessionKindVoice {
			continue
		}
		if !session.DeletedAt.IsZero() {
			continue
		}
		summary := voice.Session{
			ID:          string(session.ID),
			Title:       voiceSessionTitle(session),
			Kind:        session.Kind.String(),
			LastMessage: session.LastMessage,
			UpdatedAt:   session.UpdatedAt,
			Archived:    session.Archived,
			Pinned:      session.Pinned,
			Favorite:    session.Favorite,
			Deleted:     !session.DeletedAt.IsZero(),
		}
		owner, loadErr := c.agent.LoadSession(ctx, session.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		for _, chatRecord := range owner.Snapshot().Chats {
			if chatRecord.Archived {
				continue
			}
			summary.ChatCount++
			if isVoiceInteraction(chatRecord) {
				summary.VoiceChats++
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

// ListSessionChats returns bounded metadata for every chat in one session.
func (c *Controller) ListSessionChats(ctx context.Context, sessionID string) ([]voice.Chat, error) {
	owner, err := c.agent.LoadSession(ctx, id.ID(strings.TrimSpace(sessionID)))
	if err != nil {
		return nil, err
	}
	snapshot := owner.Snapshot()
	if !c.sessionInWorkspace(snapshot.Session) {
		return nil, fmt.Errorf("session %s does not belong to this workspace", snapshot.Session.ID)
	}
	out := make([]voice.Chat, 0, len(snapshot.Chats))
	for _, chatRecord := range snapshot.Chats {
		status, statusErr := owner.ChatStatus(ctx, chatRecord.ID)
		if statusErr != nil {
			return nil, statusErr
		}
		out = append(out, voice.Chat{
			ID: string(chatRecord.ID), SessionID: string(chatRecord.SessionID),
			Title: chatRecord.Title, Role: voiceChatRoleLabel(chatRecord),
			LastMessage: chatRecord.LastMessage, UpdatedAt: chatRecord.UpdatedAt,
			Archived: chatRecord.Archived, Busy: status.Busy,
			Status: string(status.State), StatusText: status.StatusText,
		})
	}
	slices.SortStableFunc(out, func(a, b voice.Chat) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out, nil
}

// EnsureVoiceChat verifies that chatID is a selectable voice chat inside
// sessionID. When chatID is omitted, it selects the newest voice chat.
func (c *Controller) EnsureVoiceChat(ctx context.Context, sessionID, chatID string) (voice.Chat, error) {
	chats, err := c.ListSessionChats(ctx, sessionID)
	if err != nil {
		return voice.Chat{}, err
	}
	chatID = strings.TrimSpace(chatID)
	for _, item := range chats {
		if item.Archived || item.Role != string(chatrole.Voice) {
			continue
		}
		if chatID == "" || item.ID == chatID {
			return item, nil
		}
	}
	if chatID == "" {
		return voice.Chat{}, fmt.Errorf("session %s has no voice chat", strings.TrimSpace(sessionID))
	}
	return voice.Chat{}, fmt.Errorf("voice chat %s was not found in session %s", chatID, strings.TrimSpace(sessionID))
}

// CreateVoiceChatInSession creates a selectable top-level voice orchestrator
// alongside the session's existing chats.
func (c *Controller) CreateVoiceChatInSession(ctx context.Context, sessionID, title string) (voice.Chat, error) {
	owner, err := c.agent.LoadSession(ctx, id.ID(strings.TrimSpace(sessionID)))
	if err != nil {
		return voice.Chat{}, err
	}
	snapshot := owner.Snapshot()
	if !c.sessionInWorkspace(snapshot.Session) {
		return voice.Chat{}, fmt.Errorf("session %s does not belong to this workspace", snapshot.Session.ID)
	}
	if snapshot.Session.Kind == domain.SessionKindQuick {
		return voice.Chat{}, fmt.Errorf("quick sessions cannot create additional chats")
	}
	title = truncateVoiceText(strings.TrimSpace(title), 80)
	if title == "" {
		title = "Voice conversation"
	}
	runtime, err := owner.NewRootChatWithDimensions(ctx, title, chatrole.Orchestrator, domain.ChatBackendKoder, domain.InteractionModeVoice)
	if err != nil {
		return voice.Chat{}, err
	}
	chatRecord := runtime.Snapshot().Chat
	c.broadcast("chats_delta", map[string]any{"session_id": snapshot.Session.ID, "chats": owner.Snapshot().Chats})
	return voice.Chat{
		ID: string(chatRecord.ID), SessionID: string(chatRecord.SessionID), Title: chatRecord.Title,
		Role: voiceChatRoleLabel(chatRecord), UpdatedAt: chatRecord.UpdatedAt,
	}, nil
}

// CreateTemporaryVoiceChat creates a quick session whose only chat is a voice
// orchestrator and whose project root is Koder-managed scratch storage.
func (c *Controller) CreateTemporaryVoiceChat(ctx context.Context, title string) (voice.Session, voice.Chat, error) {
	owner, err := c.agent.CreateQuickVoiceSession(ctx)
	if err != nil {
		return voice.Session{}, voice.Chat{}, err
	}
	snapshot := owner.Snapshot()
	if len(snapshot.Chats) != 1 {
		return voice.Session{}, voice.Chat{}, fmt.Errorf("temporary voice session must contain exactly one chat")
	}
	title = truncateVoiceText(strings.TrimSpace(title), 80)
	if title != "" {
		if err := c.RenameSession(ctx, snapshot.Session.ID, title); err != nil {
			return voice.Session{}, voice.Chat{}, err
		}
		if _, _, err := owner.UpdateChat(ctx, snapshot.Chats[0].ID, chattool.UpdateRequest{Title: title}); err != nil {
			return voice.Session{}, voice.Chat{}, err
		}
		snapshot.Session.Title = title
		snapshot.Chats[0].Title = title
	}
	sessionSummary := voice.Session{
		ID: string(snapshot.Session.ID), Title: voiceSessionTitle(snapshot.Session), Kind: snapshot.Session.Kind.String(),
		UpdatedAt: snapshot.Session.UpdatedAt, ChatCount: 1, VoiceChats: 1,
	}
	chatRecord := snapshot.Chats[0]
	chatSummary := voice.Chat{
		ID: string(chatRecord.ID), SessionID: string(chatRecord.SessionID), Title: chatRecord.Title,
		Role: string(chatRecord.WorkflowRole), UpdatedAt: chatRecord.UpdatedAt,
	}
	return sessionSummary, chatSummary, nil
}

// ListVoiceChats returns the durable coordination transcripts available to a
// native voice call. These are separate from work-session routing targets.
func (c *Controller) ListVoiceChats(ctx context.Context) ([]voice.Session, error) {
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return nil, err
	}
	live := make(map[id.ID]sessionpkg.SessionSnapshot)
	for _, owner := range c.agent.LoadedSessions() {
		snapshot := owner.Snapshot()
		live[snapshot.Session.ID] = snapshot
	}
	out := make([]voice.Session, 0)
	for _, session := range sessions {
		if session.Kind != domain.SessionKindVoice {
			continue
		}
		if !session.DeletedAt.IsZero() {
			continue
		}
		summary := voiceSessionSummary(session)
		snapshot, loaded := live[session.ID]
		if !loaded && summary.LastMessage == "" {
			// Older voice sessions predate the session-level preview. Loading
			// metadata is timeline-free and lets their persisted chat summary
			// seed the preview without hydrating the full transcript.
			owner, loadErr := c.agent.LoadSession(ctx, session.ID)
			if loadErr != nil {
				return nil, loadErr
			}
			snapshot = owner.Snapshot()
		}
		applyVoiceRuntimeSummary(&summary, snapshot)
		out = append(out, summary)
	}
	return out, nil
}

// UpdateClientSession changes session organization metadata for a native
// client without changing the session's last-activity timestamp.
func (c *Controller) UpdateClientSession(ctx context.Context, sessionID string, update voice.SessionUpdate) (voice.Session, error) {
	if c.agent == nil {
		return voice.Session{}, fmt.Errorf("no chat agent")
	}
	if update.Deleted != nil {
		return voice.Session{}, fmt.Errorf("deleted sessions cannot be restored; delete removes them permanently")
	}
	owner, err := c.agent.LoadSession(ctx, id.ID(strings.TrimSpace(sessionID)))
	if err != nil {
		return voice.Session{}, err
	}
	current := owner.Snapshot().Session
	if !c.sessionInWorkspace(current) {
		return voice.Session{}, fmt.Errorf("session %s does not belong to this workspace", current.ID)
	}
	var title *string
	if update.Title != nil {
		normalized := truncateVoiceText(strings.TrimSpace(*update.Title), 80)
		if normalized == "" {
			return voice.Session{}, fmt.Errorf("session title is required")
		}
		title = &normalized
	}
	updated, err := owner.UpdateSessionMetadata(ctx, func(session *domain.Session) {
		if title != nil {
			session.Title = *title
			session.TitleGeneratedAt = time.Time{}
			session.TitleRefreshCount = 0
		}
		if update.Archived != nil {
			session.Archived = *update.Archived
			if session.Archived {
				session.Pinned = false
			}
		}
		if update.Pinned != nil {
			session.Pinned = *update.Pinned
			if session.Pinned {
				session.Archived = false
			}
		}
		if update.Favorite != nil {
			session.Favorite = *update.Favorite
		}
	})
	if err != nil {
		return voice.Session{}, fmt.Errorf("update session: %w", err)
	}
	state, err := c.Sessions(ctx)
	if err != nil {
		return voice.Session{}, err
	}
	c.broadcast("sessions_delta", sessionListPayload(state, ""))
	return clientSessionSummary(updated, owner.Snapshot().Chats), nil
}

// DeleteClientSession permanently removes an idle session and its chats.
func (c *Controller) DeleteClientSession(ctx context.Context, sessionID string) error {
	return c.DeleteSession(ctx, id.ID(strings.TrimSpace(sessionID)))
}

// UpdateClientChat changes one chat's title or archive state.
func (c *Controller) UpdateClientChat(ctx context.Context, sessionID, chatID string, update voice.ChatUpdate) (voice.Chat, error) {
	if c.agent == nil {
		return voice.Chat{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, id.ID(strings.TrimSpace(sessionID)))
	if err != nil {
		return voice.Chat{}, err
	}
	snapshot := owner.Snapshot()
	if !c.sessionInWorkspace(snapshot.Session) {
		return voice.Chat{}, fmt.Errorf("session %s does not belong to this workspace", snapshot.Session.ID)
	}
	request := chattool.UpdateRequest{Archived: update.Archived}
	if update.Title != nil {
		request.Title = strings.TrimSpace(*update.Title)
		if request.Title == "" {
			return voice.Chat{}, fmt.Errorf("chat title is required")
		}
	}
	if _, _, err := owner.UpdateChat(ctx, id.ID(strings.TrimSpace(chatID)), request); err != nil {
		return voice.Chat{}, err
	}
	chats, err := c.ListSessionChats(ctx, sessionID)
	if err != nil {
		return voice.Chat{}, err
	}
	for _, item := range chats {
		if item.ID == strings.TrimSpace(chatID) {
			c.broadcast("chats_delta", map[string]any{"session_id": snapshot.Session.ID, "chats": owner.Snapshot().Chats})
			return item, nil
		}
	}
	return voice.Chat{}, fmt.Errorf("chat %s not found after update", chatID)
}

// DeleteClientChat permanently removes an archived leaf chat.
func (c *Controller) DeleteClientChat(ctx context.Context, sessionID, chatID string) error {
	if c.agent == nil {
		return fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, id.ID(strings.TrimSpace(sessionID)))
	if err != nil {
		return err
	}
	if !c.sessionInWorkspace(owner.Snapshot().Session) {
		return fmt.Errorf("session %s does not belong to this workspace", sessionID)
	}
	if err := owner.DeleteChat(ctx, id.ID(strings.TrimSpace(chatID))); err != nil {
		return err
	}
	c.broadcast("chats_delta", map[string]any{"session_id": sessionID, "chats": owner.Snapshot().Chats})
	return nil
}

func clientSessionSummary(session domain.Session, chats []domain.Chat) voice.Session {
	summary := voice.Session{
		ID: string(session.ID), Title: voiceSessionTitle(session), Kind: session.Kind.String(), LastMessage: session.LastMessage,
		UpdatedAt: session.UpdatedAt, Archived: session.Archived, Pinned: session.Pinned, Favorite: session.Favorite,
	}
	for _, item := range chats {
		if item.Archived {
			continue
		}
		summary.ChatCount++
		if isVoiceInteraction(item) {
			summary.VoiceChats++
		}
	}
	return summary
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
		if session.Archived || !session.DeletedAt.IsZero() {
			continue
		}
		if requestedID != "" && string(session.ID) == requestedID {
			return voiceSessionSummary(session), nil
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
	return voiceSessionSummary(newest), nil
}

// CreateVoiceSession creates a durable voice chat for a native voice client.
func (c *Controller) CreateVoiceSession(ctx context.Context, title string) (voice.Session, error) {
	session, _, err := c.CreateVoiceChat(ctx, strings.TrimSpace(title))
	if err != nil {
		return voice.Session{}, err
	}
	return voiceSessionSummary(session), nil
}

// UpdateVoiceSession changes durable organization metadata after verifying the
// session belongs to this workspace and is a voice conversation.
func (c *Controller) UpdateVoiceSession(ctx context.Context, sessionID string, update voice.SessionUpdate) (voice.Session, error) {
	if c.agent == nil {
		return voice.Session{}, fmt.Errorf("no chat agent")
	}
	owner, err := c.agent.LoadSession(ctx, id.ID(strings.TrimSpace(sessionID)))
	if err != nil {
		return voice.Session{}, err
	}
	current := owner.Snapshot().Session
	if !c.sessionInWorkspace(current) || current.Kind != domain.SessionKindVoice {
		return voice.Session{}, fmt.Errorf("session %s is not a voice chat in this workspace", current.ID)
	}
	var title *string
	if update.Title != nil {
		normalized := truncateVoiceText(strings.TrimSpace(*update.Title), 80)
		if normalized == "" {
			return voice.Session{}, fmt.Errorf("voice session title is required")
		}
		title = &normalized
	}
	updated, err := owner.UpdateSessionMetadata(ctx, func(session *domain.Session) {
		if title != nil {
			session.Title = *title
			session.TitleGeneratedAt = time.Time{}
			session.TitleRefreshCount = 0
		}
		if update.Archived != nil {
			session.Archived = *update.Archived
			if session.Archived {
				session.Pinned = false
			}
		}
		if update.Pinned != nil {
			session.Pinned = *update.Pinned
			if session.Pinned {
				session.Archived = false
			}
		}
		if update.Favorite != nil {
			session.Favorite = *update.Favorite
		}
		if update.Deleted != nil {
			if *update.Deleted {
				session.DeletedAt = time.Now().UTC()
				session.Pinned = false
			} else {
				session.DeletedAt = time.Time{}
			}
		}
	})
	if err != nil {
		return voice.Session{}, fmt.Errorf("update voice session: %w", err)
	}
	sessionState, err := c.Sessions(ctx)
	if err != nil {
		return voice.Session{}, fmt.Errorf("list sessions after voice session update: %w", err)
	}
	c.broadcast("sessions_delta", sessionListPayload(sessionState, ""))
	return voiceSessionSummary(updated), nil
}

// RenameVoiceSession changes a durable voice chat title after verifying its kind.
func (c *Controller) RenameVoiceSession(ctx context.Context, sessionID, title string) (voice.Session, error) {
	return c.UpdateVoiceSession(ctx, sessionID, voice.SessionUpdate{Title: &title})
}

// DeleteVoiceSession moves a durable voice chat into the recoverable deleted state.
func (c *Controller) DeleteVoiceSession(ctx context.Context, sessionID string) error {
	deleted := true
	_, err := c.UpdateVoiceSession(ctx, sessionID, voice.SessionUpdate{Deleted: &deleted})
	return err
}

// VoiceSessionHistory returns a bounded, presentation-safe transcript for a
// native client reconnecting to an existing voice conversation.
func (c *Controller) VoiceSessionHistory(ctx context.Context, voiceSessionID, beforeID string, limit int) (voice.TranscriptPage, error) {
	return c.voiceChatHistory(ctx, strings.TrimSpace(voiceSessionID), "", beforeID, limit)
}

// VoiceChatHistory returns transcript turns for a voice chat inside an
// ordinary or quick session.
func (c *Controller) VoiceChatHistory(ctx context.Context, sessionID, chatID, beforeID string, limit int) (voice.TranscriptPage, error) {
	return c.voiceChatHistory(ctx, strings.TrimSpace(sessionID), strings.TrimSpace(chatID), beforeID, limit)
}

func (c *Controller) voiceChatHistory(ctx context.Context, sessionID, chatID, beforeID string, limit int) (voice.TranscriptPage, error) {
	owner, session, chatRecord, err := c.resolveSelectedChatWithTouch(ctx, Selection{SessionID: id.ID(sessionID), ChatID: id.ID(chatID)}, true, false)
	if err != nil {
		return voice.TranscriptPage{}, err
	}
	if !isVoiceInteraction(chatRecord) {
		return voice.TranscriptPage{}, fmt.Errorf("chat %s in session %s is not a voice chat", chatRecord.ID, session.ID)
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

// SearchVoiceSessionHistory searches the complete durable voice transcript and
// returns recent matches with enough neighboring messages for an anchored jump.
func (c *Controller) SearchVoiceSessionHistory(ctx context.Context, voiceSessionID, query string, limit int) ([]voice.TranscriptSearchResult, error) {
	return c.searchVoiceChatHistory(ctx, strings.TrimSpace(voiceSessionID), "", query, limit)
}

// SearchVoiceChatHistory searches one selected voice chat inside a session.
func (c *Controller) SearchVoiceChatHistory(ctx context.Context, sessionID, chatID, query string, limit int) ([]voice.TranscriptSearchResult, error) {
	return c.searchVoiceChatHistory(ctx, strings.TrimSpace(sessionID), strings.TrimSpace(chatID), query, limit)
}

func (c *Controller) searchVoiceChatHistory(ctx context.Context, sessionID, chatID, query string, limit int) ([]voice.TranscriptSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("transcript search query is required")
	}
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	_, session, chatRecord, runtime, err := c.resolveSelectedRuntimeWithoutTouch(ctx, Selection{SessionID: id.ID(sessionID), ChatID: id.ID(chatID)}, true)
	if err != nil {
		return nil, err
	}
	if !isVoiceInteraction(chatRecord) {
		return nil, fmt.Errorf("chat %s in session %s is not a voice chat", chatRecord.ID, session.ID)
	}
	timeline, err := runtime.FullTimeline(ctx)
	if err != nil {
		return nil, fmt.Errorf("load voice transcript for search: %w", err)
	}
	entries := voiceTranscriptEntries(timeline)
	foldedQuery := strings.ToLower(query)
	results := make([]voice.TranscriptSearchResult, 0, min(limit, len(entries)))
	for index := len(entries) - 1; index >= 0 && len(results) < limit; index-- {
		if !strings.Contains(strings.ToLower(entries[index].Text), foldedQuery) {
			continue
		}
		start := max(0, index-3)
		end := min(len(entries), index+4)
		results = append(results, voice.TranscriptSearchResult{
			Match: entries[index], Context: append([]voice.TranscriptEntry(nil), entries[start:end]...),
		})
	}
	return results, nil
}

func voiceTranscriptEntries(items []domain.TimelineItem) []voice.TranscriptEntry {
	entries := make([]voice.TranscriptEntry, 0, len(items))
	for _, item := range items {
		var role, text string
		var parts []voice.Part
		switch content := item.Content.(type) {
		case domain.UserMessage:
			role, text = "user", content.Text
		case domain.AssistantMessage:
			role, text = "assistant", content.Text
			parts = voiceRenderParts([]domain.TimelineItem{item}, item.Seq-1)
		}
		text = strings.TrimSpace(text)
		if text == "" && len(parts) == 0 {
			continue
		}
		if text == "" {
			role = "activity"
		}
		entry := voice.TranscriptEntry{ID: string(item.ID), Role: role, Text: text, CreatedAt: item.CreatedAt, Parts: parts}
		entries = append(entries, entry)
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
func (c *Controller) RunVoiceTurn(ctx context.Context, voiceSessionID, text string, options voice.TurnOptions, onWorking func(voice.Session) error) (voice.Message, error) {
	return c.runVoiceChatTurn(ctx, strings.TrimSpace(voiceSessionID), "", text, options, onWorking)
}

// RunVoiceChatTurn executes an utterance in a selected voice chat inside its
// owning session.
func (c *Controller) RunVoiceChatTurn(ctx context.Context, sessionID, chatID, text string, options voice.TurnOptions, onWorking func(voice.Session) error) (voice.Message, error) {
	return c.runVoiceChatTurn(ctx, strings.TrimSpace(sessionID), strings.TrimSpace(chatID), text, options, onWorking)
}

func (c *Controller) runVoiceChatTurn(ctx context.Context, sessionID, chatID, text string, options voice.TurnOptions, onWorking func(voice.Session) error) (voice.Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return voice.Message{}, fmt.Errorf("voice transcript is required")
	}
	pacing, err := voice.ParseResponsePacing(string(options.ResponsePacing))
	if err != nil {
		return voice.Message{}, err
	}
	owner, session, chatRecord, runtime, err := c.resolveSelectedRuntimeWithoutTouch(ctx, Selection{SessionID: id.ID(sessionID), ChatID: id.ID(chatID)}, true)
	if err != nil {
		return voice.Message{}, err
	}
	if !isVoiceInteraction(chatRecord) {
		return voice.Message{}, fmt.Errorf("chat %s in session %s is not a voice chat", chatRecord.ID, session.ID)
	}
	if err := runtime.EnsureTimeline(ctx); err != nil {
		return voice.Message{}, err
	}
	initial := runtime.Snapshot()
	if initial.Active || (initial.Status != "" && initial.Status != chat.StatusIdle && initial.Status != chat.StatusErrored) {
		return voice.Message{}, fmt.Errorf("voice chat is already busy")
	}
	releasePhonePolicy := c.phone.BeginVoiceTurnForChat(phonedevice.CallIDFromContext(ctx), string(chatRecord.ID), text)
	defer releasePhonePolicy()
	initialSeq := latestTimelineSequence(initial.Timeline)
	updates, unsubscribe := runtime.Subscribe()
	defer unsubscribe()
	runtime.Enqueue(chat.QueueItem{
		Kind:   chat.QueueKindUser,
		Source: domain.UserMessageSourceVoice,
		Text:   text,
		EphemeralInstructions: []provider.InstructionBlock{{
			Kind: provider.InstructionKindRuntime,
			Text: pacing.Instruction(),
		}},
	})
	started := false
	workingSent := false
	workingTargetID := ""
	rendered := map[string]struct{}{}
	for {
		select {
		case <-ctx.Done():
			return voice.Message{}, fmt.Errorf("wait for voice chat: %w", ctx.Err())
		case update, ok := <-updates:
			if !ok {
				return voice.Message{}, fmt.Errorf("voice chat closed before replying")
			}
			snapshot := update.Snapshot
			if options.OnRender != nil {
				parts := unseenRenderParts(voicePresentationParts(runtime.SnapshotTimeline(), initialSeq), rendered)
				if len(parts) != 0 {
					if err := options.OnRender(voice.RenderEvent{Parts: parts}); err != nil {
						return voice.Message{}, err
					}
				}
			}
			if voiceTurnStarted(snapshot.Status, snapshot.Active) {
				started = true
			}
			if snapshot.Status == chat.StatusRunningTools && onWorking != nil {
				target, found := voiceWorkingTarget(runtime.SnapshotTimeline(), initialSeq)
				if found {
					target = c.describeVoiceTarget(ctx, target)
				}
				if !workingSent || (found && target.ID != workingTargetID) {
					if err := onWorking(target); err != nil {
						return voice.Message{}, err
					}
					workingSent = true
					workingTargetID = target.ID
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
			if responseItem, response := latestAssistantItemAfter(timeline, initialSeq); response != "" {
				spoken := pacedSpokenResponse(response, pacing)
				parts := []voice.Part{{MIMEType: "text/plain", Data: spoken}}
				parts = append(parts, voiceRenderParts(timeline, initialSeq)...)
				if _, err := owner.UpdateSession(ctx, func(session *domain.Session) {
					session.LastMessage = truncateVoiceText(spoken, 240)
					session.VoiceResultCount++
				}); err != nil {
					return voice.Message{}, fmt.Errorf("store voice conversation preview: %w", err)
				}
				return voice.Message{SpokenText: spoken, TranscriptID: string(responseItem.ID), Parts: parts}, nil
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

func pacedSpokenResponse(text string, pacing voice.ResponsePacing) string {
	maxWords := pacing.MaxSpokenWords()
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
		chats, chatErr := c.ListSessionChats(ctx, session.ID)
		if chatErr != nil {
			continue
		}
		for _, chat := range chats {
			if chat.ID == target.ID {
				target.Title = chat.Title
				target.Kind = "chat"
				return target
			}
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
			if call.Status != domain.ToolStatusPending && call.Status != domain.ToolStatusRunning {
				continue
			}
			switch call.Tool {
			case domain.ToolKindChatSend:
				return voice.Session{ID: strings.TrimSpace(call.Args["chat_id"]), Title: "Chat", Kind: "chat"}, true
			case domain.ToolKindSessionDelegate:
				return voice.Session{ID: strings.TrimSpace(call.Args["session_id"]), Title: "Work session"}, true
			}
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
		partMetadata["render_key"] = metadata.ID
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
					"title": result.Title, "presentation": "true", "render_key": string(call.ToolCallID),
				}})
			case *tools.PresentationStoredResult:
				if result != nil {
					parts = append(parts, voice.Part{MIMEType: result.MIMEType, Data: result.Content, Metadata: map[string]string{
						"title": result.Title, "presentation": "true", "render_key": string(call.ToolCallID),
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

func voiceRenderParts(timeline []domain.TimelineItem, sequence int64) []voice.Part {
	parts := voicePresentationParts(timeline, sequence)
	for _, item := range timeline {
		if item.Seq <= sequence {
			continue
		}
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range assistant.Tools {
			summary := truncateVoiceText(strings.TrimSpace(tools.Preview(tools.Request{Tool: call.Tool, ToolCallID: string(call.ToolCallID), Args: call.Args})), 180)
			parts = append(parts, voice.Part{
				ID:       string(call.ToolCallID),
				MIMEType: "application/vnd.koder.tool-activity+json",
				Data: map[string]any{
					"tool": call.Tool, "title": tools.Info(call.Tool).Title, "status": call.Status, "summary": summary,
				},
				Metadata: map[string]string{"surface": "transcript", "render_key": "tool:" + string(call.ToolCallID)},
			})
		}
	}
	return parts
}

func unseenRenderParts(parts []voice.Part, seen map[string]struct{}) []voice.Part {
	out := make([]voice.Part, 0, len(parts))
	for _, part := range parts {
		key := strings.TrimSpace(part.Metadata["render_key"])
		if key == "" {
			key = part.MIMEType + "\x00" + part.URI
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, part)
	}
	return out
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

func isVoiceInteraction(chatRecord domain.Chat) bool {
	return chatRecord.EffectiveInteractionMode() == domain.InteractionModeVoice
}

// Keep the native voice protocol compatible while Android migrates from the
// old composite role field to an explicit interaction-mode field.
func voiceChatRoleLabel(chatRecord domain.Chat) string {
	if isVoiceInteraction(chatRecord) {
		return string(chatrole.Voice)
	}
	return string(chatRecord.EffectiveWorkflowRole())
}

func voiceSessionSummary(session domain.Session) voice.Session {
	return voice.Session{
		ID: string(session.ID), Title: voiceSessionTitle(session), LastMessage: session.LastMessage, UpdatedAt: session.UpdatedAt,
		Archived: session.Archived, Pinned: session.Pinned, Favorite: session.Favorite, Deleted: !session.DeletedAt.IsZero(),
		ResultCount: session.VoiceResultCount,
	}
}

func applyVoiceRuntimeSummary(summary *voice.Session, snapshot sessionpkg.SessionSnapshot) {
	if summary == nil || snapshot.Session.ID == "" {
		return
	}
	for _, chatRecord := range snapshot.Chats {
		if !isVoiceInteraction(chatRecord) {
			continue
		}
		if summary.LastMessage == "" {
			summary.LastMessage = truncateVoiceText(chatRecord.LastMessage, 240)
		}
		runtime, ok := snapshot.Snapshots[chatRecord.ID]
		if !ok {
			continue
		}
		summary.Status = string(runtime.Status)
		summary.Busy = runtime.Active || runtime.Status == chat.StatusWaitingLLM ||
			runtime.Status == chat.StatusStreamingThoughts || runtime.Status == chat.StatusStreamingResponse ||
			runtime.Status == chat.StatusRunningTools || runtime.Status == chat.StatusWaitingApproval ||
			runtime.Status == chat.StatusWaitingInput
	}
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
	_, text := latestAssistantItemAfter(timeline, sequence)
	return text
}

func latestAssistantItemAfter(timeline []domain.TimelineItem, sequence int64) (domain.TimelineItem, string) {
	for index := len(timeline) - 1; index >= 0; index-- {
		item := timeline[index]
		if item.Seq <= sequence {
			break
		}
		message, ok := item.Content.(domain.AssistantMessage)
		if ok && item.Sealed() && strings.TrimSpace(message.Text) != "" {
			return item, strings.TrimSpace(message.Text)
		}
	}
	return domain.TimelineItem{}, ""
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
