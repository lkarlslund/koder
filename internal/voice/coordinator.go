// Package voice coordinates ephemeral voice calls with ordinary Koder sessions.
package voice

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

// Session is the bounded session summary exposed to a voice call.
type Session struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Kind        string    `json:"kind,omitempty"`
	LastMessage string    `json:"last_message,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	ChatCount   int       `json:"chat_count,omitempty"`
	VoiceChats  int       `json:"voice_chat_count,omitempty"`
	Archived    bool      `json:"archived,omitempty"`
	Pinned      bool      `json:"pinned,omitempty"`
	Favorite    bool      `json:"favorite,omitempty"`
	Deleted     bool      `json:"deleted,omitempty"`
	ResultCount uint64    `json:"result_count,omitempty"`
	Busy        bool      `json:"busy,omitempty"`
	Status      string    `json:"status,omitempty"`
}

// Chat is a bounded chat summary shown beneath a session in the native app.
// InteractionMode controls whether it may be selected as a live voice
// conversation; Backend and WorkflowRole remain independent.
type Chat struct {
	ID                string    `json:"id"`
	SessionID         string    `json:"session_id"`
	Title             string    `json:"title"`
	Role              string    `json:"role,omitempty"` // Deprecated: use WorkflowRole.
	Backend           string    `json:"backend"`
	WorkflowRole      string    `json:"workflow_role"`
	InteractionMode   string    `json:"interaction_mode"`
	ModelID           string    `json:"model_id,omitempty"`
	PermissionProfile string    `json:"permission_profile,omitempty"`
	LastMessage       string    `json:"last_message,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
	Archived          bool      `json:"archived,omitempty"`
	Busy              bool      `json:"busy,omitempty"`
	Status            string    `json:"status,omitempty"`
	StatusText        string    `json:"status_text,omitempty"`
}

// ChatBackendOption describes a live turn backend offered during chat creation.
type ChatBackendOption struct {
	ID        string            `json:"id"`
	Label     string            `json:"label"`
	Available bool              `json:"available"`
	Detail    string            `json:"detail,omitempty"`
	Models    []ChatModelOption `json:"models,omitempty"`
}

type ChatModelOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

// SessionUpdate changes user-managed voice conversation metadata.
type SessionUpdate struct {
	Title    *string
	Archived *bool
	Pinned   *bool
	Favorite *bool
	Deleted  *bool
}

// ChatUpdate changes user-managed chat metadata without affecting history.
type ChatUpdate struct {
	Title    *string
	Archived *bool
}

// DelegationResult is the public outcome of work performed in an ordinary chat.
type DelegationResult struct {
	SessionID      string `json:"session_id"`
	SessionTitle   string `json:"session_title"`
	ChatID         string `json:"chat_id,omitempty"`
	Text           string `json:"text"`
	NeedsAttention bool   `json:"needs_attention,omitempty"`
	Parts          []Part `json:"parts,omitempty"`
}

// Backend is the process-owned session capability needed by a voice call.
type Backend interface {
	ListVoiceSessions(context.Context) ([]Session, error)
}

const PCM16LE = "pcm_s16le"

// AudioFormat describes raw audio without tying presentations to a fixed set
// of UI widgets.
type AudioFormat struct {
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
}

// AudioConfig is the server-owned voice transport contract.
type AudioConfig struct {
	Input               AudioFormat `json:"input"`
	Output              AudioFormat `json:"output"`
	MaxUtteranceSeconds int         `json:"max_utterance_seconds"`
}

// TranscriptionHints are client-specific recognition preferences. An empty
// language list leaves language detection under server configuration.
type TranscriptionHints struct {
	Languages []string
}

// SpeechBackend connects the voice transport to remote STT and TTS services.
type SpeechBackend interface {
	VoiceAudioConfig() AudioConfig
	TranscribeVoice(context.Context, AudioFormat, []byte, TranscriptionHints) (string, error)
	StreamVoiceSpeech(context.Context, string, func([]byte) error) error
}

