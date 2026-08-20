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
