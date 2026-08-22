'use strict';

const assert = require('assert');
const adapterAPI = require('../assets/knowledge_graph_adapter.js');
const graphAPI = require('../assets/knowledge_graph.js');
const fixture = require('./knowledge_graph_fixtures.js');

const store = new graphAPI.Store();
const adapter = new adapterAPI.Adapter(store);
const batches = [];
store.subscribe(event => batches.push(event.detail));

adapter.replaceSnapshot(fixture.apiSnapshot);
assert.deepStrictEqual(store.counts(), {nodes: 3, edges: 1});
assert.strictEqual(store.graph.type, 'directed');
assert.strictEqual(store.graph.multi, true);
assert.strictEqual(store.graph.hasDirectedEdge(fixture.ids.requires), true);
assert.strictEqual(batches.length, 1);
assert.strictEqual(batches[0].mode, 'replace');

const formatKey = `entry:${fixture.ids.format}`;
store.graph.setNodeAttribute(formatKey, 'x', 17);
store.graph.setNodeAttribute(formatKey, 'y', -4);
adapter.applyPatch(fixture.incrementalPatches[0]);
adapter.applyPatch(fixture.incrementalPatches[1]);
assert.deepStrictEqual(store.counts(), {nodes: 4, edges: 2});
assert.strictEqual(store.node(formatKey).attributes.state, 'active');
assert.strictEqual(store.node(formatKey).attributes.revision, 2);
assert.strictEqual(store.node(formatKey).attributes.x, 17);
assert.strictEqual(store.node(formatKey).attributes.y, -4);
assert.strictEqual(batches.length, 3);

const beforeInvalid = store.graph.export();
assert.throws(() => store.apply({
  ...fixture.incrementalPatches[1], checkpoint: {...fixture.incrementalPatches[1].checkpoint, sequence: 99},
  upsertEdges: [{...fixture.snapshotPatch.upsertEdges[0], key: 'bad-edge', target: 'entry:missing'}],
}), /missing endpoint/);
assert.deepStrictEqual(store.graph.export(), beforeInvalid);
assert.strictEqual(batches.length, 3);

adapter.applyPatch(fixture.incrementalPatches[2]);
assert.deepStrictEqual(store.counts(), {nodes: 3, edges: 0});
assert.strictEqual(store.checkpoint.sequence, 43);
assert.strictEqual(batches.length, 4);

store.graph.addNode('temporary', {title: 'discard me'});
adapter.replaceSnapshot(fixture.apiSnapshot);
assert.strictEqual(store.hasNode('temporary'), false);
assert.deepStrictEqual(store.counts(), {nodes: 3, edges: 1});
adapter.destroy();
assert.deepStrictEqual(store.counts(), {nodes: 0, edges: 0});
assert.throws(() => store.apply(fixture.snapshotPatch), /destroyed/);
