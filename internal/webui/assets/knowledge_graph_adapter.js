(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeGraphAdapter = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const PATCH_VERSION = 'knowledge.graph.patch.v1';
  const objectKinds = new Set(['chunk', 'entry', 'evidence']);

  function requiredText(value, field) {
    value = String(value || '').trim();
    if (!value) throw new TypeError(`Knowledge graph ${field} is required`);
    return value;
  }

  function objectKey(value) {
    if (!value || typeof value !== 'object') throw new TypeError('Knowledge graph object reference is required');
    const kind = requiredText(value.kind, 'object kind').toLowerCase();
    if (!objectKinds.has(kind)) throw new TypeError('Knowledge graph object kind is invalid');
    return `${kind}:${requiredText(value.id, 'object ID')}`;
  }

  function revisionAttributes(value) {
    value = value && typeof value === 'object' ? value : {};
    const number = Number(value.number);
    if (!Number.isSafeInteger(number) || number < 1) throw new TypeError('Knowledge graph revision is invalid');
    return {revision: number, revisionID: requiredText(value.id, 'revision ID')};
  }

  function nodeFromAPI(node) {
    if (!node || typeof node !== 'object') throw new TypeError('Knowledge graph node is invalid');
    const key = objectKey(node.object);
    if (requiredText(node.id, 'node ID') !== key) throw new TypeError('Knowledge graph node ID does not match its object');
    const scope = node.scope && typeof node.scope === 'object' ? node.scope : {};
    return {
      key,
      attributes: {
        objectKind: String(node.object.kind), objectID: String(node.object.id),
        semanticKind: requiredText(node.semantic_kind, 'semantic kind'), title: requiredText(node.title, 'node title'),
        summary: String(node.summary || ''), scopeKind: requiredText(scope.kind, 'scope kind'),
        scopeSelector: String(scope.selector || ''), state: requiredText(node.state, 'node state'),
        verification: String(node.verification || ''), risk: Array.isArray(node.risk) ? node.risk.map(String) : [],
        ...revisionAttributes(node.revision),
      },
    };
  }

  function edgeFromAPI(edge) {
    if (!edge || typeof edge !== 'object') throw new TypeError('Knowledge graph edge is invalid');
    return {
      key: requiredText(edge.id, 'edge ID'), source: objectKey(edge.source), target: objectKey(edge.target),
      attributes: {
        relationshipKind: requiredText(edge.kind, 'relationship kind'), label: String(edge.label || ''),
        state: requiredText(edge.state, 'edge state'), ...revisionAttributes(edge.revision), directed: true,
      },
    };
  }

  function checkpointFromAPI(value) {
    value = value && typeof value === 'object' ? value : {};
    const sequence = Number(value.sequence);
    if (!Number.isSafeInteger(sequence) || sequence < 0) throw new TypeError('Knowledge graph checkpoint sequence is invalid');
    return {streamID: requiredText(value.stream_id, 'checkpoint stream'), sequence};
  }

  function localViewPatch(patch, localView) {
    localView = localView && typeof localView === 'object' ? localView : {};
    const hiddenNodes = new Set(Array.isArray(localView.hiddenNodes) ? localView.hiddenNodes.map(String) : []);
    const hiddenEdges = new Set(Array.isArray(localView.hiddenEdges) ? localView.hiddenEdges.map(String) : []);
    const pinnedNodes = new Map();
    for (const item of Array.isArray(localView.pinnedNodes) ? localView.pinnedNodes : []) {
      const key = String(item && item.key || '');
      const x = Number(item && item.x);
      const y = Number(item && item.y);
      if (key && Number.isFinite(x) && Number.isFinite(y)) pinnedNodes.set(key, {x, y});
    }
    return {
      ...patch,
      upsertNodes: patch.upsertNodes.map(node => {
        const pin = pinnedNodes.get(node.key);
        return {
          ...node,
          attributes: {
            ...node.attributes,
            ...(hiddenNodes.has(node.key) ? {hidden: true} : {}),
            ...(pin ? {pinned: true, x: pin.x, y: pin.y} : {}),
          },
        };
      }),
      upsertEdges: patch.upsertEdges.map(edge => ({
        ...edge, attributes: {...edge.attributes, ...(hiddenEdges.has(edge.key) ? {hidden: true} : {})},
      })),
    };
  }

  function snapshotToPatch(snapshot, localView) {
    if (!snapshot || typeof snapshot !== 'object') throw new TypeError('Knowledge graph snapshot is invalid');
    const generation = Number(snapshot.generation);
    if (!Number.isSafeInteger(generation) || generation < 1) throw new TypeError('Knowledge graph generation is invalid');
    const patch = {
      patchVersion: PATCH_VERSION, generation, checkpoint: checkpointFromAPI(snapshot.checkpoint),
      upsertNodes: (snapshot.nodes || []).map(nodeFromAPI), removeNodeKeys: [],
      upsertEdges: (snapshot.edges || []).map(edgeFromAPI), removeEdgeKeys: [],
    };
    return validatePatch(localViewPatch(patch, localView));
  }

  function uniqueKeys(values, field) {
    const seen = new Set();
    for (const value of values) {
      const key = requiredText(typeof value === 'string' ? value : value && value.key, field);
      if (seen.has(key)) throw new TypeError(`Knowledge graph ${field} contains a duplicate`);
      seen.add(key);
    }
  }

  function validatePatch(patch) {
    if (!patch || typeof patch !== 'object' || patch.patchVersion !== PATCH_VERSION) throw new TypeError('Knowledge graph patch version is invalid');
    const generation = Number(patch.generation);
    if (!Number.isSafeInteger(generation) || generation < 1) throw new TypeError('Knowledge graph patch generation is invalid');
    if (!patch.checkpoint || !requiredText(patch.checkpoint.streamID, 'checkpoint stream') ||
        !Number.isSafeInteger(Number(patch.checkpoint.sequence)) || Number(patch.checkpoint.sequence) < 0) {
      throw new TypeError('Knowledge graph patch checkpoint is invalid');
    }
    for (const field of ['upsertNodes', 'removeNodeKeys', 'upsertEdges', 'removeEdgeKeys']) {
      if (!Array.isArray(patch[field])) throw new TypeError(`Knowledge graph patch ${field} must be an array`);
      uniqueKeys(patch[field], field);
    }
    for (const node of patch.upsertNodes) {
      if (!node.attributes || !node.attributes.objectKind || !node.attributes.objectID) throw new TypeError('Knowledge graph node attributes are invalid');
    }
    for (const edge of patch.upsertEdges) {
      requiredText(edge.source, 'edge source');
      requiredText(edge.target, 'edge target');
      if (!edge.attributes || edge.attributes.directed !== true) throw new TypeError('Knowledge graph edge attributes are invalid');
    }
    return patch;
  }

  class Adapter {
    constructor(target) {
      if (!target || typeof target.replace !== 'function' || typeof target.apply !== 'function') {
        throw new TypeError('Knowledge graph target must implement replace and apply');
      }
      this.target = target;
      this.listeners = new Set();
      this.selections = new Map();
      this.selection = null;
      this.destroyed = false;
    }

    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('Knowledge graph listener must be a function');
      if (this.destroyed) return () => {};
      this.listeners.add(listener);
      return () => this.listeners.delete(listener);
    }

    emit(type, detail) {
      const event = Object.freeze({type, detail, adapter: this});
      for (const listener of [...this.listeners]) listener(event);
      return event;
    }

    replaceSnapshot(snapshot, localView) {
      this.assertActive();
      const patch = snapshotToPatch(snapshot, localView);
      this.emit('beforechange', {mode: 'replace', patch});
      const result = this.target.replace(patch);
      if (result && result.action && result.action !== 'applied') {
        this.emit(result.action, result);
        return patch;
      }
      this.selections.clear();
      this.selection = null;
      this.emit('change', {mode: 'replace', patch, counts: this.counts()});
      return patch;
    }

    mergeSnapshot(snapshot, localView) {
      this.assertActive();
      if (typeof this.target.merge !== 'function') throw new TypeError('Knowledge graph target does not support snapshot merging');
      const patch = snapshotToPatch(snapshot, localView);
      this.emit('beforechange', {mode: 'merge', patch});
      const result = this.target.merge(patch);
      if (result && result.action && result.action !== 'applied') {
        this.emit(result.action, result);
        return Object.freeze({patch, result});
      }
      this.emit('change', {mode: 'merge', patch, counts: this.counts()});
      return Object.freeze({patch, result});
    }

    applyPatch(patch) {
      this.assertActive();
      validatePatch(patch);
      this.emit('beforechange', {mode: 'apply', patch});
      const result = this.target.apply(patch);
      if (result && result.action && result.action !== 'applied') {
        this.emit(result.action, result);
        return patch;
      }
      let selectionChanged = false;
      for (const [token, item] of this.selections) {
        if (this.has(item.kind, item.key)) continue;
        this.selections.delete(token);
        selectionChanged = true;
      }
      if (this.selection && !this.has(this.selection.kind, this.selection.key)) {
        this.selection = [...this.selections.values()].at(-1) || null;
        selectionChanged = true;
      }
      if (selectionChanged) this.emit('selection', this.selectionSnapshot());
      this.emit('change', {mode: 'apply', patch, counts: this.counts()});
      return patch;
    }

    has(kind, key) {
      const method = kind === 'edge' ? 'hasEdge' : kind === 'node' ? 'hasNode' : '';
      return !!(method && typeof this.target[method] === 'function' && this.target[method](String(key || '')));
    }

    get(kind, key) {
      const method = kind === 'edge' ? 'edge' : kind === 'node' ? 'node' : '';
      if (!method || typeof this.target[method] !== 'function') return null;
      return this.target[method](String(key || '')) || null;
    }

    counts() {
      if (typeof this.target.counts !== 'function') return {nodes: 0, edges: 0};
      const counts = this.target.counts() || {};
      return {nodes: Number(counts.nodes) || 0, edges: Number(counts.edges) || 0};
    }

    select(kind, key, options) {
      this.assertActive();
      options = options || {};
      kind = String(kind || '');
      key = String(key || '');
      if (!this.has(kind, key)) return false;
      const token = `${kind}:${key}`;
      if (!options.additive && !options.toggle) this.selections.clear();
      if (options.toggle && this.selections.has(token)) {
        this.selections.delete(token);
        this.selection = [...this.selections.values()].at(-1) || null;
      } else {
        const item = Object.freeze({kind, key});
        this.selections.set(token, item);
        this.selection = item;
      }
      this.emit('selection', this.selectionSnapshot());
      return true;
    }

    selectMany(kind, keys, options) {
      this.assertActive();
      options = options || {};
      kind = String(kind || '');
      const valid = [...new Set((keys || []).map(String))].filter(key => this.has(kind, key));
      if (!options.additive) this.selections.clear();
      for (const key of valid) {
        const item = Object.freeze({kind, key});
        this.selections.set(`${kind}:${key}`, item);
        this.selection = item;
      }
      if (!valid.length && !options.additive) this.selection = null;
      else if (!this.selection || !this.selections.has(`${this.selection.kind}:${this.selection.key}`)) this.selection = [...this.selections.values()].at(-1) || null;
      this.emit('selection', this.selectionSnapshot());
      return valid.length;
    }

    selectionSnapshot() {
      return Object.freeze({primary: this.selection, items: Object.freeze([...this.selections.values()])});
    }

    clearSelection() {
      this.assertActive();
      if (!this.selection && !this.selections.size) return false;
      this.selections.clear();
      this.selection = null;
      this.emit('selection', this.selectionSnapshot());
      return true;
    }

    assertActive() {
      if (this.destroyed) throw new Error('Knowledge graph adapter is destroyed');
    }

    destroy() {
      if (this.destroyed) return;
      this.destroyed = true;
      this.selections.clear();
      this.selection = null;
      this.emit('destroy', null);
      this.listeners.clear();
      if (typeof this.target.destroy === 'function') this.target.destroy();
    }
  }

  return Object.freeze({PATCH_VERSION, Adapter, objectKey, nodeFromAPI, edgeFromAPI, snapshotToPatch, validatePatch});
});
