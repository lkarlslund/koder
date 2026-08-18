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
	DelegateVoice(context.Context, string, string) (DelegationResult, error)
}

// RouteAction is a constrained decision produced by the voice coordination
// profile. The coordinator validates every decision before acting on it.
type RouteAction string

const (
	RouteExisting      RouteAction = "existing"
	RouteNewTemporary  RouteAction = "new_temporary"
	RouteNewPersistent RouteAction = "new_persistent"
	RouteClarify       RouteAction = "clarify"
)

// RouteRequest contains only bounded summaries, never full chat transcripts.
type RouteRequest struct {
	Text            string
	ActiveSessionID string
	Sessions        []Session
}

// RouteDecision selects a target lifecycle and whether the utterance itself is
// work to delegate. A selection-only request can set Delegate to false.
type RouteDecision struct {
	Action    RouteAction
	SessionID string
	Title     string
	Question  string
	Delegate  bool
}

// RoutingBackend is optional so simple embedders can retain deterministic
// lexical routing. Koder's production controller implements it.
type RoutingBackend interface {
	ResolveVoiceRoute(context.Context, RouteRequest) (RouteDecision, error)
	CreateVoiceTarget(context.Context, string, bool) (Session, error)
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

// VoiceSessionBackend owns the durable coordination chat layered above an
// ephemeral phone connection.
type VoiceSessionBackend interface {
	ListVoiceChats(context.Context) ([]Session, error)
	EnsureVoiceSession(context.Context, string) (Session, error)
	RecordVoiceExchange(context.Context, string, string, Message) error
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
	VoiceSessionID  string    `json:"voice_session_id"`
	ActiveSessionID string    `json:"active_session_id,omitempty"`
	Sessions        []Session `json:"sessions"`
	VoiceSessions   []Session `json:"voice_sessions"`
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
	return CallState{
		VoiceSessionID: c.voiceSessionID, ActiveSessionID: c.activeSessionID,
		Sessions: sessions, VoiceSessions: voiceSessions,
	}, nil
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
	if targetSessionID = strings.TrimSpace(targetSessionID); targetSessionID != "" {
		if _, ok := sessionByID(state.Sessions, targetSessionID); !ok {
			return Message{}, fmt.Errorf("session %q was not found", targetSessionID)
		}
		c.activeSessionID = targetSessionID
		return c.delegate(ctx, text)
	}

	if asksForSessionList(text) {
		if len(state.Sessions) == 0 {
			return textMessage("There are no work sessions yet."), nil
		}
		return textMessage(sessionListResponse(state.Sessions)), nil
	}
	if session, ok := requestedSession(state.Sessions, text); ok {
		c.activeSessionID = session.ID
		return textMessage("I found " + session.Title + ". What should we do there?"), nil
	}
	if router, ok := c.backend.(RoutingBackend); ok {
		decision, routeErr := router.ResolveVoiceRoute(ctx, RouteRequest{
			Text: text, ActiveSessionID: c.activeSessionID, Sessions: state.Sessions,
		})
		if routeErr == nil {
			return c.applyRoute(ctx, router, state.Sessions, text, decision)
		}
	}
	if len(state.Sessions) == 0 {
		return textMessage("There are no Koder sessions yet. Create one in the browser first."), nil
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

func (c *Call) applyRoute(
	ctx context.Context,
	backend RoutingBackend,
	sessions []Session,
	text string,
	decision RouteDecision,
) (Message, error) {
	switch decision.Action {
	case RouteExisting:
		session, ok := sessionByID(sessions, strings.TrimSpace(decision.SessionID))
		if !ok {
			return Message{}, fmt.Errorf("voice router selected unknown session %q", decision.SessionID)
		}
		c.activeSessionID = session.ID
		if !decision.Delegate {
			return textMessage("Using " + session.Title + ". What should we do there?"), nil
		}
		return c.delegate(ctx, text)
	case RouteNewTemporary, RouteNewPersistent:
		created, err := backend.CreateVoiceTarget(ctx, strings.TrimSpace(decision.Title), decision.Action == RouteNewPersistent)
		if err != nil {
			return Message{}, err
		}
		if strings.TrimSpace(created.ID) == "" {
			return Message{}, fmt.Errorf("voice backend created a target without an id")
		}
		c.activeSessionID = created.ID
		if !decision.Delegate {
			return textMessage("Created " + created.Title + ". What should we do there?"), nil
		}
		return c.delegate(ctx, text)
	case RouteClarify:
		question := strings.TrimSpace(decision.Question)
		if question == "" {
			question = "Should I use an existing session or start a new one?"
		}
		return textMessage(concise(question, 240)), nil
	default:
		return Message{}, fmt.Errorf("voice router returned unsupported action %q", decision.Action)
	}
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
	parts := []Part{{MIMEType: "text/plain", Data: result.Text}}
	parts = append(parts, result.Parts...)
	return Message{
		SpokenText: spoken,
		Parts:      parts,
		Delegation: &result,
	}, nil
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
