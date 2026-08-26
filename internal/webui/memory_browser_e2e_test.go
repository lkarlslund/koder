package webui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/lkarlslund/koder/internal/app"
	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
)

func TestMemoryBrowserCRUDLinkSearchExpandArchiveAndRestart(t *testing.T) {
	chromium := memoryBrowserChromium(t)
	ctrl := newTestController(t)
	stateDir := t.TempDir()
	store, service := openMemoryBrowserPebble(t, stateDir)
	ctrl.SetMemoryService(service)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startMemoryBrowserTestServer(t, serverCtx, ctrl)

	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromium),
		chromedp.WSURLReadTimeout(60*time.Second),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("use-gl", "swiftshader"),
	)
	allocatorCtx, stopAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	t.Cleanup(stopAllocator)
	browserCtx, stopBrowser := chromedp.NewContext(allocatorCtx)
	t.Cleanup(stopBrowser)
	browserCtx, cancelBrowserDeadline := context.WithTimeout(browserCtx, 90*time.Second)
	t.Cleanup(cancelBrowserDeadline)

	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()+"/memory"),
		chromedp.WaitReady(`#memory-browser`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('#memory-browser').__koderMemoryApp !== undefined`, nil),
	); err != nil {
		t.Fatalf("open Memory explorer: %v", err)
	}
	var initialState string
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(`document.querySelector('#memory-browser').dataset.state !== 'loading'`, nil),
		chromedp.AttributeValue(`#memory-browser`, "data-state", &initialState, nil, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("wait for Memory explorer state: %v", err)
	}
	if initialState != "empty" && initialState != "ready" {
		var detail string
		_ = chromedp.Run(browserCtx, chromedp.Text(`[data-memory-source-status]`, &detail, chromedp.ByQuery))
		t.Fatalf("Memory explorer state=%q detail=%q", initialState, detail)
	}

	const chunkTitle = "Linux partition tools E2E"
	if err := chromedp.Run(browserCtx,
		chromedp.Click(`[data-memory-chunk-create]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-memory-chunk-dialog]`, chromedp.ByQuery),
		chromedp.SetValue(`[data-memory-chunk-form] [name="title"]`, chunkTitle, chromedp.ByQuery),
		chromedp.SetValue(`[data-memory-chunk-form] [name="description"]`, "Durable partitioning memory created through the browser.", chromedp.ByQuery),
		chromedp.SetValue(`[data-memory-chunk-form] [name="tags"]`, "linux, storage, e2e", chromedp.ByQuery),
		chromedp.Click(`[data-memory-chunk-submit]`, chromedp.ByQuery),
		chromedp.Poll(`!document.querySelector('[data-memory-chunk-dialog]').open`, nil),
		chromedp.Poll(`document.querySelector('[data-memory-inspector-title]').textContent === "Linux partition tools E2E"`, nil),
	); err != nil {
		t.Fatalf("create chunk through browser: %v", err)
	}
	chunkID := selectedMemoryObjectID(t, browserCtx, "chunk")

	const firstEntryTitle = "Use sfdisk when fdisk is unavailable"
	firstEntryID := createMemoryBrowserEntry(t, browserCtx, chunkID, firstEntryTitle,
		"The partitioning sentinel says to use sfdisk for scripted Linux partition changes.")
	selectMemoryBrowserResult(t, browserCtx, "chunk", chunkID)

	const secondEntryTitle = "Verify partition table after changes"
	secondEntryID := createMemoryBrowserEntry(t, browserCtx, chunkID, secondEntryTitle,
		"Run sfdisk --dump after changing a partition table and retain the output.")
	if firstEntryID == secondEntryID {
		t.Fatalf("browser created duplicate entry identity %q", firstEntryID)
	}

	// A graph can only select nodes that are already in one connected snapshot. Seed a
	// relationship representing existing memory, then create another relationship
	// entirely through the graph table and relationship editor below.
	if _, err := service.CreateLink(context.Background(), memoryService.CreateLinkRequest{Link: memory.Link{
		Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: firstEntryID},
		Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: chunkID},
		Kind:   memory.LinkKindPartOf, Label: "Belongs to partition guidance",
	}}); err != nil {
		t.Fatalf("seed connected graph: %v", err)
	}
	selectMemoryBrowserResult(t, browserCtx, "chunk", chunkID)
	var graphDebug struct {
		Nodes int      `json:"nodes"`
		Edges int      `json:"edges"`
		Keys  []string `json:"keys"`
	}
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(fmt.Sprintf(`(() => {
			const app = document.querySelector('#memory-browser').__koderMemoryApp;
			return app.graphRoot && app.graphRoot.kind === 'chunk' && app.graphRoot.id === %q && document.querySelector('#memory-graph').dataset.graphState !== 'loading';
		})()`, chunkID), nil),
		chromedp.Evaluate(`(() => {
			const adapter = document.querySelector('#memory-browser').__koderMemoryApp.graphAdapter;
			const counts = adapter.counts();
			return {nodes: counts.nodes, edges: counts.edges, keys: adapter.target.graph.nodes()};
		})()`, &graphDebug),
	); err != nil {
		t.Fatalf("load seeded browser graph: %v", err)
	}
	if graphDebug.Nodes < 2 {
		t.Fatalf("seeded browser graph nodes=%d edges=%d keys=%v", graphDebug.Nodes, graphDebug.Edges, graphDebug.Keys)
	}
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`(() => {
			const toggle = document.querySelector('[data-memory-graph-table-toggle]');
			if (toggle.getAttribute('aria-pressed') !== 'true') toggle.click();
		})()`, nil),
		chromedp.WaitVisible(`[data-memory-graph-table]`, chromedp.ByQuery),
		chromedp.Poll(fmt.Sprintf(`document.querySelector('[data-graph-key="entry:%s"] [data-graph-table-select]') !== null`, firstEntryID), nil),
		chromedp.Click(fmt.Sprintf(`[data-graph-key="entry:%s"] [data-graph-table-select]`, firstEntryID), chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('#memory-browser').__koderMemoryApp.graphAdapter.selectionSnapshot().items.length === 2`, nil),
		chromedp.Poll(`!document.querySelector('[data-memory-link-create]').disabled`, nil),
		chromedp.Click(`[data-memory-link-create]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-memory-link-dialog]`, chromedp.ByQuery),
		setMemoryBrowserControl(`[data-memory-link-form] [name="kind"]`, memory.LinkKindRelatedTo.String()),
		chromedp.SetValue(`[data-memory-link-form] [name="label"]`, "Recommended partition method", chromedp.ByQuery),
		chromedp.SetValue(`[data-memory-link-form] [name="notes"]`, "Connected through the browser E2E workflow.", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const app = document.querySelector('#memory-browser').__koderMemoryApp;
			app.linkForm.requestSubmit();
			if (!app.linkFormError.hidden) {
				throw new Error(app.linkFormError.textContent + ' values=' + JSON.stringify(Object.fromEntries(new FormData(app.linkForm).entries())));
			}
		})()`, nil),
		chromedp.Poll(`!document.querySelector('[data-memory-link-dialog]').open`, nil),
		chromedp.Poll(`document.querySelector('[data-memory-inspector-title]').textContent === "Recommended partition method"`, nil),
	); err != nil {
		t.Fatalf("create graph relationship through browser: %v", err)
	}

	selectMemoryBrowserResult(t, browserCtx, "chunk", chunkID)
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(`document.querySelector('[data-memory-expand-outgoing]') && !document.querySelector('[data-memory-expand-outgoing]').disabled`, nil),
		chromedp.Click(`[data-memory-expand-outgoing]`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-memory-expand-status]').textContent.startsWith('Added up to')`, nil),
		chromedp.SetValue(`[data-memory-search]`, "partitioning sentinel", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-memory-search-form]').requestSubmit()`, nil),
		chromedp.Poll(fmt.Sprintf(`document.querySelector('[data-memory-results] [data-object-id="%s"]') !== null`, firstEntryID), nil),
		chromedp.Click(fmt.Sprintf(`[data-memory-results] [data-object-id="%s"]`, firstEntryID), chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-memory-inspector-title]').textContent === "Use sfdisk when fdisk is unavailable"`, nil),
		chromedp.Click(`[data-memory-entry-edit]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-memory-entry-dialog]`, chromedp.ByQuery),
		chromedp.SetValue(`[data-memory-entry-form] [name="summary"]`, "Edited in the browser: prefer sfdisk and verify the result.", chromedp.ByQuery),
		chromedp.Click(`[data-memory-entry-submit]`, chromedp.ByQuery),
		chromedp.Poll(`!document.querySelector('[data-memory-entry-dialog]').open`, nil),
		chromedp.Poll(`document.querySelector('[data-memory-inspector-summary]').textContent.includes('Edited in the browser')`, nil),
		chromedp.Evaluate(`globalThis.confirm = () => true`, nil),
		chromedp.Click(`[data-memory-entry-archive]`, chromedp.ByQuery),
		chromedp.Poll(`Array.from(document.querySelectorAll('[data-memory-inspector-badges] span')).some(node => node.textContent === 'Archived')`, nil),
	); err != nil {
		t.Fatalf("search, expand, edit, and archive through browser: %v", err)
	}

	shutdownMemoryBrowserTestServer(t, stopServer, server)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	if err := service.ShutdownOperations(shutdownCtx); err != nil {
		cancelShutdown()
		t.Fatalf("stop Memory operations before restart: %v", err)
	}
	cancelShutdown()
	if err := store.Close(); err != nil {
		t.Fatalf("close Memory store before restart: %v", err)
	}

	reopenedStore, reopenedService := openMemoryBrowserPebble(t, stateDir)
	ctrl.SetMemoryService(reopenedService)
	restartedCtx, stopRestarted := context.WithCancel(context.Background())
	restartedServer := startMemoryBrowserTestServer(t, restartedCtx, ctrl)
	t.Cleanup(func() {
		shutdownMemoryBrowserTestServer(t, stopRestarted, restartedServer)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := reopenedService.ShutdownOperations(ctx); err != nil {
			t.Errorf("stop reopened Memory operations: %v", err)
		}
		if err := reopenedStore.Close(); err != nil {
			t.Errorf("close reopened Memory store: %v", err)
		}
	})

	restartURL := fmt.Sprintf("%s/memory?object_kind=entry&id=%s", restartedServer.URL(), firstEntryID)
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(restartURL),
		chromedp.WaitReady(`#memory-browser`, chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-memory-inspector-title]').textContent === "Use sfdisk when fdisk is unavailable"`, nil),
		chromedp.Poll(`document.querySelector('[data-memory-inspector-summary]').textContent.includes('Edited in the browser')`, nil),
		chromedp.Poll(`Array.from(document.querySelectorAll('[data-memory-inspector-badges] span')).some(node => node.textContent === 'Archived')`, nil),
	); err != nil {
		t.Fatalf("reload durable archived memory after restart: %v", err)
	}
	entry, err := reopenedService.Entry(context.Background(), memory.EntryID(firstEntryID))
	if err != nil {
		t.Fatalf("read restarted entry: %v", err)
	}
	if entry.State != memory.EntryStateArchived || entry.Revision.Number != 3 {
		t.Fatalf("restarted entry state=%s revision=%d, want archived revision 3", entry.State, entry.Revision.Number)
	}
}

