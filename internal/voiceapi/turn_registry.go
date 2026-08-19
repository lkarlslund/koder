package voiceapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"github.com/lkarlslund/koder/internal/voice"
)

type turnResume struct {
	utteranceID        string
	transcriptReceived bool
	messageReceived    bool
	outputSequence     uint32
}

type cachedTurn struct {
	mu             sync.Mutex
	callID         string
	utteranceID    string
	fingerprint    string
	voiceSessionID string
	events         []turnEvent
	audioStarted   bool
	done           bool
	notify         chan struct{}
}

type turnEvent struct {
	frame         *serverFrame
	audio         []byte
	audioSequence uint32
}

type turnSnapshot struct {
	events []turnEvent
	done   bool
	notify <-chan struct{}
}

func newCachedTurn(callID, utteranceID, fingerprint, voiceSessionID string) *cachedTurn {
	return &cachedTurn{
		callID: callID, utteranceID: utteranceID, fingerprint: fingerprint,
		voiceSessionID: voiceSessionID, notify: make(chan struct{}),
	}
}

func (t *cachedTurn) signalLocked() {
	close(t.notify)
	t.notify = make(chan struct{})
}

func (t *cachedTurn) appendState(state string, workingOn *voice.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	frame := serverFrame{Type: "state", UtteranceID: t.utteranceID, State: state, WorkingOn: workingOn}
	t.events = append(t.events, turnEvent{frame: &frame})
	t.signalLocked()
}

func (t *cachedTurn) setTranscript(transcript string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	frame := serverFrame{Type: "transcript", UtteranceID: t.utteranceID, Transcript: strings.TrimSpace(transcript)}
	t.events = append(t.events, turnEvent{frame: &frame})
	t.signalLocked()
}

func (t *cachedTurn) setMessage(message voice.Message) {
	t.mu.Lock()
	defer t.mu.Unlock()
	copy := message
	frame := serverFrame{Type: "message", UtteranceID: t.utteranceID, Message: &copy}
	t.events = append(t.events, turnEvent{frame: &frame})
	t.signalLocked()
}

func (t *cachedTurn) startAudio(format voice.AudioFormat) {
	t.mu.Lock()
	defer t.mu.Unlock()
	copy := format
	frame := serverFrame{Type: "tts_start", UtteranceID: t.utteranceID, AudioFormat: &copy}
	t.audioStarted = true
	t.events = append(t.events, turnEvent{frame: &frame})
	t.signalLocked()
}

func (t *cachedTurn) appendAudio(sequence uint32, encoded []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, turnEvent{audio: append([]byte(nil), encoded...), audioSequence: sequence})
	t.signalLocked()
}

func (t *cachedTurn) finish(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		frame := serverFrame{Type: "error", UtteranceID: t.utteranceID, Error: err.Error()}
		t.events = append(t.events, turnEvent{frame: &frame})
	} else if t.audioStarted {
		frame := serverFrame{Type: "tts_end", UtteranceID: t.utteranceID}
		t.events = append(t.events, turnEvent{frame: &frame})
	}
	t.done = true
	t.signalLocked()
}

func (t *cachedTurn) snapshot() turnSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	events := make([]turnEvent, len(t.events))
	for index, event := range t.events {
		events[index] = event
		if event.audio != nil {
			events[index].audio = append([]byte(nil), event.audio...)
		}
	}
	return turnSnapshot{events: events, done: t.done, notify: t.notify}
}

type turnRegistry struct {
	mu     sync.Mutex
	active *cachedTurn
}

func newTurnRegistry() *turnRegistry { return &turnRegistry{} }

func textTurnFingerprint(text string) string {
	digest := sha256.Sum256([]byte("text\x00" + text))
	return hex.EncodeToString(digest[:])
}

func audioTurnFingerprint(audio *incomingAudio) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "audio\x00%s\x00%d\x00%d\x00%s\x00", audio.format.Encoding, audio.format.SampleRate, audio.format.Channels, strings.Join(audio.languages, ","))
	_, _ = hash.Write(audio.pcm)
	return hex.EncodeToString(hash.Sum(nil))
}

