'use strict';

const assert = require('assert');
const graphology = require('../assets/vendor/graphology/graphology.umd.min.js');
require('../assets/vendor/knowledge-layouts/knowledge-layouts.min.js');
const worker = require('../assets/knowledge_layout_worker.js');

const progress = [];
const result = worker.runLayout({
  type: 'layout', generation: 3, total: 4, batch: 2, coarse: 2,
  nodes: [
    {key: 'one', attributes: {x: 0, y: 0, layoutSize: 1}},
    {key: 'two', attributes: {x: 1, y: 0, layoutSize: 1.9}},
  ],
  edges: [{key: 'edge', source: 'one', target: 'two'}],
  settings: {gravity: 1, scalingRatio: 2, slowDown: 2},
  overlapIterations: 5, overlapSettings: {gridSize: 20, margin: 0.1, expansion: 1.1, ratio: 1, speed: 3},
}, graphology, globalThis.KoderKnowledgeLayouts, value => progress.push(value));
assert.strictEqual(result.type, 'ready');
assert.strictEqual(result.generation, 3);
assert.strictEqual(result.completed, 4);
assert(progress.some(value => value.stage === 'overlap'));
for (const position of Object.values(result.positions)) {
  assert(Number.isFinite(position.x));
  assert(Number.isFinite(position.y));
}
assert.throws(() => worker.runLayout({generation: 0}, graphology, globalThis.KoderKnowledgeLayouts), /generation/);
