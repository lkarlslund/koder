package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
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
			if r.FormValue("model") != "asr" || r.FormValue("language") != "en" || len(wav) != 44+len(pcm) || string(wav[:4]) != "RIFF" || !bytes.Equal(wav[44:], pcm) {
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
	transcript, err := controller.TranscribeVoice(context.Background(), format, pcm)
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
