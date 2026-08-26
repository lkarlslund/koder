package webui

import (
	"os/exec"
	"testing"
)

func TestMemoryLiveJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	command := exec.CommandContext(t.Context(), node, "testdata/memory_live_test.js")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("memory live JavaScript test: %v\n%s", err, output)
	}
}
