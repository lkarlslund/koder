package webui

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	memoryBrowserLimitNodes = 1000
	memoryBrowserLimitEdges = 2000
)

type memoryBrowserPerformanceResult struct {
	Error                string  `json:"error"`
	Nodes                int     `json:"nodes"`
	Edges                int     `json:"edges"`
	GraphState           string  `json:"graph_state"`
	SnapshotToPaintMS    float64 `json:"snapshot_to_paint_ms"`
	InitialRefreshMS     float64 `json:"initial_refresh_ms"`
	InteractionP50MS     float64 `json:"interaction_p50_ms"`
	InteractionP95MS     float64 `json:"interaction_p95_ms"`
	InteractionMaxMS     float64 `json:"interaction_max_ms"`
	RendererRefreshP50MS float64 `json:"renderer_refresh_p50_ms"`
	RendererRefreshP95MS float64 `json:"renderer_refresh_p95_ms"`
	RendererRefreshMaxMS float64 `json:"renderer_refresh_max_ms"`
	Refreshes            int     `json:"refreshes"`
}

// TestMemoryBrowserPerformanceAtSnapshotLimits exercises the actual Graphology,
// Sigma, canvas, table, and browser-orchestration path at the server's enforced
// snapshot bounds. The generous ceilings detect hangs and catastrophic regressions;
// docs/memory-performance.md records the observed timings instead of turning a
// particular developer machine's speed into a flaky CI contract.
func TestMemoryBrowserPerformanceAtSnapshotLimits(t *testing.T) {
	chromium := memoryBrowserChromium(t)
	ctrl := newTestController(t)
	store, service := openMemoryBrowserPebble(t, t.TempDir())
	ctrl.SetMemoryService(service)
	serverCtx, stopServer := context.WithCancel(context.Background())
	server := startMemoryBrowserTestServer(t, serverCtx, ctrl)
	t.Cleanup(func() {
		shutdownMemoryBrowserTestServer(t, stopServer, server)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.ShutdownOperations(shutdownCtx); err != nil {
			t.Errorf("stop Memory operations: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Errorf("close Memory store: %v", err)
		}
	})

	allocatorOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromium),
		chromedp.WSURLReadTimeout(60*time.Second),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("enable-webgl", true),
		chromedp.Flag("enable-unsafe-swiftshader", true),
		chromedp.Flag("ignore-gpu-blocklist", true),
		chromedp.Flag("use-gl", "angle"),
		chromedp.Flag("use-angle", "swiftshader"),
	)
	allocatorCtx, stopAllocator := chromedp.NewExecAllocator(context.Background(), allocatorOptions...)
	t.Cleanup(stopAllocator)
	browserCtx, stopBrowser := chromedp.NewContext(allocatorCtx)
	t.Cleanup(stopBrowser)
	browserCtx, cancelBrowserDeadline := context.WithTimeout(browserCtx, 120*time.Second)
	t.Cleanup(cancelBrowserDeadline)

	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(server.URL()+"/memory"),
		chromedp.WaitReady(`#memory-browser`, chromedp.ByQuery),
		chromedp.Poll(`(() => {
			const app = document.querySelector('#memory-browser').__koderMemoryApp;
			return app && app.graphAdapter;
		})()`, nil),
	); err != nil {
		t.Fatalf("open rendered Memory explorer: %v", err)
	}
	var environment struct {
		Renderer      bool   `json:"renderer"`
		State         string `json:"state"`
		GraphFallback string `json:"graph_fallback"`
		WebGL         bool   `json:"webgl"`
	}
	if err := chromedp.Run(browserCtx, chromedp.Evaluate(`(() => {
		const shell = document.querySelector('#memory-browser');
		const app = shell.__koderMemoryApp;
		const canvas = document.createElement('canvas');
		return {renderer: !!app.graphRenderer, state: shell.dataset.state,
			graph_fallback: shell.dataset.graphFallback || '', webgl: !!canvas.getContext('webgl2') || !!canvas.getContext('webgl')};
	})()`, &environment)); err != nil {
		t.Fatalf("inspect Memory explorer rendering environment: %v", err)
	}
	if !environment.Renderer {
		t.Fatalf("Memory explorer renderer unavailable: state=%q fallback=%q webgl=%t", environment.State, environment.GraphFallback, environment.WebGL)
	}

	var result memoryBrowserPerformanceResult
	if err := chromedp.Run(browserCtx,
		chromedp.Evaluate(`(() => {
		globalThis.__koderMemoryPerformanceResult = null;
		void (async () => {
		try {
		const app = document.querySelector('#memory-browser').__koderMemoryApp;
		const frame = () => new Promise(resolve => requestAnimationFrame(() => resolve()));
		const percentile = (values, fraction) => {
			const sorted = [...values].sort((left, right) => left - right);
			return sorted[Math.min(sorted.length - 1, Math.ceil(sorted.length * fraction) - 1)];
		};
		const nodes = Array.from({length: 1000}, (_, index) => {
			const id = 'perf' + String(index).padStart(4, '0');
			return {
				id: 'entry:' + id, object: {kind: 'entry', id}, semantic_kind: 'fact',
				title: 'Performance node ' + index, summary: 'Browser performance fixture node ' + index,
				scope: {kind: 'global'}, state: 'active', verification: 'unverified',
				revision: {number: 1, id: 'perf-node-revision-' + index},
			};
		});
		const edges = [];
		for (let index = 0; index < 1000; index++) {
			for (const [suffix, offset] of [['next', 1], ['cross', 17]]) {
				const sourceID = 'perf' + String(index).padStart(4, '0');
				const targetID = 'perf' + String((index + offset) % 1000).padStart(4, '0');
				edges.push({
					id: 'perf-' + suffix + '-' + index,
					source: {kind: 'entry', id: sourceID}, target: {kind: 'entry', id: targetID},
					kind: 'related_to', label: suffix === 'next' ? 'Next' : 'Cross-link', state: 'active',
					revision: {number: 1, id: 'perf-edge-revision-' + suffix + '-' + index},
				});
			}
		}
		const snapshot = {
			generation: 1, checkpoint: {stream_id: 'performance-snapshot', sequence: 0},
			nodes, edges,
			page: {limit: 1000, returned: 1000, has_more: true, truncated: true},
		};

		if (app.graphLayout) app.graphLayout.stop('performance_fixture');
		app.graphRoot = {kind: 'entry', id: 'perf0000'};
		app.client.graphSnapshot = async () => snapshot;
		const before = app.graphRenderer.getMetrics();
		const started = performance.now();
		await app.loadGraphSelection();
		await frame();
		await frame();
		const snapshotToPaintMS = performance.now() - started;
		if (app.graphLayout) app.graphLayout.stop('performance_measurement');
		const loaded = app.graphRenderer.getMetrics();

		const frameTimes = [];
		const refreshTimes = [];
		for (let index = 0; index < 35; index++) {
			const key = 'entry:perf' + String((index * 29) % 1000).padStart(4, '0');
			const frameStarted = performance.now();
			app.graphRenderer.setSelection('node', key);
			await frame();
			await frame();
			const elapsed = performance.now() - frameStarted;
			if (index >= 5) {
				frameTimes.push(elapsed);
				refreshTimes.push(app.graphRenderer.getMetrics().lastRefreshMS);
			}
		}
		const finished = app.graphRenderer.getMetrics();
		globalThis.__koderMemoryPerformanceResult = {
			nodes: finished.nodes, edges: finished.edges,
			graph_state: document.querySelector('#memory-graph').dataset.graphState,
			snapshot_to_paint_ms: snapshotToPaintMS,
			initial_refresh_ms: loaded.maxRefreshMS > before.maxRefreshMS ? loaded.maxRefreshMS : loaded.lastRefreshMS,
			interaction_p50_ms: percentile(frameTimes, 0.50), interaction_p95_ms: percentile(frameTimes, 0.95),
			interaction_max_ms: Math.max(...frameTimes),
			renderer_refresh_p50_ms: percentile(refreshTimes, 0.50), renderer_refresh_p95_ms: percentile(refreshTimes, 0.95),
			renderer_refresh_max_ms: Math.max(...refreshTimes), refreshes: finished.refreshes - before.refreshes,
		};
		} catch (error) {
			globalThis.__koderMemoryPerformanceResult = {error: String(error && error.stack || error)};
		}
		})();
		return true;
	})()`, nil),
		chromedp.Poll(`globalThis.__koderMemoryPerformanceResult !== null`, nil),
		chromedp.Evaluate(`globalThis.__koderMemoryPerformanceResult`, &result),
	); err != nil {
		t.Fatalf("measure bounded Memory explorer rendering: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("measure bounded Memory explorer rendering: %s", result.Error)
	}

	t.Logf("Memory explorer limit: nodes=%d edges=%d state=%s snapshot-to-paint=%.2fms initial-refresh=%.2fms interaction p50/p95/max=%.2f/%.2f/%.2fms renderer-refresh p50/p95/max=%.2f/%.2f/%.2fms refreshes=%d",
		result.Nodes, result.Edges, result.GraphState, result.SnapshotToPaintMS, result.InitialRefreshMS,
		result.InteractionP50MS, result.InteractionP95MS, result.InteractionMaxMS,
		result.RendererRefreshP50MS, result.RendererRefreshP95MS, result.RendererRefreshMaxMS, result.Refreshes)
	if result.Nodes != memoryBrowserLimitNodes || result.Edges != memoryBrowserLimitEdges {
		t.Fatalf("rendered graph = %d nodes/%d edges, want %d/%d", result.Nodes, result.Edges, memoryBrowserLimitNodes, memoryBrowserLimitEdges)
	}
	if result.GraphState != "truncated" {
		t.Fatalf("rendered graph state = %q, want truncated at enforced bounds", result.GraphState)
	}
	if result.Refreshes < 31 {
		t.Fatalf("renderer refreshes = %d, want initial paint and 30 measured interaction paints", result.Refreshes)
	}
	if result.SnapshotToPaintMS <= 0 || result.SnapshotToPaintMS > 15_000 {
		t.Fatalf("snapshot-to-paint = %.2fms, want a completed paint within 15s", result.SnapshotToPaintMS)
	}
	if result.InteractionP95MS <= 0 || result.InteractionP95MS > 1_000 {
		t.Fatalf("interaction p95 = %.2fms, want completed interaction paints within 1s", result.InteractionP95MS)
	}
}
