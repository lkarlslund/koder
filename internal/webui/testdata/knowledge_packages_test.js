'use strict';

const assert = require('assert');
const packages = require('../assets/knowledge_packages.js');

const preview = {
  package: {id: 'dk.tools', version: '1.2.3'}, publisher: {id: 'pub', name: 'Trusted tools'}, license: {name: 'MIT'},
  chunk_id: 'chunk-1', chunk_title: 'Danish tools', signature_state: 'verified', classification: {decision: 'review', findings: [{message: 'Personal content'}]},
  publisher_trust: {state: 'verified', trusted: true},
  summary: {additions: 4, unchanged: 1, conflicts: 2, missing_dependencies: 1, blockers: 0}, review_required: true, ready_to_stage: false,
  impacts: [
    {kind: 'entry', id: 'normal', action: 'add', reason: 'new'},
    {kind: 'chunk', id: 'conflict', action: 'conflict', reason: 'changed'},
    {kind: 'chunk', id: 'blocked', action: 'missing_dependency', reason: 'missing', blocking: true},
  ],
};

const view = packages.previewView(preview);
assert.strictEqual(view.title, 'Danish tools');
assert.strictEqual(view.publisher, 'Trusted tools');
assert.strictEqual(view.signature, 'Verified');
assert.strictEqual(view.publisherTrust, 'Verified');
assert.strictEqual(view.conflicts, 2);
assert.strictEqual(view.reviewRequired, true);
assert.deepStrictEqual(packages.importantImpacts(preview.impacts, 2).map(item => item.id), ['blocked', 'conflict']);
assert.strictEqual(packages.isPersonalChunk({kind: 'personal', scope: {kind: 'personal', selector: 'me'}}), true);
assert.strictEqual(packages.isPersonalChunk({kind: 'reference', scope: {kind: 'global'}}), false);

class Element {
  constructor() { this.hidden = false; this.disabled = false; this.value = ''; this.checked = false; this.textContent = ''; this.children = []; this.listeners = {}; this.open = false; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  replaceChildren(...children) { this.children = children; }
  append(...children) { this.children.push(...children); }
  appendChild(child) { this.children.push(child); return child; }
  focus() {}
  click() {}
  remove() {}
  showModal() { this.open = true; }
  close() { this.open = false; }
  setAttribute(name) { if (name === 'open') this.open = true; }
  removeAttribute(name) { if (name === 'open') this.open = false; }
}

function fakeShell() {
  const selectors = [
    'open', 'dialog', 'file', 'file-name', 'preview', 'preview-title', 'preview-meta', 'summary', 'findings', 'impacts', 'impact-note',
    'conflict-field', 'conflict-policy', 'review-field', 'review', 'error', 'status', 'preview-button', 'stage-button', 'activate-button', 'export',
  ];
  const values = new Map(selectors.map(name => [`[data-knowledge-package-${name}]`, new Element()]));
  const personalDialog = new Element();
  const personalForm = new Element();
  const acknowledge = new Element();
  personalForm.elements = {namedItem: name => name === 'acknowledge' ? acknowledge : null};
  values.set('[data-knowledge-personal-export-dialog]', personalDialog);
  values.set('[data-knowledge-personal-export-form]', personalForm);
  values.set('[data-knowledge-personal-export-submit]', new Element());
  const shell = new Element();
  shell.querySelector = selector => values.get(selector) || null;
  shell.querySelectorAll = () => [];
  shell.ownerDocument = {
    createElement: () => new Element(),
    createTextNode: value => ({textContent: String(value)}),
  };
  shell.elements = values;
  return shell;
}

async function testControllerFlow() {
  const shell = fakeShell();
  const calls = [];
  let imported = null;
  const client = {
    previewPackage: async file => { calls.push(['preview', file]); return {preview}; },
    stagePackage: async (file, query) => { calls.push(['stage', file, query]); return {stage: {id: 'stage-1', expires_at: '2026-08-23T00:00:00Z'}}; },
    activatePackage: async id => { calls.push(['activate', id]); return {result: {chunk_id: 'chunk-1'}}; },
    discardPackage: async id => calls.push(['discard', id]),
  };
  const controller = new packages.Controller({shell, client, onImported: async result => { imported = result; }});
  const file = {name: 'tools.kknowledge', size: 123};
  controller.selectFile(file);
  await controller.previewSelected();
  assert.strictEqual(controller.preview, preview);
  assert.strictEqual(shell.querySelector('[data-knowledge-package-preview]').hidden, false);
  assert.strictEqual(shell.querySelector('[data-knowledge-package-conflict-field]').hidden, false);
  shell.querySelector('[data-knowledge-package-conflict-policy]').value = 'keep_both';
  shell.querySelector('[data-knowledge-package-review]').checked = true;
  await controller.stageSelected();
  assert.strictEqual(controller.stage.id, 'stage-1');
  assert.deepStrictEqual(calls[1][2], {conflict_policy: 'keep_both', review_approved: true});
  await controller.activateSelected();
  assert.deepStrictEqual(imported, {chunk_id: 'chunk-1'});
  assert.strictEqual(controller.stage, null);

  const exports = [];
  client.exportPackage = async (id, options) => {
    exports.push([id, options]);
    return {blob: new Blob(['personal']), filename: 'personal.kknowledge'};
  };
  controller.selectedChunk = {id: 'personal', title: 'About me', personal: true};
  assert.strictEqual(controller.requestExport(), true);
  assert.strictEqual(shell.querySelector('[data-knowledge-personal-export-dialog]').open, true);
  assert.strictEqual(shell.querySelector('[data-knowledge-personal-export-submit]').disabled, true);
  const acknowledge = shell.querySelector('[data-knowledge-personal-export-form]').elements.namedItem('acknowledge');
  acknowledge.checked = true;
  acknowledge.listeners.change();
  assert.strictEqual(shell.querySelector('[data-knowledge-personal-export-submit]').disabled, false);
  await controller.exportSelected(true);
  assert.deepStrictEqual(exports[0], ['personal', {channel: 'package-export', query: {include_personal: true}}]);
  assert.strictEqual(shell.querySelector('[data-knowledge-personal-export-dialog]').open, false);
}

testControllerFlow().catch(error => { console.error(error); process.exitCode = 1; });