// VoiceSessionBackend owns the durable coordination chat layered above an
// ephemeral phone connection.
type VoiceSessionBackend interface {
	ListVoiceChats(context.Context) ([]Session, error)
	EnsureVoiceSession(context.Context, string) (Session, error)
	CreateVoiceSession(context.Context, string) (Session, error)
	RenameVoiceSession(context.Context, string, string) (Session, error)
	UpdateVoiceSession(context.Context, string, SessionUpdate) (Session, error)
	DeleteVoiceSession(context.Context, string) error
	RunVoiceTurn(context.Context, string, string, TurnOptions, func(Session) error) (Message, error)
}

// SessionChatBackend exposes Koder's native session/chat hierarchy to voice
// clients. It supersedes the legacy model where every voice chat occupied its
// own special session.
type SessionChatBackend interface {
	ListSessionChats(context.Context, string) ([]Chat, error)
	EnsureVoiceChat(context.Context, string, string) (Chat, error)
	CreateVoiceChatInSession(context.Context, string, domain.ChatCreateSpec) (Chat, error)
	CreateTemporaryVoiceChat(context.Context, domain.ChatCreateSpec) (Session, Chat, error)
	RunVoiceChatTurn(context.Context, string, string, string, TurnOptions, func(Session) error) (Message, error)
}

// SessionManagementBackend exposes the same organization operations used by
// Koder's web session browser to native voice clients.
type SessionManagementBackend interface {
	UpdateClientSession(context.Context, string, SessionUpdate) (Session, error)
	DeleteClientSession(context.Context, string) error
	UpdateClientChat(context.Context, string, string, ChatUpdate) (Chat, error)
	DeleteClientChat(context.Context, string, string) error
}

// ResponsePacing controls spoken answer length without adding instructions to chat history.
type ResponsePacing string

const (
	ResponsePacingConcise  ResponsePacing = "concise"
	ResponsePacingNormal   ResponsePacing = "normal"
	ResponsePacingDetailed ResponsePacing = "detailed"
)

// ParseResponsePacing validates a wire value, defaulting an omitted value to normal.
func ParseResponsePacing(value string) (ResponsePacing, error) {
	pacing := ResponsePacing(strings.ToLower(strings.TrimSpace(value)))
	if pacing == "" {
		return ResponsePacingNormal, nil
	}
	switch pacing {
	case ResponsePacingConcise, ResponsePacingNormal, ResponsePacingDetailed:
		return pacing, nil
	default:
		return "", fmt.Errorf("unsupported response pacing %q", value)
	}
}

// TurnOptions contains per-connection behavior that must not pollute durable history.
type TurnOptions struct {
	ResponsePacing ResponsePacing
	OnRender       func(RenderEvent) error
}

// RenderEvent is non-spoken content that a client may display while the model
// continues working. Parts use the same generic MIME adapter as final messages.
type RenderEvent struct {
	Parts []Part `json:"parts"`
}

// Instruction returns a transient system instruction for the selected pacing.
func (p ResponsePacing) Instruction() string {
	switch p {
	case ResponsePacingConcise:
		return "Response pacing for this call is concise. Answer in one brief conversational sentence, normally no more than 25 spoken words."
	case ResponsePacingDetailed:
		return "Response pacing for this call is detailed. Give a conversational explanation of up to about five sentences and 140 spoken words when the extra detail is useful."
	default:
		return "Response pacing for this call is normal. Answer in one or two short conversational sentences, normally no more than 60 spoken words."
	}
}

// MaxSpokenWords is the last-mile safety limit for this pacing.
func (p ResponsePacing) MaxSpokenWords() int {
	switch p {
	case ResponsePacingConcise:
		return 30
	case ResponsePacingDetailed:
		return 150
	default:
		return 65
	}
}

// VoiceHistoryBackend exposes the user-visible transcript of a durable voice
// chat. It is optional so non-persistent coordinator backends stay small.
type VoiceHistoryBackend interface {
	VoiceSessionHistory(context.Context, string, string, int) (TranscriptPage, error)
}

