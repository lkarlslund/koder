'use strict';

const assert = require('assert');
const adapterAPI = require('../assets/knowledge_graph_adapter.js');
const graphAPI = require('../assets/knowledge_graph.js');
const interactions = require('../assets/knowledge_graph_interactions.js');
const fixture = require('./knowledge_graph_fixtures.js');

const store = new graphAPI.Store();
new adapterAPI.Adapter(store).replaceSnapshot(fixture.apiSnapshot);
const view = new interactions.LocalViewHistory({graph: store.graph, limit: 3});
const events = [];
view.subscribe(event => events.push([event.action, event.state.hidden]));
const partition = `entry:${fixture.ids.partition}`;
const format = `entry:${fixture.ids.format}`;
const chunk = `chunk:${fixture.ids.chunk}`;

assert.strictEqual(view.hide([{kind: 'node', key: partition}]), true);
assert.strictEqual(store.graph.getNodeAttribute(partition, 'hidden'), true);
assert.deepStrictEqual(view.state(), {hiddenNodes: 1, hiddenEdges: 0, hidden: 1, canUndo: true});
assert.strictEqual(view.undo(), true);
assert.strictEqual(store.graph.getNodeAttribute(partition, 'hidden'), false);

assert.strictEqual(view.isolate([{kind: 'node', key: partition}, {kind: 'node', key: format}]), true);
assert.strictEqual(store.graph.getNodeAttribute(chunk, 'hidden'), true);
assert.strictEqual(store.graph.getNodeAttribute(partition, 'hidden'), false);
assert.strictEqual(store.graph.getEdgeAttribute(fixture.ids.requires, 'hidden'), false);
assert.strictEqual(view.reveal(), true);
assert.strictEqual(view.state().hidden, 0);

assert.strictEqual(view.isolate([{kind: 'edge', key: fixture.ids.requires}]), true);
assert.strictEqual(store.graph.getNodeAttribute(partition, 'hidden'), false);
assert.strictEqual(store.graph.getNodeAttribute(format, 'hidden'), false);
assert.strictEqual(store.graph.getNodeAttribute(chunk, 'hidden'), true);
assert.deepStrictEqual(events.map(value => value[0]), ['hide', 'undo', 'isolate', 'reveal', 'isolate']);
view.reset();
assert.strictEqual(view.state().hidden, 0);
assert.strictEqual(view.state().canUndo, false);
assert.deepStrictEqual(events.map(value => value[0]), ['hide', 'undo', 'isolate', 'reveal', 'isolate', 'reset']);
view.destroy();
assert.throws(() => view.reveal(), /destroyed/);
