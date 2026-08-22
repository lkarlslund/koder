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

  function chatSelectionFromSearch(search) {
    const path = returnPathFromSearch(search);
    const match = path.match(/^\/s\/([A-Za-z0-9_-]+)\/c\/([A-Za-z0-9_-]+)$/);
    return match ? {sessionID: match[1], chatID: match[2], path} : null;
  }

  const browserURLKeys = Object.freeze(['query', 'kind', 'scope_kind', 'state', 'tag', 'object_kind', 'id']);
  const allowedChunkKinds = new Set(['reference', 'personal', 'project', 'environment']);
  const allowedScopeKinds = new Set(['global', 'personal', 'project', 'session', 'environment']);
  const allowedChunkStates = new Set(['draft', 'archived']);
  const allowedObjectKinds = new Set(['chunk', 'entry', 'link', 'evidence']);

  function boundedText(value, limit) {
    value = String(value || '').trim();
    return value.length > limit ? value.slice(0, limit) : value;
  }

  function allowedValue(value, allowed) {
    value = String(value || '').trim().toLowerCase();
    return allowed.has(value) ? value : '';
  }

  function browserStateFromSearch(search) {
    let params;
    try { params = new URLSearchParams(String(search || '')); } catch (_) { params = new URLSearchParams(); }
    const objectKind = allowedValue(params.get('object_kind'), allowedObjectKinds);
    const id = boundedText(params.get('id'), 160);
    return {
      query: boundedText(params.get('query'), 500),
      kind: allowedValue(params.get('kind'), allowedChunkKinds),
      scopeKind: allowedValue(params.get('scope_kind'), allowedScopeKinds),
      state: allowedValue(params.get('state'), allowedChunkStates),
      tag: boundedText(params.get('tag'), 80),
      objectKind: objectKind && /^[A-Za-z0-9_-]+$/.test(id) ? objectKind : '',
      id: objectKind && /^[A-Za-z0-9_-]+$/.test(id) ? id : '',
    };
  }

  function searchForBrowserState(search, state) {
    const params = new URLSearchParams(String(search || ''));
    for (const key of browserURLKeys) params.delete(key);
    state = {...browserStateFromSearch(''), ...(state || {})};
    if (boundedText(state.query, 500)) params.set('query', boundedText(state.query, 500));
    if (allowedValue(state.kind, allowedChunkKinds)) params.set('kind', allowedValue(state.kind, allowedChunkKinds));
    if (allowedValue(state.scopeKind, allowedScopeKinds)) params.set('scope_kind', allowedValue(state.scopeKind, allowedScopeKinds));
    if (allowedValue(state.state, allowedChunkStates)) params.set('state', allowedValue(state.state, allowedChunkStates));
    if (boundedText(state.tag, 80)) params.set('tag', boundedText(state.tag, 80));
    const objectKind = allowedValue(state.objectKind, allowedObjectKinds);
    const id = boundedText(state.id, 160);
    if (objectKind && /^[A-Za-z0-9_-]+$/.test(id)) {
      params.set('object_kind', objectKind);
      params.set('id', id);
    }
    const encoded = params.toString();
    return encoded ? '?' + encoded : '';
  }

  function displayLabel(value) {
    value = String(value || '').replaceAll('_', ' ').trim();
    return value ? value.charAt(0).toUpperCase() + value.slice(1) : '';
  }

  function plainTextLabel(value, limit) {
    value = String(value || '')
      .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
      .replace(/<[^>]*>/g, ' ')
      .replace(/[`*_~#>|]/g, ' ')
      .replace(/\s+/g, ' ')
      .trim();
    limit = Math.max(1, Number(limit) || 120);
    return value.length > limit ? value.slice(0, Math.max(1, limit - 1)).trimEnd() + '…' : value;
  }

  function graphSnapshotRequest(objectKind, id) {
    objectKind = String(objectKind || '').trim().toLowerCase();
    id = String(id || '').trim();
    if (!['chunk', 'entry'].includes(objectKind) || !/^[A-Za-z0-9_-]+$/.test(id)) return null;
    return {root: {kind: objectKind, id}, max_depth: 2, max_nodes: 200, max_edges: 400, time_limit_ms: 2500};
  }

  function graphExpansionRequest(objectKind, id, direction) {
    objectKind = String(objectKind || '').trim().toLowerCase();
    id = String(id || '').trim();
    direction = String(direction || '').trim().toLowerCase();
    if (!['chunk', 'entry'].includes(objectKind) || !/^[A-Za-z0-9_-]+$/.test(id) || !['incoming', 'outgoing'].includes(direction)) return null;
    return {root: {kind: objectKind, id}, direction, max_depth: 1, max_nodes: 100, max_edges: 200, time_limit_ms: 2000};
  }

  function graphDebugEnabled(search) {
    try { return new URLSearchParams(String(search || '')).get('graph_debug') === '1'; } catch (_) { return false; }
  }

  function supportsWebGL(document) {
    try {
      const canvas = document && document.createElement && document.createElement('canvas');
      return !!(canvas && (canvas.getContext('webgl2') || canvas.getContext('webgl')));
    } catch (_) {
      return false;
    }
  }

  function graphEnvironment(document, matchMedia) {
    if (typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches) {
      return {available: false, reason: 'reduced_motion'};
    }
    return supportsWebGL(document) ? {available: true, reason: ''} : {available: false, reason: 'webgl_unavailable'};
  }

  function sanitizedMarkdownHTML(value, markedAPI, purifyAPI) {
    if (!markedAPI || typeof markedAPI.parse !== 'function' || !purifyAPI || typeof purifyAPI.sanitize !== 'function') return '';
    const rendered = markedAPI.parse(String(value || ''), {gfm: true, breaks: false});
    return purifyAPI.sanitize(rendered, {
      USE_PROFILES: {html: true},
      ALLOW_DATA_ATTR: false,
      FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed', 'form', 'input', 'button', 'svg', 'math'],
      FORBID_ATTR: ['style', 'srcset'],
    });
  }

  function formatTimestamp(value) {
    const date = new Date(String(value || ''));
    return Number.isNaN(date.getTime()) ? '' : date.toLocaleString();
  }

  class BrowserApp {
    constructor(shell, client, options) {
      options = options || {};
      this.shell = shell;
      this.client = client;
      this.graphAdapter = options.graphAdapter || null;
      this.graphRenderer = options.graphRenderer || null;
      this.graphViewport = options.graphViewport || null;
      this.graphLayout = options.graphLayout || null;
      this.graphView = options.graphView || null;
      this.chunks = [];
      this.matches = [];
      this.page = null;
      this.lastError = null;
      this.resultMode = 'chunks';
      this.urlState = browserStateFromSearch(globalThis.location && globalThis.location.search);
      this.chatSelection = chatSelectionFromSearch(globalThis.location && globalThis.location.search);
      this.inspectedObject = null;
      this.searchTimer = 0;
      this.graphRefetchTimer = 0;
      this.refreshButton = shell.querySelector('[data-knowledge-retry]');
      this.searchForm = shell.querySelector('[data-knowledge-search-form]');
      this.searchInput = shell.querySelector('[data-knowledge-search]');
      this.searchClear = shell.querySelector('[data-knowledge-search-clear]');
      this.resultList = shell.querySelector('[data-knowledge-results]');
      this.loadMoreButton = shell.querySelector('[data-knowledge-load-more]');
      this.graphFitButton = shell.querySelector('[data-knowledge-graph-fit]');
      this.graphCenterButton = shell.querySelector('[data-knowledge-graph-center]');
      this.graphSelectionCount = shell.querySelector('[data-knowledge-selection-count]');
      this.graphExpandActions = shell.querySelector('[data-knowledge-expand-actions]');
      this.graphExpandIncoming = shell.querySelector('[data-knowledge-expand-incoming]');
      this.graphExpandOutgoing = shell.querySelector('[data-knowledge-expand-outgoing]');
      this.graphExpandStatus = shell.querySelector('[data-knowledge-expand-status]');
      this.graphHideButton = shell.querySelector('[data-knowledge-view-hide]');
      this.graphIsolateButton = shell.querySelector('[data-knowledge-view-isolate]');
      this.graphRevealButton = shell.querySelector('[data-knowledge-view-reveal]');
      this.graphUndoButton = shell.querySelector('[data-knowledge-view-undo]');
      this.filters = Object.fromEntries(Array.from(shell.querySelectorAll('[data-knowledge-filter]')).map(input => [input.dataset.knowledgeFilter, input]));
      this.inspector = {
        empty: shell.querySelector('[data-knowledge-inspector-empty]'),
        loading: shell.querySelector('[data-knowledge-inspector-loading]'),
        error: shell.querySelector('[data-knowledge-inspector-error]'),
        content: shell.querySelector('[data-knowledge-inspector-content]'),
        kind: shell.querySelector('[data-knowledge-inspector-kind]'),
        title: shell.querySelector('[data-knowledge-inspector-title]'),
        summary: shell.querySelector('[data-knowledge-inspector-summary]'),
        badges: shell.querySelector('[data-knowledge-inspector-badges]'),
        meta: shell.querySelector('[data-knowledge-inspector-meta]'),
        markdown: shell.querySelector('[data-knowledge-inspector-markdown]'),
        sendButton: shell.querySelector('[data-knowledge-send-chat]'),
        sendStatus: shell.querySelector('[data-knowledge-send-status]'),
      };
      if (this.refreshButton) this.refreshButton.addEventListener('click', () => this.refresh());
      if (this.searchForm) this.searchForm.addEventListener('submit', event => {
        event.preventDefault();
        this.applyControlState({replace: false});
      });
      if (this.searchInput) this.searchInput.addEventListener('input', () => {
        if (this.searchClear) this.searchClear.hidden = !this.searchInput.value;
        clearTimeout(this.searchTimer);
        this.searchTimer = setTimeout(() => this.applyControlState({replace: true}), 260);
      });
      if (this.searchClear) this.searchClear.addEventListener('click', () => {
        this.searchInput.value = '';
        this.applyControlState({replace: false});
        this.searchInput.focus();
      });
      for (const input of Object.values(this.filters)) input.addEventListener('change', () => this.applyControlState({replace: false}));
      if (this.loadMoreButton) this.loadMoreButton.addEventListener('click', () => this.loadMore());
      if (this.graphFitButton) this.graphFitButton.addEventListener('click', () => this.graphViewport && this.graphViewport.fit());
      if (this.graphCenterButton) this.graphCenterButton.addEventListener('click', () => {
        const key = this.urlState.objectKind && this.urlState.id ? `${this.urlState.objectKind}:${this.urlState.id}` : '';
        if (this.graphViewport && key) this.graphViewport.centerNode(key);
      });
      if (this.graphExpandIncoming) this.graphExpandIncoming.addEventListener('click', () => this.expandGraph('incoming'));
      if (this.graphExpandOutgoing) this.graphExpandOutgoing.addEventListener('click', () => this.expandGraph('outgoing'));
      if (this.graphHideButton) this.graphHideButton.addEventListener('click', () => this.applyGraphViewAction('hide'));
      if (this.graphIsolateButton) this.graphIsolateButton.addEventListener('click', () => this.applyGraphViewAction('isolate'));
      if (this.graphRevealButton) this.graphRevealButton.addEventListener('click', () => this.applyGraphViewAction('reveal'));
      if (this.graphUndoButton) this.graphUndoButton.addEventListener('click', () => this.applyGraphViewAction('undo'));
      if (this.inspector.sendButton) this.inspector.sendButton.addEventListener('click', () => this.sendSelectionToChat());
      this.onPopState = () => {
        this.urlState = browserStateFromSearch(globalThis.location && globalThis.location.search);
        this.syncControls();
        this.refresh();
      };
      globalThis.addEventListener('popstate', this.onPopState);
      this.onGraphPane = event => {
        if (event && event.detail && event.detail.pane === 'graph' && this.graphViewport) {
          setTimeout(() => this.graphViewport && this.graphViewport.fit({animate: false}), 0);
        }
      };
      this.shell.addEventListener('koder:knowledge-pane', this.onGraphPane);
      if (this.graphAdapter) {
        this.graphUnsubscribe = this.graphAdapter.subscribe(event => {
          if (event.type === 'selection') this.syncGraphSelection(event.detail);
          if (event.type === 'refetch' && !this.graphRefetchTimer) {
            this.graphRefetchTimer = setTimeout(() => {
              this.graphRefetchTimer = 0;
              this.loadGraphSelection();
            }, 0);
          }
        });
      }
      if (this.graphView) {
        this.graphViewUnsubscribe = this.graphView.subscribe(() => {
          if (this.graphRenderer) this.graphRenderer.scheduleRefresh(false);
          this.updateGraphViewControls();
        });
      }
      if (this.graphLayout) {
        this.graphLayoutUnsubscribe = this.graphLayout.subscribe(event => {
          const status = this.shell.querySelector('[data-knowledge-status-label]');
          if (!status) return;
          if (event.phase === 'start' || event.phase === 'progress') {
            const percent = event.total ? Math.round(event.completed / event.total * 100) : 0;
            status.textContent = event.phase === 'start' ? 'Arranging graph' : `Arranging graph · ${percent}%`;
          }
          if (event.phase === 'ready') {
            status.textContent = 'Graph ready';
            if (this.graphViewport) this.graphViewport.fit({animate: false});
          }
          if (event.phase === 'error') status.textContent = 'Layout unavailable';
        });
      }
      if (this.graphRenderer) {
        this.graphInteractionUnsubscribe = this.graphRenderer.subscribe(event => {
          if (event.type === 'node' && event.detail && event.detail.key) this.selectGraphObject('node', event.detail.key, event.detail);
          if (event.type === 'edge' && event.detail && event.detail.key) this.selectGraphObject('edge', event.detail.key, event.detail);
          if (event.type === 'boxselect' && event.detail) this.selectGraphObjects(event.detail.keys, event.detail.additive);
          if (event.type === 'background' && !(event.detail && event.detail.additive)) this.clearGraphSelection();
          if (event.type === 'dragstart' && this.graphLayout) this.graphLayout.stop('node_drag');
          if (event.type === 'pin') {
            const status = this.shell.querySelector('[data-knowledge-status-label]');
            if (status) status.textContent = event.detail && event.detail.pinned ? 'Node pinned locally' : 'Node released';
            if (event.detail && !event.detail.pinned && this.graphLayout) this.graphLayout.request();
          }
        });
      }
      this.syncControls();
      this.updateGraphViewControls();
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
        const noun = options.countLabel || 'chunks';
        count.setAttribute('aria-label', Number.isFinite(loaded) ? loaded + (options.hasMore ? ' or more ' : ' ') + noun : 'Knowledge count unavailable');
      }
      if (this.refreshButton) this.refreshButton.hidden = !presentation.retry;
      if (banner) {
        banner.hidden = !options.banner;
        banner.textContent = options.banner || '';
      }
      this.shell.dispatchEvent(new CustomEvent('koder:knowledge-browser-state', {detail: {state, ...options}}));
    }

    syncControls() {
      if (this.searchInput) this.searchInput.value = this.urlState.query;
      if (this.searchClear) this.searchClear.hidden = !this.urlState.query;
      const values = {kind: this.urlState.kind, scope_kind: this.urlState.scopeKind, state: this.urlState.state, tag: this.urlState.tag};
      for (const [name, input] of Object.entries(this.filters)) {
        input.value = values[name] || '';
        const browseOnly = name !== 'scope_kind';
        input.disabled = !!this.urlState.query && browseOnly;
        input.title = input.disabled ? 'This filter applies while browsing chunks, not full-text search.' : '';
      }
      this.renderSelection();
    }

    applyControlState(options) {
      clearTimeout(this.searchTimer);
      const next = {
        ...this.urlState,
        query: this.searchInput ? this.searchInput.value : '',
        kind: this.filters.kind ? this.filters.kind.value : '',
        scopeKind: this.filters.scope_kind ? this.filters.scope_kind.value : '',
        state: this.filters.state ? this.filters.state.value : '',
        tag: this.filters.tag ? this.filters.tag.value : '',
      };
      this.writeURLState(next, options && options.replace);
      this.refresh();
    }

    writeURLState(next, replace) {
      const search = searchForBrowserState(globalThis.location && globalThis.location.search, next);
      const target = globalThis.location.pathname + search;
      globalThis.history[replace ? 'replaceState' : 'pushState'](null, '', target);
      this.urlState = browserStateFromSearch(search);
      this.syncControls();
    }

    chunkQuery(cursor) {
      const query = {sort: 'updated_at', descending: true, limit: 50};
      if (this.urlState.kind) query.kind = this.urlState.kind;
      if (this.urlState.scopeKind) query.scope_kind = this.urlState.scopeKind;
      if (this.urlState.state) query.state = this.urlState.state;
      if (this.urlState.tag) query.tag = this.urlState.tag;
      if (cursor) query.cursor = cursor;
      return query;
    }

    searchRequest(cursor) {
      const request = {query: this.urlState.query, limit: 25};
      if (this.urlState.scopeKind) request.scope_kinds = [this.urlState.scopeKind];
      if (cursor) request.cursor = cursor;
      return request;
    }

    async refresh() {
      this.setState('loading');
      this.lastError = null;
      if (this.loadMoreButton) this.loadMoreButton.hidden = true;
      try {
        const searching = !!this.urlState.query;
        const response = searching
          ? await this.client.search(this.searchRequest(), {channel: 'results'})
          : await this.client.listChunks(this.chunkQuery(), {channel: 'results'});
        this.resultMode = searching ? 'search' : 'chunks';
        this.chunks = searching ? [] : (Array.isArray(response && response.chunks) ? response.chunks : []);
        this.matches = searching ? (Array.isArray(response && response.matches) ? response.matches : []) : [];
        this.page = response && response.page || {};
        this.renderResults();
        const hasMore = !!this.page.next_cursor;
        const items = searching ? this.matches : this.chunks;
        const noun = searching ? 'results' : 'chunks';
        const sourceDetail = searching
          ? (items.length ? items.length + (hasMore ? ' or more' : '') + ' matches for “' + this.urlState.query + '”.' : 'No knowledge entries match “' + this.urlState.query + '”.')
          : (items.length ? items.length + (hasMore ? ' or more' : '') + ' knowledge chunks are ready to browse.' : 'No chunks match the current filters.');
        if (this.page.truncated) {
          const reasons = Array.isArray(this.page.truncation_reasons) ? this.page.truncation_reasons.join(', ') : '';
          this.setState('truncated', {
            count: items.length, countLabel: noun, hasMore, sourceDetail,
            banner: reasons ? 'The server bounded this view: ' + reasons + '.' : 'The server bounded this view for safety.'
          });
        } else if (!searching && !items.length && !this.urlState.kind && !this.urlState.scopeKind && !this.urlState.state && !this.urlState.tag) {
          this.setState('empty', {count: 0, sourceDetail: 'There are no active knowledge chunks yet.'});
        } else {
          this.setState('ready', {count: items.length, countLabel: noun, hasMore, sourceDetail});
        }
        if (this.loadMoreButton) this.loadMoreButton.hidden = !hasMore;
        this.loadSelection();
        this.loadGraphSelection();
      } catch (error) {
        this.handleError(error);
      }
    }

    async loadMore() {
      const cursor = String(this.page && this.page.next_cursor || '');
      if (!cursor || !this.loadMoreButton) return;
      this.loadMoreButton.disabled = true;
      try {
        const searching = this.resultMode === 'search';
        const response = searching
          ? await this.client.search(this.searchRequest(cursor), {channel: 'results-more'})
          : await this.client.listChunks(this.chunkQuery(cursor), {channel: 'results-more'});
        const incoming = searching ? response.matches : response.chunks;
        if (searching) this.matches.push(...(Array.isArray(incoming) ? incoming : []));
        else this.chunks.push(...(Array.isArray(incoming) ? incoming : []));
        this.page = response.page || {};
        this.renderResults();
        const items = searching ? this.matches : this.chunks;
        const hasMore = !!this.page.next_cursor;
        const noun = searching ? 'results' : 'chunks';
        const truncated = !!this.page.truncated;
        this.setState(truncated ? 'truncated' : 'ready', {
          count: items.length, countLabel: noun, hasMore,
          sourceDetail: items.length + (hasMore ? ' or more' : '') + ' ' + noun + ' loaded.',
          banner: truncated ? 'The server bounded this view for safety.' : ''
        });
        this.loadMoreButton.hidden = !hasMore;
      } catch (error) {
        this.handleError(error);
      } finally {
        this.loadMoreButton.disabled = false;
      }
    }

    handleError(error) {
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

    renderResults() {
      if (!this.resultList) return;
      this.resultList.replaceChildren();
      const items = this.resultMode === 'search' ? this.matches : this.chunks;
      for (const item of items) this.resultList.appendChild(this.resultCard(item));
      this.renderSelection();
    }

    resultCard(item) {
      const searching = this.resultMode === 'search';
      const document = searching ? (item.document || {}) : item;
      const objectKind = searching ? 'entry' : 'chunk';
      const objectID = String(searching ? item.entry_id : item.id || '');
      const button = this.shell.ownerDocument.createElement('button');
      button.type = 'button';
      button.className = 'knowledge-result-card';
      button.dataset.objectKind = objectKind;
      button.dataset.objectId = objectID;
      button.setAttribute('role', 'listitem');
      const icon = this.shell.ownerDocument.createElement('span');
      icon.className = 'knowledge-result-icon';
      icon.innerHTML = '<i class="bi ' + (searching ? 'bi-file-text' : 'bi-collection') + '" aria-hidden="true"></i>';
      const copy = this.shell.ownerDocument.createElement('span');
      copy.className = 'knowledge-result-copy';
      const title = this.shell.ownerDocument.createElement('span');
      title.className = 'knowledge-result-title';
      title.textContent = String(document.title || objectID || 'Untitled knowledge');
      copy.appendChild(title);
      const summaryText = String(searching ? document.summary || '' : document.description || '');
      if (summaryText) {
        const summary = this.shell.ownerDocument.createElement('span');
        summary.className = 'knowledge-result-summary';
        summary.textContent = summaryText;
        copy.appendChild(summary);
      }
      const meta = this.shell.ownerDocument.createElement('span');
      meta.className = 'knowledge-result-meta';
      const metaValues = [document.kind, document.scope && document.scope.kind, searching ? document.verification : document.state];
      for (const value of metaValues) {
        if (!value) continue;
        const badge = this.shell.ownerDocument.createElement('span');
        badge.textContent = displayLabel(value);
        meta.appendChild(badge);
      }
      copy.appendChild(meta);
      button.append(icon, copy);
      button.addEventListener('click', () => this.selectObject(objectKind, objectID, item));
      return button;
    }

    selectObject(objectKind, id, object) {
      this.writeURLState({...this.urlState, objectKind, id}, false);
      this.renderSelection();
      const labelSource = this.resultMode === 'search' ? object && object.document && object.document.title : object && object.title;
      this.shell.dispatchEvent(new CustomEvent('koder:knowledge-selection', {detail: {objectKind, id, object, label: plainTextLabel(labelSource || id)}}));
      this.loadSelection();
      this.loadGraphSelection();
    }

    selectGraphObject(graphKind, key, options) {
      options = options || {};
      key = String(key || '');
      if (!['node', 'edge'].includes(graphKind) || !key) return false;
      if (this.graphAdapter && !this.graphAdapter.select(graphKind, key, {additive: options.additive, toggle: options.additive})) return false;
      const primary = this.graphAdapter ? this.graphAdapter.selectionSnapshot().primary : {kind: graphKind, key};
      return this.applyPrimaryGraphSelection(primary);
    }

    selectGraphObjects(keys, additive) {
      if (!this.graphAdapter) return false;
      const selected = this.graphAdapter.selectMany('node', keys, {additive: !!additive});
      const primary = this.graphAdapter.selectionSnapshot().primary;
      if (!primary) {
        this.clearGraphSelection();
        return selected > 0;
      }
      this.applyPrimaryGraphSelection(primary);
      return selected > 0;
    }

    applyPrimaryGraphSelection(primary) {
      if (!primary) {
        this.writeURLState({...this.urlState, objectKind: '', id: ''}, false);
        this.client.cancel('inspector');
        this.setInspectorMode('empty');
        return true;
      }
      let objectKind;
      let id;
      if (primary.kind === 'node') {
        const separator = primary.key.indexOf(':');
        objectKind = separator > 0 ? primary.key.slice(0, separator) : '';
        id = separator > 0 ? primary.key.slice(separator + 1) : '';
        if (!['chunk', 'entry', 'evidence'].includes(objectKind)) return false;
      } else if (primary.kind === 'edge') {
        objectKind = 'link';
        id = primary.key;
      } else return false;
      this.writeURLState({...this.urlState, objectKind, id}, false);
      this.renderSelection();
      this.shell.dispatchEvent(new CustomEvent('koder:knowledge-selection', {detail: {objectKind, id, label: plainTextLabel(id)}}));
      this.loadSelection();
      return true;
    }

    syncGraphSelection(snapshot) {
      snapshot = snapshot || {primary: null, items: []};
      const items = Array.isArray(snapshot.items) ? snapshot.items : [];
      if (this.graphRenderer) this.graphRenderer.setSelections(items);
      if (this.graphSelectionCount) {
        this.graphSelectionCount.hidden = items.length < 2;
        this.graphSelectionCount.textContent = items.length < 2 ? '' : `${items.length} selected`;
      }
      this.updateGraphViewControls();
    }

    updateGraphViewControls() {
      const selection = this.graphAdapter ? this.graphAdapter.selectionSnapshot().items : [];
      const state = this.graphView ? this.graphView.state() : {hidden: 0, canUndo: false};
      if (this.graphHideButton) this.graphHideButton.disabled = !this.graphView || !selection.length;
      if (this.graphIsolateButton) this.graphIsolateButton.disabled = !this.graphView || !selection.length;
      if (this.graphRevealButton) this.graphRevealButton.disabled = !this.graphView || !state.hidden;
      if (this.graphUndoButton) this.graphUndoButton.disabled = !this.graphView || !state.canUndo;
    }

    applyGraphViewAction(action) {
      if (!this.graphView) return false;
      const items = this.graphAdapter ? this.graphAdapter.selectionSnapshot().items : [];
      let changed = false;
      if (action === 'hide') changed = this.graphView.hide(items);
      if (action === 'isolate') changed = this.graphView.isolate(items);
      if (action === 'reveal') changed = this.graphView.reveal();
      if (action === 'undo') changed = this.graphView.undo();
      if (!changed) return false;
      if (action === 'hide' && this.graphAdapter) this.graphAdapter.clearSelection();
      const status = this.shell.querySelector('[data-knowledge-status-label]');
      if (status) {
        const labels = {hide: 'Selection hidden locally', isolate: 'Selection isolated locally', reveal: 'Hidden items revealed', undo: 'Local view change undone'};
        status.textContent = labels[action] || 'Graph view updated';
      }
      return true;
    }

    clearGraphSelection() {
      this.writeURLState({...this.urlState, objectKind: '', id: ''}, false);
      if (this.graphAdapter) this.graphAdapter.clearSelection();
      else if (this.graphRenderer) this.graphRenderer.setSelection(null, null);
      this.client.cancel('inspector');
      this.setInspectorMode('empty');
    }

    renderSelection() {
      if (!this.resultList) return;
      for (const card of this.resultList.querySelectorAll('[data-object-kind][data-object-id]')) {
        const selected = card.dataset.objectKind === this.urlState.objectKind && card.dataset.objectId === this.urlState.id;
        card.classList.toggle('is-selected', selected);
        card.setAttribute('aria-pressed', selected ? 'true' : 'false');
      }
    }

    setInspectorMode(mode, message) {
      for (const [name, element] of Object.entries(this.inspector)) {
        if (!['empty', 'loading', 'error', 'content'].includes(name) || !element) continue;
        element.hidden = name !== mode;
      }
      if (mode === 'error' && this.inspector.error) this.inspector.error.textContent = String(message || 'Knowledge could not load this selection.');
      if (mode !== 'content') {
        this.inspectedObject = null;
        if (this.inspector.sendButton) this.inspector.sendButton.disabled = true;
        if (this.graphExpandActions) this.graphExpandActions.hidden = true;
      }
    }

    async loadSelection() {
      const objectKind = this.urlState.objectKind;
      const id = this.urlState.id;
      if (!objectKind || !id) {
        this.client.cancel('inspector');
        this.setInspectorMode('empty');
        return;
      }
      if (!['chunk', 'entry', 'link'].includes(objectKind)) {
        this.setInspectorMode('error', 'This object type does not yet have a direct inspector endpoint.');
        return;
      }
      this.setInspectorMode('loading');
      try {
        let response;
        if (objectKind === 'chunk') response = await this.client.getChunk(id, {channel: 'inspector'});
        else if (objectKind === 'entry') response = await this.client.getEntry(id, {channel: 'inspector'});
        else response = await this.client.getLink(id, {channel: 'inspector'});
        if (this.urlState.objectKind !== objectKind || this.urlState.id !== id) return;
        const record = response && response[objectKind];
        if (!record) throw new Error('Knowledge returned no selected object.');
        this.renderInspector(objectKind, record);
      } catch (error) {
        if (error && (error.code === 'canceled' || error.code === 'stale_response')) return;
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'Knowledge could not load this selection.');
        this.setInspectorMode('error', requestID ? message + ' Audit ID: ' + requestID : message);
      }
    }

    setGraphState(state, detail) {
      if (this.graphRenderer) this.graphRenderer.setState(state);
      const status = this.shell.querySelector('[data-knowledge-status-label]');
      const title = this.shell.querySelector('[data-knowledge-state-title]');
      const message = this.shell.querySelector('[data-knowledge-state-detail]');
      const labels = {
        empty: ['Choose knowledge', 'Select a chunk or search result to draw its neighborhood.'],
        loading: ['Loading graph', 'Reading a bounded, authorized neighborhood.'],
        ready: ['Graph ready', detail || 'Knowledge neighborhood ready.'],
        truncated: ['Partial graph', detail || 'The server bounded this neighborhood for safety.'],
        stale: ['Refreshing graph', 'Knowledge changed; loading a consistent snapshot.'],
        error: ['Graph unavailable', detail || 'This neighborhood could not be loaded.'],
      };
      const selected = labels[state] || labels.error;
      if (status) status.textContent = selected[0];
      if (title) title.textContent = selected[0];
      if (message) message.textContent = selected[1];
    }

    async loadGraphSelection() {
      if (!this.graphAdapter || !this.graphRenderer) return;
      const request = graphSnapshotRequest(this.urlState.objectKind, this.urlState.id);
      if (!request) {
        this.client.cancel('graph');
        if (this.graphLayout) this.graphLayout.stop('selection_cleared');
        this.graphRenderer.setSelection(null, null);
        this.setGraphState('empty');
        return;
      }
      if (this.graphLayout) this.graphLayout.stop('selection_changed');
      this.setGraphState('loading');
      try {
        const response = await this.client.graphSnapshot(request, {channel: 'graph', timeoutMS: 10000});
        this.graphAdapter.replaceSnapshot(response);
        if (this.graphView) this.graphView.reset();
        const rootKey = `${request.root.kind}:${request.root.id}`;
        this.graphAdapter.select('node', rootKey);
        const counts = this.graphAdapter.counts();
        const truncated = !!(response && response.page && response.page.truncated);
        const detail = `${counts.nodes} ${counts.nodes === 1 ? 'node' : 'nodes'} and ${counts.edges} ${counts.edges === 1 ? 'relationship' : 'relationships'}.`;
        this.setGraphState(truncated ? 'truncated' : 'ready', detail);
        if (this.graphLayout) this.graphLayout.request();
        else if (this.graphViewport) this.graphViewport.fit({animate: false});
      } catch (error) {
        if (error && (error.code === 'canceled' || error.code === 'stale_response')) return;
        const requestID = String(error && error.requestID || '');
        const detail = String(error && error.message || 'This neighborhood could not be loaded.');
        this.setGraphState(error && error.code === 'invalid_cursor' ? 'stale' : 'error', requestID ? `${detail} Audit ID: ${requestID}` : detail);
      }
    }

    renderInspector(objectKind, record) {
      const title = objectKind === 'link'
        ? String(record.label || displayLabel(record.kind) + ' relationship')
        : String(record.title || record.id || 'Untitled knowledge');
      const summary = objectKind === 'entry' ? String(record.summary || '') : '';
      const markdown = objectKind === 'entry' ? String(record.body || '')
        : objectKind === 'chunk' ? String(record.description || '') : String(record.notes || '');
      if (this.inspector.kind) this.inspector.kind.textContent = displayLabel(objectKind) + (record.kind ? ' · ' + displayLabel(record.kind) : '');
      if (this.inspector.title) this.inspector.title.textContent = title;
      if (this.inspector.summary) {
        this.inspector.summary.hidden = !summary;
        this.inspector.summary.textContent = summary;
      }
      if (this.inspector.badges) {
        this.inspector.badges.replaceChildren();
        const badges = [record.state, record.scope && record.scope.kind, record.visibility, record.verification && record.verification.status];
        for (const value of badges) {
          if (!value) continue;
          const badge = this.shell.ownerDocument.createElement('span');
          badge.textContent = displayLabel(value);
          this.inspector.badges.appendChild(badge);
        }
      }
      if (this.inspector.meta) {
        this.inspector.meta.replaceChildren();
        const revision = record.revision || {};
        const rows = [
          ['ID', record.id],
          ['Updated', formatTimestamp(record.updated_at || revision.created_at)],
          ['Revision', revision.number],
          ['Author', revision.actor && [revision.actor.kind, revision.actor.id].filter(Boolean).join(': ')],
        ];
        if (objectKind === 'entry') rows.push(['Chunk', record.chunk_id]);
        if (objectKind === 'link') {
          rows.push(['From', record.source && record.source.kind + ': ' + record.source.id]);
          rows.push(['To', record.target && record.target.kind + ': ' + record.target.id]);
        }
        for (const [name, value] of rows) {
          if (value === undefined || value === null || value === '') continue;
          const term = this.shell.ownerDocument.createElement('dt');
          const description = this.shell.ownerDocument.createElement('dd');
          term.textContent = name;
          description.textContent = String(value);
          this.inspector.meta.append(term, description);
        }
      }
      if (this.inspector.markdown) {
        this.inspector.markdown.hidden = !markdown;
        this.inspector.markdown.replaceChildren();
        if (markdown) {
          const html = sanitizedMarkdownHTML(markdown, globalThis.marked, globalThis.DOMPurify);
          if (html) {
            this.inspector.markdown.innerHTML = html;
            for (const link of this.inspector.markdown.querySelectorAll('a[href]')) {
              link.target = '_blank';
              link.rel = 'noopener noreferrer';
            }
          } else {
            this.inspector.markdown.textContent = markdown;
          }
        }
      }
      this.inspectedObject = {kind: objectKind, id: String(record.id || '')};
      const expandable = ['chunk', 'entry'].includes(objectKind);
      if (this.graphExpandActions) this.graphExpandActions.hidden = !expandable;
      if (this.graphExpandIncoming) this.graphExpandIncoming.disabled = !expandable;
      if (this.graphExpandOutgoing) this.graphExpandOutgoing.disabled = !expandable;
      if (this.graphExpandStatus) this.graphExpandStatus.textContent = expandable ? 'Add a bounded one-hop neighborhood to the visible graph.' : '';
      if (this.inspector.sendButton) this.inspector.sendButton.disabled = !this.chatSelection;
      if (this.inspector.sendStatus) {
        this.inspector.sendStatus.textContent = this.chatSelection
          ? 'Send an explicit reference to the chat that opened this explorer.'
          : 'Open Knowledge from a chat to send context back.';
      }
      this.setInspectorMode('content');
      this.shell.dispatchEvent(new CustomEvent('koder:knowledge-inspected', {
        detail: {objectKind, id: String(record.id || ''), record, label: plainTextLabel(title)}
      }));
    }

    async expandGraph(direction) {
      const selected = this.inspectedObject;
      const request = selected && graphExpansionRequest(selected.kind, selected.id, direction);
      if (!request || !this.graphAdapter) return false;
      if (this.graphExpandIncoming) this.graphExpandIncoming.disabled = true;
      if (this.graphExpandOutgoing) this.graphExpandOutgoing.disabled = true;
      if (this.graphExpandStatus) this.graphExpandStatus.textContent = `Loading ${direction} relationships…`;
      try {
        const response = await this.client.graphSnapshot(request, {channel: 'graph-expand', timeoutMS: 10000});
        const merged = this.graphAdapter.mergeSnapshot(response);
        if (merged.result && merged.result.action !== 'applied') {
          if (this.graphExpandStatus) this.graphExpandStatus.textContent = 'Knowledge changed; refreshing the graph consistently.';
          return false;
        }
        const nodes = Array.isArray(response.nodes) ? response.nodes.length : 0;
        const edges = Array.isArray(response.edges) ? response.edges.length : 0;
        const truncated = !!(response.page && response.page.truncated);
        if (this.graphExpandStatus) {
          this.graphExpandStatus.textContent = `Added up to ${nodes} ${direction} nodes and ${edges} relationships${truncated ? ' (bounded)' : ''}.`;
        }
        if (this.graphLayout) this.graphLayout.request({iterations: 70});
        return true;
      } catch (error) {
        if (error && (error.code === 'canceled' || error.code === 'stale_response')) return false;
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'The neighborhood could not be expanded.');
        if (this.graphExpandStatus) this.graphExpandStatus.textContent = requestID ? `${message} Audit ID: ${requestID}` : message;
        return false;
      } finally {
        const stillExpandable = this.inspectedObject && ['chunk', 'entry'].includes(this.inspectedObject.kind);
        if (this.graphExpandIncoming) this.graphExpandIncoming.disabled = !stillExpandable;
        if (this.graphExpandOutgoing) this.graphExpandOutgoing.disabled = !stillExpandable;
      }
    }

    async sendSelectionToChat() {
      if (!this.chatSelection || !this.inspectedObject || !this.inspector.sendButton) return;
      this.inspector.sendButton.disabled = true;
      if (this.inspector.sendStatus) this.inspector.sendStatus.textContent = 'Sending Knowledge reference…';
      try {
        await this.client.sendToChat({
          session_id: this.chatSelection.sessionID,
          chat_id: this.chatSelection.chatID,
          object: {kind: this.inspectedObject.kind, id: this.inspectedObject.id},
        }, {channel: 'send-to-chat'});
        if (this.inspector.sendStatus) this.inspector.sendStatus.textContent = 'Sent to chat. Return there to continue.';
      } catch (error) {
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'Knowledge could not send this reference.');
        if (this.inspector.sendStatus) this.inspector.sendStatus.textContent = requestID ? message + ' Audit ID: ' + requestID : message;
      } finally {
        this.inspector.sendButton.disabled = !this.chatSelection || !this.inspectedObject;
      }
    }

    destroy() {
      clearTimeout(this.searchTimer);
      clearTimeout(this.graphRefetchTimer);
      globalThis.removeEventListener('popstate', this.onPopState);
      this.shell.removeEventListener('koder:knowledge-pane', this.onGraphPane);
      if (this.graphUnsubscribe) this.graphUnsubscribe();
      if (this.graphInteractionUnsubscribe) this.graphInteractionUnsubscribe();
      if (this.graphLayoutUnsubscribe) this.graphLayoutUnsubscribe();
      if (this.graphViewUnsubscribe) this.graphViewUnsubscribe();
      if (this.graphLayout) this.graphLayout.destroy();
      if (this.graphViewport) this.graphViewport.destroy();
      if (this.graphRenderer) this.graphRenderer.destroy();
      if (this.graphView) this.graphView.destroy();
      if (this.graphAdapter) this.graphAdapter.destroy();
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
      shell.dispatchEvent(new CustomEvent('koder:knowledge-pane', {detail: {pane}}));
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
    shell.addEventListener('koder:knowledge-selection', () => {
      if (globalThis.matchMedia && globalThis.matchMedia('(max-width: 980px)').matches) selectPane('inspector');
    });
    selectPane(shell.dataset.mobilePane);
    if (!globalThis.KoderKnowledgeAPI) {
      const unavailable = new BrowserApp(shell, {cancelAll() {}});
      unavailable.setState('error', {detail: 'The Knowledge API client did not load.'});
      shell.__koderKnowledgeApp = unavailable;
      return unavailable;
    }
    try {
      const client = globalThis.KoderKnowledgeAPI.fromPageConfig(globalThis.KODER_KNOWLEDGE_CONFIG || {});
      const runtime = {};
      const canvas = shell.querySelector('[data-knowledge-graph-canvas]');
      const graphDebug = graphDebugEnabled(globalThis.location && globalThis.location.search);
      const environment = graphEnvironment(document, globalThis.matchMedia && globalThis.matchMedia.bind(globalThis));
      const fallback = shell.querySelector('[data-knowledge-graph-fallback]');
      if (!environment.available) {
        const stage = shell.querySelector('#knowledge-graph');
        if (stage) stage.dataset.graphState = 'fallback';
        shell.dataset.graphFallback = environment.reason;
        if (fallback) {
          fallback.hidden = false;
          const detail = fallback.querySelector('[data-knowledge-graph-fallback-detail]');
          if (detail) detail.textContent = environment.reason === 'reduced_motion'
            ? 'Reduced motion is enabled. Browse Knowledge and Inspector without the animated canvas.'
            : 'WebGL is unavailable. Browse the same knowledge through Knowledge and Inspector.';
        }
      }
      if (canvas && globalThis.KoderKnowledgeGraph && globalThis.KoderKnowledgeGraphAdapter &&
          environment.available && globalThis.KoderKnowledgeGraphRendering && globalThis.KoderKnowledgeGraphRenderer && globalThis.KoderKnowledgeGraphViewport && globalThis.Sigma) {
        const graphStore = new globalThis.KoderKnowledgeGraph.Store();
        runtime.graphAdapter = new globalThis.KoderKnowledgeGraphAdapter.Adapter(graphStore);
        if (globalThis.KoderKnowledgeGraphInteractions) {
          runtime.graphView = new globalThis.KoderKnowledgeGraphInteractions.LocalViewHistory({graph: graphStore.graph});
        }
        runtime.graphRenderer = new globalThis.KoderKnowledgeGraphRenderer.Renderer({
          store: graphStore, container: canvas, stage: shell.querySelector('#knowledge-graph'),
          legend: shell.querySelector('[data-knowledge-legend]'), rendering: globalThis.KoderKnowledgeGraphRendering,
          selectionBox: shell.querySelector('[data-knowledge-selection-box]'), SigmaAPI: globalThis.Sigma, debug: graphDebug,
        });
        runtime.graphViewport = new globalThis.KoderKnowledgeGraphViewport.Viewport({
          renderer: runtime.graphRenderer.sigma, container: canvas,
          getInsets: () => {
            const style = globalThis.getComputedStyle(canvas);
            const read = name => Number.parseFloat(style.getPropertyValue(name)) || 0;
            return {
              top: read('--knowledge-viewport-inset-top'), right: read('--knowledge-viewport-inset-right'),
              bottom: read('--knowledge-viewport-inset-bottom'), left: read('--knowledge-viewport-inset-left'),
            };
          },
        });
        if (globalThis.KoderKnowledgeGraphLayout && globalThis.KoderKnowledgeLayouts) {
          runtime.graphLayout = new globalThis.KoderKnowledgeGraphLayout.ForceAtlasController({
            graph: graphStore.graph, layouts: globalThis.KoderKnowledgeLayouts, debug: graphDebug,
          });
        }
      }
      const app = new BrowserApp(shell, client, runtime);
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

  return Object.freeze({panes, states, BrowserApp, normalizePane, normalizeState, stateForError, presentationForState, adjacentPane, safeReturnPath, returnPathFromSearch, chatSelectionFromSearch, browserStateFromSearch, searchForBrowserState, displayLabel, plainTextLabel, graphSnapshotRequest, graphExpansionRequest, graphDebugEnabled, supportsWebGL, graphEnvironment, sanitizedMarkdownHTML, mount});
});
