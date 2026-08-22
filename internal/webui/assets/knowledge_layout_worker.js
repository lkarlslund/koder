(function (scope, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (!scope || typeof scope.importScripts !== 'function') return;
  scope.importScripts('/assets/vendor/graphology/graphology.umd.min.js', '/assets/vendor/knowledge-layouts/knowledge-layouts.min.js');
  scope.onmessage = event => {
    const message = event && event.data || {};
    if (message.type !== 'layout') return;
    try {
      const result = api.runLayout(message, scope.graphology, scope.KoderKnowledgeLayouts, progress => scope.postMessage(progress));
      scope.postMessage(result);
    } catch (error) {
      scope.postMessage({type: 'error', generation: Number(message.generation) || 0, message: String(error && error.message || 'Knowledge layout failed')});
    }
  };
})(typeof self !== 'undefined' ? self : null, function () {
  'use strict';

  function runLayout(message, graphology, layouts, onProgress) {
    if (!graphology || typeof graphology.MultiDirectedGraph !== 'function' || !layouts || typeof layouts.forceAtlas2 !== 'function') {
      throw new Error('Knowledge layout dependencies did not load');
    }
    const generation = Number(message.generation);
    const total = Math.max(1, Math.min(1000, Number(message.total) || 120));
    const batch = Math.max(1, Math.min(50, Number(message.batch) || 10));
    if (!Number.isSafeInteger(generation) || generation < 1) throw new Error('Knowledge layout generation is invalid');
    const graph = new graphology.MultiDirectedGraph({allowSelfLoops: true});
    for (const node of message.nodes || []) graph.addNode(String(node.key), {...(node.attributes || {})});
    for (const edge of message.edges || []) graph.addDirectedEdgeWithKey(String(edge.key), String(edge.source), String(edge.target), {});
    let completed = 0;
    while (completed < total) {
      const iterations = Math.min(batch, total - completed);
      const positions = layouts.forceAtlas2(graph, {iterations, settings: message.settings || {}});
      graph.updateEachNodeAttributes((key, attributes) => {
        const position = positions[key];
        return attributes.pinned || !position ? attributes : {...attributes, x: position.x, y: position.y};
      }, {attributes: ['x', 'y']});
      completed += iterations;
      if (onProgress) onProgress({type: 'progress', generation, completed, total, stage: completed < Number(message.coarse || 40) ? 'coarse' : 'settling'});
    }
    if (typeof layouts.noverlap === 'function') {
      if (onProgress) onProgress({type: 'progress', generation, completed, total, stage: 'overlap'});
      const positions = layouts.noverlap(graph, {
        maxIterations: Number(message.overlapIterations) || 80,
        settings: message.overlapSettings || {},
        inputReducer: (key, attributes) => ({...attributes, size: Number(attributes.layoutSize) || 1}),
      });
      graph.updateEachNodeAttributes((key, attributes) => {
        const position = positions[key];
        return attributes.pinned || !position ? attributes : {...attributes, x: position.x, y: position.y};
      }, {attributes: ['x', 'y']});
    }
    const positions = {};
    graph.forEachNode((key, attributes) => { positions[key] = {x: attributes.x, y: attributes.y}; });
    return {type: 'ready', generation, completed, total, stage: 'settled', positions};
  }

  return Object.freeze({runLayout});
});
