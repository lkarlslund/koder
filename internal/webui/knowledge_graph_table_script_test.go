package webui

import (
	"os/exec"
	"testing"
)

func TestKnowledgeGraphTableJavaScript(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	command := exec.CommandContext(t.Context(), node, "testdata/knowledge_graph_table_test.js")
	command.Dir = "."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("knowledge graph table JavaScript test: %v\n%s", err, output)
	}
}
