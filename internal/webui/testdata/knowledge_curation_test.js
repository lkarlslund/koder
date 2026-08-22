'use strict';

const assert = require('assert');
const curation = require('../assets/knowledge_curation.js');

const view = curation.candidateView({
  id: 'candidate-1', version: 3, status: 'pending_review', created_at: '2026-08-22T12:00:00Z',
  draft: {
    action: 'create_entry', chunk_id: 'chunk-1', reason: 'The workaround succeeded twice.', route: 'pending_review',
    review_reasons: ['Medical knowledge requires review.'], classification: {decision: 'review'},
    entry: {kind: 'fact', title: 'Use sfdisk', summary: 'Use sfdisk when fdisk is unavailable.', scope: {kind: 'global'}, risk: ['medical'], confidence: 0.9},
  },
});

assert.strictEqual(view.id, 'candidate-1');
assert.strictEqual(view.title, 'Use sfdisk');
assert.strictEqual(view.statusLabel, 'Pending Review');
assert.strictEqual(view.actionLabel, 'Create Entry');
assert.strictEqual(view.classification, 'Review');
assert.deepStrictEqual(view.risks, ['Medical']);
assert.deepStrictEqual(view.reasons, ['Medical knowledge requires review.']);
assert.strictEqual(view.canAccept, true);
assert.strictEqual(view.canReject, true);
assert.strictEqual(view.canUndo, false);

const applied = curation.candidateView({id: 'candidate-2', version: 2, status: 'applied', draft: {entry: {title: 'Accepted'}}});
assert.strictEqual(applied.canAccept, false);
assert.strictEqual(applied.canUndo, true);

console.log('knowledge curation tests passed');
