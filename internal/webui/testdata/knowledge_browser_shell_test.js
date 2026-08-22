'use strict';

const assert = require('assert');
const browser = require('../assets/knowledge_browser.js');

assert.deepStrictEqual([...browser.panes], ['sources', 'graph', 'inspector']);
assert.strictEqual(browser.normalizePane(' SOURCES '), 'sources');
assert.strictEqual(browser.normalizePane('missing'), 'graph');
assert.strictEqual(browser.adjacentPane('graph', 1), 'inspector');
assert.strictEqual(browser.adjacentPane('inspector', 1), 'sources');
assert.strictEqual(browser.adjacentPane('sources', -1), 'inspector');
