'use strict';

const assert = require('assert');
const graphology = require('../assets/vendor/graphology/graphology.umd.min.js');
require('../assets/vendor/knowledge-layouts/knowledge-layouts.min.js');

const layouts = globalThis.KoderKnowledgeLayouts;
assert.strictEqual(typeof graphology.Graph, 'function');
assert.strictEqual(typeof layouts.forceAtlas2, 'function');
assert.strictEqual(typeof layouts.forceAtlas2.assign, 'function');
assert.strictEqual(typeof layouts.noverlap, 'function');
assert.strictEqual(typeof layouts.noverlap.assign, 'function');

const graph = new graphology.Graph();
graph.addNode('left', {x: 0, y: 0, size: 2});
graph.addNode('right', {x: 1, y: 0, size: 2});
graph.addEdge('left', 'right', {weight: 1});

for (const positions of [
  layouts.forceAtlas2(graph, {iterations: 2}),
  layouts.noverlap(graph, {maxIterations: 5}),
]) {
  assert.deepStrictEqual(Object.keys(positions).sort(), ['left', 'right']);
  for (const position of Object.values(positions)) {
    assert(Number.isFinite(position.x));
    assert(Number.isFinite(position.y));
  }
}
