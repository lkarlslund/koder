// Package voice coordinates ephemeral voice calls with ordinary Koder sessions.
package voice

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Session is the bounded session summary exposed to a voice call.
type Session struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	LastMessage string    `json:"last_message,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
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
	RunVoiceTurn(context.Context, string, string, func(Session) error) (Message, error)
}

// VoiceHistoryBackend exposes the user-visible transcript of a durable voice
// chat. It is optional so non-persistent coordinator backends stay small.
type VoiceHistoryBackend interface {
	VoiceSessionHistory(context.Context, string, string, int) (TranscriptPage, error)
}

// TranscriptEntry is one user-visible turn from a durable voice chat.
type TranscriptEntry struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// TranscriptPage is a newest-edge or cursor-bounded page of complete voice
// turns, returned in chronological order.
type TranscriptPage struct {
	Entries []TranscriptEntry `json:"entries"`
	HasMore bool              `json:"has_more"`
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
	SpokenText string            `json:"spoken_text"`
	Parts      []Part            `json:"parts,omitempty"`
	Delegation *DelegationResult `json:"delegation,omitempty"`
}

// CallState is the current server-owned routing state for one connection.
type CallState struct {
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
	voiceSessionID  string
	activeSessionID string
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
		slices.SortStableFunc(voiceSessions, func(a, b Session) int {
			return b.UpdatedAt.Compare(a.UpdatedAt)
		})
	}
	if c.activeSessionID != "" && !sessionExists(sessions, c.activeSessionID) {
		c.activeSessionID = ""
	}
	var history TranscriptPage
	if c.voiceSessionID != "" {
		if backend, ok := c.backend.(VoiceHistoryBackend); ok {
			history, err = backend.VoiceSessionHistory(ctx, c.voiceSessionID, "", 5)
			if err != nil {
				return CallState{}, err
			}
		}
	}
	return CallState{
		VoiceSessionID: c.voiceSessionID, ActiveSessionID: c.activeSessionID,
		Sessions: sessions, VoiceSessions: voiceSessions,
		History: history.Entries, HistoryHasMore: history.HasMore,
	}, nil
}

// History returns older transcript turns for the selected durable voice chat.
func (c *Call) History(ctx context.Context, beforeID string, limit int) (TranscriptPage, error) {
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
		c.activeSessionID = ""
		return textMessage("Automatic session selection is on."), nil
	}
	session, ok := sessionByID(state.Sessions, strings.TrimSpace(sessionID))
	if !ok {
		return Message{}, fmt.Errorf("session %q was not found", sessionID)
	}
	c.activeSessionID = session.ID
	text := "Using " + session.Title + "."
	return textMessage(text), nil
}

// SelectVoiceSession changes the durable coordination transcript used by this
// call without releasing or acquiring the process-wide live-call lease.
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
