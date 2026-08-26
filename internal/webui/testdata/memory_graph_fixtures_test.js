'use strict';

const assert = require('assert');
const fixture = require('./memory_graph_fixtures.js');

assert(Object.isFrozen(fixture));
assert(Object.isFrozen(fixture.snapshotPatch.upsertNodes[0].attributes));
assert.strictEqual(fixture.snapshotPatch.patchVersion, 'memory.graph.patch.v1');
assert.strictEqual(fixture.snapshotPatch.generation, fixture.apiSnapshot.generation);
assert.deepStrictEqual(fixture.incrementalPatches.map(patch => patch.checkpoint.sequence), [41, 42, 43]);

const nodeKeys = new Set();
const edgeKeys = new Set();
function apply(patch) {
  assert.deepStrictEqual(Object.keys(patch).sort(), [
    'checkpoint', 'generation', 'patchVersion', 'removeEdgeKeys', 'removeNodeKeys', 'upsertEdges', 'upsertNodes',
  ]);
  for (const node of patch.upsertNodes) {
    assert(node.key.includes(':'));
    assert(node.attributes.objectKind);
    assert(node.attributes.objectID);
    assert(node.attributes.revision > 0);
    nodeKeys.add(node.key);
  }
  for (const edge of patch.upsertEdges) {
    assert(edge.key && edge.source && edge.target);
    assert.strictEqual(edge.attributes.directed, true);
    assert(edge.attributes.relationshipKind);
    edgeKeys.add(edge.key);
  }
  for (const key of patch.removeEdgeKeys) edgeKeys.delete(key);
  for (const key of patch.removeNodeKeys) {
    nodeKeys.delete(key);
    for (const edge of patch.upsertEdges) assert.notStrictEqual(edge.source, key);
  }
}

apply(fixture.snapshotPatch);
assert.strictEqual(nodeKeys.size, 3);
assert.strictEqual(edgeKeys.size, 1);
for (const edge of fixture.snapshotPatch.upsertEdges) {
  assert(nodeKeys.has(edge.source));
  assert(nodeKeys.has(edge.target));
}
for (const patch of fixture.incrementalPatches) apply(patch);
assert.deepStrictEqual([...nodeKeys].sort(), [
  `chunk:${fixture.ids.chunk}`, `entry:${fixture.ids.partition}`, `entry:${fixture.ids.verify}`,
].sort());
assert.deepStrictEqual([...edgeKeys], []);
