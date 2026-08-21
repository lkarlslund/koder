// Package voicecodec provides the portable audio codecs used by voice.v1.
package voicecodec

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/thesyncim/gopus"
)

const (
	FrameDurationMilliseconds = 20
	MaxPacketBytes            = 1275
)

// SupportsFormat reports whether raw Opus can represent the PCM format
// without resampling or remixing it.
func SupportsFormat(sampleRate, channels int) bool {
	return validateFormat(sampleRate, channels) == nil
}

// OpusEncoder converts exact 20 ms PCM16LE frames into raw Opus packets.
// It owns state and must not be used concurrently.
type OpusEncoder struct {
	codec      *gopus.Encoder
	frameBytes int
	pcm        []int16
	packet     []byte
}

// NewOpusEncoder creates a speech-tuned constrained-VBR encoder.
func NewOpusEncoder(sampleRate, channels, bitrate int) (*OpusEncoder, error) {
	if err := validateFormat(sampleRate, channels); err != nil {
		return nil, err
	}
	if bitrate <= 0 {
		return nil, errors.New("opus bitrate must be positive")
	}
	codec, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationVoIP,
	})
	if err != nil {
		return nil, fmt.Errorf("create Opus encoder: %w", err)
	}
	if err := codec.SetBitrate(bitrate); err != nil {
		return nil, fmt.Errorf("set Opus bitrate: %w", err)
	}
	if err := codec.SetBitrateMode(gopus.BitrateModeCVBR); err != nil {
		return nil, fmt.Errorf("set Opus constrained VBR: %w", err)
	}
	frameSamples := sampleRate * FrameDurationMilliseconds / 1000 * channels
	return &OpusEncoder{
		codec:      codec,
		frameBytes: frameSamples * 2,
		pcm:        make([]int16, frameSamples),
		packet:     make([]byte, MaxPacketBytes),
	}, nil
}

// FrameBytes returns the exact PCM16LE byte count accepted by Encode.
func (e *OpusEncoder) FrameBytes() int {
	if e == nil {
		return 0
	}
	return e.frameBytes
}

// Encode converts one exact 20 ms PCM16LE frame into a caller-owned packet.
func (e *OpusEncoder) Encode(pcm []byte) ([]byte, error) {
	if e == nil || e.codec == nil {
		return nil, errors.New("opus encoder is not initialized")
	}
	if len(pcm) != e.frameBytes {
		return nil, fmt.Errorf("opus PCM frame has %d bytes; expected %d", len(pcm), e.frameBytes)
	}
	for index := range e.pcm {
		e.pcm[index] = int16(binary.LittleEndian.Uint16(pcm[index*2:]))
	}
	n, err := e.codec.EncodeInt16(e.pcm, e.packet)
	if err != nil {
		return nil, fmt.Errorf("encode Opus frame: %w", err)
	}
	return append([]byte(nil), e.packet[:n]...), nil
}

// OpusDecoder converts raw Opus packets into PCM16LE. It owns state and must
// not be used concurrently.
type OpusDecoder struct {
	codec    *gopus.Decoder
	channels int
	pcm      []int16
}

// NewOpusDecoder creates a decoder for one negotiated PCM output format.
func NewOpusDecoder(sampleRate, channels int) (*OpusDecoder, error) {
	if err := validateFormat(sampleRate, channels); err != nil {
		return nil, err
	}
	codec, err := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: sampleRate, Channels: channels})
	if err != nil {
		return nil, fmt.Errorf("create Opus decoder: %w", err)
	}
	// Opus permits packets up to 120 ms. The negotiated sender uses 20 ms,
	// but accepting the protocol maximum makes malformed-size failures safe.
	return &OpusDecoder{
		codec:    codec,
		channels: channels,
		pcm:      make([]int16, sampleRate*120/1000*channels),
	}, nil
}

// Decode converts one Opus packet into caller-owned PCM16LE bytes.
func (d *OpusDecoder) Decode(packet []byte) ([]byte, error) {
	if d == nil || d.codec == nil {
		return nil, errors.New("opus decoder is not initialized")
	}
	if len(packet) == 0 || len(packet) > MaxPacketBytes {
		return nil, fmt.Errorf("opus packet size %d is outside 1..%d bytes", len(packet), MaxPacketBytes)
	}
	samplesPerChannel, err := d.codec.DecodeInt16(packet, d.pcm)
	if err != nil {
		return nil, fmt.Errorf("decode Opus packet: %w", err)
	}
	samples := samplesPerChannel * d.channels
	out := make([]byte, samples*2)
	for index := range samples {
		binary.LittleEndian.PutUint16(out[index*2:], uint16(d.pcm[index]))
	}
	return out, nil
}

func validateFormat(sampleRate, channels int) error {
	switch sampleRate {
	case 8000, 12000, 16000, 24000, 48000:
	default:
		return fmt.Errorf("unsupported Opus sample rate %d", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return fmt.Errorf("unsupported Opus channel count %d", channels)
	}
	return nil
}