func (r *turnRegistry) start(callID, utteranceID, fingerprint, voiceSessionID string, run func(*cachedTurn)) (*cachedTurn, bool, error) {
	if r == nil {
		return nil, false, errors.New("voice turn registry is unavailable")
	}
	callID = strings.TrimSpace(callID)
	utteranceID = strings.TrimSpace(utteranceID)
	if callID == "" || utteranceID == "" {
		return nil, false, errors.New("voice turn requires call and utterance ids")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.active; current != nil && current.callID == callID {
		if current.utteranceID == utteranceID {
			if current.fingerprint != fingerprint {
				return nil, false, errors.New("utterance id was reused with different content")
			}
			return current, false, nil
		}
		if !current.snapshot().done {
			return nil, false, errors.New("another voice utterance is still active")
		}
	}
	turn := newCachedTurn(callID, utteranceID, fingerprint, voiceSessionID)
	r.active = turn
	go run(turn)
	return turn, true, nil
}

func (h *Handler) runTextTurn(turn *cachedTurn, text string, pacing voice.ResponsePacing) {
	ctx, cancel := context.WithTimeout(context.Background(), delegationTimeout)
	defer cancel()
	h.runVoiceTurn(ctx, turn, text, pacing)
}

func (h *Handler) runVoiceTurn(ctx context.Context, turn *cachedTurn, text string, pacing voice.ResponsePacing) {
	turn.appendState("processing", nil)
	message, err := h.Backend.RunVoiceTurn(ctx, turn.voiceSessionID, text, voice.TurnOptions{ResponsePacing: pacing}, func(session voice.Session) error {
		copy := session
		turn.appendState("working", &copy)
		return nil
	})
	if err != nil {
		turn.finish(err)
		return
	}
	turn.setMessage(message)
	if strings.TrimSpace(message.SpokenText) != "" {
		if err := h.cacheSpeech(ctx, turn, message.SpokenText); err != nil {
			turn.finish(fmt.Errorf("speech synthesis: %w", err))
			return
		}
	}
	turn.finish(nil)
}

func (h *Handler) runAudioTurn(turn *cachedTurn, audio *incomingAudio, pacing voice.ResponsePacing) {
	ctx, cancel := context.WithTimeout(context.Background(), delegationTimeout)
	defer cancel()
	turn.appendState("transcribing", nil)
	transcript, err := h.Backend.TranscribeVoice(ctx, audio.format, audio.pcm, voice.TranscriptionHints{Languages: audio.languages})
	if err != nil {
		turn.finish(err)
		return
	}
	turn.setTranscript(transcript)
	h.runVoiceTurn(ctx, turn, transcript, pacing)
}

func (h *Handler) cacheSpeech(ctx context.Context, turn *cachedTurn, text string) error {
	format := h.Backend.VoiceAudioConfig().Output
	turn.appendState("speaking", nil)
	turn.startAudio(format)
	var sequence uint32
	var pending []byte
	maxBytes := int64(h.Backend.VoiceAudioConfig().MaxUtteranceSeconds) * int64(format.SampleRate) * int64(format.Channels) * 2
	var total int64
	err := h.Backend.StreamVoiceSpeech(ctx, text, func(chunk []byte) error {
		total += int64(len(chunk))
		if total > maxBytes {
			return errors.New("speech output exceeded the configured voice duration")
		}
		pending = append(pending, chunk...)
		for len(pending) >= 2 {
			size := min(len(pending), voice.MaxAudioPayloadSize)
			size -= size % 2
			encoded, err := voice.EncodeAudioFrame(voice.AudioFrame{Kind: voice.AudioFrameOutputPCM, Sequence: sequence, PCM: pending[:size]})
			if err != nil {
				return err
			}
			turn.appendAudio(sequence, encoded)
			sequence++
			pending = append(pending[:0], pending[size:]...)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return errors.New("TTS returned an odd PCM16 byte count")
	}
	return nil
}

func streamCachedTurn(ctx context.Context, conn *websocket.Conn, writeMu *sync.Mutex, turn *cachedTurn, resume turnResume) error {
	eventIndex := 0
	transcriptSent := resume.utteranceID == turn.utteranceID && resume.transcriptReceived
	messageSent := resume.utteranceID == turn.utteranceID && resume.messageReceived
	nextAudio := uint32(0)
	if resume.utteranceID == turn.utteranceID {
		nextAudio = resume.outputSequence
	}
	for {
		snapshot := turn.snapshot()
		for eventIndex < len(snapshot.events) {
			event := snapshot.events[eventIndex]
			eventIndex++
			if event.audio != nil {
				if event.audioSequence < nextAudio {
					continue
				}
				writeMu.Lock()
				err := conn.Write(ctx, websocket.MessageBinary, event.audio)
				writeMu.Unlock()
				if err != nil {
					return err
				}
				nextAudio = event.audioSequence + 1
				continue
			}
			if event.frame == nil {
				continue
			}
			if event.frame.Type == "transcript" && transcriptSent {
				continue
			}
			if event.frame.Type == "message" && messageSent {
				continue
			}
			if err := writeFrame(ctx, conn, writeMu, *event.frame); err != nil {
				return err
			}
			transcriptSent = transcriptSent || event.frame.Type == "transcript"
			messageSent = messageSent || event.frame.Type == "message"
		}
		if snapshot.done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-snapshot.notify:
		}
	}
}
