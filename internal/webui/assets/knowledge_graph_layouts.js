(function (root, factory) {
  const api = factory(root && root.KoderKnowledgeLayouts);
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeGraphLayout = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function (defaultLayouts) {
  'use strict';

  function layoutNodeSize(attributes) {
    attributes = attributes || {};
    if (attributes.objectKind === 'chunk') return 1.9;
    if (attributes.semanticKind === 'warning' || attributes.semanticKind === 'decision') return 1.3;
    return 1;
  }

  class ForceAtlasController {
    constructor(options) {
      options = options || {};
      if (!options.graph) throw new TypeError('Knowledge layout requires a graph');
      this.graph = options.graph;
      this.layouts = options.layouts || defaultLayouts;
      if (!this.layouts || typeof this.layouts.forceAtlas2 !== 'function') throw new Error('ForceAtlas2 did not load');
      this.schedule = options.schedule || (callback => globalThis.requestAnimationFrame(callback));
      this.cancelSchedule = options.cancelSchedule || (handle => globalThis.cancelAnimationFrame(handle));
      this.workerFactory = options.workerFactory === null ? null : options.workerFactory ||
        (typeof globalThis.Worker === 'function' ? () => new globalThis.Worker('/assets/knowledge_layout_worker.js') : null);
      this.setTimer = options.setTimer || globalThis.setTimeout.bind(globalThis);
      this.clearTimer = options.clearTimer || globalThis.clearTimeout.bind(globalThis);
      this.debounceMS = Math.max(0, Number(options.debounceMS) || 120);
      this.now = options.now || (() => globalThis.performance.now());
      this.debug = !!options.debug;
      this.logger = options.logger || globalThis.console;
      this.metrics = {runs: 0, completed: 0, canceled: 0, errors: 0, lastDurationMS: 0, lastNodeCount: 0, lastEdgeCount: 0};
      this.listeners = new Set();
      this.generation = 0;
      this.handle = 0;
      this.running = false;
      this.destroyed = false;
    }

    request(options) {
      if (this.destroyed) throw new Error('Knowledge layout is destroyed');
      if (this.pendingTimer) this.clearTimer(this.pendingTimer);
      this.pendingOptions = {...(options || {})};
      this.pendingTimer = this.setTimer(() => {
        this.pendingTimer = 0;
        const pending = this.pendingOptions;
        this.pendingOptions = null;
        this.start(pending);
      }, Math.max(0, Number(options && options.debounceMS) || this.debounceMS));
      return this.pendingTimer;
    }

    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('Knowledge layout listener must be a function');
      this.listeners.add(listener);
      return () => this.listeners.delete(listener);
    }

    emit(phase, detail) {
      if (phase === 'ready') {
        this.metrics.completed++;
        this.metrics.lastDurationMS = Math.max(0, this.now() - this.startedAt);
      } else if (phase === 'error') {
        this.metrics.errors++;
        this.metrics.lastDurationMS = Math.max(0, this.now() - this.startedAt);
      }
      const event = Object.freeze({
        phase, generation: this.generation, ...(detail || {}),
        durationMS: phase === 'ready' || phase === 'error' ? this.metrics.lastDurationMS : undefined,
      });
      for (const listener of [...this.listeners]) listener(event);
      if (this.debug && this.logger && typeof this.logger.debug === 'function') {
        this.logger.debug('[Koder Knowledge layout]', {
          phase, generation: this.generation, completed: event.completed || 0, total: event.total || 0,
          stage: event.stage || '', duration_ms: event.durationMS || 0,
          nodes: this.graph.order, edges: this.graph.size,
        });
      }
      return event;
    }

    start(options) {
      if (this.destroyed) throw new Error('Knowledge layout is destroyed');
      options = options || {};
      this.stop('replaced');
      this.generation++;
      this.startedAt = this.now();
      this.metrics.runs++;
      this.metrics.lastNodeCount = this.graph.order;
      this.metrics.lastEdgeCount = this.graph.size;
      const token = this.generation;
      const total = Math.max(1, Math.min(1000, Number(options.iterations) || 120));
      const batch = Math.max(1, Math.min(50, Number(options.batchIterations) || 10));
      const coarse = Math.min(total, Math.max(batch, Number(options.coarseIterations) || 40));
      const settings = {
        barnesHutOptimize: this.graph.order > 80,
        gravity: 1.2, scalingRatio: this.graph.order > 150 ? 8 : 4, slowDown: 2,
        ...(options.settings || {}),
      };
      const overlapIterations = Math.max(1, Math.min(500, Number(options.overlapIterations) || 80));
      const overlapSettings = {gridSize: 20, margin: 0.35, expansion: 1.1, ratio: 1.12, speed: 3, ...(options.overlapSettings || {})};
      this.running = true;
      this.emit('start', {completed: 0, total, stage: 'coarse'});
      if (this.graph.order <= 1) {
        this.running = false;
        this.emit('ready', {completed: total, total, stage: 'settled'});
        return token;
      }
      if (this.workerFactory) {
        this.startWorker(token, {total, batch, coarse, settings, overlapIterations, overlapSettings});
        return token;
      }
      let completed = 0;
      const step = () => {
        this.handle = 0;
        if (!this.running || token !== this.generation || this.destroyed) return;
        const iterations = Math.min(batch, total - completed);
        try {
          const positions = this.layouts.forceAtlas2(this.graph, {iterations, settings});
          if (!this.running || token !== this.generation || this.destroyed) return;
          this.graph.updateEachNodeAttributes((key, attributes) => {
            const position = positions && positions[key];
            if (attributes.pinned || !position || !Number.isFinite(position.x) || !Number.isFinite(position.y)) return attributes;
            return {...attributes, x: position.x, y: position.y};
          }, {attributes: ['x', 'y']});
          completed += iterations;
          if (completed >= total) {
            if (typeof this.layouts.noverlap !== 'function') {
              this.running = false;
              this.emit('ready', {completed, total, stage: 'settled'});
              return;
            }
            this.emit('progress', {completed, total, stage: 'overlap'});
            this.handle = this.schedule(() => {
              this.handle = 0;
              if (!this.running || token !== this.generation || this.destroyed) return;
              try {
                const positions = this.layouts.noverlap(this.graph, {
                  maxIterations: overlapIterations, settings: overlapSettings,
                  inputReducer: (key, attributes) => ({...attributes, size: layoutNodeSize(attributes)}),
                });
                if (!this.running || token !== this.generation || this.destroyed) return;
                this.graph.updateEachNodeAttributes((key, attributes) => {
                  const position = positions && positions[key];
                  if (attributes.pinned || !position || !Number.isFinite(position.x) || !Number.isFinite(position.y)) return attributes;
                  return {...attributes, x: position.x, y: position.y};
                }, {attributes: ['x', 'y']});
                this.running = false;
                this.emit('ready', {completed, total, stage: 'settled'});
              } catch (error) {
                this.running = false;
                this.emit('error', {completed, total, stage: 'overlap', error});
              }
            });
            return;
          }
          this.emit('progress', {completed, total, stage: completed < coarse ? 'coarse' : 'settling'});
          this.handle = this.schedule(step);
        } catch (error) {
          this.running = false;
          this.emit('error', {completed, total, stage: 'failed', error});
        }
      };
      this.handle = this.schedule(step);
      return token;
    }

    startWorker(token, options) {
      const worker = this.workerFactory();
      if (!worker || typeof worker.postMessage !== 'function') throw new Error('Knowledge layout worker could not start');
      this.worker = worker;
      worker.onmessage = event => {
        const message = event && event.data || {};
        if (!this.running || token !== this.generation || Number(message.generation) !== token) return;
        if (message.type === 'progress') {
          this.emit('progress', {completed: message.completed, total: message.total, stage: message.stage});
          return;
        }
        if (message.type === 'error') {
          this.finishWorker();
          this.running = false;
          this.emit('error', {completed: 0, total: options.total, stage: 'worker', error: new Error(String(message.message || 'Knowledge layout failed'))});
          return;
        }
        if (message.type !== 'ready') return;
        this.applyPositions(message.positions);
        this.finishWorker();
        this.running = false;
        this.emit('ready', {completed: message.completed, total: message.total, stage: 'settled'});
      };
      worker.onerror = event => {
        if (!this.running || token !== this.generation) return;
        this.finishWorker();
        this.running = false;
        this.emit('error', {completed: 0, total: options.total, stage: 'worker', error: new Error(String(event && event.message || 'Knowledge layout worker failed'))});
      };
      worker.postMessage({
        type: 'layout', generation: token, ...options,
        nodes: this.graph.mapNodes((key, attributes) => ({
          key, attributes: {x: attributes.x, y: attributes.y, pinned: !!attributes.pinned, layoutSize: layoutNodeSize(attributes)},
        })),
        edges: this.graph.mapEdges((key, attributes, source, target) => ({key, source, target})),
      });
    }

    applyPositions(positions) {
      this.graph.updateEachNodeAttributes((key, attributes) => {
        const position = positions && positions[key];
        if (attributes.pinned || !position || !Number.isFinite(position.x) || !Number.isFinite(position.y)) return attributes;
        return {...attributes, x: position.x, y: position.y};
      }, {attributes: ['x', 'y']});
    }

    finishWorker() {
      if (!this.worker) return;
      this.worker.terminate();
      this.worker = null;
    }

    stop(reason) {
      const wasPending = !!this.pendingTimer;
      if (this.pendingTimer) this.clearTimer(this.pendingTimer);
      this.pendingTimer = 0;
      this.pendingOptions = null;
      if (!this.running && !this.handle && !this.worker) return wasPending;
      if (this.handle) this.cancelSchedule(this.handle);
      this.handle = 0;
      this.finishWorker();
      const wasRunning = this.running;
      this.running = false;
      if (wasRunning) {
        this.metrics.canceled++;
        this.emit('stopped', {reason: String(reason || 'canceled')});
      }
      return wasRunning || wasPending;
    }

    getMetrics() { return Object.freeze({...this.metrics}); }

    destroy() {
      if (this.destroyed) return;
      this.stop('destroyed');
      this.destroyed = true;
      this.listeners.clear();
    }
  }

  return Object.freeze({ForceAtlasController, layoutNodeSize});
});
