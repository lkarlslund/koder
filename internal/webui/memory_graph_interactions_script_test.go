package webui

import (
	"os/exec"
	"testing"
)

func TestMemoryGraphInteractionsJavaScript(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	command := exec.CommandContext(t.Context(), node, "testdata/memory_graph_interactions_test.js")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("memory graph interactions JavaScript test: %v\n%s", err, output)
	}
}
