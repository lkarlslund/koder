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

  function pointerModifiers(value) {
    const source = value && value.event && (value.event.original || value.event) || value && value.original || value || {};
    return {additive: !!(source.ctrlKey || source.metaKey), shift: !!source.shiftKey};
  }

  function pointerLocation(value) {
    const event = value && value.event || value || {};
    const original = event.original || event;
    const read = (primary, fallback) => Number.isFinite(Number(primary)) ? Number(primary) : Number(fallback) || 0;
    return {x: read(event.x, original.offsetX), y: read(event.y, original.offsetY)};
  }

  function preventPointerDefault(value) {
    const event = value && value.event || value || {};
    if (typeof event.preventSigmaDefault === 'function') event.preventSigmaDefault();
    const original = event.original || event;
    if (typeof original.preventDefault === 'function') original.preventDefault();
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
      this.selectionBox = options.selectionBox || null;
      this.rendering = options.rendering;
      this.SigmaAPI = options.SigmaAPI || defaultSigmaAPI;
      this.requestFrame = options.requestAnimationFrame || globalThis.requestAnimationFrame.bind(globalThis);
      this.cancelFrame = options.cancelAnimationFrame || globalThis.cancelAnimationFrame.bind(globalThis);
      this.ResizeObserver = options.ResizeObserver || globalThis.ResizeObserver;
      this.now = options.now || (() => globalThis.performance.now());
      this.debug = !!options.debug;
      this.logger = options.logger || globalThis.console;
      this.metrics = {refreshes: 0, resizes: 0, lastRefreshMS: 0, maxRefreshMS: 0};
      this.listeners = new Set();
      this.frame = 0;
      this.destroyed = false;
      this.selection = null;
      this.selectionKeys = new Set();
      this.hover = null;

      provisionalPositions(this.store.graph);
      const Sigma = sigmaClass(this.SigmaAPI);
      const programs = this.SigmaAPI && this.SigmaAPI.rendering || {};
      const settings = {
        // Responsive tabs deliberately make this container 0x0 while the inspector is
        // visible. Sigma must remain attached and resize when the graph pane returns.
        allowInvalidContainer: true, renderLabels: true, renderEdgeLabels: false, enableEdgeEvents: true, zIndex: true,
        labelDensity: 0.12, labelGridCellSize: 90, labelRenderedSizeThreshold: 7,
        minCameraRatio: 0.04, maxCameraRatio: 25,
        nodeReducer: (key, attributes) => this.rendering.styledNodeAttributes(attributes, {
          selected: this.selectionKeys.has(`node:${key}`),
          hovered: !!this.hover && this.hover.kind === 'node' && this.hover.key === key,
          hidden: !!attributes.hidden,
        }),
        edgeReducer: (key, attributes) => this.rendering.styledEdgeAttributes(attributes, {
          selected: this.selectionKeys.has(`edge:${key}`),
          hovered: !!this.hover && this.hover.kind === 'edge' && this.hover.key === key,
          hidden: !!attributes.hidden || this.store.graph.getNodeAttribute(this.store.graph.source(key), 'hidden') || this.store.graph.getNodeAttribute(this.store.graph.target(key), 'hidden'),
        }),
      };
      if (programs.EdgeArrowProgram && programs.EdgeLineProgram) {
        settings.edgeProgramClasses = {arrow: programs.EdgeArrowProgram, line: programs.EdgeLineProgram};
        settings.defaultEdgeType = 'arrow';
      }
      this.sigma = new Sigma(this.store.graph, this.container, settings);
      this.interactionHandlers = {
        downNode: event => this.beginNodeDrag(event),
        doubleClickNode: event => this.toggleNodePin(event && event.node, event),
        clickNode: event => this.emit('node', {key: String(event && event.node || ''), ...pointerModifiers(event)}),
        clickEdge: event => this.emit('edge', {key: String(event && event.edge || ''), ...pointerModifiers(event)}),
        clickStage: event => {
          if (this.suppressStageClick) { this.suppressStageClick = false; return; }
          this.emit('background', pointerModifiers(event));
        },
        rightClickNode: event => {
          preventPointerDefault(event);
          this.emit('context', {kind: 'node', key: String(event && event.node || ''), ...pointerLocation(event)});
        },
        rightClickEdge: event => {
          preventPointerDefault(event);
          this.emit('context', {kind: 'edge', key: String(event && event.edge || ''), ...pointerLocation(event)});
        },
        enterNode: event => this.setHover('node', event && event.node),
        leaveNode: event => this.clearHover('node', event && event.node),
        enterEdge: event => this.setHover('edge', event && event.edge),
        leaveEdge: event => this.clearHover('edge', event && event.edge),
      };
      if (typeof this.sigma.on === 'function') {
        for (const [name, handler] of Object.entries(this.interactionHandlers)) this.sigma.on(name, handler);
      }
      this.mouseCaptor = typeof this.sigma.getMouseCaptor === 'function' ? this.sigma.getMouseCaptor() : null;
      this.mouseHandlers = {
        mousemovebody: event => this.moveNodeDrag(event),
        mouseup: event => this.endNodeDrag(event),
      };
      if (this.mouseCaptor && typeof this.mouseCaptor.on === 'function') {
        for (const [name, handler] of Object.entries(this.mouseHandlers)) this.mouseCaptor.on(name, handler);
      }
      this.pointerHandlers = {
        pointerdown: event => this.beginBoxSelection(event),
        pointermove: event => this.moveBoxSelection(event),
        pointerup: event => this.endBoxSelection(event),
        pointercancel: event => this.cancelBoxSelection(event),
      };
      if (this.selectionBox && typeof this.container.addEventListener === 'function') {
        for (const [name, handler] of Object.entries(this.pointerHandlers)) this.container.addEventListener(name, handler, true);
      }
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

    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('Knowledge renderer listener must be a function');
      this.listeners.add(listener);
      return () => this.listeners.delete(listener);
    }

    emit(type, detail) {
      const event = Object.freeze({type, detail, renderer: this});
      for (const listener of [...this.listeners]) listener(event);
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
        const started = this.now();
        if (this.needsResize && typeof this.sigma.resize === 'function') {
          this.sigma.resize();
          this.metrics.resizes++;
        }
        this.needsResize = false;
        if (typeof this.sigma.refresh === 'function') this.sigma.refresh();
        const duration = Math.max(0, this.now() - started);
        this.metrics.refreshes++;
        this.metrics.lastRefreshMS = duration;
        this.metrics.maxRefreshMS = Math.max(this.metrics.maxRefreshMS, duration);
        if (this.debug && this.logger && typeof this.logger.debug === 'function') {
          this.logger.debug('[Koder Knowledge render]', {
            duration_ms: duration, nodes: this.store.graph.order, edges: this.store.graph.size,
            resized: this.metrics.resizes,
          });
        }
      });
    }

    setSelection(kind, key) {
      this.setSelections(kind && key ? [{kind: String(kind), key: String(key)}] : []);
    }

    setSelections(items) {
      this.selectionKeys = new Set((items || []).map(item => `${String(item.kind)}:${String(item.key)}`));
      this.selection = (items || []).at(-1) || null;
      this.scheduleRefresh(false);
    }

    beginBoxSelection(event) {
      if (!event || !event.shiftKey || event.button !== 0) return;
      const bounds = this.container.getBoundingClientRect();
      this.boxDrag = {
        pointerID: event.pointerId, startX: event.clientX - bounds.left, startY: event.clientY - bounds.top,
        x: event.clientX - bounds.left, y: event.clientY - bounds.top,
        additive: !!(event.ctrlKey || event.metaKey),
      };
      if (this.container.setPointerCapture) this.container.setPointerCapture(event.pointerId);
      this.updateSelectionBox();
      event.preventDefault();
      event.stopPropagation();
    }

    moveBoxSelection(event) {
      if (!this.boxDrag || event.pointerId !== this.boxDrag.pointerID) return;
      const bounds = this.container.getBoundingClientRect();
      this.boxDrag.x = event.clientX - bounds.left;
      this.boxDrag.y = event.clientY - bounds.top;
      this.updateSelectionBox();
      event.preventDefault();
      event.stopPropagation();
    }

    updateSelectionBox() {
      if (!this.boxDrag || !this.selectionBox) return;
      const left = Math.min(this.boxDrag.startX, this.boxDrag.x);
      const top = Math.min(this.boxDrag.startY, this.boxDrag.y);
      this.selectionBox.style.left = `${left}px`;
      this.selectionBox.style.top = `${top}px`;
      this.selectionBox.style.width = `${Math.abs(this.boxDrag.x - this.boxDrag.startX)}px`;
      this.selectionBox.style.height = `${Math.abs(this.boxDrag.y - this.boxDrag.startY)}px`;
      this.selectionBox.hidden = false;
    }

    endBoxSelection(event) {
      if (!this.boxDrag || event.pointerId !== this.boxDrag.pointerID) return;
      const drag = this.boxDrag;
      this.boxDrag = null;
      if (this.container.releasePointerCapture) this.container.releasePointerCapture(event.pointerId);
      if (this.selectionBox) this.selectionBox.hidden = true;
      const left = Math.min(drag.startX, drag.x);
      const right = Math.max(drag.startX, drag.x);
      const top = Math.min(drag.startY, drag.y);
      const bottom = Math.max(drag.startY, drag.y);
      if (right - left >= 4 && bottom - top >= 4) {
        const keys = this.store.graph.filterNodes(key => {
          const data = this.sigma.getNodeDisplayData && this.sigma.getNodeDisplayData(key);
          const point = data && this.sigma.graphToViewport ? this.sigma.graphToViewport(data) : null;
          return !!point && point.x >= left && point.x <= right && point.y >= top && point.y <= bottom;
        });
        this.suppressStageClick = true;
        this.emit('boxselect', {keys, additive: drag.additive});
      }
      event.preventDefault();
      event.stopPropagation();
    }

    cancelBoxSelection(event) {
      if (!this.boxDrag || event && event.pointerId !== this.boxDrag.pointerID) return;
      this.boxDrag = null;
      if (this.selectionBox) this.selectionBox.hidden = true;
    }

    setHover(kind, key) {
      key = String(key || '');
      if (!key) return;
      this.hover = {kind, key};
      this.emit('hover', this.hover);
      this.scheduleRefresh(false);
    }

    beginNodeDrag(event) {
      const key = String(event && event.node || '');
      if (!key || !this.store.graph.hasNode(key)) return;
      const pointer = event && event.event || {};
      this.nodeDrag = {key, startX: Number(pointer.x) || 0, startY: Number(pointer.y) || 0, moved: false};
      const camera = this.sigma.getCamera && this.sigma.getCamera();
      if (camera && typeof camera.disable === 'function') camera.disable();
      this.emit('dragstart', {key});
    }

    moveNodeDrag(event) {
      if (!this.nodeDrag || !event || !this.sigma.viewportToGraph) return;
      const point = this.sigma.viewportToGraph(event);
      if (!point || !Number.isFinite(point.x) || !Number.isFinite(point.y)) return;
      const distance = Math.hypot((Number(event.x) || 0) - this.nodeDrag.startX, (Number(event.y) || 0) - this.nodeDrag.startY);
      if (distance >= 2) this.nodeDrag.moved = true;
      if (!this.nodeDrag.moved) return;
      this.store.graph.mergeNodeAttributes(this.nodeDrag.key, {x: point.x, y: point.y, pinned: true});
      if (typeof event.preventSigmaDefault === 'function') event.preventSigmaDefault();
      if (event.original && typeof event.original.preventDefault === 'function') event.original.preventDefault();
      this.scheduleRefresh(false);
      this.emit('drag', {key: this.nodeDrag.key, x: point.x, y: point.y});
    }

    endNodeDrag() {
      if (!this.nodeDrag) return;
      const drag = this.nodeDrag;
      this.nodeDrag = null;
      const camera = this.sigma.getCamera && this.sigma.getCamera();
      if (camera && typeof camera.enable === 'function') camera.enable();
      if (!drag.moved) return;
      const attributes = this.store.graph.getNodeAttributes(drag.key);
      this.emit('pin', {key: drag.key, pinned: true, x: attributes.x, y: attributes.y});
    }

    toggleNodePin(key, event) {
      key = String(key || '');
      if (!key || !this.store.graph.hasNode(key)) return false;
      const pinned = !this.store.graph.getNodeAttribute(key, 'pinned');
      this.store.graph.setNodeAttribute(key, 'pinned', pinned);
      if (event && event.event && typeof event.event.preventSigmaDefault === 'function') event.event.preventSigmaDefault();
      this.scheduleRefresh(false);
      const attributes = this.store.graph.getNodeAttributes(key);
      this.emit('pin', {key, pinned, x: attributes.x, y: attributes.y});
      return pinned;
    }

    clearHover(kind, key) {
      if (!this.hover || this.hover.kind !== kind || this.hover.key !== String(key || '')) return;
      this.hover = null;
      this.emit('hover', null);
      this.scheduleRefresh(false);
    }

    setState(state) {
      state = String(state || 'empty');
      if (this.stage && this.stage.dataset) this.stage.dataset.graphState = state;
      if (this.stage && typeof this.stage.setAttribute === 'function') this.stage.setAttribute('aria-busy', state === 'loading' ? 'true' : 'false');
    }

    getMetrics() { return Object.freeze({...this.metrics, nodes: this.store.graph.order, edges: this.store.graph.size}); }

    destroy() {
      if (this.destroyed) return;
      this.destroyed = true;
      if (this.frame) this.cancelFrame(this.frame);
      this.frame = 0;
      if (this.unsubscribe) this.unsubscribe();
      if (this.sigma && typeof this.sigma.off === 'function') {
        for (const [name, handler] of Object.entries(this.interactionHandlers)) this.sigma.off(name, handler);
      }
      if (this.mouseCaptor && typeof this.mouseCaptor.off === 'function') {
        for (const [name, handler] of Object.entries(this.mouseHandlers)) this.mouseCaptor.off(name, handler);
      }
      this.endNodeDrag();
      if (this.selectionBox && typeof this.container.removeEventListener === 'function') {
        for (const [name, handler] of Object.entries(this.pointerHandlers)) this.container.removeEventListener(name, handler, true);
      }
      this.cancelBoxSelection();
      this.listeners.clear();
      this.selectionKeys.clear();
      this.hover = null;
      if (this.resizeObserver) this.resizeObserver.disconnect();
      if (this.sigma && typeof this.sigma.kill === 'function') this.sigma.kill();
      if (this.legend) {
        this.legend.replaceChildren();
        this.legend.hidden = true;
      }
    }
  }

  return Object.freeze({Renderer, provisionalPositions, sigmaClass, pointerModifiers, pointerLocation});
});
