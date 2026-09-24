package provider

import "testing"

func TestIsContextWindowExceeded(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "openai code", err: &APIError{StatusCode: 400, Body: `{"error":{"code":"context_length_exceeded"}}`}, want: true},
		{name: "compatible wording", err: &APIError{StatusCode: 422, Body: `maximum context length is 32768 tokens`}, want: true},
		{name: "unrelated bad request", err: &APIError{StatusCode: 400, Body: `unknown field`}, want: false},
		{name: "server failure", err: &APIError{StatusCode: 500, Body: `context window`}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := IsContextWindowExceeded(test.err); got != test.want {
				t.Fatalf("IsContextWindowExceeded() = %v, want %v", got, test.want)
			}
		})
	}
}
