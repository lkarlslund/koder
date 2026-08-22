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
assert.deepStrictEqual(browser.chatSelectionFromSearch('?return=%2Fs%2Fsession-1%2Fc%2Fchat-2'), {
  sessionID: 'session-1', chatID: 'chat-2', path: '/s/session-1/c/chat-2',
});
assert.strictEqual(browser.chatSelectionFromSearch('?return=%2Fs%2Fsession-1'), null);
assert.strictEqual(browser.chatSelectionFromSearch('?return=https%3A%2F%2Fexample.com'), null);
assert.deepStrictEqual([...browser.states], ['shell', 'loading', 'ready', 'empty', 'unavailable', 'truncated', 'stale', 'error']);
assert.strictEqual(browser.normalizeState(' READY '), 'ready');
assert.strictEqual(browser.normalizeState('unknown'), 'error');
assert.strictEqual(browser.stateForError({code: 'unavailable'}), 'unavailable');
assert.strictEqual(browser.stateForError({code: 'network'}), 'unavailable');
assert.strictEqual(browser.stateForError({code: 'invalid_cursor'}), 'stale');
assert.strictEqual(browser.stateForError({code: 'conflict'}), 'error');
assert.strictEqual(browser.presentationForState('empty').title, 'No knowledge yet');
assert.strictEqual(browser.presentationForState('error', 'Safe detail').detail, 'Safe detail');

const urlState = browser.browserStateFromSearch('?return=%2Fs%2Fsession-1%2Fc%2Fchat-2&query=partition&kind=reference&scope_kind=project&state=archived&tag=linux&object_kind=entry&id=entry-7');
assert.deepStrictEqual(urlState, {
  query: 'partition', kind: 'reference', scopeKind: 'project', state: 'archived', tag: 'linux', objectKind: 'entry', id: 'entry-7',
});
assert.strictEqual(
  browser.searchForBrowserState('?return=%2Fs%2Fsession-1%2Fc%2Fchat-2&unknown=kept', urlState),
  '?return=%2Fs%2Fsession-1%2Fc%2Fchat-2&unknown=kept&query=partition&kind=reference&scope_kind=project&state=archived&tag=linux&object_kind=entry&id=entry-7'
);
assert.deepStrictEqual(browser.browserStateFromSearch('?kind=bad&scope_kind=bad&object_kind=entry&id=..%2Fsecret'), {
  query: '', kind: '', scopeKind: '', state: '', tag: '', objectKind: '', id: '',
});
assert.strictEqual(browser.displayLabel('partially_verified'), 'Partially verified');
assert.strictEqual(browser.plainTextLabel('**Safe** [label](https://example.com) <script>bad</script>'), 'Safe label bad');
assert.strictEqual(browser.plainTextLabel('abcdefghijklmnopqrstuvwxyz', 8), 'abcdefg…');
assert.deepStrictEqual(browser.graphSnapshotRequest('entry', 'entry-7'), {
  root: {kind: 'entry', id: 'entry-7'}, max_depth: 2, max_nodes: 200, max_edges: 400, time_limit_ms: 2500,
});
assert.strictEqual(browser.graphSnapshotRequest('link', 'link-1'), null);
assert.strictEqual(browser.graphSnapshotRequest('entry', '../secret'), null);
assert.deepStrictEqual(browser.graphExpansionRequest('entry', 'entry-7', 'incoming'), {
  root: {kind: 'entry', id: 'entry-7'}, direction: 'incoming', max_depth: 1, max_nodes: 100, max_edges: 200, time_limit_ms: 2000,
});
assert.strictEqual(browser.graphExpansionRequest('entry', 'entry-7', 'both'), null);
assert.deepStrictEqual(browser.graphObjectForSelection({kind: 'node', key: 'entry:entry-7'}), {kind: 'entry', id: 'entry-7'});
assert.deepStrictEqual(browser.graphObjectForSelection({kind: 'chunk', id: 'chunk-7'}), {kind: 'chunk', id: 'chunk-7'});
assert.strictEqual(browser.graphObjectForSelection({kind: 'edge', key: 'link-7'}), null);
assert.strictEqual(browser.graphKeyboardAction({key: 'h'}), 'hide');
assert.strictEqual(browser.graphKeyboardAction({key: 'Delete'}), 'hide');
assert.strictEqual(browser.graphKeyboardAction({key: 'z', ctrlKey: true}), 'undo');
assert.strictEqual(browser.graphKeyboardAction({key: 'h', ctrlKey: true}), '');
assert.strictEqual(browser.graphDebugEnabled('?graph_debug=1'), true);
assert.strictEqual(browser.graphDebugEnabled('?graph_debug=true'), false);
const webglDocument = {createElement: () => ({getContext: name => name === 'webgl2' ? {} : null})};
assert.strictEqual(browser.supportsWebGL(webglDocument), true);
assert.deepStrictEqual(browser.graphEnvironment(webglDocument, () => ({matches: false})), {available: true, reason: ''});
assert.deepStrictEqual(browser.graphEnvironment(webglDocument, () => ({matches: true})), {available: false, reason: 'reduced_motion'});
assert.deepStrictEqual(browser.graphEnvironment({createElement: () => ({getContext: () => null})}, () => ({matches: false})), {available: false, reason: 'webgl_unavailable'});
let sanitizeOptions;
const sanitized = browser.sanitizedMarkdownHTML('unsafe', {parse: () => '<script>bad</script><p>safe</p>'}, {
  sanitize: (html, options) => { sanitizeOptions = options; return html.replace('<script>bad</script>', ''); },
});
assert.strictEqual(sanitized, '<p>safe</p>');
assert(sanitizeOptions.FORBID_TAGS.includes('script'));
assert(sanitizeOptions.FORBID_TAGS.includes('iframe'));
assert(sanitizeOptions.FORBID_ATTR.includes('style'));
