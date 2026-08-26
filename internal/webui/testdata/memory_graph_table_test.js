'use strict';

const assert = require('assert');
const table = require('../assets/memory_graph_table.js');

assert.deepStrictEqual(table.tableWindow(1000, 620, 620, 62, 5), {start: 5, end: 25, top: 310, bottom: 60450});
assert.deepStrictEqual(table.tableWindow(3, -10, 0, 62, 2), {start: 0, end: 3, top: 0, bottom: 0});

const nodes = [
  ['chunk:one', {title: 'Disk tools', semanticKind: 'reference', objectKind: 'chunk', objectID: 'one', scopeKind: 'global', state: 'active'}],
  ['entry:two', {title: 'Use sfdisk', semanticKind: 'procedure', objectKind: 'entry', objectID: 'two', scopeKind: 'project', verification: 'verified', state: 'active'}],
  ['entry:hidden', {title: 'Hidden entry', semanticKind: 'fact', hidden: true, state: 'active'}],
];
const edges = [
  ['link-visible', {relationshipKind: 'part_of', state: 'active'}, 'entry:two', 'chunk:one'],
  ['link-hidden-endpoint', {relationshipKind: 'related_to', state: 'active'}, 'entry:two', 'entry:hidden'],
  ['link-hidden', {relationshipKind: 'related_to', state: 'active', hidden: true}, 'entry:two', 'chunk:one'],
];
const graph = {
  mapNodes: callback => nodes.map(([key, attributes]) => callback(key, attributes)),
  mapEdges: callback => edges.map(([key, attributes, source, target]) => callback(key, attributes, source, target)),
};
const items = table.graphTableItems(graph);
assert.deepStrictEqual(items.map(item => [item.kind, item.key]), [
  ['node', 'chunk:one'], ['node', 'entry:two'], ['edge', 'link-visible'],
]);
assert.strictEqual(items[1].detail, 'Project · Verified');
assert.strictEqual(items[2].title, 'Use sfdisk → Disk tools');
assert.strictEqual(items[2].detail, 'Part of');