func memoryBrowserChromium(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return path
		}
	}
	if os.Getenv("CI") != "" {
		t.Fatal("Memory browser E2E requires Chromium in CI")
	}
	t.Skip("Memory browser E2E requires Chromium")
	return ""
}

func openMemoryBrowserPebble(t *testing.T, stateDir string) (*memoryPebble.Store, *memoryService.Service) {
	t.Helper()
	store, err := memoryPebble.Open(stateDir)
	if err != nil {
		t.Fatalf("open Memory Pebble store: %v", err)
	}
	service, err := memoryService.New(memoryService.Config{
		Store: store,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:browser-e2e"}),
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("new Memory service: %v", err)
	}
	return store, service
}

func startMemoryBrowserTestServer(t *testing.T, ctx context.Context, ctrl *app.Controller) *Server {
	t.Helper()
	server, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start Memory browser server: %v", err)
	}
	return server
}

func shutdownMemoryBrowserTestServer(t *testing.T, stop context.CancelFunc, server *Server) {
	t.Helper()
	stop()
	// The browser keeps streaming Memory mutation requests open. A process
	// restart closes those client connections rather than draining them, so the
	// E2E restart must model that boundary with Close rather than Shutdown.
	if err := server.server.Close(); err != nil {
		t.Fatalf("shutdown Memory browser server: %v", err)
	}
}

