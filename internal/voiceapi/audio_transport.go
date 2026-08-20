package voiceapi

import (
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/voice"
	"github.com/lkarlslund/koder/internal/voicecodec"
)

const (
	inputOpusBitrate  = 18_000
	outputOpusBitrate = 32_000
)

func advertisedAudioConfig(base voice.AudioConfig) voice.AudioConfig {
	base.TransportEncodings = []string{voice.Opus, voice.PCM16LE}
	base.InputTransport = nil
	base.OutputTransport = nil
	return base
}

func negotiatedAudioConfig(base voice.AudioConfig, offered []string) voice.AudioConfig {
	encoding := voice.PCM16LE
	for _, candidate := range offered {
		if candidate == voice.Opus || candidate == voice.PCM16LE {
			encoding = candidate
			break
		}
	}
	input := base.Input
	input.Encoding = encoding
	output := base.Output
	output.Encoding = encoding
	base.TransportEncodings = []string{voice.Opus, voice.PCM16LE}
	base.InputTransport = &input
	base.OutputTransport = &output
	return base
}

func selectedInputFormat(config voice.AudioConfig) voice.AudioFormat {
	if config.InputTransport != nil {
		return *config.InputTransport
	}
	return config.Input
}

func selectedOutputFormat(config voice.AudioConfig) voice.AudioFormat {
	if config.OutputTransport != nil {
		return *config.OutputTransport
	}
	return config.Output
}

type audioPacketDecoder interface {
	Decode([]byte) ([]byte, error)
}

func newIncomingAudio(utteranceID string, serviceFormat, transportFormat voice.AudioFormat, languages []string) (*incomingAudio, error) {
	incoming := &incomingAudio{
		utteranceID: utteranceID,
		format:      serviceFormat,
		transport:   transportFormat,
		languages:   languages,
	}
	switch transportFormat.Encoding {
	case voice.PCM16LE:
	case voice.Opus:
		decoder, err := voicecodec.NewOpusDecoder(transportFormat.SampleRate, transportFormat.Channels)
		if err != nil {
			return nil, err
		}
		incoming.decoder = decoder
	default:
		return nil, fmt.Errorf("unsupported input transport encoding %q", transportFormat.Encoding)
	}
	return incoming, nil
}

type audioPacketEncoder struct {
	transport voice.AudioFormat
	kind      voice.AudioFrameKind
	opus      *voicecodec.OpusEncoder
	pending   []byte
}

func newAudioPacketEncoder(transport voice.AudioFormat) (*audioPacketEncoder, error) {
	encoder := &audioPacketEncoder{transport: transport}
	switch transport.Encoding {
	case voice.PCM16LE:
		encoder.kind = voice.AudioFrameOutputPCM
	case voice.Opus:
		codec, err := voicecodec.NewOpusEncoder(transport.SampleRate, transport.Channels, outputOpusBitrate)
		if err != nil {
			return nil, err
		}
		encoder.kind = voice.AudioFrameOutputOpus
		encoder.opus = codec
	default:
		return nil, fmt.Errorf("unsupported output transport encoding %q", transport.Encoding)
	}
	return encoder, nil
}

func (e *audioPacketEncoder) appendPCM(chunk []byte, emit func(voice.AudioFrameKind, []byte) error) error {
	e.pending = append(e.pending, chunk...)
	if e.opus != nil {
		for len(e.pending) >= e.opus.FrameBytes() {
			packet, err := e.opus.Encode(e.pending[:e.opus.FrameBytes()])
			if err != nil {
				return err
			}
			if err := emit(e.kind, packet); err != nil {
				return err
			}
			e.pending = append(e.pending[:0], e.pending[e.opus.FrameBytes():]...)
		}
		return nil
	}
	for len(e.pending) >= 2 {
		size := min(len(e.pending), voice.MaxAudioPayloadSize)
		size -= size % 2
		if err := emit(e.kind, e.pending[:size]); err != nil {
			return err
		}
		e.pending = append(e.pending[:0], e.pending[size:]...)
	}
	return nil
}

func (e *audioPacketEncoder) finish(emit func(voice.AudioFrameKind, []byte) error) error {
	if len(e.pending)%2 != 0 {
		return errors.New("TTS returned an odd PCM16 byte count")
	}
	if e.opus == nil || len(e.pending) == 0 {
		return nil
	}
	padded := make([]byte, e.opus.FrameBytes())
	copy(padded, e.pending)
	packet, err := e.opus.Encode(padded)
	if err != nil {
		return err
	}
	e.pending = nil
	return emit(e.kind, packet)
}
