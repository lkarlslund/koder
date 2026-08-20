package voiceapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/androidupdate"
	"github.com/lkarlslund/koder/internal/deviceauth"
	"github.com/lkarlslund/koder/internal/phonedevice"
	"github.com/lkarlslund/koder/internal/voice"
)

type fakeUpdateSource struct {
	meta androidupdate.Manifest
	path string
}

func (f fakeUpdateSource) Manifest() (androidupdate.Manifest, bool, error) {
	return f.meta, true, nil
}

func (f fakeUpdateSource) OpenAPK() (fs.File, error) { return os.Open(f.path) }

type fakeBackend struct {
	delegated      bool
	transcript     string
	languages      []string
	spoken         string
	artifact       voice.ArtifactFile
	voiceChats     []voice.Session
	history        []voice.TranscriptEntry
	pacing         voice.ResponsePacing
	searchQuery    string
	genericWorking bool
}

type handoffBackend struct {
	fakeBackend
	turnCalls       atomic.Int32
	firstAudioReady chan struct{}
	continueAudio   chan struct{}
}

func (b *handoffBackend) RunVoiceTurn(_ context.Context, _ string, _ string, _ voice.TurnOptions, _ func(voice.Session) error) (voice.Message, error) {
	b.turnCalls.Add(1)
	return voice.Message{SpokenText: "The handoff worked.", TranscriptID: "answer-1"}, nil
}

func (b *handoffBackend) StreamVoiceSpeech(ctx context.Context, _ string, consume func([]byte) error) error {
	if err := consume([]byte{1, 0}); err != nil {
		return err
	}
	close(b.firstAudioReady)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.continueAudio:
	}
	return consume([]byte{2, 0})
}

func (f *fakeBackend) SearchVoiceSessionHistory(_ context.Context, _ string, query string, _ int) ([]voice.TranscriptSearchResult, error) {
	f.searchQuery = query
	match := voice.TranscriptEntry{ID: "message-2", Role: "assistant", Text: "The laptop boots normally."}
	return []voice.TranscriptSearchResult{{Match: match, Context: []voice.TranscriptEntry{
		{ID: "message-1", Role: "user", Text: "Check the laptop"}, match,
	}}}, nil
}

func (f *fakeBackend) VoiceSessionHistory(_ context.Context, _ string, beforeID string, limit int) (voice.TranscriptPage, error) {
	end := len(f.history)
	if beforeID != "" {
		for index, entry := range f.history {
			if entry.ID == beforeID {
				end = index
				break
			}
		}
	}
	start := max(0, end-limit)
	return voice.TranscriptPage{Entries: append([]voice.TranscriptEntry(nil), f.history[start:end]...), HasMore: start > 0}, nil
}

func (f *fakeBackend) VoiceSessionArtifact(_, _ string) (voice.ArtifactFile, error) {
	if f.artifact.Path == "" {
		return voice.ArtifactFile{}, os.ErrNotExist
	}
	return f.artifact, nil
}

func (f *fakeBackend) VoiceOfferedArtifact(context.Context, string) (voice.ArtifactFile, error) {
	return f.VoiceSessionArtifact("", "")
}

func (f *fakeBackend) ListVoiceSessions(context.Context) ([]voice.Session, error) {
	return []voice.Session{{ID: "session-1", Title: "Laptop repair"}}, nil
}

func (f *fakeBackend) ListVoiceChats(context.Context) ([]voice.Session, error) {
	if f.voiceChats != nil {
		return append([]voice.Session(nil), f.voiceChats...), nil
	}
	return []voice.Session{
		{ID: "voice-1", Title: "Personal"},
		{ID: "voice-2", Title: "Work"},
	}, nil
}

func (f *fakeBackend) CreateVoiceSession(_ context.Context, title string) (voice.Session, error) {
	if strings.TrimSpace(title) == "" {
		title = "Voice Chat"
	}
	created := voice.Session{ID: "voice-created", Title: title}
	if f.voiceChats == nil {
		f.voiceChats, _ = f.ListVoiceChats(context.Background())
	}
	f.voiceChats = append(f.voiceChats, created)
	return created, nil
}

func (f *fakeBackend) RenameVoiceSession(_ context.Context, sessionID, title string) (voice.Session, error) {
	return f.UpdateVoiceSession(context.Background(), sessionID, voice.SessionUpdate{Title: &title})
}

func (f *fakeBackend) UpdateVoiceSession(_ context.Context, sessionID string, update voice.SessionUpdate) (voice.Session, error) {
	for index := range f.voiceChats {
		if f.voiceChats[index].ID == sessionID {
			if update.Title != nil {
				f.voiceChats[index].Title = *update.Title
			}
			if update.Archived != nil {
				f.voiceChats[index].Archived = *update.Archived
			}
			if update.Pinned != nil {
				f.voiceChats[index].Pinned = *update.Pinned
			}
			if update.Favorite != nil {
				f.voiceChats[index].Favorite = *update.Favorite
			}
			if update.Deleted != nil {
				f.voiceChats[index].Deleted = *update.Deleted
			}
			return f.voiceChats[index], nil
		}
	}
	return voice.Session{}, fmt.Errorf("voice session not found")
}

func (f *fakeBackend) DeleteVoiceSession(_ context.Context, sessionID string) error {
	for index := range f.voiceChats {
		if f.voiceChats[index].ID == sessionID {
			f.voiceChats[index].Deleted = true
			return nil
		}
	}
	return fmt.Errorf("voice session not found")
}