func selectedMemoryObjectID(t *testing.T, browserCtx context.Context, kind string) string {
	t.Helper()
	var selected struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(`(() => {
		const app = document.querySelector('#memory-browser').__koderMemoryApp;
		return {kind: app.urlState.objectKind, id: app.urlState.id};
	})()`, &selected)); err != nil {
		t.Fatalf("read selected Memory object: %v", err)
	}
	if selected.Kind != kind || selected.ID == "" {
		t.Fatalf("selected Memory object = %s:%s, want %s", selected.Kind, selected.ID, kind)
	}
	return selected.ID
}

func createMemoryBrowserEntry(t *testing.T, browserCtx context.Context, chunkID, title, body string) string {
	t.Helper()
	if err := chromedp.Run(browserCtx,
		chromedp.Click(`[data-memory-entry-create]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-memory-entry-dialog]`, chromedp.ByQuery),
		chromedp.SetValue(`[data-memory-entry-form] [name="title"]`, title, chromedp.ByQuery),
		chromedp.SetValue(`[data-memory-entry-form] [name="summary"]`, body, chromedp.ByQuery),
		chromedp.SetValue(`[data-memory-entry-form] [name="body"]`, body, chromedp.ByQuery),
		chromedp.Click(`[data-memory-entry-submit]`, chromedp.ByQuery),
		chromedp.Poll(`!document.querySelector('[data-memory-entry-dialog]').open`, nil),
		chromedp.Poll(fmt.Sprintf(`document.querySelector('[data-memory-inspector-title]').textContent === %q`, title), nil),
	); err != nil {
		t.Fatalf("create entry %q through browser: %v", title, err)
	}
	entryID := selectedMemoryObjectID(t, browserCtx, "entry")
	if chunkID == "" || entryID == "" {
		t.Fatalf("created entry has empty identity: chunk=%q entry=%q", chunkID, entryID)
	}
	return entryID
}

func selectMemoryBrowserResult(t *testing.T, browserCtx context.Context, kind, objectID string) {
	t.Helper()
	selector := fmt.Sprintf(`[data-memory-results] [data-object-kind="%s"][data-object-id="%s"]`, kind, objectID)
	if err := chromedp.Run(browserCtx,
		chromedp.Poll(fmt.Sprintf(`document.querySelector(%q) !== null`, selector), nil),
		chromedp.Click(selector, chromedp.ByQuery),
		chromedp.Poll(fmt.Sprintf(`document.querySelector('#memory-browser').__koderMemoryApp.urlState.id === %q`, objectID), nil),
		chromedp.Poll(`!document.querySelector('[data-memory-inspector-content]').hidden`, nil),
	); err != nil {
		t.Fatalf("select Memory result %s:%s: %v", kind, objectID, err)
	}
}

func setMemoryBrowserControl(selector, value string) chromedp.Action {
	return chromedp.Evaluate(fmt.Sprintf(`(() => {
		const control = document.querySelector(%q);
		control.value = %q;
		control.dispatchEvent(new Event('input', {bubbles: true}));
		control.dispatchEvent(new Event('change', {bubbles: true}));
	})()`, selector, value), nil)
}
