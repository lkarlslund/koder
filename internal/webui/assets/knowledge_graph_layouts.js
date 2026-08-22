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
      this.listeners = new Set();
      this.generation = 0;
      this.handle = 0;
      this.running = false;
      this.destroyed = false;
    }

    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('Knowledge layout listener must be a function');
      this.listeners.add(listener);
      return () => this.listeners.delete(listener);
    }

    emit(phase, detail) {
      const event = Object.freeze({phase, generation: this.generation, ...(detail || {})});
      for (const listener of [...this.listeners]) listener(event);
      return event;
    }

    start(options) {
      if (this.destroyed) throw new Error('Knowledge layout is destroyed');
      options = options || {};
      this.stop('replaced');
      this.generation++;
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

    stop(reason) {
      if (!this.running && !this.handle) return false;
      if (this.handle) this.cancelSchedule(this.handle);
      this.handle = 0;
      const wasRunning = this.running;
      this.running = false;
      if (wasRunning) this.emit('stopped', {reason: String(reason || 'canceled')});
      return wasRunning;
    }

    destroy() {
      if (this.destroyed) return;
      this.stop('destroyed');
      this.destroyed = true;
      this.listeners.clear();
    }
  }

  return Object.freeze({ForceAtlasController, layoutNodeSize});
});
