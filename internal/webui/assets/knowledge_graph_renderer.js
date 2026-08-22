(function (root, factory) {
  const api = factory(root && root.Sigma);
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeGraphRenderer = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function (defaultSigmaAPI) {
  'use strict';

  function provisionalPositions(graph) {
    const missing = graph.nodes().filter(key => {
      const attributes = graph.getNodeAttributes(key);
      return !Number.isFinite(attributes.x) || !Number.isFinite(attributes.y);
    }).sort();
    const goldenAngle = Math.PI * (3 - Math.sqrt(5));
    missing.forEach((key, index) => {
      const radius = 1.2 * Math.sqrt(index + 1);
      graph.mergeNodeAttributes(key, {x: Math.cos(index * goldenAngle) * radius, y: Math.sin(index * goldenAngle) * radius});
    });
    return missing.length;
  }

  function sigmaClass(api) {
    const value = api && (api.Sigma || api);
    if (typeof value !== 'function') throw new Error('Sigma did not load');
    return value;
  }

  class Renderer {
    constructor(options) {
      options = options || {};
      if (!options.store || !options.store.graph) throw new TypeError('Knowledge graph renderer requires a graph store');
      if (!options.container) throw new TypeError('Knowledge graph renderer requires a container');
      if (!options.rendering || typeof options.rendering.styledNodeAttributes !== 'function') {
        throw new TypeError('Knowledge graph renderer requires rendering rules');
      }
      this.store = options.store;
      this.container = options.container;
      this.stage = options.stage || options.container.parentElement || options.container;
      this.legend = options.legend || null;
      this.rendering = options.rendering;
      this.SigmaAPI = options.SigmaAPI || defaultSigmaAPI;
      this.requestFrame = options.requestAnimationFrame || globalThis.requestAnimationFrame.bind(globalThis);
      this.cancelFrame = options.cancelAnimationFrame || globalThis.cancelAnimationFrame.bind(globalThis);
      this.ResizeObserver = options.ResizeObserver || globalThis.ResizeObserver;
      this.frame = 0;
      this.destroyed = false;
      this.selection = null;

      provisionalPositions(this.store.graph);
      const Sigma = sigmaClass(this.SigmaAPI);
      const programs = this.SigmaAPI && this.SigmaAPI.rendering || {};
      const settings = {
        // Responsive tabs deliberately make this container 0x0 while the inspector is
        // visible. Sigma must remain attached and resize when the graph pane returns.
        allowInvalidContainer: true, renderLabels: true, renderEdgeLabels: false, zIndex: true,
        labelDensity: 0.12, labelGridCellSize: 90, labelRenderedSizeThreshold: 7,
        minCameraRatio: 0.04, maxCameraRatio: 25,
        nodeReducer: (key, attributes) => this.rendering.styledNodeAttributes(attributes, {
          selected: !!this.selection && this.selection.kind === 'node' && this.selection.key === key,
        }),
        edgeReducer: (key, attributes) => this.rendering.styledEdgeAttributes(attributes, {
          selected: !!this.selection && this.selection.kind === 'edge' && this.selection.key === key,
        }),
      };
      if (programs.EdgeArrowProgram && programs.EdgeLineProgram) {
        settings.edgeProgramClasses = {arrow: programs.EdgeArrowProgram, line: programs.EdgeLineProgram};
        settings.defaultEdgeType = 'arrow';
      }
      this.sigma = new Sigma(this.store.graph, this.container, settings);
      this.unsubscribe = this.store.subscribe(event => this.onStoreEvent(event));
      if (typeof this.ResizeObserver === 'function') {
        this.resizeObserver = new this.ResizeObserver(() => this.scheduleRefresh(true));
        this.resizeObserver.observe(this.container);
      }
      this.updateLegend();
      this.scheduleRefresh(true);
    }

    onStoreEvent(event) {
      if (this.destroyed) return;
      if (event.type === 'change') {
        provisionalPositions(this.store.graph);
        this.updateLegend();
        this.scheduleRefresh(false);
      }
      if (event.type === 'refetch') this.setState('stale');
    }

    updateLegend() {
      if (!this.legend) return;
      const edges = this.store.graph.mapEdges((key, attributes) => ({key, attributes}));
      this.rendering.renderLegend(this.legend, this.rendering.legendForEdges(edges));
    }

    scheduleRefresh(resize) {
      if (this.destroyed) return;
      this.needsResize = this.needsResize || !!resize;
      if (this.frame) return;
      this.frame = this.requestFrame(() => {
        this.frame = 0;
        if (this.destroyed) return;
        if (this.needsResize && typeof this.sigma.resize === 'function') this.sigma.resize();
        this.needsResize = false;
        if (typeof this.sigma.refresh === 'function') this.sigma.refresh();
      });
    }

    setSelection(kind, key) {
      this.selection = kind && key ? {kind: String(kind), key: String(key)} : null;
      this.scheduleRefresh(false);
    }

    setState(state) {
      state = String(state || 'empty');
      if (this.stage && this.stage.dataset) this.stage.dataset.graphState = state;
      if (this.stage && typeof this.stage.setAttribute === 'function') this.stage.setAttribute('aria-busy', state === 'loading' ? 'true' : 'false');
    }

    destroy() {
      if (this.destroyed) return;
      this.destroyed = true;
      if (this.frame) this.cancelFrame(this.frame);
      this.frame = 0;
      if (this.unsubscribe) this.unsubscribe();
      if (this.resizeObserver) this.resizeObserver.disconnect();
      if (this.sigma && typeof this.sigma.kill === 'function') this.sigma.kill();
      if (this.legend) {
        this.legend.replaceChildren();
        this.legend.hidden = true;
      }
    }
  }

  return Object.freeze({Renderer, provisionalPositions, sigmaClass});
});
