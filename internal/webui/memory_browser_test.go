package webui

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServerServesStandaloneMemoryExplorerShell(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	response, err := http.Get(srv.URL() + "/memory")
	if err != nil {
		t.Fatalf("get Memory explorer: %v", err)
	}
	defer closeMemoryHTTPResponse(t, response)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read Memory explorer: %v", err)
	}
	page := string(body)
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("Memory explorer response status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	for _, required := range []string{
		`id="memory-browser"`, `id="memory-graph"`, "Koder Memory",
		`data-memory-state-title`, `data-memory-state-detail`, `data-memory-retry`, `data-memory-banner`,
		`data-memory-graph-canvas`, `data-memory-selection-box`, `data-memory-selection-count`, `data-memory-graph-fallback`, `data-memory-graph-fallback-detail`, `data-memory-legend`, `data-memory-graph-center`, `data-memory-graph-fit`,
		`data-memory-view-hide`, `data-memory-view-isolate`, `data-memory-view-reveal`, `data-memory-view-undo`,
		`data-memory-preferences-reset`,
		`data-memory-graph-table-toggle`, `data-memory-graph-table`, `data-memory-graph-table-viewport`, `data-memory-graph-table-body`,
		`data-memory-saved-view`, `data-memory-saved-view-create`, `data-memory-saved-view-update`, `data-memory-saved-view-delete`,
		`data-memory-saved-view-dialog`, `data-memory-saved-view-form`, `data-memory-saved-view-error`,
		`data-memory-context-menu`, `data-memory-context-action="inspect"`, `data-memory-context-action="hide"`,
		`data-memory-search-form`, `data-memory-search`, `data-memory-filter="kind"`, `data-memory-filter="scope_kind"`,
		`data-memory-results`, `data-memory-load-more`,
		`data-memory-inspector-empty`, `data-memory-inspector-content`, `data-memory-inspector-markdown`,
		`data-memory-inspector-warnings`, `data-memory-inspector-applicability`, `data-memory-inspector-evidence`,
		`data-memory-chunk-create`, `data-memory-chunk-edit`, `data-memory-chunk-dialog`, `data-memory-chunk-form`,
		`data-memory-package-open`, `data-memory-package-dialog`, `data-memory-package-file`, `data-memory-package-preview-button`, `data-memory-package-stage-button`, `data-memory-package-activate-button`, `data-memory-package-export`,
		`data-memory-entry-create`, `data-memory-entry-edit`, `data-memory-entry-dialog`, `data-memory-entry-form`, `data-memory-supersede-dialog`,
		`data-memory-link-create`, `data-memory-link-dialog`, `data-memory-link-form`, `data-memory-link-unlink`, `data-memory-link-preview`,
		`data-memory-chunk-conflict`, `data-memory-chunk-conflict-reload`, `data-memory-entry-conflict`, `data-memory-entry-conflict-rebase`,
		`data-memory-inspector-history`, `data-memory-history-list`, `data-memory-history-more`,
		`data-memory-delete-dialog`, `data-memory-delete-form`, `data-memory-delete-blockers`, `data-memory-delete-submit`,
		`data-memory-expand-actions`, `data-memory-expand-incoming`, `data-memory-expand-outgoing`, `data-memory-expand-status`,
		`data-memory-send-chat`, `data-memory-send-status`,
		`/assets/vendor/marked/marked.umd.js`, `/assets/vendor/dompurify/purify.min.js`,
		`data-memory-pane="sources"`, `data-memory-pane="graph"`, `data-memory-pane="inspector"`, `data-memory-return`,
		`/assets/memory_browser.css`, `/assets/memory_api_client.js`, `/assets/memory_graph_adapter.js`, `/assets/memory_graph.js`, `/assets/memory_graph_rendering.js`, `/assets/memory_graph_renderer.js`, `/assets/memory_graph_viewport.js`, `/assets/memory_graph_layouts.js`, `/assets/memory_graph_interactions.js`, `/assets/memory_graph_table.js`, `/assets/memory_packages.js`, `/assets/memory_curation.js`, `/assets/memory_browser.js`,
		`/assets/vendor/graphology/graphology.umd.min.js`,
		`/assets/vendor/memory-layouts/memory-layouts.min.js`,
		`/assets/vendor/sigma/sigma.min.js`, currentAssetHash,
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("Memory explorer does not contain %q", required)
		}
	}
	if !strings.Contains(page, `"token":"`+srv.memoryBrowserToken+`"`) || !strings.Contains(page, `"api_base":"/api/memory/v1"`) {
		t.Fatal("Memory explorer does not contain its process-scoped API configuration")
	}
	if strings.Contains(page, assetHashPlaceholder) || strings.Contains(page, memoryBrowserConfigPlaceholder) || strings.Contains(page, "https://") || strings.Contains(page, "http://") {
		t.Fatal("Memory explorer contains an unresolved hash or remote dependency")
	}
}

func TestMemoryBrowserCredentialsAreRotatedAndComparedSafely(t *testing.T) {
	first, err := newMemoryBrowserToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newMemoryBrowserToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "kbw1_") || len(first) < 40 {
		t.Fatalf("Memory browser credentials are not independently random: %q %q", first, second)
	}
	if !memoryBrowserTokenMatches(first, first) || memoryBrowserTokenMatches(first, second) || memoryBrowserTokenMatches(first, "") {
		t.Fatal("Memory browser credential comparison is incorrect")
	}
}

func TestMainWebUIExposesMemoryExplorerEntry(t *testing.T) {
	page := renderIndexHTML()
	script := mustReadAsset("assets/app.js")
	for value, source := range map[string]string{
		`title="Open Memory"`:                     page,
		`@click="openMemoryExplorer()"`:           page,
		`memoryExplorerURL()`:                     script,
		`return memoryExplorerHref()`:             script,
		`params.set('return', location.pathname)`: script,
	} {
		if !strings.Contains(source, value) {
			t.Fatalf("main Web UI Memory entry does not contain %q", value)
		}
	}
}

func TestMemoryExplorerResponsiveShellAssetsAreEmbedded(t *testing.T) {
	for _, path := range []string{"assets/memory_browser.css", "assets/memory_api_client.js", "assets/memory_graph_adapter.js", "assets/memory_graph.js", "assets/memory_graph_rendering.js", "assets/memory_graph_renderer.js", "assets/memory_graph_viewport.js", "assets/memory_graph_layouts.js", "assets/memory_graph_interactions.js", "assets/memory_graph_table.js", "assets/memory_layout_worker.js", "assets/memory_packages.js", "assets/memory_curation.js", "assets/memory_browser.js"} {
		data, err := webAssets.ReadFile(path)
		if err != nil {
			t.Fatalf("read embedded Memory shell asset %q: %v", path, err)
		}
		if len(data) < 100 {
			t.Fatalf("Memory shell asset %q is unexpectedly small", path)
		}
	}
}

func TestMemoryExplorerRouteIsExact(t *testing.T) {
	if !isMemoryBrowserPath("/memory") || isMemoryBrowserPath("/memory/other") || isMemoryBrowserPath("/") {
		t.Fatal("Memory explorer path matching is not exact")
	}
}
