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

func TestChatDimensionDefaultsAndLegacyVoiceMigration(t *testing.T) {
	var ordinary Chat
	if err := json.Unmarshal([]byte(`{"ID":"chat-1","SessionID":"session-1","WorkflowRole":"orchestrator"}`), &ordinary); err != nil {
		t.Fatal(err)
	}
	if ordinary.Backend != ChatBackendKoder || ordinary.InteractionMode != InteractionModeText || ordinary.WorkflowRole != WorkflowRoleOrchestrator {
		t.Fatalf("ordinary dimensions = backend %q mode %q role %q", ordinary.Backend, ordinary.InteractionMode, ordinary.WorkflowRole)
	}

	var voice Chat
	if err := json.Unmarshal([]byte(`{"ID":"chat-2","SessionID":"session-1","WorkflowRole":"voice"}`), &voice); err != nil {
		t.Fatal(err)
	}
	if voice.Backend != ChatBackendKoder || voice.InteractionMode != InteractionModeVoice || voice.WorkflowRole != WorkflowRoleOrchestrator {
		t.Fatalf("legacy voice dimensions = backend %q mode %q role %q", voice.Backend, voice.InteractionMode, voice.WorkflowRole)
	}
}

func TestNormalizeChatDimensions(t *testing.T) {
	got := NormalizeChatDimensions(Chat{WorkflowRole: WorkflowRoleVoice})
	if got.Backend != ChatBackendKoder || got.InteractionMode != InteractionModeVoice || got.WorkflowRole != WorkflowRoleOrchestrator {
		t.Fatalf("normalized dimensions = %#v", got)
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
