(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeGraphInteractions = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  class LocalViewHistory {
    constructor(options) {
      options = options || {};
      if (!options.graph) throw new TypeError('Knowledge local view requires a graph');
      this.graph = options.graph;
      this.limit = Math.max(1, Math.min(200, Number(options.limit) || 50));
      this.history = [];
      this.listeners = new Set();
      this.destroyed = false;
    }

    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('Knowledge local-view listener must be a function');
      this.listeners.add(listener);
      return () => this.listeners.delete(listener);
    }

    snapshot() {
      return {
        hiddenNodes: this.graph.filterNodes((key, attributes) => !!attributes.hidden),
        hiddenEdges: this.graph.filterEdges((key, attributes) => !!attributes.hidden),
      };
    }

    state() {
      const snapshot = this.snapshot();
      return Object.freeze({
        hiddenNodes: snapshot.hiddenNodes.length, hiddenEdges: snapshot.hiddenEdges.length,
        hidden: snapshot.hiddenNodes.length + snapshot.hiddenEdges.length, canUndo: this.history.length > 0,
      });
    }

    remember() {
      this.history.push(this.snapshot());
      if (this.history.length > this.limit) this.history.shift();
    }

    emit(action) {
      const event = Object.freeze({action, state: this.state(), view: this});
      for (const listener of [...this.listeners]) listener(event);
      return event;
    }

    hide(items) {
      this.assertActive();
      const selected = (items || []).filter(item => item && ['node', 'edge'].includes(item.kind));
      if (!selected.length) return false;
      this.remember();
      for (const item of selected) {
        if (item.kind === 'node' && this.graph.hasNode(item.key)) this.graph.setNodeAttribute(item.key, 'hidden', true);
        if (item.kind === 'edge' && this.graph.hasEdge(item.key)) this.graph.setEdgeAttribute(item.key, 'hidden', true);
      }
      this.emit('hide');
      return true;
    }

    isolate(items) {
      this.assertActive();
      const selectedNodes = new Set();
      const selectedEdges = new Set();
      for (const item of items || []) {
        if (item.kind === 'node' && this.graph.hasNode(item.key)) selectedNodes.add(item.key);
        if (item.kind === 'edge' && this.graph.hasEdge(item.key)) {
          selectedEdges.add(item.key);
          selectedNodes.add(this.graph.source(item.key));
          selectedNodes.add(this.graph.target(item.key));
        }
      }
      if (!selectedNodes.size && !selectedEdges.size) return false;
      this.remember();
      this.graph.updateEachNodeAttributes((key, attributes) => ({...attributes, hidden: !selectedNodes.has(key)}), {attributes: ['hidden']});
      this.graph.updateEachEdgeAttributes((key, attributes, source, target) => ({
        ...attributes,
        hidden: selectedEdges.size ? !selectedEdges.has(key) : !selectedNodes.has(source) || !selectedNodes.has(target),
      }), {attributes: ['hidden']});
      this.emit('isolate');
      return true;
    }

    reveal() {
      this.assertActive();
      if (!this.state().hidden) return false;
      this.remember();
      this.apply({hiddenNodes: [], hiddenEdges: []});
      this.emit('reveal');
      return true;
    }

    undo() {
      this.assertActive();
      const prior = this.history.pop();
      if (!prior) return false;
      this.apply(prior);
      this.emit('undo');
      return true;
    }

    reset() {
      this.assertActive();
      const hadHiddenItems = this.state().hidden > 0;
      this.history.length = 0;
      // A freshly replaced Graphology graph has no local visibility state. Avoid
      // emitting attribute-update events in that case: Sigma may still be assigning
      // render-program slots to the newly added nodes and cannot partially repaint yet.
      if (hadHiddenItems) this.apply({hiddenNodes: [], hiddenEdges: []});
      this.emit('reset');
    }

    apply(snapshot) {
      const hiddenNodes = new Set(snapshot.hiddenNodes || []);
      const hiddenEdges = new Set(snapshot.hiddenEdges || []);
      this.graph.updateEachNodeAttributes((key, attributes) => ({...attributes, hidden: hiddenNodes.has(key)}), {attributes: ['hidden']});
      this.graph.updateEachEdgeAttributes((key, attributes) => ({...attributes, hidden: hiddenEdges.has(key)}), {attributes: ['hidden']});
    }

    assertActive() { if (this.destroyed) throw new Error('Knowledge local view is destroyed'); }
    destroy() { this.destroyed = true; this.history.length = 0; this.listeners.clear(); }
  }

  return Object.freeze({LocalViewHistory});
});
