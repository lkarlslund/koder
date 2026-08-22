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
assert.deepStrictEqual([...browser.states], ['shell', 'loading', 'ready', 'empty', 'unavailable', 'truncated', 'stale', 'error']);
assert.strictEqual(browser.normalizeState(' READY '), 'ready');
assert.strictEqual(browser.normalizeState('unknown'), 'error');
assert.strictEqual(browser.stateForError({code: 'unavailable'}), 'unavailable');
assert.strictEqual(browser.stateForError({code: 'network'}), 'unavailable');
assert.strictEqual(browser.stateForError({code: 'invalid_cursor'}), 'stale');
assert.strictEqual(browser.stateForError({code: 'conflict'}), 'error');
assert.strictEqual(browser.presentationForState('empty').title, 'No knowledge yet');
assert.strictEqual(browser.presentationForState('error', 'Safe detail').detail, 'Safe detail');