func (f *fakeBackend) VoiceAudioConfig() voice.AudioConfig {
	return voice.AudioConfig{
		Input:               voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 16000, Channels: 1},
		Output:              voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 24000, Channels: 1},
		MaxUtteranceSeconds: 60,
	}
}

func (f *fakeBackend) EnsureVoiceSession(_ context.Context, requestedID string) (voice.Session, error) {
	if requestedID == "" {
		requestedID = "voice-1"
	}
	return voice.Session{ID: requestedID, Title: "Voice Chat"}, nil
}

func (f *fakeBackend) RunVoiceTurn(ctx context.Context, _ string, text string, options voice.TurnOptions, onWorking func(voice.Session) error) (voice.Message, error) {
	f.pacing = options.ResponsePacing
	target := voice.Session{ID: "session-1", Title: "Laptop repair"}
	if f.genericWorking {
		target = voice.Session{}
	}
	if onWorking != nil {
		if err := onWorking(target); err != nil {
			return voice.Message{}, err
		}
	}
	result, err := f.DelegateVoice(ctx, target.ID, text)
	if err != nil {
		return voice.Message{}, err
	}
	spoken := result.Text
	if !strings.HasSuffix(spoken, ".") {
		spoken += "."
	}
	return voice.Message{SpokenText: spoken, Parts: []voice.Part{{MIMEType: "text/plain", Data: spoken}}, Delegation: &result}, nil
}

func TestRunVoiceTurnKeepsGenericToolWorkTargetless(t *testing.T) {
	backend := &fakeBackend{genericWorking: true}
	handler := NewHandler(backend, "")
	turn := newCachedTurn("call-1", "utterance-1", textTurnFingerprint("check it"), "voice-1")
	handler.runVoiceTurn(t.Context(), turn, "check it", voice.ResponsePacingNormal)

	var working *serverFrame
	for _, event := range turn.snapshot().events {
		if event.frame != nil && event.frame.State == "working" {
			working = event.frame
			break
		}
	}
	if working == nil || working.WorkingOn != nil {
		t.Fatalf("generic working frame = %#v, want working state without a fabricated session", working)
	}
}

func (f *fakeBackend) TranscribeVoice(_ context.Context, format voice.AudioFormat, pcm []byte, hints voice.TranscriptionHints) (string, error) {
	if format != f.VoiceAudioConfig().Input || len(pcm) == 0 {
		return "", context.Canceled
	}
	f.transcript = "check the laptop"
	f.languages = append([]string(nil), hints.Languages...)
	return f.transcript, nil
}

func (f *fakeBackend) StreamVoiceSpeech(_ context.Context, text string, consume func([]byte) error) error {
	f.spoken = text
	if err := consume([]byte{1}); err != nil {
		return err
	}
	return consume([]byte{0, 2, 0})
}

