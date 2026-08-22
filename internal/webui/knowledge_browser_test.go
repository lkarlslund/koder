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
		`data-knowledge-pane="sources"`, `data-knowledge-pane="graph"`, `data-knowledge-pane="inspector"`, `data-knowledge-return`,
		`/assets/knowledge_browser.css`, `/assets/knowledge_browser.js`,
		`/assets/vendor/graphology/graphology.umd.min.js`,
		`/assets/vendor/knowledge-layouts/knowledge-layouts.min.js`,
		`/assets/vendor/sigma/sigma.min.js`, currentAssetHash,
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("Knowledge explorer does not contain %q", required)
		}
	}
	if strings.Contains(page, assetHashPlaceholder) || strings.Contains(page, "https://") || strings.Contains(page, "http://") {
		t.Fatal("Knowledge explorer contains an unresolved hash or remote dependency")
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
	for _, path := range []string{"assets/knowledge_browser.css", "assets/knowledge_browser.js"} {
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
