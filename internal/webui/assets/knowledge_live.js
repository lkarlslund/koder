(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeLive = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  function checkpoint(value) {
    const streamID = String((value && value.stream_id) || '').trim();
    const sequence = Number(value && value.sequence);
    if (!streamID || !Number.isSafeInteger(sequence) || sequence < 0) return null;
    return {streamID, sequence};
  }

  class Tracker {
    constructor() {
      this.streamID = '';
      this.sequence = 0;
      this.refetchRequired = true;
      this.reason = 'baseline_missing';
    }

    reset(value) {
      const next = checkpoint(value);
      if (!next) return this.invalidate('invalid_checkpoint');
      this.streamID = next.streamID;
      this.sequence = next.sequence;
      this.refetchRequired = false;
      this.reason = '';
      return {action: 'ready', checkpoint: this.current()};
    }

    invalidate(reason) {
      const nextReason = String(reason || 'refetch_required');
      const changed = !this.refetchRequired || this.reason !== nextReason;
      this.refetchRequired = true;
      this.reason = nextReason;
      return {action: changed ? 'refetch' : 'ignore', reason: this.reason, checkpoint: this.current()};
    }

    observe(event) {
      const next = checkpoint(event);
      if (!next) return this.invalidate('invalid_event');
      if (this.refetchRequired) return {action: 'ignore', reason: this.reason, checkpoint: this.current()};
      if (next.streamID !== this.streamID) return this.invalidate('stream_changed');
      if (next.sequence <= this.sequence) return {action: 'ignore', reason: 'duplicate_or_stale', checkpoint: this.current()};
      if (next.sequence !== this.sequence + 1) return this.invalidate('sequence_gap');
      this.sequence = next.sequence;
      return {action: 'apply', event, checkpoint: this.current()};
    }

    observeCheckpoint(value) {
      const next = checkpoint(value);
      if (!next) return this.invalidate('invalid_checkpoint');
      if (this.refetchRequired) return {action: 'ignore', reason: this.reason, checkpoint: this.current()};
      if (next.streamID !== this.streamID) return this.invalidate('stream_changed');
      if (next.sequence > this.sequence) return this.invalidate('checkpoint_ahead');
      return {action: 'ignore', reason: 'current', checkpoint: this.current()};
    }

    current() {
      return {stream_id: this.streamID, sequence: this.sequence, refetch_required: this.refetchRequired};
    }
  }

  return {Tracker};
});