func TestSharedProtocolFixturesDecode(t *testing.T) {
	historyData, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	var history serverFrame
	if err := json.Unmarshal(historyData, &history); err != nil {
		t.Fatal(err)
	}
	if history.Type != "history" || len(history.History) != 2 || !history.HasMore {
		t.Fatalf("history fixture = %#v", history)
	}
	audioStartData, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", "audio_start.json"))
	if err != nil {
		t.Fatal(err)
	}
	var audioStart clientFrame
	if err := json.Unmarshal(audioStartData, &audioStart); err != nil {
		t.Fatal(err)
	}
	if audioStart.Type != "audio_start" || !slices.Equal(audioStart.Languages, []string{"da", "en"}) {
		t.Fatalf("audio_start fixture = %#v", audioStart)
	}
	utteranceData, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", "utterance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var utterance clientFrame
	if err := json.Unmarshal(utteranceData, &utterance); err != nil {
		t.Fatal(err)
	}
	if utterance.Protocol != protocolVersion || utterance.Type != "utterance" || utterance.UtteranceID == "" {
		t.Fatalf("utterance fixture = %#v", utterance)
	}
	messageData, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", "message.json"))
	if err != nil {
		t.Fatal(err)
	}
	var message serverFrame
	if err := json.Unmarshal(messageData, &message); err != nil {
		t.Fatal(err)
	}
	if message.Protocol != protocolVersion || message.Type != "message" || message.Message == nil || len(message.Message.Parts) == 0 {
		t.Fatalf("message fixture = %#v", message)
	}
	presentationData, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", "presentation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var presentation serverFrame
	if err := json.Unmarshal(presentationData, &presentation); err != nil {
		t.Fatal(err)
	}
	if presentation.Message == nil || len(presentation.Message.Parts) != 2 || presentation.Message.Parts[1].Metadata["presentation"] != "true" {
		t.Fatalf("presentation fixture = %#v", presentation)
	}
	workingData, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", "working.json"))
	if err != nil {
		t.Fatal(err)
	}
	var working serverFrame
	if err := json.Unmarshal(workingData, &working); err != nil {
		t.Fatal(err)
	}
	if working.Protocol != protocolVersion || working.State != "working" || working.WorkingOn == nil || working.WorkingOn.ID != "session-fixture-1" {
		t.Fatalf("working fixture = %#v", working)
	}
	requestData, err := os.ReadFile(filepath.Join("..", "..", "protocol", "voice", "v1", "testdata", "device_tool_request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request deviceFrame
	if err := json.Unmarshal(requestData, &request); err != nil {
		t.Fatal(err)
	}
	if request.Protocol != protocolVersion || request.Type != "device_tool_request" || request.Action != phonedevice.SearchContacts || request.Arguments["query"] != "Steen" {
		t.Fatalf("phone request fixture = %#v", request)
	}
}

func TestNormalizeLanguages(t *testing.T) {
	got, err := normalizeLanguages([]string{" EN ", "da", "en"})
	if err != nil || !slices.Equal(got, []string{"da", "en"}) {
		t.Fatalf("normalizeLanguages() = %v, %v", got, err)
	}
	for _, invalid := range [][]string{{"german"}, {"de-DE"}, {"1x"}, make([]string, 9)} {
		if _, err := normalizeLanguages(invalid); err == nil {
			t.Fatalf("normalizeLanguages(%v) succeeded", invalid)
		}
	}
}

func (f *fakeBackend) DelegateVoice(_ context.Context, sessionID, text string) (voice.DelegationResult, error) {
	f.delegated = true
	return voice.DelegationResult{SessionID: sessionID, SessionTitle: "Laptop repair", ChatID: "chat-1", Text: "Done: " + text}, nil
}

func TestHandlerAuthenticatesAndDelegates(t *testing.T) {
	backend := &fakeBackend{}
	server := httptest.NewServer(NewHandler(backend, "secret"))
	defer server.Close()
	ctx := context.Background()
	url := "ws" + server.URL[len("http"):] + "/voice/v1"

	_, response, err := websocket.Dial(ctx, url, nil)
	if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dial response=%v err=%v", response, err)
	}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	readType(t, ctx, conn, "ready")
	if err := writeClientFrame(ctx, conn, clientFrame{Type: "search_history", Protocol: protocolVersion, Query: "boots", Limit: 20}); err != nil {
		t.Fatal(err)
	}
	search := readType(t, ctx, conn, "history_search")
	if backend.searchQuery != "boots" || len(search.SearchResults) != 1 || search.SearchResults[0].Match.ID != "message-2" || len(search.SearchResults[0].Context) != 2 {
		t.Fatalf("history search = %#v query=%q", search.SearchResults, backend.searchQuery)
	}
	if err := writeClientFrame(ctx, conn, clientFrame{Type: "hello", Protocol: protocolVersion, ResponsePacing: "detailed"}); err != nil {
		t.Fatal(err)
	}
	readType(t, ctx, conn, "ready")
	if err := writeClientFrame(ctx, conn, clientFrame{Type: "utterance", Protocol: protocolVersion, UtteranceID: "utt-1", Text: "check it", SessionID: "session-1"}); err != nil {
		t.Fatal(err)
	}
	processing := readType(t, ctx, conn, "state")
	if processing.State != "processing" {
		t.Fatalf("initial state = %#v", processing)
	}
	working := readType(t, ctx, conn, "state")
	if working.State != "working" || working.WorkingOn == nil || working.WorkingOn.ID != "session-1" {
		t.Fatalf("working state = %#v", working)
	}
	message := readType(t, ctx, conn, "message")
	if message.Message == nil || message.Message.SpokenText != "Done: check it." || !backend.delegated || backend.pacing != voice.ResponsePacingDetailed {
		t.Fatalf("message=%#v delegated=%v", message, backend.delegated)
	}
	readType(t, ctx, conn, "state")
	ttsStart := readType(t, ctx, conn, "tts_start")
	if ttsStart.AudioFormat == nil || *ttsStart.AudioFormat != backend.VoiceAudioConfig().Output {
		t.Fatalf("tts_start = %#v", ttsStart)
	}
	frame := readAudioFrame(t, ctx, conn)
	if frame.Kind != voice.AudioFrameOutputPCM || frame.Sequence != 0 || len(frame.PCM) != 4 {
		t.Fatalf("output audio = %#v", frame)
	}
	readType(t, ctx, conn, "tts_end")
	readType(t, ctx, conn, "ready")
}

func TestHandlerSwitchesDurableVoiceChat(t *testing.T) {
	backend := &fakeBackend{}
	server := httptest.NewServer(NewHandler(backend, ""))
	defer server.Close()
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, "ws"+server.URL[len("http"):]+"/voice/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	ready := readType(t, ctx, conn, "ready")
	if ready.CallState == nil || len(ready.CallState.VoiceSessions) != 2 {
		t.Fatalf("ready call state = %#v", ready.CallState)
	}
	if err := writeClientFrame(ctx, conn, clientFrame{
		Type: "select_voice_session", Protocol: protocolVersion, VoiceSessionID: "voice-2",
	}); err != nil {
		t.Fatal(err)
	}
	message := readType(t, ctx, conn, "message")
	if message.Message == nil || message.Message.SpokenText != "Using voice chat Voice Chat." {
		t.Fatalf("switch message = %#v", message)
	}
	readType(t, ctx, conn, "state")
	readType(t, ctx, conn, "tts_start")
	readAudioFrame(t, ctx, conn)
	readType(t, ctx, conn, "tts_end")
	ready = readType(t, ctx, conn, "ready")
	if ready.CallState == nil || ready.CallState.VoiceSessionID != "voice-2" {
		t.Fatalf("switched call state = %#v", ready.CallState)
	}
}

func TestHandlerCreatesAndSelectsDurableVoiceChat(t *testing.T) {
	backend := &fakeBackend{}
	server := httptest.NewServer(NewHandler(backend, ""))
	defer server.Close()
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, "ws"+server.URL[len("http"):]+"/voice/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	readType(t, ctx, conn, "ready")
	if err := writeClientFrame(ctx, conn, clientFrame{
		Type: "create_voice_session", Protocol: protocolVersion, Title: "Phone work",
	}); err != nil {
		t.Fatal(err)
	}
	ready := readType(t, ctx, conn, "ready")
	if ready.CallState == nil || ready.CallState.VoiceSessionID != "voice-created" {
		t.Fatalf("created call state = %#v", ready.CallState)
	}
	if len(ready.CallState.VoiceSessions) != 3 || ready.CallState.VoiceSessions[2].Title != "Phone work" {
		t.Fatalf("voice chats = %#v", ready.CallState.VoiceSessions)
	}
}

func TestHandlerListsAndCreatesVoiceSessionsWithoutStartingCall(t *testing.T) {
	now := time.Now().UTC()
	backend := &fakeBackend{voiceChats: []voice.Session{
		{ID: "voice-older", Title: "Older", UpdatedAt: now.Add(-time.Hour)},
		{ID: "voice-newer", Title: "Newer", UpdatedAt: now},
	}}
	handler := NewHandler(backend, "secret")
	handler.Lease = nil
	server := httptest.NewServer(handler)
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/voice/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var listed sessionsResponse
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || listed.Protocol != protocolVersion || len(listed.VoiceSessions) != 2 {
		t.Fatalf("list response status=%d body=%#v", response.StatusCode, listed)
	}
	if listed.VoiceSessions[0].ID != "voice-newer" || listed.VoiceSessions[1].ID != "voice-older" {
		t.Fatalf("voice sessions are not newest first: %#v", listed.VoiceSessions)
	}

	request, err = http.NewRequest(http.MethodPost, server.URL+"/voice/v1/sessions", strings.NewReader(`{"title":"Phone work"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var created sessionsResponse
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || created.VoiceSession == nil || created.VoiceSession.Title != "Phone work" {
		t.Fatalf("create response status=%d body=%#v", response.StatusCode, created)
	}
	if len(created.VoiceSessions) != 3 || created.VoiceSessions[2].ID != "voice-created" {
		t.Fatalf("created voice sessions = %#v", created.VoiceSessions)
	}
}

func TestHandlerRenamesVoiceSession(t *testing.T) {
	backend := &fakeBackend{voiceChats: []voice.Session{{ID: "voice-1", Title: "Old"}}}
	server := httptest.NewServer(NewHandler(backend, "secret"))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPatch, server.URL+"/voice/v1/sessions/voice-1", strings.NewReader(`{"title":"Family"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body sessionsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || body.VoiceSession == nil || body.VoiceSession.Title != "Family" {
		t.Fatalf("rename response status=%d body=%#v", response.StatusCode, body)
	}
}

func TestHandlerUpdatesVoiceSessionOrganizationAndSortsPinsFirst(t *testing.T) {
	now := time.Now().UTC()
	backend := &fakeBackend{voiceChats: []voice.Session{
		{ID: "voice-1", Title: "Older", LastMessage: "Finished it.", UpdatedAt: now.Add(-time.Hour), ResultCount: 4, Busy: true, Status: "running_tools"},
		{ID: "voice-2", Title: "Newer", UpdatedAt: now},
	}}
	server := httptest.NewServer(NewHandler(backend, "secret"))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPatch, server.URL+"/voice/v1/sessions/voice-1", strings.NewReader(`{"pinned":true,"favorite":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body sessionsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || body.VoiceSession == nil || !body.VoiceSession.Pinned || !body.VoiceSession.Favorite {
		t.Fatalf("organization response status=%d body=%#v", response.StatusCode, body)
	}
	if body.VoiceSession.LastMessage != "Finished it." || body.VoiceSession.ResultCount != 4 || !body.VoiceSession.Busy || body.VoiceSession.Status != "running_tools" {
		t.Fatalf("preview response = %#v", body.VoiceSession)
	}
	if body.VoiceSessions[0].ID != "voice-1" {
		t.Fatalf("pinned session was not sorted first: %#v", body.VoiceSessions)
	}
}

func TestHandlerDeletesVoiceSession(t *testing.T) {
	backend := &fakeBackend{voiceChats: []voice.Session{{ID: "voice-1", Title: "Old"}, {ID: "voice-2", Title: "Keep"}}}
	server := httptest.NewServer(NewHandler(backend, "secret"))
	defer server.Close()
	request, err := http.NewRequest(http.MethodDelete, server.URL+"/voice/v1/sessions/voice-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body sessionsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(body.VoiceSessions) != 2 || !body.VoiceSessions[0].Deleted {
		t.Fatalf("delete response status=%d body=%#v", response.StatusCode, body)
	}
}

func TestHandlerProtectsAndValidatesVoiceSessionsEndpoint(t *testing.T) {
	server := httptest.NewServer(NewHandler(&fakeBackend{}, "secret"))
	defer server.Close()

	response, err := http.Get(server.URL + "/voice/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.StatusCode)
	}

	request, err := http.NewRequest(http.MethodPost, server.URL+"/voice/v1/sessions", strings.NewReader(`{"title":"Work","extra":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid request status = %d", response.StatusCode)
	}
}

func TestHandlerReportsAuthenticatedServerInfo(t *testing.T) {
	handler := NewHandler(&fakeBackend{}, "secret")
	release, err := handler.Lease.Acquire("phone-1", "voice-1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	server := httptest.NewServer(handler)
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/voice/v1/server-info", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var info serverInfoResponse
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || info.Protocol != protocolVersion {
		t.Fatalf("server info status=%d body=%#v", response.StatusCode, info)
	}
	if info.Version == "" || info.Commit == "" || info.StartedAt.IsZero() || info.ServerTime.IsZero() {
		t.Fatalf("server info omitted build identity: %#v", info)
	}
	if info.Platform == "" || info.GoVersion == "" || info.LogicalCPUs < 1 || info.MaxProcs < 1 {
		t.Fatalf("server info omitted runtime identity: %#v", info)
	}
	if info.SessionCount != 1 || info.VoiceSessionCount != 2 {
		t.Fatalf("server info session counts = %d, %d", info.SessionCount, info.VoiceSessionCount)
	}
	if !info.TokenRequired || !info.VoiceConnectionActive || info.VoiceConnectionSince == nil {
		t.Fatalf("server info connection state = %#v", info)
	}
}

func TestHandlerTranscribesStreamsAndDelegatesAudio(t *testing.T) {
	backend := &fakeBackend{}
	server := httptest.NewServer(NewHandler(backend, ""))
	defer server.Close()
	ctx := context.Background()
	conn, _, err := websocket.Dial(ctx, "ws"+server.URL[len("http"):]+"/voice/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	ready := readType(t, ctx, conn, "ready")
	if ready.AudioConfig == nil {
		t.Fatal("ready did not advertise audio config")
	}
	format := ready.AudioConfig.Input
	if err := writeClientFrame(ctx, conn, clientFrame{Type: "audio_start", UtteranceID: "audio-1", AudioFormat: &format, Languages: []string{"en", "da"}}); err != nil {
		t.Fatal(err)
	}
	readType(t, ctx, conn, "state")
	encoded, err := voice.EncodeAudioFrame(voice.AudioFrame{Kind: voice.AudioFrameInputPCM, Sequence: 0, PCM: []byte{7, 0, 8, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		t.Fatal(err)
	}
	if err := writeClientFrame(ctx, conn, clientFrame{Type: "audio_commit", UtteranceID: "audio-1", SessionID: "session-1"}); err != nil {
		t.Fatal(err)
	}
	readType(t, ctx, conn, "state") // transcribing
	transcript := readType(t, ctx, conn, "transcript")
	if transcript.Transcript != "check the laptop" {
		t.Fatalf("transcript = %#v", transcript)
	}
	if !slices.Equal(backend.languages, []string{"da", "en"}) {
		t.Fatalf("transcription languages = %v", backend.languages)
	}
	readType(t, ctx, conn, "state") // processing
	working := readType(t, ctx, conn, "state")
	if working.State != "working" || working.WorkingOn == nil || working.WorkingOn.Title != "Laptop repair" {
		t.Fatalf("working state = %#v", working)
	}
	message := readType(t, ctx, conn, "message")
	if message.Message == nil || message.Message.SpokenText != "Done: check the laptop." || !backend.delegated {
		t.Fatalf("message=%#v backend=%#v", message, backend)
	}
	readType(t, ctx, conn, "state") // speaking
	readType(t, ctx, conn, "tts_start")
	output := readAudioFrame(t, ctx, conn)
	if output.Kind != voice.AudioFrameOutputPCM || output.Sequence != 0 || len(output.PCM) != 4 {
		t.Fatalf("output = %#v", output)
	}
	readType(t, ctx, conn, "tts_end")
	readType(t, ctx, conn, "ready")
	if backend.spoken != "Done: check the laptop." {
		t.Fatalf("synthesized text = %q", backend.spoken)
	}
}

func TestReadinessExercisesSpeechWithoutTakingConversationLease(t *testing.T) {
	backend := &fakeBackend{}
	handler := NewHandler(backend, "secret")
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx := context.Background()
	header := http.Header{"Authorization": []string{"Bearer secret"}}

	conversation, _, err := websocket.Dial(ctx, "ws"+server.URL[len("http"):]+"/voice/v1?call_id=active", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conversation.Close(websocket.StatusNormalClosure, "") }()
	readType(t, ctx, conversation, "ready")

	if _, response, err := websocket.Dial(ctx, "ws"+server.URL[len("http"):]+"/voice/v1/readiness", nil); err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated readiness response=%v err=%v", response, err)
	}
	readiness, _, err := websocket.Dial(ctx, "ws"+server.URL[len("http"):]+"/voice/v1/readiness", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readiness.Close(websocket.StatusNormalClosure, "") }()
	ready := readType(t, ctx, readiness, "readiness_ready")
	if ready.AudioConfig == nil {
		t.Fatal("readiness did not advertise audio formats")
	}
	format := ready.AudioConfig.Input
	if err := writeClientFrame(ctx, readiness, clientFrame{Type: "audio_start", Protocol: protocolVersion, UtteranceID: "check-1", AudioFormat: &format, Languages: []string{"en", "da"}}); err != nil {
		t.Fatal(err)
	}
	if frame := readType(t, ctx, readiness, "state"); frame.State != "recording" {
		t.Fatalf("recording state = %#v", frame)
	}
	encoded, err := voice.EncodeAudioFrame(voice.AudioFrame{Kind: voice.AudioFrameInputPCM, Sequence: 0, PCM: []byte{7, 0, 8, 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := readiness.Write(ctx, websocket.MessageBinary, encoded); err != nil {
		t.Fatal(err)
	}
	if err := writeClientFrame(ctx, readiness, clientFrame{Type: "audio_commit", Protocol: protocolVersion, UtteranceID: "check-1"}); err != nil {
		t.Fatal(err)
	}
	if frame := readType(t, ctx, readiness, "state"); frame.State != "transcribing" {
		t.Fatalf("transcribing state = %#v", frame)
	}
	transcript := readType(t, ctx, readiness, "transcript")
	if transcript.Transcript != "check the laptop" || !slices.Equal(backend.languages, []string{"da", "en"}) {
		t.Fatalf("transcript=%#v languages=%v", transcript, backend.languages)
	}
	if frame := readType(t, ctx, readiness, "state"); frame.State != "speaking" {
		t.Fatalf("speaking state = %#v", frame)
	}
	readType(t, ctx, readiness, "tts_start")
	output := readAudioFrame(t, ctx, readiness)
	if output.Kind != voice.AudioFrameOutputPCM || output.Sequence != 0 || len(output.PCM) != 4 {
		t.Fatalf("readiness output = %#v", output)
	}
	readType(t, ctx, readiness, "tts_end")
	complete := readType(t, ctx, readiness, "readiness_complete")
	if complete.Transcript != "check the laptop" || complete.State != "complete" || backend.spoken != readinessReply {
		t.Fatalf("complete=%#v spoken=%q", complete, backend.spoken)
	}
	if lease, ok := handler.Lease.Snapshot(); !ok || lease.CallID != "active" {
		t.Fatalf("readiness disturbed active conversation lease: %#v ok=%v", lease, ok)
	}
}

func TestHandlerRejectsConcurrentCalls(t *testing.T) {
	server := httptest.NewServer(NewHandler(&fakeBackend{}, ""))
	defer server.Close()
	ctx := context.Background()
	url := "ws" + server.URL[len("http"):] + "/voice/v1"
	first, _, err := websocket.Dial(ctx, url+"?call_id=first", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close(websocket.StatusNormalClosure, "") }()
	readType(t, ctx, first, "ready")
	_, response, err := websocket.Dial(ctx, url+"?call_id=second", nil)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("concurrent dial response=%v err=%v", response, err)
	}
}

func TestPhoneDeviceSidecarRequiresAndServesActiveCall(t *testing.T) {
	hub := &phonedevice.Hub{}
	handler := NewHandler(&fakeBackend{}, "")
	handler.Devices = hub
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx := context.Background()
	baseURL := "ws" + server.URL[len("http"):]

	_, response, err := websocket.Dial(ctx, baseURL+"/voice/v1/device?call_id=phone-1", nil)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("inactive sidecar response=%v err=%v", response, err)
	}

	voiceConn, _, err := websocket.Dial(ctx, baseURL+"/voice/v1?call_id=phone-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = voiceConn.Close(websocket.StatusNormalClosure, "") }()
	readType(t, ctx, voiceConn, "ready")
	deviceConn, _, err := websocket.Dial(ctx, baseURL+"/voice/v1/device?call_id=phone-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = deviceConn.Close(websocket.StatusNormalClosure, "") }()
	writeJSON(t, ctx, deviceConn, deviceFrame{
		Type: "device_hello", Protocol: protocolVersion,
		Capabilities:         []string{"search_contacts", "device_status", "invented_action"},
		ConfirmationPolicies: map[string]string{"search_contacts": "on", "device_status": "off"},
	})
	var ready deviceFrame
	readJSON(t, ctx, deviceConn, &ready)
	if ready.Type != "device_ready" || len(ready.Capabilities) != 1 || ready.Capabilities[0] != "search_contacts" {
		t.Fatalf("device ready = %#v", ready)
	}
	if capabilities := hub.Capabilities(); len(capabilities) != 1 || capabilities[0].Action != phonedevice.SearchContacts || capabilities[0].Confirmation {
		t.Fatalf("device confirmation policies were not applied: %#v", capabilities)
	}

	type outcome struct {
		result phonedevice.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := hub.Execute(ctx, phonedevice.SearchContacts, map[string]string{"query": "Steen"})
		done <- outcome{result: result, err: err}
	}()
	var request deviceFrame
	readJSON(t, ctx, deviceConn, &request)
	if request.Type != "device_tool_request" || request.Action != phonedevice.SearchContacts || request.Arguments["query"] != "Steen" {
		t.Fatalf("device request = %#v", request)
	}
	writeJSON(t, ctx, deviceConn, deviceFrame{Type: "device_tool_result", Protocol: protocolVersion, RequestID: request.RequestID, Result: &phonedevice.Result{Text: "Found Steen"}})
	select {
	case got := <-done:
		if got.err != nil || got.result.Text != "Found Steen" {
			t.Fatalf("device result=%#v err=%v", got.result, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("phone action did not complete")
	}
}

func TestHandlerReconnectsSameCallAndRestoresHistory(t *testing.T) {
	backend := &fakeBackend{}
	for index := 1; index <= 7; index++ {
		backend.history = append(backend.history, voice.TranscriptEntry{
			ID: fmt.Sprintf("message-%d", index), Role: "user", Text: fmt.Sprintf("Message %d", index), CreatedAt: time.Now().UTC(),
		})
	}
	server := httptest.NewServer(NewHandler(backend, ""))
	defer server.Close()
	ctx := context.Background()
	url := "ws" + server.URL[len("http"):] + "/voice/v1?call_id=phone-1&voice_session_id=voice-1"
	first, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close(websocket.StatusNormalClosure, "") }()
	readType(t, ctx, first, "ready")

	second, response, err := websocket.Dial(ctx, url, nil)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err != nil {
		t.Fatalf("same-owner reconnect: %v", err)
	}
	defer func() { _ = second.Close(websocket.StatusNormalClosure, "") }()
	ready := readType(t, ctx, second, "ready")
	if ready.CallState == nil || len(ready.CallState.History) != 5 || ready.CallState.History[0].Text != "Message 3" || !ready.CallState.HistoryHasMore {
		t.Fatalf("restored history = %#v", ready.CallState)
	}
	if err := writeClientFrame(ctx, second, clientFrame{Type: "history", BeforeID: ready.CallState.History[0].ID, Limit: 5}); err != nil {
		t.Fatal(err)
	}
	page := readType(t, ctx, second, "history")
	if len(page.History) != 2 || page.History[0].Text != "Message 1" || page.HasMore {
		t.Fatalf("older history page = %#v", page)
	}

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, _, err := first.Read(readCtx); err == nil {
		t.Fatal("replaced connection remained open")
	}
	_, response, err = websocket.Dial(ctx, strings.Replace(url, "phone-1", "phone-2", 1), nil)
	if response != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("different-owner dial response=%v err=%v", response, err)
	}
}

func TestHandlerResumesInFlightTurnWithoutDuplicateOutput(t *testing.T) {
	backend := &handoffBackend{firstAudioReady: make(chan struct{}), continueAudio: make(chan struct{})}
	server := httptest.NewServer(NewHandler(backend, ""))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	baseURL := "ws" + server.URL[len("http"):] + "/voice/v1?call_id=handoff&voice_session_id=voice-1"

	first, _, err := websocket.Dial(ctx, baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	readType(t, ctx, first, "ready")
	request := clientFrame{Type: "utterance", Protocol: protocolVersion, UtteranceID: "turn-1", Text: "continue after handoff"}
	if err := writeClientFrame(ctx, first, request); err != nil {
		t.Fatal(err)
	}
	readType(t, ctx, first, "state")
	readType(t, ctx, first, "message")
	readType(t, ctx, first, "state")
	readType(t, ctx, first, "tts_start")
	if frame := readAudioFrame(t, ctx, first); frame.Sequence != 0 {
		t.Fatalf("first output sequence = %d, want 0", frame.Sequence)
	}
	select {
	case <-backend.firstAudioReady:
	case <-ctx.Done():
		t.Fatal("TTS did not produce its first frame")
	}
	_ = first.CloseNow()

	resumeURL := baseURL + "&resume_utterance_id=turn-1&resume_message=true&resume_output_sequence=1"
	second, _, err := websocket.Dial(ctx, resumeURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.CloseNow() }()
	readType(t, ctx, second, "ready")
	if err := writeClientFrame(ctx, second, request); err != nil {
		t.Fatal(err)
	}
	readType(t, ctx, second, "state")
	readType(t, ctx, second, "state")
	readType(t, ctx, second, "tts_start")
	close(backend.continueAudio)
	if frame := readAudioFrame(t, ctx, second); frame.Sequence != 1 || !slices.Equal(frame.PCM, []byte{2, 0}) {
		t.Fatalf("resumed output = %#v, want sequence 1 only", frame)
	}
	readType(t, ctx, second, "tts_end")
	readType(t, ctx, second, "ready")
	if got := backend.turnCalls.Load(); got != 1 {
		t.Fatalf("RunVoiceTurn calls = %d, want 1", got)
	}
}

func TestHandlerServesAuthenticatedVoiceArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screen.png")
	if err := os.WriteFile(path, []byte("png-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(&fakeBackend{artifact: voice.ArtifactFile{
		Path: path, Name: "current-state.png", MIMEType: "image/png",
	}}, "secret"))
	defer server.Close()
	resourceURL := server.URL + "/voice/v1/artifacts/session/session-1/012345678901234567890123"
	response, err := http.Get(resourceURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated artifact status = %d", response.StatusCode)
	}
	request, err := http.NewRequest(http.MethodGet, resourceURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" || !strings.Contains(response.Header.Get("Content-Disposition"), "current-state.png") {
		t.Fatalf("artifact response status=%d headers=%v", response.StatusCode, response.Header)
	}
}

func TestHandlerAdvertisesAndServesAuthenticatedAndroidUpdate(t *testing.T) {
	apk := []byte("signed-apk")
	path := filepath.Join(t.TempDir(), "koder.apk")
	if err := os.WriteFile(path, apk, 0o600); err != nil {
		t.Fatal(err)
	}
	meta := androidupdate.Manifest{
		Channel: "local", ApplicationID: "com.lkarlslund.koder.dev", VersionCode: 42,
		VersionName: "0.1.0-local.test", APKSHA256: strings.Repeat("a", 64), APKSize: int64(len(apk)),
	}
	handler := NewHandler(&fakeBackend{}, "secret")
	handler.Updates = fakeUpdateSource{meta: meta, path: path}
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx := context.Background()
	url := "ws" + server.URL[len("http"):] + "/voice/v1"
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	ready := readType(t, ctx, conn, "ready")
	_ = conn.Close(websocket.StatusNormalClosure, "")
	if ready.AppUpdate == nil || ready.AppUpdate.VersionCode != 42 || ready.AppUpdate.DownloadURI != "/voice/v1/android/koder.apk" {
		t.Fatalf("ready app update = %#v", ready.AppUpdate)
	}

	downloadURL := server.URL + ready.AppUpdate.DownloadURI
	response, err := http.Get(downloadURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated APK status = %d", response.StatusCode)
	}
	request, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != string(apk) || response.Header.Get("ETag") != `"`+meta.APKSHA256+`"` {
		t.Fatalf("APK response status=%d headers=%v body=%q", response.StatusCode, response.Header, body)
	}
}

func TestHandlerBindsPerDeviceTokenAndRevokesIt(t *testing.T) {
	registry, err := deviceauth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := registry.CreateInvitation()
	if err != nil {
		t.Fatal(err)
	}
	apk := []byte("signed-apk")
	path := filepath.Join(t.TempDir(), "koder.apk")
	if err := os.WriteFile(path, apk, 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(&fakeBackend{}, "")
	handler.Auth = registry
	handler.Updates = fakeUpdateSource{meta: androidupdate.Manifest{
		Channel: "local", ApplicationID: "com.lkarlslund.koder.dev", VersionCode: 42,
		VersionName: "0.1.0-local.test", APKSHA256: strings.Repeat("a", 64), APKSize: int64(len(apk)),
	}, path: path}
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := http.Get(server.URL + "/voice/v1/android/koder.apk?bind_code=" + url.QueryEscape(invitation.Code))
	if err != nil {
		t.Fatal(err)
	}
	downloaded, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(downloaded) != string(apk) {
		t.Fatalf("invited APK download status=%d body=%q err=%v", response.StatusCode, downloaded, err)
	}

	payload := fmt.Sprintf(`{"code":%q,"device":{"installation_id":"phone-1","name":"Lak's Pixel","manufacturer":"Google","model":"Pixel 9","android_version":"16","app_version":"0.1.0","app_id":"com.lkarlslund.koder.dev"}}`, invitation.Code)
	response, err = http.Post(server.URL+"/voice/v1/bind", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var bound bindDeviceResponse
	if err := json.NewDecoder(response.Body).Decode(&bound); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || bound.Protocol != protocolVersion || bound.Binding.Token == "" || bound.Binding.Device.Name != "Lak's Pixel" {
		t.Fatalf("bind response status=%d body=%#v", response.StatusCode, bound)
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/voice/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+bound.Binding.Token)
	request.Header.Set("X-Koder-App-Version", "0.1.1")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("device token status = %d", response.StatusCode)
	}
	devices := registry.List()
	if len(devices) != 1 || devices[0].AppVersion != "0.1.1" {
		t.Fatalf("registered devices = %#v", devices)
	}
	if _, err := registry.Revoke(bound.Binding.Device.ID); err != nil {
		t.Fatal(err)
	}
	request, _ = http.NewRequest(http.MethodGet, server.URL+"/voice/v1/sessions", nil)
	request.Header.Set("Authorization", "Bearer "+bound.Binding.Token)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked device token status = %d", response.StatusCode)
	}
}

func TestMigratedDeviceRevocationOverridesConfiguredLegacyToken(t *testing.T) {
	registry, err := deviceauth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := registry.ImportLegacy("shared-secret")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(&fakeBackend{}, "shared-secret")
	handler.Auth = registry
	server := httptest.NewServer(handler)
	defer server.Close()
	request := func() int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+"/voice/v1/sessions", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer shared-secret")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	if got := request(); got != http.StatusOK {
		t.Fatalf("migrated token status = %d", got)
	}
	if _, err := registry.Revoke(device.ID); err != nil {
		t.Fatal(err)
	}
	if got := request(); got != http.StatusUnauthorized {
		t.Fatalf("revoked migrated token status = %d", got)
	}
}

func writeClientFrame(ctx context.Context, conn *websocket.Conn, frame clientFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}

func writeJSON(t *testing.T, ctx context.Context, conn *websocket.Conn, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, ctx context.Context, conn *websocket.Conn, value any) {
	t.Helper()
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("message type = %v, want text", messageType)
	}
	if err := json.Unmarshal(payload, value); err != nil {
		t.Fatal(err)
	}
}

func readType(t *testing.T, ctx context.Context, conn *websocket.Conn, want string) serverFrame {
	t.Helper()
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var frame serverFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != want {
		t.Fatalf("frame type = %q, want %q; payload=%s", frame.Type, want, payload)
	}
	return frame
}

func readAudioFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) voice.AudioFrame {
	t.Helper()
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageBinary {
		t.Fatalf("message type = %v, want binary; payload=%s", messageType, payload)
	}
	frame, err := voice.DecodeAudioFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}
