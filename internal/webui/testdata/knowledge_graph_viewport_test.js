'use strict';

const assert = require('assert');
const viewportAPI = require('../assets/knowledge_graph_viewport.js');

assert.deepStrictEqual(viewportAPI.normalizeInsets({left: 100, top: -2}), {top: 0, right: 0, bottom: 0, left: 100});
assert.strictEqual(viewportAPI.cameraStateForFit({width: 0, height: 400}, {}, {}), null);
const fit = viewportAPI.cameraStateForFit({width: 1000, height: 600}, {left: 200, right: 0, top: 0, bottom: 0}, {paddingFactor: 1});
assert.strictEqual(fit.ratio, 1.25);
assert(fit.x < 0.5);
assert.strictEqual(fit.y, 0.5);

const camera = {
  state: {x: 0.5, y: 0.5, ratio: 1, angle: 0},
  getState() { return this.state; },
  setState(state) { this.state = state; this.setCount = (this.setCount || 0) + 1; },
  animate(state, options) { this.state = state; this.animation = options; },
};
const renderer = {
  getCamera: () => camera,
  getDimensions: () => ({width: 1200, height: 800}),
  getBBox: () => ({x: [-3, 4], y: [-2, 8]}),
  getNodeDisplayData: key => key === 'node-1' ? {x: 0.7, y: 0.2} : null,
};
const viewport = new viewportAPI.Viewport({renderer, getInsets: () => ({left: 240}), duration: 180});
assert.deepStrictEqual(viewport.bounds(), {x: [-3, 4], y: [-2, 8]});
assert.strictEqual(viewport.fit(), true);
assert.strictEqual(camera.animation.duration, 180);
assert(camera.state.x < 0.5);
assert.strictEqual(viewport.centerNode('missing'), false);
assert.strictEqual(viewport.centerNode('node-1', {animate: false, ratio: 0.35}), true);
assert.deepStrictEqual(camera.state, {x: 0.7, y: 0.2, ratio: 0.35, angle: 0});
assert.strictEqual(camera.setCount, 1);
viewport.destroy();
assert.strictEqual(viewport.fit(), false);
