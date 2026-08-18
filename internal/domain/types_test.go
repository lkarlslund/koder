package domain

import (
	"encoding/json"
	"testing"
)

func TestSessionKindJSONAndZeroValue(t *testing.T) {
	if got := SessionKind(0); got != SessionKindRegular || got.String() != "regular" {
		t.Fatalf("zero session kind = %v, want regular", got)
	}
	data, err := json.Marshal(Session{Kind: SessionKindQuick})
	if err != nil {
		t.Fatal(err)
	}
	var decoded Session
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != SessionKindQuick {
		t.Fatalf("decoded kind = %v, want quick", decoded.Kind)
	}
	decoded = Session{}
	if err := json.Unmarshal([]byte(`{"Title":"legacy"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != SessionKindRegular {
		t.Fatalf("legacy kind = %v, want regular", decoded.Kind)
	}
}

func TestVoiceSessionKindJSON(t *testing.T) {
	data, err := json.Marshal(Session{Kind: SessionKindVoice})
	if err != nil {
		t.Fatal(err)
	}
	var decoded Session
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Kind != SessionKindVoice || decoded.Kind.String() != "voice" {
		t.Fatalf("decoded kind = %v, want voice", decoded.Kind)
	}
}

func TestUsageContextTokens(t *testing.T) {
	tests := []struct {
		name string
		in   Usage
		want int
		ok   bool
	}{
		{name: "prompt", in: Usage{PromptTokens: 120, CompletionTokens: 5, TotalTokens: 125}, want: 120, ok: true},
		{name: "infer", in: Usage{CompletionTokens: 5, TotalTokens: 125}, want: 120, ok: true},
		{name: "none", in: Usage{CompletionTokens: 5}, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.in.ContextTokens()
			if got != tt.want || ok != tt.ok {
				t.Fatalf("ContextTokens() = %d, %v; want %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
