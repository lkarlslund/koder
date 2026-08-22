(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeBrowser = api;
  if (root && root.document) api.mount(root.document);
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const panes = Object.freeze(['sources', 'graph', 'inspector']);
  const states = Object.freeze(['shell', 'loading', 'ready', 'empty', 'unavailable', 'truncated', 'stale', 'error']);

  const statePresentations = Object.freeze({
    shell: {status: 'Preparing explorer', title: 'Building your knowledge map', detail: 'Connecting to the local Knowledge store.'},
    loading: {status: 'Loading knowledge', title: 'Loading your knowledge map', detail: 'Reading chunks and checking the local store.'},
    ready: {status: 'Knowledge ready', title: 'Knowledge is ready', detail: 'Choose a chunk or search to begin exploring.'},
    empty: {status: 'Knowledge is empty', title: 'No knowledge yet', detail: 'Chats can save useful discoveries here, or you can create a knowledge chunk.'},
    unavailable: {status: 'Knowledge unavailable', title: 'Knowledge is unavailable', detail: 'Koder is running, but the Knowledge store could not be reached.', retry: true},
    truncated: {status: 'Partial knowledge', title: 'Showing a bounded view', detail: 'Some results were omitted by a safety limit. Refine the view to continue.'},
    stale: {status: 'Refresh required', title: 'This view is stale', detail: 'Knowledge changed while this view was loading.', retry: true},
    error: {status: 'Knowledge error', title: 'Knowledge could not load', detail: 'Retry the request. The audit ID can help diagnose a repeated failure.', retry: true},
  });

  function normalizePane(value) {
    value = String(value || '').trim().toLowerCase();
    return panes.includes(value) ? value : 'graph';
  }

  function normalizeState(value) {
    value = String(value || '').trim().toLowerCase();
    return states.includes(value) ? value : 'error';
  }

  function stateForError(error) {
    const code = String(error && error.code || '').toLowerCase();
    if (code === 'stale' || code === 'invalid_cursor') return 'stale';
    if (code === 'unavailable' || code === 'network' || code === 'timeout') return 'unavailable';
    return 'error';
  }

  function presentationForState(state, detail) {
    state = normalizeState(state);
    const base = statePresentations[state];
    const safeDetail = String(detail || '').trim();
    return {...base, detail: safeDetail || base.detail};
  }

  function adjacentPane(current, direction) {
    const index = panes.indexOf(normalizePane(current));
    const offset = direction < 0 ? -1 : 1;
    return panes[(index + offset + panes.length) % panes.length];
  }

  function safeReturnPath(value) {
    value = String(value || '').trim();
    return /^\/s\/[A-Za-z0-9_-]+(?:\/c\/[A-Za-z0-9_-]+)?$/.test(value) ? value : '/';
  }

  function returnPathFromSearch(search) {
    try {
      return safeReturnPath(new URLSearchParams(String(search || '')).get('return'));
    } catch (_) {
      return '/';
    }
  }

  class BrowserApp {
    constructor(shell, client) {
      this.shell = shell;
      this.client = client;
      this.chunks = [];
      this.page = null;
      this.lastError = null;
      this.refreshButton = shell.querySelector('[data-knowledge-retry]');
      if (this.refreshButton) this.refreshButton.addEventListener('click', () => this.refresh());
    }

    setState(state, options) {
      options = options || {};
      state = normalizeState(state);
      const presentation = presentationForState(state, options.detail);
      this.shell.dataset.state = state;
      if (options.errorCode) this.shell.dataset.errorCode = String(options.errorCode);
      else delete this.shell.dataset.errorCode;
      const title = this.shell.querySelector('[data-knowledge-state-title]');
      const detail = this.shell.querySelector('[data-knowledge-state-detail]');
      const status = this.shell.querySelector('[data-knowledge-status-label]');
      const source = this.shell.querySelector('[data-knowledge-source-status]');
      const count = this.shell.querySelector('[data-knowledge-count]');
      const banner = this.shell.querySelector('[data-knowledge-banner]');
      if (title) title.textContent = presentation.title;
      if (detail) detail.textContent = presentation.detail;
      if (status) status.textContent = presentation.status;
      if (source) source.textContent = options.sourceDetail || presentation.detail;
      if (count) {
        const loaded = Number(options.count);
        count.textContent = Number.isFinite(loaded) ? String(loaded) + (options.hasMore ? '+' : '') : '—';
        count.setAttribute('aria-label', Number.isFinite(loaded) ? loaded + (options.hasMore ? ' or more chunks' : ' chunks') : 'Chunk count unavailable');
      }
      if (this.refreshButton) this.refreshButton.hidden = !presentation.retry;
      if (banner) {
        banner.hidden = !options.banner;
        banner.textContent = options.banner || '';
      }
      this.shell.dispatchEvent(new CustomEvent('koder:knowledge-browser-state', {detail: {state, ...options}}));
    }

    async refresh() {
      this.setState('loading');
      this.lastError = null;
      try {
        const response = await this.client.listChunks({sort: 'updated_at', descending: true, limit: 50}, {channel: 'initial-chunks'});
        this.chunks = Array.isArray(response && response.chunks) ? response.chunks : [];
        this.page = response && response.page || {};
        const hasMore = !!this.page.next_cursor;
        const sourceDetail = this.chunks.length
          ? this.chunks.length + (hasMore ? ' or more' : '') + ' knowledge chunks are ready to browse.'
          : '';
        if (this.page.truncated) {
          const reasons = Array.isArray(this.page.truncation_reasons) ? this.page.truncation_reasons.join(', ') : '';
          this.setState('truncated', {
            count: this.chunks.length, hasMore, sourceDetail,
            banner: reasons ? 'The server bounded this view: ' + reasons + '.' : 'The server bounded this view for safety.'
          });
        } else if (!this.chunks.length) {
          this.setState('empty', {count: 0, sourceDetail: 'There are no active knowledge chunks yet.'});
        } else {
          this.setState('ready', {count: this.chunks.length, hasMore, sourceDetail});
        }
      } catch (error) {
        if (error && (error.code === 'canceled' || error.code === 'stale_response')) return;
        this.lastError = error;
        const state = stateForError(error);
        const requestID = String(error && error.requestID || '');
        const detail = String(error && error.message || '').trim();
        this.setState(state, {
          detail, errorCode: error && error.code,
          sourceDetail: requestID ? detail + ' Audit ID: ' + requestID : detail,
          banner: state === 'stale' ? 'Knowledge changed. Refresh to load a consistent view.' : ''
        });
      }
    }

    destroy() {
      this.client.cancelAll();
    }
  }

  function mount(document) {
    const shell = document.getElementById('knowledge-browser');
    if (!shell || shell.dataset.shellMounted === 'true') return shell && shell.__koderKnowledgeApp;
    shell.dataset.shellMounted = 'true';
    const tabs = Array.from(shell.querySelectorAll('[data-knowledge-tab]'));
    const returnLink = shell.querySelector('[data-knowledge-return]');
    if (returnLink) returnLink.setAttribute('href', returnPathFromSearch(globalThis.location && globalThis.location.search));

    function selectPane(value, options) {
      const pane = normalizePane(value);
      shell.dataset.mobilePane = pane;
      for (const tab of tabs) {
        const selected = tab.dataset.knowledgeTab === pane;
        tab.setAttribute('aria-selected', selected ? 'true' : 'false');
        tab.tabIndex = selected ? 0 : -1;
        if (selected && options && options.focus) tab.focus();
      }
    }

    for (const tab of tabs) {
      tab.addEventListener('click', () => selectPane(tab.dataset.knowledgeTab));
      tab.addEventListener('keydown', event => {
        let target = '';
        if (event.key === 'ArrowLeft') target = adjacentPane(shell.dataset.mobilePane, -1);
        if (event.key === 'ArrowRight') target = adjacentPane(shell.dataset.mobilePane, 1);
        if (event.key === 'Home') target = panes[0];
        if (event.key === 'End') target = panes[panes.length - 1];
        if (!target) return;
        event.preventDefault();
        selectPane(target, {focus: true});
      });
    }
    selectPane(shell.dataset.mobilePane);
    if (!globalThis.KoderKnowledgeAPI) {
      const unavailable = new BrowserApp(shell, {cancelAll() {}});
      unavailable.setState('error', {detail: 'The Knowledge API client did not load.'});
      shell.__koderKnowledgeApp = unavailable;
      return unavailable;
    }
    try {
      const client = globalThis.KoderKnowledgeAPI.fromPageConfig(globalThis.KODER_KNOWLEDGE_CONFIG || {});
      const app = new BrowserApp(shell, client);
      shell.__koderKnowledgeApp = app;
      app.refresh();
      return app;
    } catch (error) {
      const unavailable = new BrowserApp(shell, {cancelAll() {}});
      unavailable.setState('error', {detail: String(error && error.message || error)});
      shell.__koderKnowledgeApp = unavailable;
      return unavailable;
    }
  }

  return Object.freeze({panes, states, BrowserApp, normalizePane, normalizeState, stateForError, presentationForState, adjacentPane, safeReturnPath, returnPathFromSearch, mount});
});
