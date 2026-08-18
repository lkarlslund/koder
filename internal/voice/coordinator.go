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
}

// Backend is the process-owned session capability needed by a voice call.
type Backend interface {
	ListVoiceSessions(context.Context) ([]Session, error)
	DelegateVoice(context.Context, string, string) (DelegationResult, error)
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

// SpeechBackend connects the voice transport to remote STT and TTS services.
type SpeechBackend interface {
	VoiceAudioConfig() AudioConfig
	TranscribeVoice(context.Context, AudioFormat, []byte) (string, error)
	StreamVoiceSpeech(context.Context, string, func([]byte) error) error
}

// Part is one generic MIME-typed visible response part.
type Part struct {
	MIMEType string `json:"mime_type"`
	Text     string `json:"text,omitempty"`
	URL      string `json:"url,omitempty"`
	Name     string `json:"name,omitempty"`
	Alt      string `json:"alt,omitempty"`
}

// Message is the concise response returned to the voice client.
type Message struct {
	SpokenText string            `json:"spoken_text"`
	Parts      []Part            `json:"parts,omitempty"`
	Delegation *DelegationResult `json:"delegation,omitempty"`
}

// CallState is the current server-owned routing state for one connection.
type CallState struct {
	ActiveSessionID string    `json:"active_session_id,omitempty"`
	Sessions        []Session `json:"sessions"`
}

// Call is an ephemeral coordinator bound to one voice connection.
type Call struct {
	backend         Backend
	activeSessionID string
}

// NewCall starts a voice call coordinator.
func NewCall(backend Backend) *Call {
	return &Call{backend: backend}
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
	if c.activeSessionID != "" && !sessionExists(sessions, c.activeSessionID) {
		c.activeSessionID = ""
	}
	return CallState{ActiveSessionID: c.activeSessionID, Sessions: sessions}, nil
}

// SelectSession explicitly changes the target for subsequent utterances.
func (c *Call) SelectSession(ctx context.Context, sessionID string) (Message, error) {
	state, err := c.State(ctx)
	if err != nil {
		return Message{}, err
	}
	session, ok := sessionByID(state.Sessions, strings.TrimSpace(sessionID))
	if !ok {
		return Message{}, fmt.Errorf("session %q was not found", sessionID)
	}
	c.activeSessionID = session.ID
	text := "Using " + session.Title + "."
	return textMessage(text), nil
}

// HandleText routes or delegates one final user utterance.
func (c *Call) HandleText(ctx context.Context, text, targetSessionID string) (Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Message{}, fmt.Errorf("utterance text is required")
	}
	state, err := c.State(ctx)
	if err != nil {
		return Message{}, err
	}
	if len(state.Sessions) == 0 {
		return textMessage("There are no Koder sessions yet. Create one in the browser first."), nil
	}

	if targetSessionID = strings.TrimSpace(targetSessionID); targetSessionID != "" {
		if _, ok := sessionByID(state.Sessions, targetSessionID); !ok {
			return Message{}, fmt.Errorf("session %q was not found", targetSessionID)
		}
		c.activeSessionID = targetSessionID
		return c.delegate(ctx, text)
	}

	if asksForSessionList(text) {
		return textMessage(sessionListResponse(state.Sessions)), nil
	}
	if session, ok := requestedSession(state.Sessions, text); ok {
		c.activeSessionID = session.ID
		return textMessage("I found " + session.Title + ". What should we do there?"), nil
	}
	if c.activeSessionID == "" {
		if len(state.Sessions) == 1 {
			c.activeSessionID = state.Sessions[0].ID
		} else if session, ok := bestSessionMatch(state.Sessions, text); ok {
			c.activeSessionID = session.ID
		} else {
			return textMessage("Which session should I use? " + sessionListResponse(state.Sessions)), nil
		}
	}
	return c.delegate(ctx, text)
}

func (c *Call) delegate(ctx context.Context, text string) (Message, error) {
	result, err := c.backend.DelegateVoice(ctx, c.activeSessionID, text)
	if err != nil {
		return Message{}, err
	}
	spoken := concise(result.Text, 480)
	if spoken == "" {
		spoken = "The delegated chat finished without a text response."
	}
	return Message{
		SpokenText: spoken,
		Parts:      []Part{{MIMEType: "text/plain", Text: result.Text}},
		Delegation: &result,
	}, nil
}

func textMessage(text string) Message {
	return Message{SpokenText: text, Parts: []Part{{MIMEType: "text/plain", Text: text}}}
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

func asksForSessionList(text string) bool {
	normalized := strings.ToLower(text)
	return strings.Contains(normalized, "list sessions") ||
		strings.Contains(normalized, "what sessions") ||
		strings.Contains(normalized, "which sessions")
}

func requestedSession(sessions []Session, text string) (Session, bool) {
	normalized := strings.ToLower(text)
	selectionLanguage := strings.Contains(normalized, "session") && (strings.Contains(normalized, "pick up") ||
		strings.Contains(normalized, "switch") ||
		strings.Contains(normalized, "use ") ||
		strings.Contains(normalized, "resume") ||
		strings.Contains(normalized, "continue"))
	if !selectionLanguage {
		return Session{}, false
	}
	return bestSessionMatch(sessions, text)
}

func bestSessionMatch(sessions []Session, text string) (Session, bool) {
	query := significantTokens(text)
	bestScore, runnerUp := 0, 0
	var best Session
	for _, session := range sessions {
		haystack := significantTokens(session.Title + " " + session.LastMessage)
		score := overlapScore(query, haystack)
		if score > bestScore {
			runnerUp = bestScore
			bestScore = score
			best = session
		} else if score > runnerUp {
			runnerUp = score
		}
	}
	return best, best.ID != "" && bestScore >= 2 && bestScore > runnerUp
}

func significantTokens(text string) map[string]struct{} {
	replacer := strings.NewReplacer(",", " ", ".", " ", "?", " ", "!", " ", ":", " ", ";", " ", "-", " ", "_", " ")
	stop := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "for": {}, "i": {}, "in": {}, "is": {}, "it": {}, "me": {},
		"of": {}, "on": {}, "session": {}, "that": {}, "the": {}, "this": {}, "to": {}, "we": {}, "where": {},
	}
	out := map[string]struct{}{}
	for _, token := range strings.Fields(strings.ToLower(replacer.Replace(text))) {
		if len(token) < 3 {
			continue
		}
		if _, ignored := stop[token]; !ignored {
			out[token] = struct{}{}
		}
	}
	return out
}

func overlapScore(query, candidate map[string]struct{}) int {
	score := 0
	for token := range query {
		if _, ok := candidate[token]; ok {
			score++
		}
	}
	return score
}

func sessionListResponse(sessions []Session) string {
	const limit = 5
	names := make([]string, 0, min(len(sessions), limit))
	for _, session := range sessions[:min(len(sessions), limit)] {
		names = append(names, session.Title)
	}
	response := "Available sessions: " + strings.Join(names, ", ") + "."
	if len(sessions) > limit {
		response += fmt.Sprintf(" There are %d more.", len(sessions)-limit)
	}
	return response
}

func concise(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}
