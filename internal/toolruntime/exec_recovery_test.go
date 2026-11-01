package toolruntime

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
	_ "github.com/lkarlslund/koder/internal/tools/exectool"
)

func TestPersistedExecDescriptionRecovery(t *testing.T) {
	for _, tt := range []struct {
		name string
		args map[string]string
		want string
	}{
		{"before descriptions", map[string]string{"cmd": "pwd"}, "Run previously queued command"},
		{"legacy comment", map[string]string{"cmd": "pwd", "comment": "Show directory"}, "Show directory"},
		{"current description", map[string]string{"cmd": "pwd", "description": "Show current directory"}, "Show current directory"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, err := requestFromPersistedToolCall(domain.ToolCall{ToolCallID: "queued-call", Tool: tools.ExecCommand, Args: tt.args})
			if err != nil {
				t.Fatal(err)
			}
			if req.Args["description"] != tt.want || req.ToolCallID != "queued-call" || req.Args["cmd"] != "pwd" {
				t.Fatalf("recovered request = %#v", req)
			}
			if _, err := tools.Normalize(req); err != nil {
				t.Fatalf("recovered request cannot execute: %v", err)
			}
			if tt.name != "current description" && tt.args["description"] != "" {
				t.Fatalf("recovery mutated stored args: %v", tt.args)
			}
		})
	}
}

func TestNewExecCallsStillRequireDescription(t *testing.T) {
	_, err := tools.ParseProviderCall(provider.ToolCall{
		ID: "new-call", Type: "function",
		Function: provider.FunctionCall{Name: "exec_command", Arguments: `{"cmd":"pwd","comment":"Legacy annotation"}`},
	})
	if err == nil || !strings.Contains(err.Error(), "description is required") {
		t.Fatalf("new call error = %v", err)
	}
}

func TestHistoricalExecPreview(t *testing.T) {
	for _, tt := range []struct {
		args map[string]string
		want string
	}{
		{map[string]string{"cmd": "pwd"}, "pwd"},
		{map[string]string{"cmd": "pwd", "comment": "Show directory"}, "Show directory"},
		{map[string]string{"cmd": "pwd", "description": "Describe directory", "comment": "Old label"}, "Describe directory"},
	} {
		if got := tools.Preview(tools.Request{Tool: tools.ExecCommand, Args: tt.args}); got != tt.want {
			t.Fatalf("preview = %q, want %q", got, tt.want)
		}
	}
}
