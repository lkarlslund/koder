'use strict';

const assert = require('assert');
const graphology = require('../assets/vendor/graphology/graphology.umd.min.js');
const layoutAPI = require('../assets/knowledge_graph_layouts.js');

const graph = new graphology.MultiDirectedGraph();
graph.addNode('one', {x: 0, y: 0});
graph.addNode('two', {x: 1, y: 1, pinned: true});
graph.addEdgeWithKey('edge', 'one', 'two');
const queue = [];
const canceled = [];
let calls = 0;
const controller = new layoutAPI.ForceAtlasController({
  graph,
  layouts: {forceAtlas2(value, options) {
    calls++;
    assert.strictEqual(value, graph);
    assert(options.iterations <= 2);
    return {one: {x: calls * 2, y: calls * -1}, two: {x: 99, y: 99}};
  }},
  schedule(callback) { queue.push(callback); return queue.length; },
  cancelSchedule(handle) { canceled.push(handle); },
});
const events = [];
controller.subscribe(event => events.push(event));
controller.start({iterations: 5, batchIterations: 2, coarseIterations: 2});
assert.strictEqual(events[0].phase, 'start');
while (controller.running) queue.shift()();
assert.deepStrictEqual(events.map(event => event.phase), ['start', 'progress', 'progress', 'ready']);
assert.strictEqual(events.at(-1).completed, 5);
assert.deepStrictEqual(graph.getNodeAttributes('one'), {x: 6, y: -3});
assert.deepStrictEqual(graph.getNodeAttributes('two'), {x: 1, y: 1, pinned: true});

controller.start({iterations: 4, batchIterations: 1});
assert.strictEqual(controller.stop('selection_changed'), true);
assert.strictEqual(events.at(-1).phase, 'stopped');
assert.strictEqual(events.at(-1).reason, 'selection_changed');
assert(canceled.length > 0);
controller.destroy();
assert.throws(() => controller.start(), /destroyed/);
