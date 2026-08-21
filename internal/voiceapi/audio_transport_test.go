package voiceapi

import (
	"testing"

	"github.com/lkarlslund/koder/internal/voice"
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
			got := negotiatedAudioConfig(base, test.offered)
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

	got := negotiatedAudioConfig(base, []string{voice.Opus, voice.PCM16LE})
	if input := selectedInputFormat(got); input.Encoding != voice.Opus {
		t.Fatalf("input transport = %#v, want Opus", input)
	}
	if output := selectedOutputFormat(got); output.Encoding != voice.PCM16LE {
		t.Fatalf("output transport = %#v, want PCM fallback for 44.1 kHz", output)
	}
	if got.Input != base.Input || got.Output != base.Output {
		t.Fatalf("negotiation changed service PCM formats: got %#v, want %#v", got, base)
	}
	if _, err := newAudioPacketEncoder(selectedOutputFormat(got)); err != nil {
		t.Fatalf("create negotiated output encoder: %v", err)
	}
}
