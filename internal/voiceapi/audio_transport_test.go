package voiceapi

import (
	"encoding/binary"
	"testing"

	"github.com/lkarlslund/koder/internal/voice"
	"github.com/lkarlslund/koder/internal/voicecodec"
)

func TestAudioNegotiationHonorsClientPreferenceAndLegacyFallback(t *testing.T) {
	base := voice.AudioConfig{
		Input:  voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 16_000, Channels: 1},
		Output: voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 24_000, Channels: 1},
	}
	for _, test := range []struct {
		name    string
		offered []string
		want    string
	}{
		{name: "Android prefers Opus", offered: []string{voice.Opus, voice.PCM16LE}, want: voice.Opus},
		{name: "client prefers PCM", offered: []string{voice.PCM16LE, voice.Opus}, want: voice.PCM16LE},
		{name: "legacy client", want: voice.PCM16LE},
		{name: "unknown capability falls back safely", offered: []string{"future_codec"}, want: voice.PCM16LE},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := negotiatedAudioConfig(base, test.offered, nil, nil)
			if selectedInputFormat(got).Encoding != test.want || selectedOutputFormat(got).Encoding != test.want {
				t.Fatalf("negotiated %#v, want %s", got, test.want)
			}
			if got.Input.Encoding != voice.PCM16LE || got.Output.Encoding != voice.PCM16LE {
				t.Fatalf("negotiation changed service PCM formats: %#v", got)
			}
		})
	}
}

func TestAudioNegotiationFallsBackPerDirectionForUnsupportedOpusFormat(t *testing.T) {
	base := voice.AudioConfig{
		Input:  voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 16_000, Channels: 1},
		Output: voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 44_100, Channels: 1},
	}

	got := negotiatedAudioConfig(base, []string{voice.Opus, voice.PCM16LE}, nil, nil)
	if input := selectedInputFormat(got); input.Encoding != voice.Opus {
		t.Fatalf("input transport = %#v, want Opus", input)
	}
	if output := selectedOutputFormat(got); output.Encoding != voice.PCM16LE {
		t.Fatalf("output transport = %#v, want PCM fallback for 44.1 kHz", output)
	}
	if got.Input != base.Input || got.Output != base.Output {
		t.Fatalf("negotiation changed service PCM formats: got %#v, want %#v", got, base)
	}
	if _, err := newAudioPacketEncoder(base.Output, selectedOutputFormat(got)); err != nil {
		t.Fatalf("create negotiated output encoder: %v", err)
	}
}

func TestAudioNegotiationUsesIndependentExplicitPreferences(t *testing.T) {
	base := voice.AudioConfig{
		Input:  voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 16_000, Channels: 1},
		Output: voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 44_100, Channels: 1},
	}
	input := &voice.AudioTransportPreference{Encoding: voice.PCM16LE}
	output := &voice.AudioTransportPreference{Encoding: voice.Opus, Bitrate: 16_000}

	got := negotiatedAudioConfig(base, []string{voice.PCM16LE, voice.Opus}, input, output)
	if transport := selectedInputFormat(got); transport != base.Input {
		t.Fatalf("input transport = %#v, want uncompressed %#v", transport, base.Input)
	}
	wantOutput := voice.AudioFormat{Encoding: voice.Opus, SampleRate: 48_000, Channels: 1, Bitrate: 16_000}
	if transport := selectedOutputFormat(got); transport != wantOutput {
		t.Fatalf("output transport = %#v, want %#v", transport, wantOutput)
	}
	if got.Input != base.Input || got.Output != base.Output {
		t.Fatalf("negotiation changed service PCM formats: got %#v, want %#v", got, base)
	}
}

func TestAudioPacketEncoderResamplesStreamingPCMForOpus(t *testing.T) {
	service := voice.AudioFormat{Encoding: voice.PCM16LE, SampleRate: 44_100, Channels: 1}
	transport := voice.AudioFormat{Encoding: voice.Opus, SampleRate: 48_000, Channels: 1, Bitrate: 24_000}
	encoder, err := newAudioPacketEncoder(service, transport)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]byte, service.SampleRate*2)
	for index := 0; index < service.SampleRate; index++ {
		binary.LittleEndian.PutUint16(pcm[index*2:], uint16(int16(index%2000-1000)))
	}
	var packets [][]byte
	emit := func(kind voice.AudioFrameKind, packet []byte) error {
		if kind != voice.AudioFrameOutputOpus {
			t.Fatalf("packet kind = %v, want Opus", kind)
		}
		packets = append(packets, append([]byte(nil), packet...))
		return nil
	}
	// Deliberately split a PCM frame and sample across input chunks.
	if err := encoder.appendPCM(pcm[:7_001], emit); err != nil {
		t.Fatal(err)
	}
	if err := encoder.appendPCM(pcm[7_001:], emit); err != nil {
		t.Fatal(err)
	}
	if err := encoder.finish(emit); err != nil {
		t.Fatal(err)
	}

	decoder, err := voicecodec.NewOpusDecoder(transport.SampleRate, transport.Channels)
	if err != nil {
		t.Fatal(err)
	}
	decodedBytes := 0
	for _, packet := range packets {
		decoded, decodeErr := decoder.Decode(packet)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		decodedBytes += len(decoded)
	}
	if want := transport.SampleRate * transport.Channels * 2; decodedBytes != want {
		t.Fatalf("decoded bytes = %d, want one second (%d)", decodedBytes, want)
	}
}
