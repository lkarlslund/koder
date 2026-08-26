'use strict';

const assert = require('node:assert/strict');
const {Tracker} = require('../assets/memory_live.js');

const tracker = new Tracker();
assert.equal(tracker.observe({stream_id: 'stream-a', sequence: 1}).action, 'ignore');
assert.equal(tracker.reset({stream_id: 'stream-a', sequence: 4}).action, 'ready');
assert.equal(tracker.observe({stream_id: 'stream-a', sequence: 5, kind: 'updated'}).action, 'apply');
assert.equal(tracker.observe({stream_id: 'stream-a', sequence: 5}).reason, 'duplicate_or_stale');

let result = tracker.observe({stream_id: 'stream-a', sequence: 7});
assert.equal(result.action, 'refetch');
assert.equal(result.reason, 'sequence_gap');
assert.equal(tracker.observe({stream_id: 'stream-a', sequence: 8}).action, 'ignore');

assert.equal(tracker.reset({stream_id: 'stream-a', sequence: 8}).action, 'ready');
result = tracker.observeCheckpoint({stream_id: 'stream-a', sequence: 9});
assert.equal(result.action, 'refetch');
assert.equal(result.reason, 'checkpoint_ahead');

assert.equal(tracker.reset({stream_id: 'stream-a', sequence: 9}).action, 'ready');
result = tracker.observe({stream_id: 'stream-b', sequence: 1});
assert.equal(result.action, 'refetch');
assert.equal(result.reason, 'stream_changed');

assert.equal(tracker.reset({stream_id: '', sequence: -1}).reason, 'invalid_checkpoint');
