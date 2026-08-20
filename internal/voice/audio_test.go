package voice

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestAudioFrameWireFormat(t *testing.T) {
	encoded, err := EncodeAudioFrame(AudioFrame{
		Kind: AudioFrameInputPCM, Sequence: 0x01020304, Payload: []byte{0x34, 0x12, 0xcc, 0xff},
	})
	if err != nil {
		t.Fatal(err)
	}
	const wantHex = "4b56413101000000010203043412ccff"
	if got := hex.EncodeToString(encoded); got != wantHex {
		t.Fatalf("encoded frame = %s, want %s", got, wantHex)
	}
	decoded, err := DecodeAudioFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != AudioFrameInputPCM || decoded.Sequence != 0x01020304 || !bytes.Equal(decoded.Payload, []byte{0x34, 0x12, 0xcc, 0xff}) {
		t.Fatalf("decoded frame = %#v", decoded)
	}
}

func TestAudioFrameRejectsMalformedInput(t *testing.T) {
	valid, err := EncodeAudioFrame(AudioFrame{Kind: AudioFrameOutputPCM, Payload: []byte{0, 0}})
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"short":    valid[:AudioFrameHeaderSize-1],
		"magic":    append([]byte("NOPE"), valid[4:]...),
		"reserved": append(append([]byte(nil), valid[:6]...), append([]byte{1, 0}, valid[8:]...)...),
		"odd pcm":  valid[:len(valid)-1],
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAudioFrame(data); err == nil {
				t.Fatal("expected malformed frame to be rejected")
			}
		})
	}
}

func TestAudioFrameAcceptsOddLengthOpusPacket(t *testing.T) {
	want := AudioFrame{Kind: AudioFrameInputOpus, Sequence: 7, Payload: []byte{1, 2, 3}}
	encoded, err := EncodeAudioFrame(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeAudioFrame(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != want.Kind || got.Sequence != want.Sequence || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
	}
}

func FuzzAudioFrameRoundTrip(f *testing.F) {
	f.Add(uint8(AudioFrameInputPCM), uint8(0), uint32(3), []byte{1, 0, 2, 0})
	f.Fuzz(func(t *testing.T, kind, flags uint8, sequence uint32, pcm []byte) {
		if (kind != uint8(AudioFrameInputPCM) && kind != uint8(AudioFrameOutputPCM)) || len(pcm) == 0 || len(pcm) > MaxAudioPayloadSize || len(pcm)%2 != 0 {
			t.Skip()
		}
		want := AudioFrame{Kind: AudioFrameKind(kind), Flags: flags, Sequence: sequence, Payload: pcm}
		encoded, err := EncodeAudioFrame(want)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeAudioFrame(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != want.Kind || got.Flags != want.Flags || got.Sequence != want.Sequence || !bytes.Equal(got.Payload, want.Payload) {
			t.Fatalf("round trip mismatch: got %#v want %#v", got, want)
		}
	})
}
