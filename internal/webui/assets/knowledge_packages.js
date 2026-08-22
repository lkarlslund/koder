(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgePackages = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  function text(value, fallback) {
    value = String(value === undefined || value === null ? '' : value).trim();
    return value || String(fallback || '');
  }

  function displayLabel(value) {
    return text(value).replace(/_/g, ' ').replace(/\b\w/g, character => character.toUpperCase());
  }

  function previewView(preview) {
    preview = preview || {};
    const summary = preview.summary || {};
    const classification = preview.classification || {};
    return {
      title: text(preview.chunk_title, 'Untitled knowledge'),
      packageLabel: [text(preview.package && preview.package.id), text(preview.package && preview.package.version)].filter(Boolean).join(' · '),
      publisher: text(preview.publisher && (preview.publisher.name || preview.publisher.id), 'Unknown publisher'),
      license: text(preview.license && preview.license.name, 'No asserted license'),
      signature: displayLabel(preview.signature_state || 'unsigned'),
      publisherTrust: displayLabel(preview.publisher_trust && preview.publisher_trust.state || 'unknown'),
      classification: displayLabel(classification.decision || 'allow'),
      findings: Array.isArray(classification.findings) ? classification.findings.slice(0, 20) : [],
      counts: [
        ['Add', Number(summary.additions) || 0], ['Unchanged', Number(summary.unchanged) || 0],
        ['Conflicts', Number(summary.conflicts) || 0], ['Missing', Number(summary.missing_dependencies) || 0],
        ['Blockers', Number(summary.blockers) || 0],
      ],
      conflicts: Number(summary.conflicts) || 0,
      blockers: Number(summary.blockers) || 0,
      reviewRequired: !!preview.review_required,
      ready: !!preview.ready_to_stage,
      impacts: Array.isArray(preview.impacts) ? preview.impacts.slice() : [],
    };
  }

  function importantImpacts(values, limit) {
    values = Array.isArray(values) ? values : [];
    limit = Math.max(1, Number(limit) || 100);
    return values.slice().sort((left, right) => {
      const score = value => value && value.blocking ? 3 : value && value.action === 'conflict' ? 2 : value && value.action === 'missing_dependency' ? 1 : 0;
      return score(right) - score(left);
    }).slice(0, limit);
  }

  class Controller {
    constructor(options) {
      options = options || {};
      this.shell = options.shell;
      this.client = options.client;
      this.onImported = typeof options.onImported === 'function' ? options.onImported : async () => {};
      if (!this.shell || !this.client) throw new TypeError('Knowledge package controller requires shell and client');
      const find = selector => this.shell.querySelector(selector);
      this.openButton = find('[data-knowledge-package-open]');
      this.dialog = find('[data-knowledge-package-dialog]');
      this.fileInput = find('[data-knowledge-package-file]');
      this.fileName = find('[data-knowledge-package-file-name]');
      this.previewPanel = find('[data-knowledge-package-preview]');
      this.previewTitle = find('[data-knowledge-package-preview-title]');
      this.previewMeta = find('[data-knowledge-package-preview-meta]');
      this.summary = find('[data-knowledge-package-summary]');
	  this.findings = find('[data-knowledge-package-findings]');
      this.impacts = find('[data-knowledge-package-impacts]');
      this.impactNote = find('[data-knowledge-package-impact-note]');
      this.conflictField = find('[data-knowledge-package-conflict-field]');
      this.conflictSelect = find('[data-knowledge-package-conflict-policy]');
      this.reviewField = find('[data-knowledge-package-review-field]');
      this.reviewInput = find('[data-knowledge-package-review]');
      this.error = find('[data-knowledge-package-error]');
      this.status = find('[data-knowledge-package-status]');
      this.previewButton = find('[data-knowledge-package-preview-button]');
      this.stageButton = find('[data-knowledge-package-stage-button]');
      this.activateButton = find('[data-knowledge-package-activate-button]');
      this.exportButton = find('[data-knowledge-package-export]');
	  this.exportStatus = find('[data-knowledge-chunk-mutation-status]');
      this.selectedChunk = null;
      this.file = null;
      this.preview = null;
      this.stage = null;
      this.busy = false;
      this.bind();
    }

    bind() {
      if (this.openButton) this.openButton.addEventListener('click', () => this.open());
      if (this.fileInput) this.fileInput.addEventListener('change', () => this.selectFile(this.fileInput.files && this.fileInput.files[0]));
      if (this.previewButton) this.previewButton.addEventListener('click', () => this.previewSelected());
      if (this.stageButton) this.stageButton.addEventListener('click', () => this.stageSelected());
      if (this.activateButton) this.activateButton.addEventListener('click', () => this.activateSelected());
      if (this.exportButton) this.exportButton.addEventListener('click', () => this.exportSelected());
      for (const button of this.shell.querySelectorAll('[data-knowledge-package-cancel]')) button.addEventListener('click', () => this.close());
      if (this.dialog) {
        this.dialog.addEventListener('cancel', event => { event.preventDefault(); this.close(); });
        this.dialog.addEventListener('close', () => this.discardStage());
      }
      this.shell.addEventListener('koder:knowledge-inspected', event => {
        const detail = event && event.detail || {};
        this.selectedChunk = detail.objectKind === 'chunk' ? {id: text(detail.id), title: text(detail.label, detail.id)} : null;
      });
    }

    open() {
      this.reset();
      if (this.dialog && typeof this.dialog.showModal === 'function') this.dialog.showModal();
      if (this.fileInput) this.fileInput.focus();
    }

    close() {
      if (this.busy) return;
      this.discardStage();
      if (this.dialog && this.dialog.open) this.dialog.close();
    }

    reset() {
      this.discardStage();
      this.file = null;
      this.preview = null;
      if (this.fileInput) this.fileInput.value = '';
      if (this.fileName) this.fileName.textContent = 'Choose a .kknowledge package from this device.';
      if (this.previewPanel) this.previewPanel.hidden = true;
      if (this.conflictField) this.conflictField.hidden = true;
      if (this.reviewField) this.reviewField.hidden = true;
      if (this.conflictSelect) this.conflictSelect.value = '';
      if (this.reviewInput) this.reviewInput.checked = false;
      this.setError('');
      this.setStatus('Nothing is imported until you review and activate a staged package.');
      this.syncButtons();
    }

    selectFile(file) {
      this.discardStage();
      this.file = file || null;
      this.preview = null;
      if (this.previewPanel) this.previewPanel.hidden = true;
      if (this.fileName) this.fileName.textContent = this.file
        ? `${text(this.file.name, 'Selected package')} · ${Math.max(0, Number(this.file.size) || 0).toLocaleString()} bytes`
        : 'Choose a .kknowledge package from this device.';
      this.setError('');
      this.setStatus(this.file ? 'Preview validates the archive without changing Knowledge.' : 'Nothing is imported until you review and activate a staged package.');
      this.syncButtons();
    }

    async previewSelected() {
      if (!this.file || this.busy) return;
      this.setBusy(true, 'Validating and comparing package…');
      try {
        const response = await this.client.previewPackage(this.file, {channel: 'package-import'});
        this.preview = response && response.preview || null;
        if (!this.preview) throw new Error('Koder returned no package preview.');
        this.renderPreview(this.preview);
        this.setStatus('Preview complete. Review every warning before staging.');
      } catch (error) {
        this.setError(error && error.message || 'Knowledge could not preview this package.', error && error.requestID);
      } finally {
        this.setBusy(false);
      }
    }

    renderPreview(preview) {
      const view = previewView(preview);
      if (this.previewPanel) this.previewPanel.hidden = false;
      if (this.previewTitle) this.previewTitle.textContent = view.title;
      if (this.previewMeta) this.previewMeta.textContent = [view.packageLabel, view.publisher, view.license, view.signature + ' signature', view.publisherTrust + ' publisher', view.classification].filter(Boolean).join(' · ');
      if (this.summary) {
        this.summary.replaceChildren();
        for (const [label, count] of view.counts) {
          const item = this.shell.ownerDocument.createElement('span');
          const strong = this.shell.ownerDocument.createElement('strong');
          strong.textContent = String(count);
          item.append(strong, this.shell.ownerDocument.createTextNode(label));
          if ((label === 'Blockers' || label === 'Conflicts') && count) item.className = 'is-warning';
          this.summary.appendChild(item);
        }
      }
	  if (this.findings) {
		this.findings.replaceChildren();
		if (!view.findings.length) this.findings.textContent = 'No prohibited findings reported.';
		for (const finding of view.findings) {
		  const item = this.shell.ownerDocument.createElement('span');
		  item.textContent = finding && typeof finding === 'object'
			? [finding.kind, finding.field, finding.rule].map(value => text(value)).filter(Boolean).join(' · ')
			: text(finding, 'Review required');
		  this.findings.appendChild(item);
		}
	  }
      if (this.impacts) {
        this.impacts.replaceChildren();
        const shown = importantImpacts(view.impacts, 100);
        for (const impact of shown) {
          const row = this.shell.ownerDocument.createElement('li');
          const heading = this.shell.ownerDocument.createElement('strong');
          heading.textContent = `${displayLabel(impact.action)} · ${displayLabel(impact.kind)} ${text(impact.id)}`;
          const reason = this.shell.ownerDocument.createElement('span');
          reason.textContent = text(impact.reason);
          if (impact.blocking) row.className = 'is-blocking';
          row.append(heading, reason);
          this.impacts.appendChild(row);
        }
        if (this.impactNote) this.impactNote.textContent = view.impacts.length > shown.length ? `Showing the 100 most important of ${view.impacts.length} impacts.` : `${view.impacts.length} package impacts checked.`;
      }
      if (this.conflictField) this.conflictField.hidden = view.conflicts === 0;
      if (this.reviewField) this.reviewField.hidden = !view.reviewRequired;
      if (this.stageButton) this.stageButton.hidden = false;
      if (this.activateButton) this.activateButton.hidden = true;
      if (view.blockers) this.setError(`${view.blockers} blocking package impact${view.blockers === 1 ? '' : 's'} must be resolved before import.`);
      else this.setError('');
      this.syncButtons();
    }

    async stageSelected() {
      if (!this.file || !this.preview || this.busy) return;
      const view = previewView(this.preview);
      const policy = text(this.conflictSelect && this.conflictSelect.value);
      if (view.conflicts && !policy) {
        this.setError('Choose how Koder should resolve package conflicts.');
        return;
      }
      if (view.reviewRequired && !(this.reviewInput && this.reviewInput.checked)) {
        this.setError('Confirm that you reviewed the sensitive-content findings before staging.');
        return;
      }
      this.setBusy(true, 'Creating a short-lived, private import stage…');
      try {
        const query = {};
        if (policy) query.conflict_policy = policy;
        if (view.reviewRequired) query.review_approved = true;
        const response = await this.client.stagePackage(this.file, query, {channel: 'package-import'});
        this.stage = response && response.stage || null;
        if (!this.stage) throw new Error('Koder returned no package stage.');
        if (this.stageButton) this.stageButton.hidden = true;
        if (this.activateButton) this.activateButton.hidden = false;
        this.setError('');
        this.setStatus(`Staged until ${new Date(this.stage.expires_at).toLocaleString()}. Activation publishes all changes atomically.`);
      } catch (error) {
        this.setError(error && error.message || 'Knowledge could not stage this package.', error && error.requestID);
      } finally {
        this.setBusy(false);
      }
    }

    async activateSelected() {
      if (!this.stage || this.busy) return;
      this.setBusy(true, 'Activating package atomically…');
      try {
        const response = await this.client.activatePackage(this.stage.id, {channel: 'package-import'});
        const result = response && response.result || {};
        this.stage = null;
        this.setError('');
        this.setStatus('Package imported successfully.');
        await this.onImported(result);
        if (this.dialog && this.dialog.open) this.dialog.close();
      } catch (error) {
        this.setError(error && error.message || 'Knowledge could not activate this package.', error && error.requestID);
      } finally {
        this.setBusy(false);
      }
    }

    async discardStage() {
      const stage = this.stage;
      this.stage = null;
      if (!stage || !stage.id || typeof this.client.discardPackage !== 'function') return;
      try { await this.client.discardPackage(stage.id, {channel: 'package-discard'}); } catch (_) {}
    }

    async exportSelected() {
      if (!this.selectedChunk || this.busy) return;
      this.setBusy(true);
      if (this.exportButton) this.exportButton.textContent = 'Exporting…';
      try {
        const result = await this.client.exportPackage(this.selectedChunk.id, {channel: 'package-export'});
        const url = URL.createObjectURL(result.blob);
        const link = this.shell.ownerDocument.createElement('a');
        link.href = url;
        link.download = result.filename;
        link.hidden = true;
        this.shell.appendChild(link);
        link.click();
        link.remove();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
		if (this.exportStatus) this.exportStatus.textContent = `Exported ${result.filename}.`;
      } catch (error) {
        if (this.exportButton) this.exportButton.title = text(error && error.message, 'Knowledge could not export this chunk.');
		if (this.exportStatus) this.exportStatus.textContent = text(error && error.message, 'Knowledge could not export this chunk.');
      } finally {
        if (this.exportButton) this.exportButton.innerHTML = '<i class="bi bi-download" aria-hidden="true"></i> Export';
        this.setBusy(false);
      }
    }

    setBusy(value, status) {
      this.busy = !!value;
      if (status) this.setStatus(status);
      this.syncButtons();
    }

    syncButtons() {
      if (this.previewButton) this.previewButton.disabled = this.busy || !this.file;
      if (this.stageButton) this.stageButton.disabled = this.busy || !this.preview || previewView(this.preview).blockers > 0;
      if (this.activateButton) this.activateButton.disabled = this.busy || !this.stage;
      if (this.openButton) this.openButton.disabled = this.busy;
      if (this.exportButton) this.exportButton.disabled = this.busy || !this.selectedChunk;
    }

    setError(message, requestID) {
      if (!this.error) return;
      message = text(message);
      this.error.hidden = !message;
      this.error.textContent = message ? message + (requestID ? ` Audit ID: ${requestID}` : '') : '';
    }

    setStatus(message) {
      if (this.status) this.status.textContent = text(message);
    }
  }

  return Object.freeze({Controller, previewView, importantImpacts});
});
