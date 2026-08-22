'use strict';

const assert = require('assert');
const adapterAPI = require('../assets/knowledge_graph_adapter.js');
const graphAPI = require('../assets/knowledge_graph.js');
const rendering = require('../assets/knowledge_graph_rendering.js');
const rendererAPI = require('../assets/knowledge_graph_renderer.js');
const fixture = require('./knowledge_graph_fixtures.js');

assert.deepStrictEqual(rendererAPI.pointerModifiers({event: {original: {metaKey: true, shiftKey: true}}}), {additive: true, shift: true});

class FakeSigma {
  constructor(graph, container, settings) { this.graph = graph; this.container = container; this.settings = settings; FakeSigma.instance = this; }
  on(name, handler) { (this.handlers ||= {})[name] = handler; }
  off(name, handler) { if (this.handlers && this.handlers[name] === handler) delete this.handlers[name]; }
  emit(name, value) { this.handlers[name](value); }
  getNodeDisplayData(key) {
    if (key.endsWith(fixture.ids.partition)) return {x: 20, y: 20};
    if (key.endsWith(fixture.ids.format)) return {x: 70, y: 70};
    return {x: 180, y: 180};
  }
  graphToViewport(value) { return value; }
  resize() { this.resizeCount = (this.resizeCount || 0) + 1; }
  refresh() { this.refreshCount = (this.refreshCount || 0) + 1; }
  kill() { this.killed = true; }
}
class FakeResizeObserver {
  constructor(callback) { this.callback = callback; FakeResizeObserver.instance = this; }
  observe(value) { this.observed = value; }
  disconnect() { this.disconnected = true; }
}

const store = new graphAPI.Store();
const adapter = new adapterAPI.Adapter(store);
adapter.replaceSnapshot(fixture.apiSnapshot);
let scheduled;
let canceled = 0;
let clock = 10;
const stage = {dataset: {}, setAttribute(name, value) { this[name] = value; }};
const container = {
  parentElement: stage, handlers: {},
  addEventListener(name, handler) { this.handlers[name] = handler; },
  removeEventListener(name, handler) { if (this.handlers[name] === handler) delete this.handlers[name]; },
  getBoundingClientRect() { return {left: 0, top: 0, width: 240, height: 240}; },
  setPointerCapture(id) { this.captured = id; }, releasePointerCapture(id) { this.released = id; },
};
const selectionBox = {hidden: true, style: {}};
const renderer = new rendererAPI.Renderer({
  store, container, stage, rendering, selectionBox,
  SigmaAPI: {Sigma: FakeSigma, rendering: {EdgeArrowProgram: class {}, EdgeLineProgram: class {}}},
  ResizeObserver: FakeResizeObserver,
  requestAnimationFrame(callback) { scheduled = callback; return 7; },
  cancelAnimationFrame(id) { canceled = id; },
  now() { clock += 2; return clock; },
});

for (const key of store.graph.nodes()) {
  const attributes = store.graph.getNodeAttributes(key);
  assert(Number.isFinite(attributes.x));
  assert(Number.isFinite(attributes.y));
}
assert.strictEqual(FakeSigma.instance.settings.defaultEdgeType, 'arrow');
assert.strictEqual(FakeSigma.instance.settings.allowInvalidContainer, true);
assert.strictEqual(FakeSigma.instance.settings.enableEdgeEvents, true);
assert.strictEqual(FakeResizeObserver.instance.observed, container);
const interactions = [];
renderer.subscribe(event => interactions.push([event.type, event.detail && event.detail.key]));
FakeSigma.instance.emit('clickNode', {node: `entry:${fixture.ids.partition}`});
FakeSigma.instance.emit('clickEdge', {edge: fixture.ids.requires});
FakeSigma.instance.emit('clickStage', {});
assert.deepStrictEqual(interactions, [
  ['node', `entry:${fixture.ids.partition}`], ['edge', fixture.ids.requires], ['background', undefined],
]);
FakeSigma.instance.emit('enterNode', {node: `entry:${fixture.ids.partition}`});
assert.strictEqual(FakeSigma.instance.settings.nodeReducer(`entry:${fixture.ids.partition}`, store.graph.getNodeAttributes(`entry:${fixture.ids.partition}`)).knowledgeStyle.hovered, true);
FakeSigma.instance.emit('leaveNode', {node: `entry:${fixture.ids.partition}`});
FakeSigma.instance.emit('enterEdge', {edge: fixture.ids.requires});
assert.strictEqual(FakeSigma.instance.settings.edgeReducer(fixture.ids.requires, store.graph.getEdgeAttributes(fixture.ids.requires)).knowledgeStyle.hovered, true);
FakeSigma.instance.emit('leaveEdge', {edge: fixture.ids.requires});
assert.deepStrictEqual(interactions.slice(3), [
  ['hover', `entry:${fixture.ids.partition}`], ['hover', null], ['hover', fixture.ids.requires], ['hover', null],
]);
const boxSelections = [];
renderer.subscribe(event => { if (event.type === 'boxselect') boxSelections.push(event.detail); });
function pointer(values) { return {button: 0, pointerId: 4, preventDefault() {}, stopPropagation() {}, ...values}; }
container.handlers.pointerdown(pointer({clientX: 10, clientY: 10, shiftKey: true, ctrlKey: true}));
container.handlers.pointermove(pointer({clientX: 100, clientY: 100, shiftKey: true}));
assert.strictEqual(selectionBox.hidden, false);
assert.strictEqual(selectionBox.style.width, '90px');
container.handlers.pointerup(pointer({clientX: 100, clientY: 100, shiftKey: true}));
assert.strictEqual(selectionBox.hidden, true);
assert.deepStrictEqual(boxSelections, [{
  keys: [`entry:${fixture.ids.partition}`, `entry:${fixture.ids.format}`], additive: true,
}]);
scheduled();
assert.strictEqual(FakeSigma.instance.resizeCount, 1);
assert.strictEqual(FakeSigma.instance.refreshCount, 1);
assert.deepStrictEqual(renderer.getMetrics(), {refreshes: 1, resizes: 1, lastRefreshMS: 2, maxRefreshMS: 2, nodes: 3, edges: 1});

const node = FakeSigma.instance.settings.nodeReducer(`entry:${fixture.ids.partition}`, store.graph.getNodeAttributes(`entry:${fixture.ids.partition}`));
assert.strictEqual(node.label, 'Partition a disk with sfdisk');
const edge = FakeSigma.instance.settings.edgeReducer(fixture.ids.requires, store.graph.getEdgeAttributes(fixture.ids.requires));
assert.strictEqual(edge.type, 'arrow');

renderer.setSelection('node', `entry:${fixture.ids.partition}`);
scheduled();
const selected = FakeSigma.instance.settings.nodeReducer(`entry:${fixture.ids.partition}`, store.graph.getNodeAttributes(`entry:${fixture.ids.partition}`));
assert.strictEqual(selected.highlighted, true);
renderer.setState('loading');
assert.strictEqual(stage.dataset.graphState, 'loading');
assert.strictEqual(stage['aria-busy'], 'true');
FakeResizeObserver.instance.callback();
scheduled();
assert.strictEqual(FakeSigma.instance.resizeCount, 2);

renderer.scheduleRefresh(false);
renderer.destroy();
assert.strictEqual(canceled, 7);
assert.strictEqual(FakeSigma.instance.killed, true);
assert.strictEqual(FakeResizeObserver.instance.disconnected, true);
assert.deepStrictEqual(FakeSigma.instance.handlers, {});
assert.deepStrictEqual(container.handlers, {});
renderer.destroy();
