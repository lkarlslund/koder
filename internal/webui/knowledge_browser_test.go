package webui

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServerServesStandaloneKnowledgeExplorerShell(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, err := Start(ctx, ctrl, Options{Bind: "127.0.0.1:0", NoOpenBrowser: true})
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	response, err := http.Get(srv.URL() + "/knowledge")
	if err != nil {
		t.Fatalf("get Knowledge explorer: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read Knowledge explorer: %v", err)
	}
	page := string(body)
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("Knowledge explorer response status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	for _, required := range []string{
		`id="knowledge-browser"`, `id="knowledge-graph"`, "Koder Knowledge",
		`data-knowledge-state-title`, `data-knowledge-state-detail`, `data-knowledge-retry`, `data-knowledge-banner`,
		`data-knowledge-graph-canvas`, `data-knowledge-legend`, `data-knowledge-graph-center`, `data-knowledge-graph-fit`,
		`data-knowledge-search-form`, `data-knowledge-search`, `data-knowledge-filter="kind"`, `data-knowledge-filter="scope_kind"`,
		`data-knowledge-results`, `data-knowledge-load-more`,
		`data-knowledge-inspector-empty`, `data-knowledge-inspector-content`, `data-knowledge-inspector-markdown`,
		`data-knowledge-send-chat`, `data-knowledge-send-status`,
		`/assets/vendor/marked/marked.umd.js`, `/assets/vendor/dompurify/purify.min.js`,
		`data-knowledge-pane="sources"`, `data-knowledge-pane="graph"`, `data-knowledge-pane="inspector"`, `data-knowledge-return`,
		`/assets/knowledge_browser.css`, `/assets/knowledge_api_client.js`, `/assets/knowledge_graph_adapter.js`, `/assets/knowledge_graph.js`, `/assets/knowledge_graph_rendering.js`, `/assets/knowledge_graph_renderer.js`, `/assets/knowledge_graph_viewport.js`, `/assets/knowledge_graph_layouts.js`, `/assets/knowledge_browser.js`,
		`/assets/vendor/graphology/graphology.umd.min.js`,
		`/assets/vendor/knowledge-layouts/knowledge-layouts.min.js`,
		`/assets/vendor/sigma/sigma.min.js`, currentAssetHash,
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("Knowledge explorer does not contain %q", required)
		}
	}
	if !strings.Contains(page, `"token":"`+srv.knowledgeBrowserToken+`"`) || !strings.Contains(page, `"api_base":"/api/knowledge/v1"`) {
		t.Fatal("Knowledge explorer does not contain its process-scoped API configuration")
	}
	if strings.Contains(page, assetHashPlaceholder) || strings.Contains(page, knowledgeBrowserConfigPlaceholder) || strings.Contains(page, "https://") || strings.Contains(page, "http://") {
		t.Fatal("Knowledge explorer contains an unresolved hash or remote dependency")
	}
}

func TestKnowledgeBrowserCredentialsAreRotatedAndComparedSafely(t *testing.T) {
	first, err := newKnowledgeBrowserToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newKnowledgeBrowserToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "kbw1_") || len(first) < 40 {
		t.Fatalf("Knowledge browser credentials are not independently random: %q %q", first, second)
	}
	if !knowledgeBrowserTokenMatches(first, first) || knowledgeBrowserTokenMatches(first, second) || knowledgeBrowserTokenMatches(first, "") {
		t.Fatal("Knowledge browser credential comparison is incorrect")
	}
}

func TestMainWebUIExposesKnowledgeExplorerEntry(t *testing.T) {
	page := renderIndexHTML()
	script := mustReadAsset("assets/app.js")
	for value, source := range map[string]string{
		`title="Open Knowledge"`:                  page,
		`@click="openKnowledgeExplorer()"`:        page,
		`knowledgeExplorerURL()`:                  script,
		`return knowledgeExplorerHref()`:          script,
		`params.set('return', location.pathname)`: script,
	} {
		if !strings.Contains(source, value) {
			t.Fatalf("main Web UI Knowledge entry does not contain %q", value)
		}
	}
}

func TestKnowledgeExplorerResponsiveShellAssetsAreEmbedded(t *testing.T) {
	for _, path := range []string{"assets/knowledge_browser.css", "assets/knowledge_api_client.js", "assets/knowledge_graph_adapter.js", "assets/knowledge_graph.js", "assets/knowledge_graph_rendering.js", "assets/knowledge_graph_renderer.js", "assets/knowledge_graph_viewport.js", "assets/knowledge_graph_layouts.js", "assets/knowledge_browser.js"} {
		data, err := webAssets.ReadFile(path)
		if err != nil {
			t.Fatalf("read embedded Knowledge shell asset %q: %v", path, err)
		}
		if len(data) < 100 {
			t.Fatalf("Knowledge shell asset %q is unexpectedly small", path)
		}
	}
}

func TestKnowledgeExplorerRouteIsExact(t *testing.T) {
	if !isKnowledgeBrowserPath("/knowledge") || isKnowledgeBrowserPath("/knowledge/other") || isKnowledgeBrowserPath("/") {
		t.Fatal("Knowledge explorer path matching is not exact")
	}
}
