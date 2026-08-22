package webui

import (
	"context"
	"os/exec"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestKnowledgeBrowserResponsiveTouchWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("responsive browser test is a component test")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	chromium, err := exec.LookPath("chromium")
	if err != nil {
		chromium, err = exec.LookPath("chromium-browser")
	}
	if err != nil {
		t.Skip("Chromium is not installed")
	}

	ctrl := newTestController(t)
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:responsive-test"}),
	})
	if err != nil {
		t.Fatalf("new Knowledge service: %v", err)
	}
	ctrl.SetKnowledgeService(service)
	ctrl.SetKnowledgeCuration(webCurationManager(t))
	createAPIChunk(t, service, "Touch-friendly knowledge", knowledge.Scope{Kind: knowledge.ScopeKindGlobal})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	command := exec.CommandContext(t.Context(), node, "testdata/knowledge_responsive_browser_test.js")
	command.Dir = "."
	command.Env = append(command.Environ(),
		"KODER_KNOWLEDGE_TEST_URL="+srv.URL()+"/knowledge",
		"KODER_CHROMIUM="+chromium,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("responsive Knowledge browser test: %v\n%s", err, output)
	}
}
