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
