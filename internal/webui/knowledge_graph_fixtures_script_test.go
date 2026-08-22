package webui

import (
	"os/exec"
	"testing"
)

func TestKnowledgeGraphPatchFixturesJavaScript(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	command := exec.CommandContext(t.Context(), node, "testdata/knowledge_graph_fixtures_test.js")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("knowledge graph patch fixtures JavaScript test: %v\n%s", err, output)
	}
}
