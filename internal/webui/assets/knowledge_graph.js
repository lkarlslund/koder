(function (root, factory) {
  const graphology = typeof module === 'object' && module.exports
    ? require('./vendor/graphology/graphology.umd.min.js')
    : root && root.graphology;
  const api = factory(graphology);
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeGraph = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function (graphology) {
  'use strict';

  const PATCH_VERSION = 'knowledge.graph.patch.v1';
  const localNodeAttributes = Object.freeze(['x', 'y', 'size', 'hidden', 'highlighted', 'pinned']);

  function graphConstructor() {
    if (!graphology || typeof graphology.MultiDirectedGraph !== 'function') {
      throw new Error('Graphology did not load');
    }
    return graphology.MultiDirectedGraph;
  }

  function validatePatchShape(patch) {
    if (!patch || patch.patchVersion !== PATCH_VERSION) throw new TypeError('Knowledge graph patch version is invalid');
    for (const field of ['upsertNodes', 'removeNodeKeys', 'upsertEdges', 'removeEdgeKeys']) {
      if (!Array.isArray(patch[field])) throw new TypeError(`Knowledge graph patch ${field} must be an array`);
    }
    return patch;
  }

  function uniquePatchKeys(values, field) {
    const seen = new Set();
    for (const value of values) {
      const key = String(typeof value === 'string' ? value : value && value.key || '');
      if (!key || seen.has(key)) throw new TypeError(`Knowledge graph patch ${field} has an invalid or duplicate key`);
      seen.add(key);
    }
  }

  function cloneAttributes(value) {
    const result = {...(value || {})};
    if (Array.isArray(result.risk)) result.risk = [...result.risk];
    return result;
  }

  class Store {
    constructor(options) {
      options = options || {};
      const Graph = options.Graph || graphConstructor();
      this.graph = options.graph || new Graph({allowSelfLoops: true});
      this.listeners = new Set();
      this.destroyed = false;
      this.generation = 0;
      this.checkpoint = null;
    }

    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('Knowledge graph listener must be a function');
      this.assertActive();
      this.listeners.add(listener);
      return () => this.listeners.delete(listener);
    }

    emit(detail) {
      const event = Object.freeze({type: 'change', detail, store: this});
      for (const listener of [...this.listeners]) listener(event);
    }

    replace(patch) {
      return this.commit(patch, true);
    }

    apply(patch) {
      return this.commit(patch, false);
    }

    commit(patch, replace) {
      this.assertActive();
      validatePatchShape(patch);
      uniquePatchKeys(patch.upsertNodes, 'upsertNodes');
      uniquePatchKeys(patch.removeNodeKeys, 'removeNodeKeys');
      uniquePatchKeys(patch.upsertEdges, 'upsertEdges');
      uniquePatchKeys(patch.removeEdgeKeys, 'removeEdgeKeys');
      this.preflight(patch, replace);

      const before = this.counts();
      if (replace) this.graph.clear();
      for (const key of patch.removeEdgeKeys) {
        if (this.graph.hasEdge(key)) this.graph.dropEdge(key);
      }
      for (const key of patch.removeNodeKeys) {
        if (this.graph.hasNode(key)) this.graph.dropNode(key);
      }
      for (const node of patch.upsertNodes) this.upsertNode(node);
      for (const edge of patch.upsertEdges) this.upsertEdge(edge);

      this.generation = Number(patch.generation);
      this.checkpoint = Object.freeze({...patch.checkpoint});
      const after = this.counts();
      const detail = Object.freeze({
        mode: replace ? 'replace' : 'apply', patch, before, after,
        generation: this.generation, checkpoint: this.checkpoint,
      });
      this.emit(detail);
      return detail;
    }

    preflight(patch, replace) {
      const nodes = new Set(replace ? [] : this.graph.nodes());
      for (const key of patch.removeNodeKeys) nodes.delete(String(key));
      for (const node of patch.upsertNodes) nodes.add(String(node.key));
      for (const edge of patch.upsertEdges) {
        if (!edge || !edge.attributes || edge.attributes.directed !== true) throw new TypeError('Knowledge graph edge must be directed');
        if (!nodes.has(String(edge.source)) || !nodes.has(String(edge.target))) {
          throw new TypeError(`Knowledge graph edge ${String(edge.key || '')} has a missing endpoint`);
        }
      }
    }

    upsertNode(node) {
      const key = String(node.key);
      const attributes = cloneAttributes(node.attributes);
      if (!this.graph.hasNode(key)) {
        this.graph.addNode(key, attributes);
        return;
      }
      const current = this.graph.getNodeAttributes(key);
      for (const name of localNodeAttributes) {
        if (current[name] !== undefined && attributes[name] === undefined) attributes[name] = current[name];
      }
      this.graph.replaceNodeAttributes(key, attributes);
    }

    upsertEdge(edge) {
      const key = String(edge.key);
      const source = String(edge.source);
      const target = String(edge.target);
      const attributes = cloneAttributes(edge.attributes);
      if (this.graph.hasEdge(key)) {
        if (this.graph.source(key) !== source || this.graph.target(key) !== target) {
          this.graph.dropEdge(key);
          this.graph.addDirectedEdgeWithKey(key, source, target, attributes);
        } else {
          this.graph.replaceEdgeAttributes(key, attributes);
        }
        return;
      }
      this.graph.addDirectedEdgeWithKey(key, source, target, attributes);
    }

    hasNode(key) { return !this.destroyed && this.graph.hasNode(String(key)); }
    hasEdge(key) { return !this.destroyed && this.graph.hasEdge(String(key)); }

    node(key) {
      key = String(key);
      if (!this.hasNode(key)) return null;
      return {key, attributes: cloneAttributes(this.graph.getNodeAttributes(key))};
    }

    edge(key) {
      key = String(key);
      if (!this.hasEdge(key)) return null;
      return {
        key, source: this.graph.source(key), target: this.graph.target(key),
        attributes: cloneAttributes(this.graph.getEdgeAttributes(key)),
      };
    }

    counts() {
      return this.destroyed ? {nodes: 0, edges: 0} : {nodes: this.graph.order, edges: this.graph.size};
    }

    assertActive() {
      if (this.destroyed) throw new Error('Knowledge graph store is destroyed');
    }

    destroy() {
      if (this.destroyed) return;
      this.graph.clear();
      this.listeners.clear();
      this.generation = 0;
      this.checkpoint = null;
      this.destroyed = true;
    }
  }

  return Object.freeze({PATCH_VERSION, Store});
});
