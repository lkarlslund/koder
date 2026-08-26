(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderMemoryGraphViewport = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  function finite(value, fallback) {
    value = Number(value);
    return Number.isFinite(value) ? value : fallback;
  }

  function normalizeInsets(value) {
    value = value || {};
    return {
      top: Math.max(0, finite(value.top, 0)), right: Math.max(0, finite(value.right, 0)),
      bottom: Math.max(0, finite(value.bottom, 0)), left: Math.max(0, finite(value.left, 0)),
    };
  }

  function cameraStateForFit(dimensions, insets, options) {
    dimensions = dimensions || {};
    options = options || {};
    const width = Math.max(0, finite(dimensions.width, 0));
    const height = Math.max(0, finite(dimensions.height, 0));
    insets = normalizeInsets(insets);
    const usableWidth = width - insets.left - insets.right;
    const usableHeight = height - insets.top - insets.bottom;
    if (width <= 0 || height <= 0 || usableWidth <= 0 || usableHeight <= 0) return null;
    const padding = Math.max(1, finite(options.paddingFactor, 1.08));
    const ratio = Math.max(width / usableWidth, height / usableHeight) * padding;
    return {
      x: 0.5 - ((insets.left - insets.right) / (2 * width)) * ratio,
      y: 0.5 - ((insets.top - insets.bottom) / (2 * height)) * ratio,
      ratio, angle: 0,
    };
  }

  class Viewport {
    constructor(options) {
      options = options || {};
      if (!options.renderer || typeof options.renderer.getCamera !== 'function') throw new TypeError('Memory viewport requires a Sigma renderer');
      this.renderer = options.renderer;
      this.container = options.container || (this.renderer.getContainer && this.renderer.getContainer());
      this.getInsets = options.getInsets || (() => ({top: 0, right: 0, bottom: 0, left: 0}));
      this.duration = Math.max(0, finite(options.duration, 260));
      this.destroyed = false;
    }

    dimensions() {
      const dimensions = this.renderer.getDimensions ? this.renderer.getDimensions() : null;
      if (dimensions && dimensions.width > 0 && dimensions.height > 0) return dimensions;
      const rectangle = this.container && this.container.getBoundingClientRect ? this.container.getBoundingClientRect() : {};
      return {width: finite(rectangle.width, 0), height: finite(rectangle.height, 0)};
    }

    bounds() {
      return this.renderer.getBBox ? this.renderer.getBBox() : null;
    }

    fit(options) {
      if (this.destroyed) return false;
      options = options || {};
      const state = cameraStateForFit(this.dimensions(), this.getInsets(), options);
      if (!state) return false;
      const camera = this.renderer.getCamera();
      if (options.animate === false || typeof camera.animate !== 'function') camera.setState(state);
      else camera.animate(state, {duration: this.duration});
      return true;
    }

    centerNode(key, options) {
      if (this.destroyed || !this.renderer.getNodeDisplayData) return false;
      const node = this.renderer.getNodeDisplayData(String(key || ''));
      if (!node || !Number.isFinite(node.x) || !Number.isFinite(node.y)) return false;
      const camera = this.renderer.getCamera();
      const current = camera.getState();
      const state = {x: node.x, y: node.y, ratio: Math.min(current.ratio, finite(options && options.ratio, 0.45)), angle: current.angle || 0};
      if (options && options.animate === false || typeof camera.animate !== 'function') camera.setState(state);
      else camera.animate(state, {duration: this.duration});
      return true;
    }

    destroy() { this.destroyed = true; }
  }

  return Object.freeze({Viewport, normalizeInsets, cameraStateForFit});
});
