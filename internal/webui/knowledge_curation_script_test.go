package webui

import (
	"os/exec"
	"testing"
)

func TestKnowledgeCurationJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	command := exec.CommandContext(t.Context(), node, "testdata/knowledge_curation_test.js")
	command.Dir = "."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("knowledge curation JavaScript test: %v\n%s", err, output)
	}
}
