package webui

import (
	"context"
	"os/exec"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestMemoryBrowserResponsiveTouchWorkflow(t *testing.T) {
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
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:responsive-test"}),
	})
	if err != nil {
		t.Fatalf("new Memory service: %v", err)
	}
	ctrl.SetMemoryService(service)
	ctrl.SetMemoryCuration(webCurationManager(t))
	createAPIChunk(t, service, "Touch-friendly memory", memory.Scope{Kind: memory.ScopeKindGlobal})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	command := exec.CommandContext(t.Context(), node, "testdata/memory_responsive_browser_test.js")
	command.Dir = "."
	command.Env = append(command.Environ(),
		"KODER_MEMORY_TEST_URL="+srv.URL()+"/memory",
		"KODER_CHROMIUM="+chromium,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("responsive Memory browser test: %v\n%s", err, output)
	}
}
