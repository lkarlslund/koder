'use strict';

const assert = require('assert');
const graphology = require('../assets/vendor/graphology/graphology.umd.min.js');
const layoutAPI = require('../assets/memory_graph_layouts.js');

const graph = new graphology.MultiDirectedGraph();
graph.addNode('one', {x: 0, y: 0, objectKind: 'chunk'});
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
  }, noverlap(value, options) {
    assert.strictEqual(value, graph);
    assert.strictEqual(options.inputReducer('one', graph.getNodeAttributes('one')).size, 1.9);
    return {one: {x: 8, y: -4}, two: {x: 100, y: 100}};
  }},
  schedule(callback) { queue.push(callback); return queue.length; },
  cancelSchedule(handle) { canceled.push(handle); },
  workerFactory: null,
});
const events = [];
controller.subscribe(event => events.push(event));
controller.start({iterations: 5, batchIterations: 2, coarseIterations: 2});
assert.strictEqual(events[0].phase, 'start');
while (controller.running) queue.shift()();
assert.deepStrictEqual(events.map(event => event.phase), ['start', 'progress', 'progress', 'progress', 'ready']);
assert.strictEqual(events.at(-2).stage, 'overlap');
assert.strictEqual(events.at(-1).completed, 5);
assert.strictEqual(controller.getMetrics().runs, 1);
assert.strictEqual(controller.getMetrics().completed, 1);
assert.deepStrictEqual(graph.getNodeAttributes('one'), {x: 8, y: -4, objectKind: 'chunk'});
assert.deepStrictEqual(graph.getNodeAttributes('two'), {x: 1, y: 1, pinned: true});

controller.start({iterations: 4, batchIterations: 1});
assert.strictEqual(controller.stop('selection_changed'), true);
assert.strictEqual(events.at(-1).phase, 'stopped');
assert.strictEqual(events.at(-1).reason, 'selection_changed');
assert.strictEqual(controller.getMetrics().canceled, 1);
assert(canceled.length > 0);
assert.strictEqual(layoutAPI.layoutNodeSize({objectKind: 'entry', semanticKind: 'warning'}), 1.3);
assert.strictEqual(layoutAPI.layoutNodeSize({objectKind: 'entry', semanticKind: 'fact'}), 1);
controller.destroy();
assert.throws(() => controller.start(), /destroyed/);

class FakeWorker {
  postMessage(message) { this.message = message; }
  terminate() { this.terminated = true; }
  send(data) { this.onmessage({data}); }
}
let worker;
const workerEvents = [];
const workerController = new layoutAPI.ForceAtlasController({
  graph, layouts: {forceAtlas2() {}}, workerFactory: () => (worker = new FakeWorker()),
});
workerController.subscribe(event => workerEvents.push(event));
const workerGeneration = workerController.start({iterations: 20});
assert.strictEqual(worker.message.type, 'layout');
assert.strictEqual(worker.message.generation, workerGeneration);
assert.strictEqual(worker.message.nodes.find(node => node.key === 'one').attributes.layoutSize, 1.9);
worker.send({type: 'progress', generation: workerGeneration, completed: 10, total: 20, stage: 'coarse'});
worker.send({type: 'ready', generation: workerGeneration, completed: 20, total: 20, positions: {one: {x: 12, y: 7}, two: {x: 30, y: 30}}});
assert.strictEqual(worker.terminated, true);
assert.strictEqual(graph.getNodeAttribute('one', 'x'), 12);
assert.strictEqual(graph.getNodeAttribute('two', 'x'), 1);
assert.deepStrictEqual(workerEvents.map(event => event.phase), ['start', 'progress', 'ready']);
const staleGeneration = workerController.start({iterations: 20});
const staleWorker = worker;
const replacementGeneration = workerController.start({iterations: 20});
assert.strictEqual(staleWorker.terminated, true);
staleWorker.send({type: 'ready', generation: staleGeneration, completed: 20, total: 20, positions: {one: {x: 999, y: 999}}});
assert.strictEqual(graph.getNodeAttribute('one', 'x'), 12);
worker.send({type: 'ready', generation: replacementGeneration, completed: 20, total: 20, positions: {one: {x: 15, y: 9}}});
assert.strictEqual(graph.getNodeAttribute('one', 'x'), 15);
workerController.start({iterations: 20});
assert.strictEqual(workerController.stop('selection_changed'), true);
assert.strictEqual(worker.terminated, true);

const timerGraph = new graphology.MultiDirectedGraph();
timerGraph.addNode('only', {x: 0, y: 0});
const timers = [];
const clearedTimers = [];
const timerEvents = [];
const timerController = new layoutAPI.ForceAtlasController({
  graph: timerGraph, layouts: {forceAtlas2() {}}, workerFactory: null,
  setTimer(callback, delay) { timers.push({callback, delay}); return timers.length; },
  clearTimer(handle) { clearedTimers.push(handle); }, debounceMS: 90,
});
timerController.subscribe(event => timerEvents.push(event));
timerController.request({iterations: 3});
timerController.request({iterations: 7});
assert.deepStrictEqual(clearedTimers, [1]);
assert.strictEqual(timers[1].delay, 90);
timers[1].callback();
assert.deepStrictEqual(timerEvents.map(event => [event.phase, event.total]), [['start', 7], ['ready', 7]]);
timerController.request({iterations: 9});
assert.strictEqual(timerController.stop('selection_changed'), true);
assert(clearedTimers.includes(3));
timerController.destroy();