// VoiceHistorySearchBackend performs explicit full-history search for native clients.
type VoiceHistorySearchBackend interface {
	SearchVoiceSessionHistory(context.Context, string, string, int) ([]TranscriptSearchResult, error)
}

// SessionChatHistoryBackend serves history for a selected voice chat within a
// normal session.
type SessionChatHistoryBackend interface {
	VoiceChatHistory(context.Context, string, string, string, int) (TranscriptPage, error)
	SearchVoiceChatHistory(context.Context, string, string, string, int) ([]TranscriptSearchResult, error)
}

// TranscriptEntry is one user-visible turn from a durable voice chat.
type TranscriptEntry struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	Parts     []Part    `json:"parts,omitempty"`
}

// TranscriptPage is a newest-edge or cursor-bounded page of complete voice
// turns, returned in chronological order.
type TranscriptPage struct {
	Entries []TranscriptEntry `json:"entries"`
	HasMore bool              `json:"has_more"`
}

// TranscriptSearchResult carries the exact match and a small jump context.
type TranscriptSearchResult struct {
	Match   TranscriptEntry   `json:"match"`
	Context []TranscriptEntry `json:"context"`
}

// ArtifactFile is an authenticated presentation resource resolved by Koder.
type ArtifactFile struct {
	Path     string
	Name     string
	MIMEType string
}

// ArtifactBackend resolves only resources already surfaced by a delegated
// chat. It does not provide arbitrary filesystem access to the voice client.
type ArtifactBackend interface {
	VoiceSessionArtifact(string, string) (ArtifactFile, error)
	VoiceOfferedArtifact(context.Context, string) (ArtifactFile, error)
}

