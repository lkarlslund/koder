package webui

import (
	"os/exec"
	"testing"
)

func TestMemoryCurationJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	command := exec.CommandContext(t.Context(), node, "testdata/memory_curation_test.js")
	command.Dir = "."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("memory curation JavaScript test: %v\n%s", err, output)
	}
}
