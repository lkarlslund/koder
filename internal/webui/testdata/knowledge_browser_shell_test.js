'use strict';

const assert = require('assert');
const browser = require('../assets/knowledge_browser.js');

assert.deepStrictEqual([...browser.panes], ['sources', 'graph', 'inspector']);
assert.strictEqual(browser.normalizePane(' SOURCES '), 'sources');
assert.strictEqual(browser.normalizePane('missing'), 'graph');
assert.strictEqual(browser.adjacentPane('graph', 1), 'inspector');
assert.strictEqual(browser.adjacentPane('inspector', 1), 'sources');
assert.strictEqual(browser.adjacentPane('sources', -1), 'inspector');
assert.strictEqual(browser.safeReturnPath('/s/session-1/c/chat_2'), '/s/session-1/c/chat_2');
assert.strictEqual(browser.safeReturnPath('/s/session-1'), '/s/session-1');
assert.strictEqual(browser.safeReturnPath('//example.com'), '/');
assert.strictEqual(browser.safeReturnPath('/s/../c/chat'), '/');
assert.strictEqual(browser.returnPathFromSearch('?return=%2Fs%2Fsession-1%2Fc%2Fchat-2'), '/s/session-1/c/chat-2');
assert.strictEqual(browser.returnPathFromSearch('?return=https%3A%2F%2Fexample.com'), '/');