// Part is one generic MIME-typed presentation. Data may be any JSON value;
// clients render known MIME types and retain a useful fallback for unknown ones.
type Part struct {
	ID       string            `json:"id,omitempty"`
	MIMEType string            `json:"mime_type"`
	Data     any               `json:"data,omitempty"`
	URI      string            `json:"uri,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Message is the concise response returned to the voice client.
type Message struct {
	SpokenText   string            `json:"spoken_text"`
	TranscriptID string            `json:"transcript_id,omitempty"`
	Parts        []Part            `json:"parts,omitempty"`
	Delegation   *DelegationResult `json:"delegation,omitempty"`
}

// CallState is the current server-owned routing state for one connection.
type CallState struct {
	SessionID       string            `json:"session_id,omitempty"`
	ChatID          string            `json:"chat_id,omitempty"`
	Chats           []Chat            `json:"chats,omitempty"`
	VoiceSessionID  string            `json:"voice_session_id"`
	ActiveSessionID string            `json:"active_session_id,omitempty"`
	Sessions        []Session         `json:"sessions"`
	VoiceSessions   []Session         `json:"voice_sessions"`
	History         []TranscriptEntry `json:"history,omitempty"`
	HistoryHasMore  bool              `json:"history_has_more,omitempty"`
}

// Call is an ephemeral coordinator bound to one voice connection.
type Call struct {
	backend         Backend
	sessionID       string
	chatID          string
	voiceSessionID  string
	activeSessionID string
}

// NewSessionCall starts a voice call in Koder's native session/chat hierarchy.
func NewSessionCall(backend Backend, sessionID, chatID string) *Call {
	return &Call{backend: backend, sessionID: strings.TrimSpace(sessionID), chatID: strings.TrimSpace(chatID)}
}

// NewCall starts a voice call coordinator.
func NewCall(backend Backend, voiceSessionID ...string) *Call {
	call := &Call{backend: backend}
	if len(voiceSessionID) > 0 {
		call.voiceSessionID = strings.TrimSpace(voiceSessionID[0])
	}
	return call
}

// State returns the available sessions and current target.
func (c *Call) State(ctx context.Context) (CallState, error) {
	if c == nil || c.backend == nil {
		return CallState{}, fmt.Errorf("voice backend is unavailable")
	}
	sessions, err := c.backend.ListVoiceSessions(ctx)
	if err != nil {
		return CallState{}, err
	}
	slices.SortStableFunc(sessions, func(a, b Session) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	voiceSessions := []Session(nil)
	if backend, ok := c.backend.(VoiceSessionBackend); ok {
		voiceSessions, err = backend.ListVoiceChats(ctx)
		if err != nil {
			return CallState{}, err
		}
		voiceSessions = slices.DeleteFunc(voiceSessions, func(session Session) bool {
			return session.Archived || session.Deleted
		})
		slices.SortStableFunc(voiceSessions, func(a, b Session) int {
			if a.Pinned != b.Pinned {
				if a.Pinned {
					return -1
				}
				return 1
			}
			return b.UpdatedAt.Compare(a.UpdatedAt)
		})
	}
	if c.activeSessionID != "" && !sessionExists(sessions, c.activeSessionID) {
		c.activeSessionID = ""
	}
	var chats []Chat
	if c.sessionID != "" {
		if !sessionExists(sessions, c.sessionID) {
			c.sessionID, c.chatID = "", ""
		} else if backend, ok := c.backend.(SessionChatBackend); ok {
			chats, err = backend.ListSessionChats(ctx, c.sessionID)
			if err != nil {
				return CallState{}, err
			}
			if c.chatID != "" {
				selected, ensureErr := backend.EnsureVoiceChat(ctx, c.sessionID, c.chatID)
				if ensureErr != nil {
					c.chatID = ""
				} else {
					c.chatID = selected.ID
				}
			}
		}
	}
	var history TranscriptPage
	if c.sessionID != "" && c.chatID != "" {
		if backend, ok := c.backend.(SessionChatHistoryBackend); ok {
			history, err = backend.VoiceChatHistory(ctx, c.sessionID, c.chatID, "", 5)
			if err != nil {
				return CallState{}, err
			}
		}
	} else if c.voiceSessionID != "" {
		if backend, ok := c.backend.(VoiceHistoryBackend); ok {
			history, err = backend.VoiceSessionHistory(ctx, c.voiceSessionID, "", 5)
			if err != nil {
				return CallState{}, err
			}
		}
	}
	return CallState{
		SessionID: c.sessionID, ChatID: c.chatID, Chats: chats,
		VoiceSessionID: c.voiceSessionID, ActiveSessionID: c.activeSessionID,
		Sessions: sessions, VoiceSessions: voiceSessions,
		History: history.Entries, HistoryHasMore: history.HasMore,
	}, nil
}

// History returns older transcript turns for the selected durable voice chat.
func (c *Call) History(ctx context.Context, beforeID string, limit int) (TranscriptPage, error) {
	if c != nil && c.sessionID != "" && c.chatID != "" {
		if backend, ok := c.backend.(SessionChatHistoryBackend); ok {
			return backend.VoiceChatHistory(ctx, c.sessionID, c.chatID, strings.TrimSpace(beforeID), limit)
		}
	}
	if c == nil || c.backend == nil || c.voiceSessionID == "" {
		return TranscriptPage{}, nil
	}
	backend, ok := c.backend.(VoiceHistoryBackend)
	if !ok {
		return TranscriptPage{}, nil
	}
	return backend.VoiceSessionHistory(ctx, c.voiceSessionID, strings.TrimSpace(beforeID), limit)
}

// SelectSession explicitly changes the target for subsequent utterances.
func (c *Call) SelectSession(ctx context.Context, sessionID string) (Message, error) {
	state, err := c.State(ctx)
	if err != nil {
		return Message{}, err
	}
	if strings.TrimSpace(sessionID) == "" {
		c.sessionID, c.chatID, c.activeSessionID = "", "", ""
		return textMessage("No session selected."), nil
	}
	session, ok := sessionByID(state.Sessions, strings.TrimSpace(sessionID))
	if !ok {
		return Message{}, fmt.Errorf("session %q was not found", sessionID)
	}
	c.sessionID, c.chatID, c.activeSessionID = session.ID, "", session.ID
	text := "Opened " + session.Title + "."
	return textMessage(text), nil
}

// SelectVoiceChat selects a voice chat inside the currently selected session.
func (c *Call) SelectVoiceChat(ctx context.Context, sessionID, chatID string) (Message, error) {
	backend, ok := c.backend.(SessionChatBackend)
	if !ok {
		return Message{}, fmt.Errorf("session chat backend is unavailable")
	}
	chat, err := backend.EnsureVoiceChat(ctx, strings.TrimSpace(sessionID), strings.TrimSpace(chatID))
	if err != nil {
		return Message{}, err
	}
	c.sessionID, c.chatID, c.activeSessionID = chat.SessionID, chat.ID, chat.SessionID
	return textMessage("Using voice chat " + chat.Title + "."), nil
}

// CreateVoiceChat creates and selects a voice chat in an existing session.
func (c *Call) CreateVoiceChat(ctx context.Context, sessionID string, spec domain.ChatCreateSpec) (Chat, error) {
	backend, ok := c.backend.(SessionChatBackend)
	if !ok {
		return Chat{}, fmt.Errorf("session chat backend is unavailable")
	}
	spec.Title = strings.TrimSpace(spec.Title)
	chat, err := backend.CreateVoiceChatInSession(ctx, strings.TrimSpace(sessionID), spec)
	if err != nil {
		return Chat{}, err
	}
	c.sessionID, c.chatID, c.activeSessionID = chat.SessionID, chat.ID, chat.SessionID
	return chat, nil
}

// CreateTemporaryVoiceChat creates and selects a quick session backed by a
// managed scratch folder.
func (c *Call) CreateTemporaryVoiceChat(ctx context.Context, spec domain.ChatCreateSpec) (Session, Chat, error) {
	backend, ok := c.backend.(SessionChatBackend)
	if !ok {
		return Session{}, Chat{}, fmt.Errorf("session chat backend is unavailable")
	}
	spec.Title = strings.TrimSpace(spec.Title)
	session, chat, err := backend.CreateTemporaryVoiceChat(ctx, spec)
	if err != nil {
		return Session{}, Chat{}, err
	}
	c.sessionID, c.chatID, c.activeSessionID = session.ID, chat.ID, session.ID
	return session, chat, nil
}

// SelectVoiceSession changes the durable coordination transcript used by this
// call without releasing or acquiring the connected device's live-call lease.
func (c *Call) SelectVoiceSession(ctx context.Context, sessionID string) (Message, error) {
	backend, ok := c.backend.(VoiceSessionBackend)
	if !ok {
		return Message{}, fmt.Errorf("voice session backend is unavailable")
	}
	session, err := backend.EnsureVoiceSession(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return Message{}, err
	}
	c.voiceSessionID = session.ID
	return textMessage("Using voice chat " + session.Title + "."), nil
}

// CreateVoiceSession creates and selects a durable coordination transcript for
// this call.
func (c *Call) CreateVoiceSession(ctx context.Context, title string) (Session, error) {
	backend, ok := c.backend.(VoiceSessionBackend)
	if !ok {
		return Session{}, fmt.Errorf("voice session backend is unavailable")
	}
	session, err := backend.CreateVoiceSession(ctx, strings.TrimSpace(title))
	if err != nil {
		return Session{}, err
	}
	if strings.TrimSpace(session.ID) == "" {
		return Session{}, fmt.Errorf("voice backend created a voice session without an id")
	}
	c.voiceSessionID = session.ID
	return session, nil
}

func textMessage(text string) Message {
	return Message{SpokenText: text, Parts: []Part{{MIMEType: "text/plain", Data: text}}}
}

func sessionExists(sessions []Session, id string) bool {
	_, ok := sessionByID(sessions, id)
	return ok
}

func sessionByID(sessions []Session, id string) (Session, bool) {
	for _, session := range sessions {
		if session.ID == id {
			return session, true
		}
	}
	return Session{}, false
}
