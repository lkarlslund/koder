'use strict';

const assert = require('assert');
const adapterAPI = require('../assets/memory_graph_adapter.js');
const graphAPI = require('../assets/memory_graph.js');
const fixture = require('./memory_graph_fixtures.js');

const store = new graphAPI.Store();
const adapter = new adapterAPI.Adapter(store);
const batches = [];
store.subscribe(event => batches.push([event.type, event.detail]));

adapter.replaceSnapshot(fixture.apiSnapshot);
assert.deepStrictEqual(graphAPI.initialPoint('stable-key'), graphAPI.initialPoint('stable-key'));
assert.deepStrictEqual(store.counts(), {nodes: 3, edges: 1});
assert.strictEqual(store.graph.type, 'directed');
assert.strictEqual(store.graph.multi, true);
assert.strictEqual(store.graph.hasDirectedEdge(fixture.ids.requires), true);
for (const key of store.graph.nodes()) {
  assert(Number.isFinite(store.graph.getNodeAttribute(key, 'x')));
  assert(Number.isFinite(store.graph.getNodeAttribute(key, 'y')));
}
assert.strictEqual(batches.length, 1);
assert.strictEqual(batches[0][0], 'change');
assert.strictEqual(batches[0][1].mode, 'replace');

const formatKey = `entry:${fixture.ids.format}`;
store.graph.setNodeAttribute(formatKey, 'x', 17);
store.graph.setNodeAttribute(formatKey, 'y', -4);
adapter.applyPatch(fixture.incrementalPatches[0]);
const verifyPosition = store.node(`entry:${fixture.ids.verify}`).attributes;
assert(Math.hypot(verifyPosition.x - 17, verifyPosition.y + 4) < 1.3);
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

const stale = store.apply({...fixture.incrementalPatches[2], generation: 6, checkpoint: {streamID: 'memory-fixture-stream', sequence: 44}});
assert.deepStrictEqual([stale.action, stale.reason], ['ignored', 'stale_generation']);
assert.deepStrictEqual(store.counts(), {nodes: 3, edges: 0});
const gap = store.apply({...fixture.incrementalPatches[2], checkpoint: {streamID: 'memory-fixture-stream', sequence: 45}});
assert.deepStrictEqual([gap.action, gap.reason], ['refetch', 'checkpoint_gap']);
assert.strictEqual(store.refetchRequired, true);
const pending = store.apply({...fixture.incrementalPatches[2], checkpoint: {streamID: 'memory-fixture-stream', sequence: 44}});
assert.deepStrictEqual([pending.action, pending.reason], ['refetch', 'refetch_pending']);

const freshSnapshot = {
  ...fixture.apiSnapshot, checkpoint: {stream_id: 'memory-fixture-stream', sequence: 46},
};
adapter.replaceSnapshot(freshSnapshot);
assert.strictEqual(store.refetchRequired, false);
const revisionGap = store.apply({
  ...fixture.incrementalPatches[1], checkpoint: {streamID: 'memory-fixture-stream', sequence: 47},
  upsertNodes: [{
    ...fixture.snapshotPatch.upsertNodes[0],
    attributes: {...fixture.snapshotPatch.upsertNodes[0].attributes, revision: 3, revisionID: 'revision-gap'},
  }],
});
assert.deepStrictEqual([revisionGap.action, revisionGap.reason], ['refetch', 'node_revision_gap']);
assert.strictEqual(store.node(`chunk:${fixture.ids.chunk}`).attributes.revision, 1);

store.graph.addNode('temporary', {title: 'discard me'});
adapter.replaceSnapshot({...fixture.apiSnapshot, checkpoint: {stream_id: 'memory-fixture-stream', sequence: 48}});
assert.strictEqual(store.hasNode('temporary'), false);
assert.deepStrictEqual(store.counts(), {nodes: 3, edges: 1});
adapter.destroy();
assert.deepStrictEqual(store.counts(), {nodes: 0, edges: 0});
assert.throws(() => store.apply(fixture.snapshotPatch), /destroyed/);

const mergeStore = new graphAPI.Store();
const mergeAdapter = new adapterAPI.Adapter(mergeStore);
mergeAdapter.replaceSnapshot(fixture.apiSnapshot);
mergeStore.graph.setEdgeAttribute(fixture.ids.requires, 'hidden', true);
mergeAdapter.mergeSnapshot(fixture.apiSnapshot);
assert.strictEqual(mergeStore.graph.getEdgeAttribute(fixture.ids.requires, 'hidden'), true, 'merge must preserve local edge visibility');
mergeAdapter.mergeSnapshot({
  ...fixture.apiSnapshot, nodes: [fixture.apiNodes.verify], edges: [fixture.apiEdges.follows],
});
assert.deepStrictEqual(mergeStore.counts(), {nodes: 4, edges: 2});
const ahead = mergeStore.merge({
  ...fixture.snapshotPatch, checkpoint: {streamID: 'memory-fixture-stream', sequence: 41},
});
assert.deepStrictEqual([ahead.action, ahead.reason], ['refetch', 'checkpoint_ahead']);
