package tools

import (
	"strings"
	"testing"
)

func TestOutputBudgetBytesFollowsFreeContext(t *testing.T) {
	tests := []struct {
		window, used, want int
	}{
		{window: 0, used: 0, want: 0},
		{window: 32768, used: 0, want: 32768},
		{window: 32768, used: 30000, want: 4 << 10},
		{window: 262144, used: 20000, want: 242144},
		{window: 1 << 20, used: 0, want: 256 << 10},
	}
	for _, tt := range tests {
		if got := OutputBudgetBytes(tt.window, tt.used); got != tt.want {
			t.Errorf("OutputBudgetBytes(%d, %d) = %d, want %d", tt.window, tt.used, got, tt.want)
		}
	}
}

func TestExecResultReportsTruncation(t *testing.T) {
	text := formatExecStoredResult(ExecStoredResult{State: "completed", OutputMode: "tail", Output: "tail line\n", OutputBytes: 1010, OmittedBytes: 1000})
	if !strings.Contains(text, "the first 1000 bytes were left out; showing the last 10") {
		t.Fatalf("missing truncation note: %q", text)
	}
	if text := formatExecStoredResult(ExecStoredResult{State: "completed", Output: "all\n"}); strings.Contains(text, "truncated") {
		t.Fatalf("untruncated result has a note: %q", text)
	}
}
