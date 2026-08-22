'use strict';

const assert = require('assert');
const graph = require('../assets/knowledge_graph_adapter.js');
const fixture = require('./knowledge_graph_fixtures.js');

assert.strictEqual(graph.PATCH_VERSION, 'knowledge.graph.patch.v1');
assert.deepStrictEqual(graph.snapshotToPatch(fixture.apiSnapshot), fixture.snapshotPatch);
assert.strictEqual(graph.objectKey({kind: 'entry', id: fixture.ids.partition}), `entry:${fixture.ids.partition}`);
assert.throws(() => graph.objectKey({kind: 'link', id: fixture.ids.requires}), /kind is invalid/);
assert.throws(() => graph.snapshotToPatch({...fixture.apiSnapshot, generation: 0}), /generation/);
assert.throws(() => graph.validatePatch({...fixture.snapshotPatch, upsertNodes: [fixture.snapshotPatch.upsertNodes[0], fixture.snapshotPatch.upsertNodes[0]]}), /duplicate/);

class FakeTarget {
  constructor() { this.nodes = new Map(); this.edges = new Map(); this.calls = []; }
  replace(patch) { this.nodes.clear(); this.edges.clear(); this.calls.push('replace'); this.applyValues(patch); }
  apply(patch) { this.calls.push('apply'); this.applyValues(patch); }
  applyValues(patch) {
    for (const key of patch.removeEdgeKeys) this.edges.delete(key);
    for (const key of patch.removeNodeKeys) this.nodes.delete(key);
    for (const node of patch.upsertNodes) this.nodes.set(node.key, node);
    for (const edge of patch.upsertEdges) this.edges.set(edge.key, edge);
  }
  hasNode(key) { return this.nodes.has(key); }
  hasEdge(key) { return this.edges.has(key); }
  node(key) { return this.nodes.get(key); }
  edge(key) { return this.edges.get(key); }
  counts() { return {nodes: this.nodes.size, edges: this.edges.size}; }
  destroy() { this.destroyed = true; }
}

const target = new FakeTarget();
const adapter = new graph.Adapter(target);
const events = [];
const unsubscribe = adapter.subscribe(event => events.push([event.type, event.detail && event.detail.mode]));
adapter.replaceSnapshot(fixture.apiSnapshot);
assert.deepStrictEqual(target.calls, ['replace']);
assert.deepStrictEqual(adapter.counts(), {nodes: 3, edges: 1});
assert.strictEqual(adapter.select('node', `entry:${fixture.ids.partition}`), true);
assert.deepStrictEqual(adapter.selectionSnapshot().items, [{kind: 'node', key: `entry:${fixture.ids.partition}`}]);
assert.strictEqual(adapter.select('node', `entry:${fixture.ids.format}`, {additive: true}), true);
assert.strictEqual(adapter.selectionSnapshot().items.length, 2);
assert.strictEqual(adapter.select('node', `entry:${fixture.ids.partition}`, {toggle: true}), true);
assert.deepStrictEqual(adapter.selectionSnapshot(), {
  primary: {kind: 'node', key: `entry:${fixture.ids.format}`},
  items: [{kind: 'node', key: `entry:${fixture.ids.format}`}],
});
assert.strictEqual(adapter.selectMany('node', [`entry:${fixture.ids.partition}`, `entry:${fixture.ids.format}`]), 2);
assert.strictEqual(adapter.selectionSnapshot().items.length, 2);
assert.strictEqual(adapter.get('node', `entry:${fixture.ids.partition}`).attributes.title, 'Partition a disk with sfdisk');
assert.strictEqual(adapter.select('node', 'missing'), false);
adapter.applyPatch(fixture.incrementalPatches[0]);
assert.deepStrictEqual(adapter.counts(), {nodes: 4, edges: 2});
assert.strictEqual(adapter.clearSelection(), true);
assert.deepStrictEqual(events.map(value => value[0]), [
  'beforechange', 'change', 'selection', 'selection', 'selection', 'selection', 'beforechange', 'change', 'selection',
]);
unsubscribe();
adapter.destroy();
assert.strictEqual(target.destroyed, true);
assert.throws(() => adapter.applyPatch(fixture.incrementalPatches[1]), /destroyed/);
