package domain

import "testing"

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

func TestChatCurrentContextSize(t *testing.T) {
	t.Run("known anchor with tail and live estimate", func(t *testing.T) {
		chat := Chat{LastKnownContextTokens: 1200, ContextTokensKnown: true}
		got := chat.CurrentContextSize(300, 25)
		if got.AnchorTokens != 1200 || got.TailTokens != 300 || got.LiveTokens != 25 || got.TotalTokens != 1525 || !got.Estimated {
			t.Fatalf("unexpected context usage: %#v", got)
		}
	})

	t.Run("known anchor without estimates", func(t *testing.T) {
		chat := Chat{LastKnownContextTokens: 1200, ContextTokensKnown: true}
		got := chat.CurrentContextSize(0, 0)
		if got.TotalTokens != 1200 || got.Estimated {
			t.Fatalf("unexpected context usage: %#v", got)
		}
	})

	t.Run("unknown anchor stays estimated", func(t *testing.T) {
		chat := Chat{LastKnownContextTokens: 900, ContextTokensKnown: false}
		got := chat.CurrentContextSize(0, 0)
		if got.TotalTokens != 900 || !got.Estimated {
			t.Fatalf("unexpected context usage: %#v", got)
		}
	})
}
