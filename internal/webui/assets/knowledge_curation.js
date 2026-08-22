(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeCuration = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  function text(value, fallback) {
    value = String(value === undefined || value === null ? '' : value).trim();
    return value || String(fallback || '');
  }

  function label(value) {
    return text(value).replace(/_/g, ' ').replace(/\b\w/g, character => character.toUpperCase());
  }

  function candidateView(candidate) {
    candidate = candidate && typeof candidate === 'object' ? candidate : {};
    const draft = candidate.draft && typeof candidate.draft === 'object' ? candidate.draft : {};
    const entry = draft.entry && typeof draft.entry === 'object' ? draft.entry : {};
    const scope = entry.scope && typeof entry.scope === 'object' ? entry.scope : {};
    const classification = draft.classification && typeof draft.classification === 'object' ? draft.classification : {};
    const risks = Array.isArray(entry.risk) ? entry.risk.map(label).filter(Boolean) : [];
    const reasons = Array.isArray(draft.review_reasons) ? draft.review_reasons.map(value => text(value)).filter(Boolean) : [];
    return {
      id: text(candidate.id), version: Math.max(0, Number(candidate.version) || 0),
      status: text(candidate.status), statusLabel: label(candidate.status), action: text(draft.action), actionLabel: label(draft.action),
      title: text(entry.title, 'Untitled proposal'), summary: text(entry.summary || entry.body, 'No summary supplied.'),
      reason: text(draft.reason), route: text(draft.route), chunkID: text(draft.chunk_id), targetEntryID: text(draft.target_entry_id),
      kind: label(entry.kind), scope: [label(scope.kind), text(scope.selector)].filter(Boolean).join(' · '),
      confidence: Number.isFinite(Number(entry.confidence)) ? Number(entry.confidence) : null,
      classification: label(classification.decision), risks, reasons,
      canAccept: candidate.status === 'pending_review', canReject: candidate.status === 'pending_review', canUndo: candidate.status === 'applied',
      decisionReason: text(candidate.decision_reason), createdAt: text(candidate.created_at),
    };
  }

  class Controller {
    constructor(options) {
      options = options || {};
      this.shell = options.shell;
      this.client = options.client;
      this.onChanged = typeof options.onChanged === 'function' ? options.onChanged : async () => {};
      if (!this.shell || !this.client) throw new TypeError('Knowledge curation controller requires shell and client');
      const find = selector => this.shell.querySelector(selector);
      this.openButton = find('[data-knowledge-curation-open]');
      this.badge = find('[data-knowledge-curation-badge]');
      this.dialog = find('[data-knowledge-curation-dialog]');
      this.filter = find('[data-knowledge-curation-filter]');
      this.refreshButton = find('[data-knowledge-curation-refresh]');
      this.status = find('[data-knowledge-curation-status]');
      this.list = find('[data-knowledge-curation-list]');
      this.rejectPanel = find('[data-knowledge-curation-reject]');
      this.rejectReason = find('[data-knowledge-curation-reject-reason]');
      this.rejectConfirm = find('[data-knowledge-curation-reject-confirm]');
      this.rejectCandidate = null;
      this.busy = false;
      this.bind();
      this.refreshCount();
    }

    bind() {
      if (this.openButton) this.openButton.addEventListener('click', () => this.open());
      if (this.refreshButton) this.refreshButton.addEventListener('click', () => this.load());
      if (this.filter) this.filter.addEventListener('change', () => this.load());
      for (const button of this.shell.querySelectorAll('[data-knowledge-curation-close]')) button.addEventListener('click', () => this.close());
      const rejectCancel = this.shell.querySelector('[data-knowledge-curation-reject-cancel]');
      if (rejectCancel) rejectCancel.addEventListener('click', () => this.cancelReject());
      if (this.rejectReason) this.rejectReason.addEventListener('input', () => {
        if (this.rejectConfirm) this.rejectConfirm.disabled = !text(this.rejectReason.value);
      });
      if (this.rejectConfirm) this.rejectConfirm.addEventListener('click', () => this.confirmReject());
      if (this.dialog) this.dialog.addEventListener('cancel', event => { event.preventDefault(); this.close(); });
    }

    open() {
      if (this.dialog && typeof this.dialog.showModal === 'function') this.dialog.showModal();
      else if (this.dialog) this.dialog.setAttribute('open', '');
      this.load();
    }

    close() {
      if (this.busy || !this.dialog) return;
      this.cancelReject();
      if (typeof this.dialog.close === 'function') this.dialog.close();
      else this.dialog.removeAttribute('open');
    }

    async refreshCount() {
      try {
        const response = await this.client.listCurationCandidates({status: 'pending_review', limit: 200}, {channel: 'curation-count'});
        const count = Array.isArray(response && response.candidates) ? response.candidates.length : 0;
        if (this.openButton) this.openButton.hidden = false;
        if (this.badge) {
          this.badge.hidden = count === 0;
          this.badge.textContent = count > 99 ? '99+' : String(count);
        }
        if (this.openButton) this.openButton.title = count ? `${count} knowledge proposal${count === 1 ? '' : 's'} need review` : 'Review proposed knowledge';
      } catch (error) {
        if (this.openButton && error && error.code !== 'unavailable') this.openButton.hidden = false;
      }
    }

    async load() {
      if (this.busy) return;
      this.setBusy(true);
      this.setStatus('Loading proposed knowledge…');
      if (this.list) this.list.replaceChildren();
      try {
        const status = text(this.filter && this.filter.value);
        const response = await this.client.listCurationCandidates({status, limit: 200}, {channel: 'curation-list'});
        const candidates = Array.isArray(response && response.candidates) ? response.candidates : [];
        this.render(candidates);
        this.setStatus(candidates.length ? `${candidates.length} proposal${candidates.length === 1 ? '' : 's'} shown.` : 'No proposals match this view.');
      } catch (error) {
        this.setStatus(text(error && error.message, 'The curation queue could not be loaded.'), error && error.requestID);
      } finally {
        this.setBusy(false);
      }
    }

    render(candidates) {
      if (!this.list) return;
      this.list.replaceChildren();
      for (const candidate of candidates) this.list.appendChild(this.card(candidate));
    }

    card(candidate) {
      const view = candidateView(candidate);
      const document = this.shell.ownerDocument;
      const card = document.createElement('article');
      card.className = 'knowledge-curation-card';
      const header = document.createElement('header');
      const heading = document.createElement('div');
      const title = document.createElement('h3');
      title.textContent = view.title;
      const meta = document.createElement('span');
      meta.textContent = [view.actionLabel, view.kind, view.scope, view.confidence === null ? '' : `${Math.round(view.confidence * 100)}% confidence`].filter(Boolean).join(' · ');
      heading.append(title, meta);
      const status = document.createElement('span');
      status.className = 'knowledge-curation-state is-' + view.status;
      status.textContent = view.statusLabel;
      header.append(heading, status);
      const summary = document.createElement('p');
      summary.textContent = view.summary;
      card.append(header, summary);
      if (view.reason) {
        const reason = document.createElement('p');
        reason.className = 'knowledge-curation-rationale';
        reason.textContent = `Why Koder proposed it: ${view.reason}`;
        card.appendChild(reason);
      }
      const warnings = [...view.reasons, ...view.risks.map(value => `Risk: ${value}`), view.classification === 'Review' ? 'Content classifier requested review.' : ''].filter(Boolean);
      if (warnings.length) {
        const list = document.createElement('ul');
        list.className = 'knowledge-curation-warnings';
        for (const warning of warnings) { const item = document.createElement('li'); item.textContent = warning; list.appendChild(item); }
        card.appendChild(list);
      }
      if (view.decisionReason) {
        const decision = document.createElement('p');
        decision.className = 'knowledge-curation-decision';
        decision.textContent = `Decision: ${view.decisionReason}`;
        card.appendChild(decision);
      }
      const footer = document.createElement('footer');
      if (view.canReject) footer.appendChild(this.actionButton('Reject', 'btn-outline-danger', () => this.beginReject(candidate)));
      if (view.canAccept) footer.appendChild(this.actionButton('Accept into Knowledge', 'btn-info', () => this.decide(candidate, 'accept')));
      if (view.canUndo) footer.appendChild(this.actionButton('Undo acceptance', 'btn-outline-warning', () => this.decide(candidate, 'undo')));
      card.appendChild(footer);
      return card;
    }

    actionButton(labelText, style, action) {
      const button = this.shell.ownerDocument.createElement('button');
      button.type = 'button';
      button.className = `btn btn-sm ${style}`;
      button.textContent = labelText;
      button.addEventListener('click', action);
      return button;
    }

    beginReject(candidate) {
      this.rejectCandidate = candidate;
      if (this.rejectPanel) this.rejectPanel.hidden = false;
      if (this.rejectReason) { this.rejectReason.value = ''; this.rejectReason.focus(); }
      if (this.rejectConfirm) this.rejectConfirm.disabled = true;
    }

    cancelReject() {
      this.rejectCandidate = null;
      if (this.rejectPanel) this.rejectPanel.hidden = true;
      if (this.rejectReason) this.rejectReason.value = '';
    }

    confirmReject() {
      const candidate = this.rejectCandidate;
      const reason = text(this.rejectReason && this.rejectReason.value);
      if (!candidate || !reason) return;
      this.cancelReject();
      this.decide(candidate, 'reject', reason);
    }

    async decide(candidate, action, reason) {
      if (this.busy) return;
      const view = candidateView(candidate);
      this.setBusy(true);
      this.setStatus(`${label(action)}ing proposal…`);
      try {
        await this.client.curationCandidateDecision(view.id, action, {expected_version: view.version, reason: text(reason)}, {channel: 'curation-decision'});
        await this.onChanged();
        await this.loadAfterDecision();
        await this.refreshCount();
      } catch (error) {
        this.setStatus(text(error && error.message, 'The curation decision could not be saved.'), error && error.requestID);
      } finally {
        this.setBusy(false);
      }
    }

    async loadAfterDecision() {
      this.busy = false;
      await this.load();
      this.busy = true;
    }

    setBusy(value) {
      this.busy = !!value;
      if (this.refreshButton) this.refreshButton.disabled = this.busy;
      if (this.filter) this.filter.disabled = this.busy;
      if (this.list) for (const button of this.list.querySelectorAll('button')) button.disabled = this.busy;
    }

    setStatus(message, requestID) {
      if (this.status) this.status.textContent = text(message) + (requestID ? ` Audit ID: ${requestID}` : '');
    }
  }

  return Object.freeze({Controller, candidateView});
});
