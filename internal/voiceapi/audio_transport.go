package voiceapi

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/voice"
	"github.com/lkarlslund/koder/internal/voicecodec"
)

const (
	inputOpusBitrate   = 18_000
	outputOpusBitrate  = 32_000
	minimumOpusBitrate = 6_000
	maximumOpusBitrate = 128_000
)

var opusSampleRates = [...]int{8_000, 12_000, 16_000, 24_000, 48_000}

func advertisedAudioConfig(base voice.AudioConfig) voice.AudioConfig {
	base.TransportEncodings = []string{voice.Opus, voice.PCM16LE}
	base.InputTransport = nil
	base.OutputTransport = nil
	return base
}

func negotiatedAudioConfig(
	base voice.AudioConfig,
	offered []string,
	inputPreference, outputPreference *voice.AudioTransportPreference,
) voice.AudioConfig {
	input := negotiatedTransportFormat(base.Input, offered, inputPreference, false, inputOpusBitrate)
	output := negotiatedTransportFormat(base.Output, offered, outputPreference, true, outputOpusBitrate)
	base.TransportEncodings = []string{voice.Opus, voice.PCM16LE}
	base.InputTransport = &input
	base.OutputTransport = &output
	return base
}

func negotiatedTransportFormat(
	service voice.AudioFormat,
	offered []string,
	preference *voice.AudioTransportPreference,
	allowResampling bool,
	defaultBitrate int,
) voice.AudioFormat {
	pcm := service
	pcm.Encoding = voice.PCM16LE
	pcm.Bitrate = 0

	if preference != nil {
		if preference.Encoding != voice.Opus {
			return pcm
		}
		return opusTransportFormat(pcm, allowResampling, preference.Bitrate, defaultBitrate)
	}

	for _, candidate := range offered {
		switch candidate {
		case voice.Opus:
			// Preserve legacy negotiation: older clients never requested
			// resampling, so an unsupported service rate remains PCM.
			if voicecodec.SupportsFormat(pcm.SampleRate, pcm.Channels) {
				return opusTransportFormat(pcm, false, 0, defaultBitrate)
			}
		case voice.PCM16LE:
			return pcm
		}
	}
	return pcm
}

func opusTransportFormat(service voice.AudioFormat, allowResampling bool, requestedBitrate, defaultBitrate int) voice.AudioFormat {
	rate := service.SampleRate
	if !voicecodec.SupportsFormat(rate, service.Channels) {
		if !allowResampling || service.Channels < 1 || service.Channels > 2 {
			return service
		}
		rate = nearestOpusSampleRate(rate)
	}
	service.Encoding = voice.Opus
	service.SampleRate = rate
	service.Bitrate = normalizedOpusBitrate(requestedBitrate, defaultBitrate)
	return service
}

func normalizedOpusBitrate(requested, fallback int) int {
	if requested == 0 {
		return fallback
	}
	return max(min(requested, maximumOpusBitrate), minimumOpusBitrate)
}

