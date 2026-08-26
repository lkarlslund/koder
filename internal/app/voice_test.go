package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/voice"
)

func TestControllerVoiceSpeechRoundTrip(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Fatal(err)
			}
			wav, err := io.ReadAll(file)
			_ = file.Close()
			if err != nil {
				t.Fatal(err)
			}
			if r.FormValue("model") != "asr" || r.FormValue("language") != "" || len(wav) != 44+len(pcm) || string(wav[:4]) != "RIFF" || !bytes.Equal(wav[44:], pcm) {
				t.Fatalf("unexpected transcription request: model=%q language=%q wav=%v", r.FormValue("model"), r.FormValue("language"), wav)
			}
			_, _ = io.WriteString(w, `{"text":"hello koder","language":"en"}`)
		case "/v1/audio/speech":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["model"] != "tts" || payload["input"] != "short reply" || payload["voice"] != "F1" || payload["response_format"] != "pcm" || payload["stream_format"] != "audio" {
				t.Fatalf("unexpected synthesis request: %#v", payload)
			}
			w.Header().Set("Content-Type", "audio/pcm")
			_, _ = w.Write([]byte{9, 0, 8, 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Providers["speech"] = config.Provider{Kind: "openai-compatible", BaseURL: server.URL + "/v1", Timeout: time.Second}
	cfg.Voice.STTProviderID = "speech"
	cfg.Voice.STTModelID = "asr"
	cfg.Voice.TTSProviderID = "speech"
	cfg.Voice.TTSModelID = "tts"
	cfg.Voice.TTSVoice = "F1"
	controller := New(cfg, nil)
	format := controller.VoiceAudioConfig().Input
	transcript, err := controller.TranscribeVoice(context.Background(), format, pcm, voice.TranscriptionHints{})
	if err != nil {
		t.Fatal(err)
	}
	if transcript != "hello koder" {
		t.Fatalf("transcript = %q", transcript)
	}
	var output bytes.Buffer
	if err := controller.StreamVoiceSpeech(context.Background(), "short reply", func(chunk []byte) error {
		_, err := output.Write(chunk)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := output.Bytes(); !bytes.Equal(got, []byte{9, 0, 8, 0}) {
		t.Fatalf("streamed PCM = %v", got)
	}
	if format != (voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 16000, Channels: 1}) {
		t.Fatalf("input format = %#v", format)
	}
}

func TestTranscriptionLanguageSupportsAutomaticDetection(t *testing.T) {
	for _, test := range []struct{ configured, want string }{
		{configured: "auto", want: ""},
		{configured: " AUTO ", want: ""},
		{configured: "da", want: "da"},
	} {
		if got := transcriptionLanguage(test.configured); got != test.want {
			t.Fatalf("transcriptionLanguage(%q) = %q, want %q", test.configured, got, test.want)
		}
	}
}

func TestTranscriptPageStartKeepsCompleteRecentTurns(t *testing.T) {
	entries := make([]voice.TranscriptEntry, 0, 14)
	for turn := 1; turn <= 7; turn++ {
		entries = append(entries,
			voice.TranscriptEntry{ID: fmt.Sprintf("user-%d", turn), Role: "user"},
			voice.TranscriptEntry{ID: fmt.Sprintf("assistant-%d", turn), Role: "assistant"},
		)
	}
	start := transcriptPageStart(entries, 5)
	if start != 4 || entries[start].ID != "user-3" {
		t.Fatalf("transcriptPageStart() = %d (%s), want 4 (user-3)", start, entries[start].ID)
	}
}

func TestTranscriptionHintsUseHardSingleAndPromptedMultipleLanguages(t *testing.T) {
	for _, test := range []struct {
		name       string
		configured string
		requested  []string
		language   string
		prompt     string
	}{
		{name: "server default", configured: "da", language: "da"},
		{name: "single selection", configured: "auto", requested: []string{"en"}, language: "en"},
		{name: "multiple selection", configured: "de", requested: []string{"da", "en"}, prompt: "The speaker will use only these languages: da, en. Do not identify the speech as another language. Transcribe it in the language spoken."},
	} {
		t.Run(test.name, func(t *testing.T) {
			language, prompt := transcriptionHints(test.configured, test.requested)
			if language != test.language || prompt != test.prompt {
				t.Fatalf("transcriptionHints() = %q, %q; want %q, %q", language, prompt, test.language, test.prompt)
			}
		})
	}
}

func TestConciseSpokenResponseKeepsVisualDetailOutOfSpeech(t *testing.T) {
	short := "I found it. The appointment is tomorrow at ten."
	if got := pacedSpokenResponse(short, voice.ResponsePacingNormal); got != short {
		t.Fatalf("short response = %q", got)
	}
	long := "This is the useful first sentence. " + strings.Repeat("Supporting detail takes many words and belongs in the visual transcript instead. ", 10)
	got := pacedSpokenResponse(long, voice.ResponsePacingNormal)
	if len(strings.Fields(got)) > voice.ResponsePacingNormal.MaxSpokenWords() || !strings.HasSuffix(got, ".") {
		t.Fatalf("concise response = %q (%d words)", got, len(strings.Fields(got)))
	}
}

func TestSpokenResponseLimitFollowsPacing(t *testing.T) {
	long := strings.Repeat("useful detail ", 100)
	concise := pacedSpokenResponse(long, voice.ResponsePacingConcise)
	detailed := pacedSpokenResponse(long, voice.ResponsePacingDetailed)
	if len(strings.Fields(concise)) > voice.ResponsePacingConcise.MaxSpokenWords() {
		t.Fatalf("concise response has %d words", len(strings.Fields(concise)))
	}
	if len(strings.Fields(detailed)) <= len(strings.Fields(concise)) || len(strings.Fields(detailed)) > voice.ResponsePacingDetailed.MaxSpokenWords() {
		t.Fatalf("detailed response has %d words; concise has %d", len(strings.Fields(detailed)), len(strings.Fields(concise)))
	}
}

func TestConciseSpokenResponseRemovesDocumentFormatting(t *testing.T) {
	formatted := "## Result\n\n- **First** item\n- [Second](https://example.com) item\n\n| Time | Person |\n|---|---|\n| 10:00 | Steen |\n\n```go\nfmt.Println(\"not spoken\")\n```"
	got := pacedSpokenResponse(formatted, voice.ResponsePacingNormal)
	if got != "Result. First item. Second item. Time, Person. 10:00, Steen." {
		t.Fatalf("spoken response = %q", got)
	}
	for _, marker := range []string{"#", "*", "|", "```", "http"} {
		if strings.Contains(got, marker) {
			t.Fatalf("spoken response retained %q: %q", marker, got)
		}
	}
}

func TestLatestAssistantTextAfterRequiresNewSealedAssistant(t *testing.T) {
	timeline := []domain.TimelineItem{
		{Seq: 1, Content: domain.AssistantMessage{Text: "old"}, SealedAt: time.Now()},
		{Seq: 2, Content: domain.UserMessage{Text: "question"}, SealedAt: time.Now()},
		{Seq: 3, Content: domain.AssistantMessage{Text: "partial"}},
		{Seq: 4, Content: domain.AssistantMessage{Text: " final answer "}, SealedAt: time.Now()},
	}
	if got, want := latestAssistantTextAfter(timeline, 1), "final answer"; got != want {
		t.Fatalf("latestAssistantTextAfter() = %q, want %q", got, want)
	}
	if got := latestAssistantTextAfter(timeline, 4); got != "" {
		t.Fatalf("latestAssistantTextAfter() = %q, want empty", got)
	}
}

func TestVoicePresentationPartsExtractsGenericArtifacts(t *testing.T) {
	timeline := []domain.TimelineItem{
		{
			Seq: 1,
			Content: domain.AssistantMessage{Tools: []domain.ToolCall{{
				Tool: domain.ToolKindPresent, Args: map[string]string{"action": "media"},
				Result: &domain.ToolResult{Data: tools.ShowMediaStoredResult{
					SessionID: "session-1", MIMEType: "image/png", Title: "Current state",
					Attachment: &attachment.Metadata{ID: "012345678901234567890123", Name: "screen.png", MIME: "image/png"},
				}},
			}}},
		},
		{
			Seq: 2,
			Content: domain.AssistantMessage{Tools: []domain.ToolCall{{
				Tool: domain.ToolKindPresent, Args: map[string]string{"action": "file"},
				Result: &domain.ToolResult{Data: tools.OfferFileStoredResult{
					Token: "download-token", Name: "appointment.ics", MIMEType: "text/calendar",
				}},
			}}},
		},
		{
			Seq: 3,
			Content: domain.AssistantMessage{Tools: []domain.ToolCall{{
				Tool: domain.ToolKindPresent,
				Result: &domain.ToolResult{Data: tools.PresentationStoredResult{
					Title: "Appointments", MIMEType: "text/markdown", Content: "| Time | Person |",
				}},
			}}},
		},
	}
	parts := voicePresentationParts(timeline, 0)
	if len(parts) != 3 {
		t.Fatalf("presentation parts = %#v", parts)
	}
	if parts[0].MIMEType != "image/png" || parts[0].URI != "/voice/v1/artifacts/session/session-1/012345678901234567890123" || parts[0].Metadata["name"] != "screen.png" {
		t.Fatalf("image part = %#v", parts[0])
	}
	if parts[1].MIMEType != "text/calendar" || parts[1].URI != "/voice/v1/artifacts/offered/download-token" {
		t.Fatalf("offered file part = %#v", parts[1])
	}
	if parts[2].MIMEType != "text/markdown" || parts[2].Data != "| Time | Person |" || parts[2].Metadata["presentation"] != "true" {
		t.Fatalf("inline presentation part = %#v", parts[2])
	}
}

func TestVoiceRenderPartsAddsTranscriptOnlyToolActivity(t *testing.T) {
	timeline := []domain.TimelineItem{{Seq: 1, Content: domain.AssistantMessage{Tools: []domain.ToolCall{{
		ToolCallID: "tool-1", Tool: domain.ToolKindPhonePhotos, Status: domain.ToolStatusDone,
		Args: map[string]string{"action": "thumbnails", "limit": "4"}, Result: &domain.ToolResult{Text: "Copied four thumbnails"},
	}}}}}
	parts := voiceRenderParts(timeline, 0)
	if len(parts) != 1 || parts[0].MIMEType != "application/vnd.koder.tool-activity+json" || parts[0].Metadata["surface"] != "transcript" {
		t.Fatalf("render parts = %#v", parts)
	}
	entries := voiceTranscriptEntries(timeline)
	if len(entries) != 1 || entries[0].Role != "activity" || len(entries[0].Parts) != 1 {
		t.Fatalf("transcript entries = %#v", entries)
	}
}

func TestVoiceMemoryActivitySummarizesAndPreservesStructuredResult(t *testing.T) {
	stored := map[string]any{
		"matches": []any{
			map[string]any{"entry_id": "entry-1", "document": map[string]any{"title": "Use sfdisk", "summary": "Scriptable partitioning"}},
			map[string]any{"entry_id": "entry-2", "document": map[string]any{"title": "Back up first", "summary": "Preserve the partition table"}},
		},
	}
	timeline := []domain.TimelineItem{{Seq: 1, Content: domain.AssistantMessage{Tools: []domain.ToolCall{{
		ToolCallID: "memory-1", Tool: tools.Memory, Status: domain.ToolStatusDone,
		Args:   map[string]string{"action": "search", "query": "Linux partition tools"},
		Result: &domain.ToolResult{Text: `{"matches":[...]}`, Data: stored, Status: domain.ToolResultStatusOK},
	}}}}}
	parts := voiceRenderParts(timeline, 0)
	if len(parts) != 1 || parts[0].Metadata["surface"] != "transcript" {
		t.Fatalf("memory activity parts = %#v", parts)
	}
	data, ok := parts[0].Data.(map[string]any)
	if !ok || data["summary"] != "Found 2 memory results" || data["result"] == nil {
		t.Fatalf("memory activity data = %#v", parts[0].Data)
	}
	encoded, err := json.Marshal(data["result"])
	if err != nil || !strings.Contains(string(encoded), "Scriptable partitioning") || !strings.Contains(string(encoded), "Preserve the partition table") {
		t.Fatalf("memory activity omitted full structured result: %s (%v)", encoded, err)
	}
}

func TestVoiceTurnStartedIgnoresStaleErroredState(t *testing.T) {
	if voiceTurnStarted(chat.StatusErrored, false) {
		t.Fatal("stale errored status must not terminate a newly enqueued voice turn")
	}
	if !voiceTurnStarted(chat.StatusWaitingLLM, false) {
		t.Fatal("waiting LLM should mark the delegated turn as started")
	}
	if !voiceTurnStarted(chat.StatusErrored, true) {
		t.Fatal("an active runtime should mark the delegated turn as started")
	}
}

func TestLatestModelErrorAfter(t *testing.T) {
	timeline := []domain.TimelineItem{
		{Seq: 1, Content: domain.Notice{Kind: "model_error", Text: "old error"}, SealedAt: time.Now()},
		{Seq: 2, Content: domain.UserMessage{Text: "retry"}, SealedAt: time.Now()},
		{Seq: 3, Content: domain.Notice{Kind: "model_error", Text: "new error"}, SealedAt: time.Now()},
	}
	if got, want := latestModelErrorAfter(timeline, 1), "new error"; got != want {
		t.Fatalf("latestModelErrorAfter() = %q, want %q", got, want)
	}
	if got := latestModelErrorAfter(timeline, 3); got != "" {
		t.Fatalf("latestModelErrorAfter() = %q, want empty", got)
	}
}
