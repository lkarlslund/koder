package voicecodec

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"testing"
)

func TestSupportsFormat(t *testing.T) {
	for _, test := range []struct {
		name       string
		sampleRate int
		channels   int
		want       bool
	}{
		{name: "phone microphone", sampleRate: 16_000, channels: 1, want: true},
		{name: "Opus speech output", sampleRate: 48_000, channels: 2, want: true},
		{name: "CD sample rate", sampleRate: 44_100, channels: 1, want: false},
		{name: "surround channels", sampleRate: 48_000, channels: 6, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SupportsFormat(test.sampleRate, test.channels); got != test.want {
				t.Fatalf("SupportsFormat(%d, %d) = %t, want %t", test.sampleRate, test.channels, got, test.want)
			}
		})
	}
}

func TestOpusDecoderAcceptsConcentusPacket(t *testing.T) {
	packet, err := base64.StdEncoding.DecodeString("SIKIQFJfcb0AAACmGGAMKu3f+EXrVlamUlGH5rhZQ1KVz6MR3JE0agIoHLrFV4vtl4PF0FZZNBBISoeilA2o3Oett2SnKxaA")
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := NewOpusDecoder(16_000, 1)
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := decoder.Decode(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 640 || rmsPCM(pcm) < 500 {
		t.Fatalf("decoded Concentus packet: bytes=%d RMS=%.1f", len(pcm), rmsPCM(pcm))
	}
}

func TestOpusRoundTripSpeechFrames(t *testing.T) {
	for _, test := range []struct {
		name       string
		sampleRate int
		bitrate    int
	}{
		{name: "microphone", sampleRate: 16000, bitrate: 18000},
		{name: "speech output", sampleRate: 24000, bitrate: 32000},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoder, err := NewOpusEncoder(test.sampleRate, 1, test.bitrate)
			if err != nil {
				t.Fatal(err)
			}
			decoder, err := NewOpusDecoder(test.sampleRate, 1)
			if err != nil {
				t.Fatal(err)
			}
			var decoded []byte
			var encodedBytes int
			for frame := range 12 {
				pcm := sinePCM(test.sampleRate, frame, 220)
				packet, err := encoder.Encode(pcm)
				if err != nil {
					t.Fatalf("encode frame %d: %v", frame, err)
				}
				encodedBytes += len(packet)
				chunk, err := decoder.Decode(packet)
				if err != nil {
					t.Fatalf("decode frame %d: %v", frame, err)
				}
				decoded = append(decoded, chunk...)
			}
			pcmBytes := encoder.FrameBytes() * 12
			if encodedBytes >= pcmBytes/3 {
				t.Fatalf("encoded %d bytes from %d PCM bytes; expected substantial compression", encodedBytes, pcmBytes)
			}
			if rmsPCM(decoded[len(decoded)/2:]) < 500 {
				t.Fatalf("decoded signal RMS is unexpectedly low: %.1f", rmsPCM(decoded[len(decoded)/2:]))
			}
		})
	}
}

func TestOpusRejectsPartialPCMFrame(t *testing.T) {
	encoder, err := NewOpusEncoder(16000, 1, 18000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Encode(make([]byte, encoder.FrameBytes()-2)); err == nil {
		t.Fatal("expected partial PCM frame error")
	}
}

func sinePCM(sampleRate, frame, frequency int) []byte {
	samples := sampleRate * FrameDurationMilliseconds / 1000
	out := make([]byte, samples*2)
	for index := range samples {
		position := frame*samples + index
		value := int16(math.Sin(2*math.Pi*float64(frequency*position)/float64(sampleRate)) * 12000)
		binary.LittleEndian.PutUint16(out[index*2:], uint16(value))
	}
	return out
}

func rmsPCM(pcm []byte) float64 {
	var sum float64
	for index := 0; index+1 < len(pcm); index += 2 {
		value := float64(int16(binary.LittleEndian.Uint16(pcm[index:])))
		sum += value * value
	}
	return math.Sqrt(sum / float64(len(pcm)/2))
}