func nearestOpusSampleRate(sampleRate int) int {
	best := opusSampleRates[0]
	bestDistance := absInt(sampleRate - best)
	for _, candidate := range opusSampleRates[1:] {
		distance := absInt(sampleRate - candidate)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	return best
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
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

func playbackFormat(transport voice.AudioFormat) voice.AudioFormat {
	transport.Encoding = voice.PCM16LE
	transport.Bitrate = 0
	return transport
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
	resampler *pcm16Resampler
	pending   []byte
}

func newAudioPacketEncoder(service, transport voice.AudioFormat) (*audioPacketEncoder, error) {
	encoder := &audioPacketEncoder{transport: transport}
	switch transport.Encoding {
	case voice.PCM16LE:
		if service.SampleRate != transport.SampleRate || service.Channels != transport.Channels {
			return nil, errors.New("PCM transport must match the service audio format")
		}
		encoder.kind = voice.AudioFrameOutputPCM
	case voice.Opus:
		if service.Channels != transport.Channels {
			return nil, errors.New("Opus transport channel count must match the service audio format")
		}
		codec, err := voicecodec.NewOpusEncoder(transport.SampleRate, transport.Channels, normalizedOpusBitrate(transport.Bitrate, outputOpusBitrate))
		if err != nil {
			return nil, err
		}
		encoder.kind = voice.AudioFrameOutputOpus
		encoder.opus = codec
		if service.SampleRate != transport.SampleRate {
			encoder.resampler = newPCM16Resampler(service.SampleRate, transport.SampleRate, service.Channels)
		}
	default:
		return nil, fmt.Errorf("unsupported output transport encoding %q", transport.Encoding)
	}
	return encoder, nil
}

func (e *audioPacketEncoder) appendPCM(chunk []byte, emit func(voice.AudioFrameKind, []byte) error) error {
	if e.resampler != nil {
		converted, err := e.resampler.append(chunk)
		if err != nil {
			return err
		}
		chunk = converted
	}
	return e.appendTransportPCM(chunk, emit)
}

func (e *audioPacketEncoder) appendTransportPCM(chunk []byte, emit func(voice.AudioFrameKind, []byte) error) error {
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
	if e.resampler != nil {
		converted, err := e.resampler.finish()
		if err != nil {
			return err
		}
		if err := e.appendTransportPCM(converted, emit); err != nil {
			return err
		}
	}
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

// pcm16Resampler incrementally emits linear-interpolated PCM while retaining
// the final source frame until finish. Voice responses are duration bounded, so
// retaining source samples keeps the state simple and deterministic.
type pcm16Resampler struct {
	sourceRate   int
	targetRate   int
	channels     int
	samples      []int16
	partial      []byte
	outputFrames int64
}

func newPCM16Resampler(sourceRate, targetRate, channels int) *pcm16Resampler {
	return &pcm16Resampler{sourceRate: sourceRate, targetRate: targetRate, channels: channels}
}

func (r *pcm16Resampler) append(pcm []byte) ([]byte, error) {
	if r.sourceRate <= 0 || r.targetRate <= 0 || r.channels <= 0 {
		return nil, errors.New("invalid PCM resampler format")
	}
	bytesPerFrame := r.channels * 2
	data := append(r.partial, pcm...)
	completeBytes := len(data) - len(data)%bytesPerFrame
	for offset := 0; offset < completeBytes; offset += 2 {
		r.samples = append(r.samples, int16(binary.LittleEndian.Uint16(data[offset:offset+2])))
	}
	r.partial = append(r.partial[:0], data[completeBytes:]...)
	return r.emit(false), nil
}

func (r *pcm16Resampler) finish() ([]byte, error) {
	if len(r.partial) != 0 {
		return nil, errors.New("TTS returned an incomplete PCM16 frame")
	}
	return r.emit(true), nil
}

func (r *pcm16Resampler) emit(final bool) []byte {
	sourceFrames := int64(len(r.samples) / r.channels)
	if sourceFrames == 0 {
		return nil
	}
	var targetFrames int64
	if final {
		targetFrames = (sourceFrames*int64(r.targetRate) + int64(r.sourceRate) - 1) / int64(r.sourceRate)
	}
	var result []byte
	for {
		position := r.outputFrames * int64(r.sourceRate)
		left := position / int64(r.targetRate)
		if final {
			if r.outputFrames >= targetFrames || left >= sourceFrames {
				break
			}
		} else if left+1 >= sourceFrames {
			break
		}
		right := min(left+1, sourceFrames-1)
		fraction := position % int64(r.targetRate)
		for channel := 0; channel < r.channels; channel++ {
			leftSample := int64(r.samples[int(left)*r.channels+channel])
			rightSample := int64(r.samples[int(right)*r.channels+channel])
			value := (leftSample*(int64(r.targetRate)-fraction) + rightSample*fraction) / int64(r.targetRate)
			result = binary.LittleEndian.AppendUint16(result, uint16(int16(value)))
		}
		r.outputFrames++
	}
	return result
}
