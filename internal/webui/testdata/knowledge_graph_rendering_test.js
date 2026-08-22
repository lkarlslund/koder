'use strict';

const assert = require('assert');
const rendering = require('../assets/knowledge_graph_rendering.js');
const fixture = require('./knowledge_graph_fixtures.js');

const partition = fixture.snapshotPatch.upsertNodes.find(node => node.key.endsWith(fixture.ids.partition)).attributes;
const procedure = rendering.nodeStyle(partition);
assert.strictEqual(procedure.kind, 'entry:procedure');
assert.strictEqual(procedure.shape, 'diamond');
assert.strictEqual(procedure.borderPattern, 'solid');
assert(procedure.badges.some(badge => badge.label === 'Verified'));

const draft = rendering.nodeStyle(fixture.snapshotPatch.upsertNodes.find(node => node.key.endsWith(fixture.ids.format)).attributes);
assert.strictEqual(draft.statePattern, 'dashed');
assert(draft.opacity < procedure.opacity);
assert(draft.badges.some(badge => badge.label === 'Draft'));
assert(draft.badges.some(badge => badge.label === 'Unverified'));

const personal = rendering.nodeStyle({...partition, scopeKind: 'personal', semanticKind: 'warning', verification: 'disputed'});
assert.strictEqual(personal.shape, 'triangle');
assert.strictEqual(personal.scopeColor, rendering.scopeStyles.personal.color);
assert.strictEqual(personal.borderColor, rendering.verificationStyles.disputed.color);
assert.strictEqual(personal.borderPattern, 'double');

const selected = rendering.nodeStyle(partition, {selected: true, stale: true, searchHit: true});
assert(selected.size > procedure.size);
assert.strictEqual(selected.sigma.forceLabel, true);
assert.strictEqual(selected.sigma.zIndex, 4);
assert.strictEqual(selected.borderPattern, 'warning');
assert(selected.ariaLabel.includes('Stale'));

const chunk = rendering.nodeStyle(fixture.snapshotPatch.upsertNodes[0].attributes);
assert.strictEqual(chunk.shape, 'hexagon');
assert(chunk.size > procedure.size);
assert.notStrictEqual(chunk.color, procedure.color);

assert.strictEqual(rendering.plainLabel('**Safe** [title](https://example.com) <script>bad</script>'), 'Safe title bad');
const styled = rendering.styledNodeAttributes({...partition, x: 12, y: 8}, {hovered: true});
assert.strictEqual(styled.x, 12);
assert.strictEqual(styled.y, 8);
assert.strictEqual(styled.label, partition.title);
assert.strictEqual(styled.knowledgeStyle.hovered, true);
