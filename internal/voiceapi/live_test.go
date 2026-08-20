package voiceapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/voice"
)

// TestLiveSynthesizedVoiceRoundTrip is an opt-in local acceptance test. It
// synthesizes the user's input, sends that audio through Koder STT and the
// voice coordinator, receives streaming TTS, and transcribes the returned
// speech once more to prove the output is valid audio.
func TestLiveSynthesizedVoiceRoundTrip(t *testing.T) {
	if os.Getenv("KODER_VOICE_LIVE") != "1" {
		t.Skip("set KODER_VOICE_LIVE=1 to exercise local Koder and audio.cpp")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	audioBase := strings.TrimSpace(os.Getenv("KODER_VOICE_AUDIO_BASE"))
	if audioBase == "" {
		audioBase = "http://127.0.0.1:8099/v1"
	}
	speech, err := provider.New("live-audio", config.Provider{
		Kind: provider.ProviderKindCompatible, BaseURL: audioBase, Timeout: 90 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	inputText := strings.TrimSpace(os.Getenv("KODER_VOICE_LIVE_INPUT"))
	if inputText == "" {
		inputText = "What sessions are available?"
	}
	input, err := speech.CreateSpeech(ctx, provider.SpeechRequest{
		Model: "koder-tts", Input: inputText, Voice: "F1",
		Language: "en", ResponseFormat: "pcm", StreamFormat: "audio",
	})
	if err != nil {
		t.Fatalf("synthesize acceptance input: %v", err)
	}
	inputPCM := resamplePCM16(t, input.Audio, 44100, 16000)

	serverURL := strings.TrimSpace(os.Getenv("KODER_VOICE_LIVE_URL"))
	if serverURL == "" {
		serverURL = "ws://127.0.0.1:7979/voice/v1"
	}
	headers := http.Header{}
	if token := strings.TrimSpace(os.Getenv("KODER_VOICE_LIVE_TOKEN")); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	conn, _, err := websocket.Dial(ctx, serverURL+"?call_id=live-speech-acceptance", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(readLimit)
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	ready := readLiveTextFrame(t, ctx, conn)
	if ready.Type != "ready" || ready.AudioConfig == nil {
		t.Fatalf("initial frame = %#v", ready)
	}
	format := ready.AudioConfig.Input
	start, err := json.Marshal(clientFrame{Type: "audio_start", Protocol: protocolVersion, UtteranceID: "live-utterance", AudioFormat: &format})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, start); err != nil {
		t.Fatal(err)
	}
	if frame := readLiveTextFrame(t, ctx, conn); frame.Type != "state" || frame.State != "recording" {
		t.Fatalf("audio start response = %#v", frame)
	}
	var sequence uint32
	for offset := 0; offset < len(inputPCM); {
		size := min(32*1024, len(inputPCM)-offset)
		size -= size % 2
		encoded, err := voice.EncodeAudioFrame(voice.AudioFrame{
			Kind: voice.AudioFrameInputPCM, Sequence: sequence, Payload: inputPCM[offset : offset+size],
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Write(ctx, websocket.MessageBinary, encoded); err != nil {
			t.Fatal(err)
		}
		offset += size
		sequence++
	}
	commit, err := json.Marshal(clientFrame{Type: "audio_commit", Protocol: protocolVersion, UtteranceID: "live-utterance"})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, commit); err != nil {
		t.Fatal(err)
	}

	var transcript, spoken string
	var working bool
	var outputPCM []byte
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if messageType == websocket.MessageBinary {
			frame, err := voice.DecodeAudioFrame(payload)
			if err != nil {
				t.Fatal(err)
			}
			if frame.Kind != voice.AudioFrameOutputPCM {
				t.Fatalf("unexpected output frame kind %d", frame.Kind)
			}
			outputPCM = append(outputPCM, frame.Payload...)
			continue
		}
		var frame serverFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			t.Fatal(err)
		}
		switch frame.Type {
		case "transcript":
			transcript = frame.Transcript
		case "state":
			if frame.State == "working" {
				working = frame.WorkingOn != nil && strings.TrimSpace(frame.WorkingOn.ID) != ""
			}
		case "message":
			if frame.Message != nil {
				spoken = frame.Message.SpokenText
			}
		case "error":
			t.Fatalf("live voice error: %s", frame.Error)
		case "tts_end":
			goto complete
		}
	}

complete:
	if strings.TrimSpace(transcript) == "" || strings.TrimSpace(spoken) == "" {
		t.Fatalf("incomplete voice result: transcript=%q spoken=%q", transcript, spoken)
	}
	if os.Getenv("KODER_VOICE_LIVE_REQUIRE_DELEGATION") == "1" && !working {
		t.Fatalf("voice turn did not delegate or announce a working target: transcript=%q spoken=%q", transcript, spoken)
	}
	if len(outputPCM) < ready.AudioConfig.Output.SampleRate/2 {
		t.Fatalf("streamed TTS is too short: %d bytes", len(outputPCM))
	}
	validated, err := speech.TranscribeSpeech(ctx, provider.TranscriptionRequest{
		Model: "koder-stt", Audio: wavPCM16(outputPCM, ready.AudioConfig.Output.SampleRate),
		Filename: "voice-output.wav", Language: "en",
	})
	if err != nil {
		t.Fatalf("validate streamed TTS: %v", err)
	}
	if strings.TrimSpace(validated.Text) == "" {
		t.Fatal("streamed TTS validation returned no transcript")
	}
	t.Logf("input transcript=%q spoken=%q output transcript=%q output_pcm_bytes=%d", transcript, spoken, validated.Text, len(outputPCM))
}

func readLiveTextFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) serverFrame {
	t.Helper()
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("expected text frame, got %v", messageType)
	}
	var frame serverFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

func resamplePCM16(t *testing.T, input []byte, sourceRate, targetRate int) []byte {
	t.Helper()
	if len(input)%2 != 0 || len(input) == 0 {
		t.Fatalf("invalid source PCM size %d", len(input))
	}
	source := make([]int16, len(input)/2)
	if err := binary.Read(bytes.NewReader(input), binary.LittleEndian, source); err != nil {
		t.Fatal(err)
	}
	target := make([]int16, len(source)*targetRate/sourceRate)
	for index := range target {
		position := float64(index) * float64(sourceRate) / float64(targetRate)
		left := int(position)
		right := min(left+1, len(source)-1)
		fraction := position - float64(left)
		target[index] = int16(float64(source[left])*(1-fraction) + float64(source[right])*fraction)
	}
	var output bytes.Buffer
	if err := binary.Write(&output, binary.LittleEndian, target); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func wavPCM16(pcm []byte, sampleRate int) []byte {
	var output bytes.Buffer
	output.WriteString("RIFF")
	_ = binary.Write(&output, binary.LittleEndian, uint32(36+len(pcm)))
	output.WriteString("WAVEfmt ")
	_ = binary.Write(&output, binary.LittleEndian, uint32(16))
	_ = binary.Write(&output, binary.LittleEndian, uint16(1))
	_ = binary.Write(&output, binary.LittleEndian, uint16(1))
	_ = binary.Write(&output, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&output, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(&output, binary.LittleEndian, uint16(2))
	_ = binary.Write(&output, binary.LittleEndian, uint16(16))
	output.WriteString("data")
	_ = binary.Write(&output, binary.LittleEndian, uint32(len(pcm)))
	output.Write(pcm)
	return output.Bytes()
}
