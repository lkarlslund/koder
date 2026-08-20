package voice

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	AudioFrameHeaderSize = 12
	MaxAudioPayloadSize  = 64 * 1024
)

var audioFrameMagic = [4]byte{'K', 'V', 'A', '1'}

// AudioFrameKind identifies the direction and encoding carried by a binary
// voice.v1 WebSocket message.
type AudioFrameKind uint8

const (
	AudioFrameInputPCM   AudioFrameKind = 1
	AudioFrameOutputPCM  AudioFrameKind = 2
	AudioFrameInputOpus  AudioFrameKind = 3
	AudioFrameOutputOpus AudioFrameKind = 4
)

// AudioFrame carries one sequence-numbered PCM chunk or Opus packet.
type AudioFrame struct {
	Kind     AudioFrameKind
	Flags    uint8
	Sequence uint32
	Payload  []byte
}

// EncodeAudioFrame creates the language-neutral voice.v1 binary wire format.
func EncodeAudioFrame(frame AudioFrame) ([]byte, error) {
	if err := validateAudioFrame(frame); err != nil {
		return nil, err
	}
	out := make([]byte, AudioFrameHeaderSize+len(frame.Payload))
	copy(out[:4], audioFrameMagic[:])
	out[4] = byte(frame.Kind)
	out[5] = frame.Flags
	// Bytes 6-7 are reserved and stay zero.
	binary.BigEndian.PutUint32(out[8:12], frame.Sequence)
	copy(out[AudioFrameHeaderSize:], frame.Payload)
	return out, nil
}

// DecodeAudioFrame validates and decodes one complete binary WebSocket message.
func DecodeAudioFrame(data []byte) (AudioFrame, error) {
	if len(data) < AudioFrameHeaderSize {
		return AudioFrame{}, fmt.Errorf("voice audio frame is shorter than %d-byte header", AudioFrameHeaderSize)
	}
	if string(data[:4]) != string(audioFrameMagic[:]) {
		return AudioFrame{}, errors.New("invalid voice audio frame magic")
	}
	if data[6] != 0 || data[7] != 0 {
		return AudioFrame{}, errors.New("voice audio frame reserved bytes must be zero")
	}
	frame := AudioFrame{
		Kind:     AudioFrameKind(data[4]),
		Flags:    data[5],
		Sequence: binary.BigEndian.Uint32(data[8:12]),
		Payload:  append([]byte(nil), data[AudioFrameHeaderSize:]...),
	}
	if err := validateAudioFrame(frame); err != nil {
		return AudioFrame{}, err
	}
	return frame, nil
}

func validateAudioFrame(frame AudioFrame) error {
	switch frame.Kind {
	case AudioFrameInputPCM, AudioFrameOutputPCM, AudioFrameInputOpus, AudioFrameOutputOpus:
	default:
		return fmt.Errorf("unsupported voice audio frame kind %d", frame.Kind)
	}
	if len(frame.Payload) == 0 {
		return errors.New("voice audio frame has no payload")
	}
	if len(frame.Payload) > MaxAudioPayloadSize {
		return fmt.Errorf("voice audio frame payload exceeds %d bytes", MaxAudioPayloadSize)
	}
	if (frame.Kind == AudioFrameInputPCM || frame.Kind == AudioFrameOutputPCM) && len(frame.Payload)%2 != 0 {
		return errors.New("voice audio frame PCM16 payload has an odd byte count")
	}
	return nil
}
