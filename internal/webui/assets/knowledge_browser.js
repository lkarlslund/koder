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

  function graphObjectForSelection(selection) {
    selection = selection || {};
    if (['chunk', 'entry'].includes(selection.kind) && /^[A-Za-z0-9_-]+$/.test(String(selection.id || ''))) {
      return {kind: selection.kind, id: String(selection.id)};
    }
    if (selection.kind !== 'node') return null;
    const key = String(selection.key || '');
    const separator = key.indexOf(':');
    const kind = separator > 0 ? key.slice(0, separator) : '';
    const id = separator > 0 ? key.slice(separator + 1) : '';
    return ['chunk', 'entry'].includes(kind) && /^[A-Za-z0-9_-]+$/.test(id) ? {kind, id} : null;
  }

  function graphKeyboardAction(event) {
    event = event || {};
    const key = String(event.key || '').toLowerCase();
    if ((event.ctrlKey || event.metaKey) && key === 'z') return 'undo';
    if (event.ctrlKey || event.metaKey || event.altKey) return '';
    return ({enter: 'inspect', delete: 'hide', backspace: 'hide', h: 'hide', i: 'isolate', r: 'reveal', c: 'center', p: 'pin', '[': 'incoming', ']': 'outgoing'})[key] || '';
  }

  function applicabilityRows(value) {
    value = value || {};
    const rows = [];
    const add = (label, values) => { if (Array.isArray(values) && values.length) rows.push({label, value: values.map(String).join(', ')}); };
    add('Systems', value.operating_systems);
    add('Architectures', value.architectures);
    add('Locales', value.locales);
    add('Conditions', value.conditions);
    if (Array.isArray(value.software) && value.software.length) {
      rows.push({label: 'Software', value: value.software.map(item => [item && item.name, item && item.version_range].filter(Boolean).join(' ')).filter(Boolean).join(', ')});
    }
    return rows;
  }

  function inspectorWarnings(objectKind, record, neighbors, now) {
    record = record || {};
    const warnings = [];
    const add = (text, level) => warnings.push({text, level: level || 'warning'});
    const timestamp = value => { const date = new Date(String(value || '')); return Number.isNaN(date.getTime()) ? null : date; };
    now = now instanceof Date && !Number.isNaN(now.getTime()) ? now : new Date();
    if (record.state === 'invalid') add('This knowledge has been marked invalid and should not be used.', 'danger');
    if (record.state === 'superseded') add(record.superseded_by_id ? `This entry was superseded by ${record.superseded_by_id}.` : 'This knowledge has been superseded.', 'danger');
    const validFrom = timestamp(record.valid_from);
    const validUntil = timestamp(record.valid_until);
    const reviewAfter = timestamp(record.review_after);
    if (validFrom && now < validFrom) add(`This entry does not become valid until ${formatTimestamp(record.valid_from)}.`);
    if (validUntil && now >= validUntil) add(`This entry expired ${formatTimestamp(record.valid_until)}.`, 'danger');
    if (reviewAfter && now >= reviewAfter) add(`Review was due ${formatTimestamp(record.review_after)}; treat this knowledge as stale.`);
    const verification = record.verification && record.verification.status;
    if (verification === 'disputed') add('The current verification assessment is disputed.', 'danger');
    const contradictions = (neighbors || []).filter(item => item && item.link && item.link.kind === 'contradicts');
    if (contradictions.length) add(`${contradictions.length} active ${contradictions.length === 1 ? 'relationship contradicts' : 'relationships contradict'} this ${objectKind}.`, 'danger');
    return warnings;
  }

  function safeExternalURL(value) {
    try {
      const url = new URL(String(value || ''));
      return ['http:', 'https:'].includes(url.protocol) ? url.href : '';
    } catch (_) { return ''; }
  }

  function commaValues(value) {
    return [...new Set(String(value || '').split(',').map(item => item.trim()).filter(Boolean))];
  }

  function chunkContentFromValues(values) {
    values = values || {};
    const required = name => {
      const value = String(values[name] || '').trim();
      if (!value) throw new TypeError(`Chunk ${name.replaceAll('_', ' ')} is required.`);
      return value;
    };
    const scopeKind = required('scope_kind');
    const scopeSelector = String(values.scope_selector || '').trim();
    if (scopeKind !== 'global' && !scopeSelector) throw new TypeError('Chunk scope selector is required outside global scope.');
    const sharedWith = commaValues(values.shared_with).map(value => {
      const separator = value.indexOf(':');
      if (separator < 1 || separator === value.length - 1) throw new TypeError('Shared principals must use kind:id.');
      return {kind: value.slice(0, separator).trim(), id: value.slice(separator + 1).trim()};
    });
    const content = {
      title: required('title'), kind: required('kind'), scope: {kind: scopeKind}, visibility: required('visibility'),
    };
    if (scopeSelector) content.scope.selector = scopeSelector;
    const text = (name, target) => { const value = String(values[name] || '').trim(); if (value) content[target || name] = value; };
    text('description'); text('language'); text('locale'); text('domain'); text('license'); text('source_policy'); text('min_koder_version');
    for (const name of ['aliases', 'tags', 'risk', 'dependency_ids']) {
      const list = commaValues(values[name]);
      if (list.length) content[name] = list;
    }
    if (sharedWith.length) content.shared_with = sharedWith;
    const publisher = {id: String(values.publisher_id || '').trim(), name: String(values.publisher_name || '').trim()};
    if (publisher.id || publisher.name) content.publisher = publisher;
    const reviewAfter = String(values.review_after || '').trim();
    if (reviewAfter) {
      const date = new Date(reviewAfter);
      if (Number.isNaN(date.getTime())) throw new TypeError('Chunk review date is invalid.');
      content.review_after = date.toISOString();
    }
    return content;
  }

  function chunkEditorValues(record) {
    record = record || {};
    const localDate = value => {
      const date = new Date(String(value || ''));
      if (Number.isNaN(date.getTime())) return '';
      const offset = date.getTimezoneOffset() * 60000;
      return new Date(date.getTime() - offset).toISOString().slice(0, 16);
    };
    return {
      title: record.title || '', description: record.description || '', kind: record.kind || 'reference',
      visibility: record.visibility || 'private', scope_kind: record.scope && record.scope.kind || 'global',
      scope_selector: record.scope && record.scope.selector || '', aliases: (record.aliases || []).join(', '), tags: (record.tags || []).join(', '),
      language: record.language || '', locale: record.locale || '', domain: record.domain || '', risk: (record.risk || []).join(', '),
      shared_with: (record.shared_with || []).map(item => `${item.kind}:${item.id}`).join(', '), dependency_ids: (record.dependency_ids || []).join(', '),
      publisher_id: record.publisher && record.publisher.id || '', publisher_name: record.publisher && record.publisher.name || '',
      license: record.license || '', source_policy: record.source_policy || '', min_koder_version: record.min_koder_version || '',
      review_after: localDate(record.review_after), review_approved: false,
    };
  }

  function localDateTimeValue(value) {
    const date = new Date(String(value || ''));
    if (Number.isNaN(date.getTime())) return '';
    const offset = date.getTimezoneOffset() * 60000;
    return new Date(date.getTime() - offset).toISOString().slice(0, 16);
  }

  function entryContentFromValues(values) {
    values = values || {};
    const required = name => {
      const value = String(values[name] || '').trim();
      if (!value) throw new TypeError(`Entry ${name.replaceAll('_', ' ')} is required.`);
      return value;
    };
    const scopeKind = required('scope_kind');
    const scopeSelector = String(values.scope_selector || '').trim();
    if (scopeKind !== 'global' && !scopeSelector) throw new TypeError('Entry scope selector is required outside global scope.');
    const content = {kind: required('kind'), title: required('title'), scope: {kind: scopeKind}};
    if (scopeSelector) content.scope.selector = scopeSelector;
    for (const name of ['summary', 'body']) {
      const value = String(values[name] || '').trim();
      if (value) content[name] = value;
    }
    for (const name of ['aliases', 'tags', 'risk', 'evidence_ids']) {
      const list = commaValues(values[name]);
      if (list.length) content[name] = list;
    }
    const confidenceText = String(values.confidence || '').trim();
    if (confidenceText) {
      const confidence = Number(confidenceText);
      if (!Number.isFinite(confidence) || confidence < 0 || confidence > 1) throw new TypeError('Entry confidence must be between 0 and 1.');
      content.confidence = confidence;
    }
    const applicability = {};
    for (const name of ['operating_systems', 'architectures', 'locales', 'conditions']) {
      const list = commaValues(values[name]);
      if (list.length) applicability[name] = list;
    }
    const software = commaValues(values.software).map(value => {
      const [name, ...range] = value.split('|');
      if (!name.trim()) throw new TypeError('Entry software names cannot be empty.');
      const item = {name: name.trim()};
      if (range.length && range.join('|').trim()) item.version_range = range.join('|').trim();
      return item;
    });
    if (software.length) applicability.software = software;
    if (Object.keys(applicability).length) content.applicability = applicability;
    const dates = {};
    for (const name of ['valid_from', 'valid_until', 'observed_at', 'review_after']) {
      const value = String(values[name] || '').trim();
      if (!value) continue;
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) throw new TypeError(`Entry ${name.replaceAll('_', ' ')} is invalid.`);
      dates[name] = date;
      content[name] = date.toISOString();
    }
    if (dates.valid_from && dates.valid_until && dates.valid_from >= dates.valid_until) throw new TypeError('Entry valid until must be later than valid from.');
    const origin = String(values.personal_origin || '').trim();
    if (origin) content.personal_origin = origin;
    return content;
  }

  function entryEditorValues(record, chunk) {
    record = record || {};
    chunk = chunk || {};
    const applicability = record.applicability || {};
    const scope = record.scope || chunk.scope || {kind: 'global'};
    return {
      chunk_id: record.chunk_id || chunk.id || '', title: record.title || '', kind: record.kind || 'fact',
      confidence: record.confidence > 0 || (record.confidence === 0 && record.personal_origin === 'inferred') ? String(record.confidence) : '',
      summary: record.summary || '', body: record.body || '', scope_kind: scope.kind || 'global', scope_selector: scope.selector || '',
      aliases: (record.aliases || []).join(', '), tags: (record.tags || []).join(', '), risk: (record.risk || []).join(', '),
      operating_systems: (applicability.operating_systems || []).join(', '), architectures: (applicability.architectures || []).join(', '),
      software: (applicability.software || []).map(item => item.version_range ? `${item.name}|${item.version_range}` : item.name).join(', '),
      locales: (applicability.locales || []).join(', '), conditions: (applicability.conditions || []).join(', '),
      valid_from: localDateTimeValue(record.valid_from), valid_until: localDateTimeValue(record.valid_until), observed_at: localDateTimeValue(record.observed_at),
      review_after: localDateTimeValue(record.review_after), evidence_ids: (record.evidence_ids || []).join(', '), personal_origin: record.personal_origin || '', review_approved: false,
    };
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
      this.graphCanvas = shell.querySelector('[data-knowledge-graph-canvas]');
      this.graphContextMenu = shell.querySelector('[data-knowledge-context-menu]');
      this.graphContextHeading = shell.querySelector('[data-knowledge-context-heading]');
      this.graphContextPinLabel = shell.querySelector('[data-knowledge-context-pin-label]');
      this.graphContextButtons = this.graphContextMenu ? Array.from(this.graphContextMenu.querySelectorAll('[data-knowledge-context-action]')) : [];
      this.chunkCreateButton = shell.querySelector('[data-knowledge-chunk-create]');
      this.chunkDialog = shell.querySelector('[data-knowledge-chunk-dialog]');
      this.chunkForm = shell.querySelector('[data-knowledge-chunk-form]');
      this.chunkDialogTitle = shell.querySelector('[data-knowledge-chunk-dialog-title]');
      this.chunkFormError = shell.querySelector('[data-knowledge-chunk-form-error]');
      this.chunkSubmitButton = shell.querySelector('[data-knowledge-chunk-submit]');
      this.chunkActions = shell.querySelector('[data-knowledge-chunk-actions]');
      this.chunkEditButton = shell.querySelector('[data-knowledge-chunk-edit]');
      this.chunkArchiveButton = shell.querySelector('[data-knowledge-chunk-archive]');
      this.chunkRestoreButton = shell.querySelector('[data-knowledge-chunk-restore]');
      this.chunkDeleteButton = shell.querySelector('[data-knowledge-chunk-delete]');
      this.chunkMutationStatus = shell.querySelector('[data-knowledge-chunk-mutation-status]');
      this.entryCreateButton = shell.querySelector('[data-knowledge-entry-create]');
      this.entryDialog = shell.querySelector('[data-knowledge-entry-dialog]');
      this.entryForm = shell.querySelector('[data-knowledge-entry-form]');
      this.entryDialogTitle = shell.querySelector('[data-knowledge-entry-dialog-title]');
      this.entryFormError = shell.querySelector('[data-knowledge-entry-form-error]');
      this.entrySubmitButton = shell.querySelector('[data-knowledge-entry-submit]');
      this.entryActions = shell.querySelector('[data-knowledge-entry-actions]');
      this.entryEditButton = shell.querySelector('[data-knowledge-entry-edit]');
      this.entrySupersedeButton = shell.querySelector('[data-knowledge-entry-supersede]');
      this.entryArchiveButton = shell.querySelector('[data-knowledge-entry-archive]');
      this.entryRestoreButton = shell.querySelector('[data-knowledge-entry-restore]');
      this.entryDeleteButton = shell.querySelector('[data-knowledge-entry-delete]');
      this.entryMutationStatus = shell.querySelector('[data-knowledge-entry-mutation-status]');
      this.supersedeDialog = shell.querySelector('[data-knowledge-supersede-dialog]');
      this.supersedeForm = shell.querySelector('[data-knowledge-supersede-form]');
      this.supersedeError = shell.querySelector('[data-knowledge-supersede-error]');
      this.supersedeSubmitButton = shell.querySelector('[data-knowledge-supersede-submit]');
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
        warnings: shell.querySelector('[data-knowledge-inspector-warnings]'),
        meta: shell.querySelector('[data-knowledge-inspector-meta]'),
        markdown: shell.querySelector('[data-knowledge-inspector-markdown]'),
        applicability: shell.querySelector('[data-knowledge-inspector-applicability]'),
        applicabilityList: shell.querySelector('[data-knowledge-applicability-list]'),
        evidence: shell.querySelector('[data-knowledge-inspector-evidence]'),
        evidenceList: shell.querySelector('[data-knowledge-evidence-list]'),
        evidenceCount: shell.querySelector('[data-knowledge-evidence-count]'),
        supportStatus: shell.querySelector('[data-knowledge-support-status]'),
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
      this.graphContextClickHandlers = new Map();
      for (const button of this.graphContextButtons) {
        const handler = () => this.runGraphContextAction(button.dataset.knowledgeContextAction);
        this.graphContextClickHandlers.set(button, handler);
        button.addEventListener('click', handler);
      }
      this.onGraphKeyDown = event => this.handleGraphKeyDown(event);
      this.onGraphNativeContext = event => event.preventDefault();
      this.onContextMenuKeyDown = event => this.handleContextMenuKeyDown(event);
      this.onDocumentPointerDown = event => {
        if (this.graphContextMenu && !this.graphContextMenu.hidden && !this.graphContextMenu.contains(event.target)) this.closeGraphContextMenu();
      };
      if (this.graphCanvas) {
        this.graphCanvas.addEventListener('keydown', this.onGraphKeyDown);
        this.graphCanvas.addEventListener('contextmenu', this.onGraphNativeContext);
      }
      if (this.graphContextMenu) this.graphContextMenu.addEventListener('keydown', this.onContextMenuKeyDown);
      this.shell.ownerDocument.addEventListener('pointerdown', this.onDocumentPointerDown, true);
      if (this.chunkCreateButton) this.chunkCreateButton.addEventListener('click', () => this.openChunkEditor());
      if (this.chunkEditButton) this.chunkEditButton.addEventListener('click', () => this.openChunkEditor(this.inspectedRecord));
      if (this.chunkArchiveButton) this.chunkArchiveButton.addEventListener('click', () => this.changeChunkLifecycle('archive'));
      if (this.chunkRestoreButton) this.chunkRestoreButton.addEventListener('click', () => this.changeChunkLifecycle('restore'));
      if (this.chunkDeleteButton) this.chunkDeleteButton.addEventListener('click', () => this.deleteInspectedChunk());
      for (const button of shell.querySelectorAll('[data-knowledge-chunk-cancel]')) button.addEventListener('click', () => this.closeChunkEditor());
      if (this.chunkForm) this.chunkForm.addEventListener('submit', event => { event.preventDefault(); this.saveChunkEditor(); });
      const scopeInput = this.chunkForm && this.chunkForm.elements.namedItem('scope_kind');
      if (scopeInput) scopeInput.addEventListener('change', () => this.syncChunkScopeField());
      if (this.entryCreateButton) this.entryCreateButton.addEventListener('click', () => this.openEntryEditor(null, this.inspectedRecord));
      if (this.entryEditButton) this.entryEditButton.addEventListener('click', () => this.openEntryEditor(this.inspectedRecord));
      if (this.entrySupersedeButton) this.entrySupersedeButton.addEventListener('click', () => this.openSupersedeEditor());
      if (this.entryArchiveButton) this.entryArchiveButton.addEventListener('click', () => this.changeEntryLifecycle('archive'));
      if (this.entryRestoreButton) this.entryRestoreButton.addEventListener('click', () => this.changeEntryLifecycle('restore'));
      if (this.entryDeleteButton) this.entryDeleteButton.addEventListener('click', () => this.deleteInspectedEntry());
      for (const button of shell.querySelectorAll('[data-knowledge-entry-cancel]')) button.addEventListener('click', () => this.closeEntryEditor());
      if (this.entryForm) this.entryForm.addEventListener('submit', event => { event.preventDefault(); this.saveEntryEditor(); });
      const entryScopeInput = this.entryForm && this.entryForm.elements.namedItem('scope_kind');
      if (entryScopeInput) entryScopeInput.addEventListener('change', () => this.syncEntryScopeField());
      for (const button of shell.querySelectorAll('[data-knowledge-supersede-cancel]')) button.addEventListener('click', () => this.closeSupersedeEditor());
      if (this.supersedeForm) this.supersedeForm.addEventListener('submit', event => { event.preventDefault(); this.saveSupersedeEditor(); });
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
          if (event.type === 'node' && event.detail && event.detail.key) {
            this.closeGraphContextMenu();
            this.selectGraphObject('node', event.detail.key, event.detail);
            if (this.graphCanvas) this.graphCanvas.focus({preventScroll: true});
          }
          if (event.type === 'edge' && event.detail && event.detail.key) {
            this.closeGraphContextMenu();
            this.selectGraphObject('edge', event.detail.key, event.detail);
            if (this.graphCanvas) this.graphCanvas.focus({preventScroll: true});
          }
          if (event.type === 'boxselect' && event.detail) this.selectGraphObjects(event.detail.keys, event.detail.additive);
          if (event.type === 'background' && !(event.detail && event.detail.additive)) {
            this.closeGraphContextMenu();
            this.clearGraphSelection();
          }
          if (event.type === 'context' && event.detail) this.showGraphContextMenu(event.detail);
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

    showGraphContextMenu(target, options) {
      options = options || {};
      if (!this.graphContextMenu || !target || !['node', 'edge'].includes(target.kind) || !target.key) return false;
      const selection = this.graphAdapter ? this.graphAdapter.selectionSnapshot().items : [];
      if (!selection.some(item => item.kind === target.kind && item.key === target.key)) this.selectGraphObject(target.kind, target.key);
      this.graphContextTarget = {kind: target.kind, key: String(target.key)};
      const object = graphObjectForSelection(this.graphContextTarget);
      const isNode = target.kind === 'node';
      const graph = this.graphAdapter && this.graphAdapter.target && this.graphAdapter.target.graph;
      const pinned = !!(isNode && graph && graph.hasNode(target.key) && graph.getNodeAttribute(target.key, 'pinned'));
      if (this.graphContextHeading) {
        const count = this.graphAdapter ? this.graphAdapter.selectionSnapshot().items.length : 1;
        this.graphContextHeading.textContent = count > 1 ? `${count} selected` : `${target.kind} · ${target.key}`;
      }
      if (this.graphContextPinLabel) this.graphContextPinLabel.textContent = pinned ? 'Release local pin' : 'Pin locally';
      for (const button of this.graphContextButtons) {
        const action = button.dataset.knowledgeContextAction;
        button.hidden = ['incoming', 'outgoing'].includes(action) ? !object : ['center', 'pin'].includes(action) ? !isNode : false;
      }
      this.graphContextMenu.hidden = false;
      const stage = this.graphContextMenu.parentElement;
      const stageWidth = stage && stage.clientWidth || 0;
      const stageHeight = stage && stage.clientHeight || 0;
      const x = Number.isFinite(Number(target.x)) ? Number(target.x) : stageWidth / 2;
      const y = Number.isFinite(Number(target.y)) ? Number(target.y) : stageHeight / 2;
      const width = this.graphContextMenu.offsetWidth || 248;
      const height = this.graphContextMenu.offsetHeight || 280;
      this.graphContextMenu.style.left = `${Math.max(8, Math.min(x, Math.max(8, stageWidth - width - 8)))}px`;
      this.graphContextMenu.style.top = `${Math.max(8, Math.min(y, Math.max(8, stageHeight - height - 8)))}px`;
      if (options.focus) {
        const first = this.graphContextButtons.find(button => !button.hidden && !button.disabled);
        if (first) first.focus();
      }
      return true;
    }

    closeGraphContextMenu(options) {
      if (!this.graphContextMenu) return false;
      const wasOpen = !this.graphContextMenu.hidden;
      if (wasOpen) this.graphContextMenu.hidden = true;
      this.graphContextTarget = null;
      if (wasOpen && options && options.focusGraph && this.graphCanvas) this.graphCanvas.focus({preventScroll: true});
      return wasOpen;
    }

    runGraphContextAction(action) {
      const target = this.graphContextTarget || (this.graphAdapter && this.graphAdapter.selectionSnapshot().primary);
      if (!target) return false;
      let changed = false;
      if (action === 'inspect') changed = this.applyPrimaryGraphSelection(target);
      if (action === 'incoming' || action === 'outgoing') changed = this.expandGraph(action, target);
      if (action === 'center' && target.kind === 'node' && this.graphViewport) changed = this.graphViewport.centerNode(target.key);
      if (action === 'pin' && target.kind === 'node' && this.graphRenderer) changed = this.graphRenderer.toggleNodePin(target.key);
      if (action === 'hide' || action === 'isolate') changed = this.applyGraphViewAction(action);
      this.closeGraphContextMenu({focusGraph: true});
      return changed;
    }

    handleGraphKeyDown(event) {
      const primary = this.graphAdapter && this.graphAdapter.selectionSnapshot().primary;
      if ((event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) && primary) {
        event.preventDefault();
        return this.showGraphContextMenu(primary, {focus: true});
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        if (!this.closeGraphContextMenu({focusGraph: true})) this.clearGraphSelection();
        return true;
      }
      const action = graphKeyboardAction(event);
      if (!action) return false;
      event.preventDefault();
      if (['undo', 'reveal'].includes(action)) return this.applyGraphViewAction(action);
      if (!primary) return false;
      this.graphContextTarget = primary;
      return this.runGraphContextAction(action);
    }

    handleContextMenuKeyDown(event) {
      const available = this.graphContextButtons.filter(button => !button.hidden && !button.disabled);
      const index = available.indexOf(this.shell.ownerDocument.activeElement);
      let next = -1;
      if (event.key === 'ArrowDown') next = (index + 1 + available.length) % available.length;
      if (event.key === 'ArrowUp') next = (index - 1 + available.length) % available.length;
      if (event.key === 'Home') next = 0;
      if (event.key === 'End') next = available.length - 1;
      if (event.key === 'Escape') {
        event.preventDefault();
        this.closeGraphContextMenu({focusGraph: true});
        return true;
      }
      if (next < 0 || !available[next]) return false;
      event.preventDefault();
      available[next].focus();
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
        this.cancelInspectorSupport();
        this.inspectedObject = null;
        this.inspectedRecord = null;
        if (this.inspector.sendButton) this.inspector.sendButton.disabled = true;
        if (this.graphExpandActions) this.graphExpandActions.hidden = true;
        if (this.chunkActions) this.chunkActions.hidden = true;
        if (this.entryActions) this.entryActions.hidden = true;
      }
    }

    cancelInspectorSupport() {
      if (!this.client || typeof this.client.cancel !== 'function') return;
      this.client.cancel('inspector-evidence');
      this.client.cancel('inspector-relations');
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
      this.inspectedRecord = record;
      this.renderInspectorSupport(objectKind, record, [], [], []);
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
      const chunkSelected = objectKind === 'chunk';
      const archivedChunk = chunkSelected && record.state === 'archived';
      if (this.chunkActions) this.chunkActions.hidden = !chunkSelected;
      if (this.chunkArchiveButton) this.chunkArchiveButton.hidden = !chunkSelected || archivedChunk;
      if (this.chunkRestoreButton) this.chunkRestoreButton.hidden = !archivedChunk;
      if (this.chunkDeleteButton) this.chunkDeleteButton.hidden = !archivedChunk;
      if (this.entryCreateButton) this.entryCreateButton.disabled = !chunkSelected || record.state === 'archived';
      if (this.chunkMutationStatus) this.chunkMutationStatus.textContent = '';
      const entrySelected = objectKind === 'entry';
      const archivedEntry = entrySelected && record.state === 'archived';
      const supersededEntry = entrySelected && record.state === 'superseded';
      const activeEntry = entrySelected && record.state === 'active';
      if (this.entryActions) this.entryActions.hidden = !entrySelected;
      if (this.entryEditButton) this.entryEditButton.hidden = !entrySelected || archivedEntry || supersededEntry;
      if (this.entrySupersedeButton) this.entrySupersedeButton.hidden = !activeEntry;
      if (this.entryArchiveButton) this.entryArchiveButton.hidden = !entrySelected || archivedEntry || supersededEntry;
      if (this.entryRestoreButton) this.entryRestoreButton.hidden = !archivedEntry;
      if (this.entryDeleteButton) this.entryDeleteButton.hidden = !archivedEntry;
      if (this.entryMutationStatus) this.entryMutationStatus.textContent = '';
      this.setInspectorMode('content');
      this.loadInspectorSupport(objectKind, record);
      this.shell.dispatchEvent(new CustomEvent('koder:knowledge-inspected', {
        detail: {objectKind, id: String(record.id || ''), record, label: plainTextLabel(title)}
      }));
    }

    async loadInspectorSupport(objectKind, record) {
      this.cancelInspectorSupport();
      const id = String(record && record.id || '');
      if (!id) return;
      const evidenceTask = objectKind === 'entry' && typeof this.client.entryEvidence === 'function'
        ? this.client.entryEvidence(id, {limit: 50}, {channel: 'inspector-evidence'}).then(response => ({response})).catch(error => ({error}))
        : Promise.resolve({response: {evidence: [], page: {}}});
      const relationsTask = ['chunk', 'entry'].includes(objectKind) && typeof this.client.neighbors === 'function'
        ? this.client.neighbors({object: {kind: objectKind, id}, kinds: ['contradicts'], limit: 25}, {channel: 'inspector-relations'}).then(response => ({response})).catch(error => ({error}))
        : Promise.resolve({response: {neighbors: [], page: {}}});
      const [evidenceResult, relationsResult] = await Promise.all([evidenceTask, relationsTask]);
      if (!this.inspectedObject || this.inspectedObject.kind !== objectKind || this.inspectedObject.id !== id) return;
      const evidence = Array.isArray(evidenceResult.response && evidenceResult.response.evidence) ? evidenceResult.response.evidence : [];
      const neighbors = Array.isArray(relationsResult.response && relationsResult.response.neighbors) ? relationsResult.response.neighbors : [];
      const errors = [evidenceResult.error, relationsResult.error].filter(error => error && !['canceled', 'stale_response'].includes(error.code));
      const truncated = !!(evidenceResult.response && evidenceResult.response.page && evidenceResult.response.page.next_cursor) ||
        !!(relationsResult.response && relationsResult.response.page && relationsResult.response.page.next_cursor);
      this.renderInspectorSupport(objectKind, record, evidence, neighbors, errors, truncated);
    }

    renderInspectorSupport(objectKind, record, evidence, neighbors, errors, truncated) {
      const warnings = inspectorWarnings(objectKind, record, neighbors, new Date());
      if (this.inspector.warnings) {
        this.inspector.warnings.replaceChildren();
        this.inspector.warnings.hidden = !warnings.length;
        for (const warning of warnings) {
          const item = this.shell.ownerDocument.createElement('div');
          item.className = 'knowledge-inspector-warning' + (warning.level === 'danger' ? ' is-danger' : '');
          const icon = this.shell.ownerDocument.createElement('i');
          icon.className = warning.level === 'danger' ? 'bi bi-exclamation-octagon' : 'bi bi-clock-history';
          icon.setAttribute('aria-hidden', 'true');
          const text = this.shell.ownerDocument.createElement('span');
          text.textContent = warning.text;
          item.append(icon, text);
          this.inspector.warnings.appendChild(item);
        }
      }
      const rows = objectKind === 'entry' ? applicabilityRows(record.applicability) : [];
      if (this.inspector.applicability && this.inspector.applicabilityList) {
        this.inspector.applicability.hidden = !rows.length;
        this.inspector.applicabilityList.replaceChildren();
        for (const row of rows) {
          const item = this.shell.ownerDocument.createElement('div');
          item.className = 'knowledge-applicability-item';
          const label = this.shell.ownerDocument.createElement('strong');
          const value = this.shell.ownerDocument.createElement('span');
          label.textContent = row.label;
          value.textContent = row.value;
          item.append(label, value);
          this.inspector.applicabilityList.appendChild(item);
        }
      }
      if (this.inspector.evidence && this.inspector.evidenceList) {
        const showEvidence = objectKind === 'entry';
        this.inspector.evidence.hidden = !showEvidence;
        this.inspector.evidenceList.replaceChildren();
        if (this.inspector.evidenceCount) this.inspector.evidenceCount.textContent = showEvidence ? String(evidence.length) : '';
        for (const item of evidence) {
          const card = this.shell.ownerDocument.createElement('article');
          card.className = 'knowledge-evidence-card';
          const header = this.shell.ownerDocument.createElement('header');
          const title = this.shell.ownerDocument.createElement('strong');
          const quality = this.shell.ownerDocument.createElement('span');
          title.textContent = String(item.source && (item.source.title || item.source.id) || displayLabel(item.type) || 'Evidence');
          quality.textContent = [displayLabel(item.type), displayLabel(item.quality)].filter(Boolean).join(' · ');
          header.append(title, quality);
          card.appendChild(header);
          const excerpt = String(item.source && item.source.excerpt || '').trim();
          if (excerpt) {
            const paragraph = this.shell.ownerDocument.createElement('p');
            paragraph.textContent = plainTextLabel(excerpt, 300);
            card.appendChild(paragraph);
          }
          const href = safeExternalURL(item.source && item.source.uri);
          if (href) {
            const link = this.shell.ownerDocument.createElement('a');
            link.href = href;
            link.target = '_blank';
            link.rel = 'noopener noreferrer';
            link.textContent = href;
            card.appendChild(link);
          }
          this.inspector.evidenceList.appendChild(card);
        }
        if (showEvidence && !evidence.length) {
          const empty = this.shell.ownerDocument.createElement('span');
          empty.className = 'knowledge-support-status';
          empty.textContent = errors && errors.length ? 'Evidence could not be loaded.' : 'No evidence is attached to this entry.';
          this.inspector.evidenceList.appendChild(empty);
        }
      }
      if (this.inspector.supportStatus) {
        this.inspector.supportStatus.textContent = errors && errors.length ? 'Some supporting details are temporarily unavailable.' : truncated ? 'Showing the first bounded set of supporting records.' : '';
      }
    }

    openChunkEditor(record) {
      if (!this.chunkDialog || !this.chunkForm) return false;
      this.chunkEditorRecord = record && record.id ? record : null;
      const values = chunkEditorValues(this.chunkEditorRecord);
      for (const [name, value] of Object.entries(values)) {
        const input = this.chunkForm.elements.namedItem(name);
        if (!input) continue;
        if (input.type === 'checkbox') input.checked = !!value;
        else input.value = value;
      }
      if (this.chunkDialogTitle) this.chunkDialogTitle.textContent = this.chunkEditorRecord ? 'Edit chunk' : 'Create chunk';
      if (this.chunkSubmitButton) this.chunkSubmitButton.textContent = this.chunkEditorRecord ? 'Save changes' : 'Create chunk';
      if (this.chunkFormError) { this.chunkFormError.hidden = true; this.chunkFormError.textContent = ''; }
      this.syncChunkScopeField();
      if (typeof this.chunkDialog.showModal === 'function') this.chunkDialog.showModal();
      else this.chunkDialog.setAttribute('open', '');
      const title = this.chunkForm.elements.namedItem('title');
      if (title) title.focus();
      return true;
    }

    closeChunkEditor() {
      if (!this.chunkDialog) return;
      if (typeof this.chunkDialog.close === 'function') this.chunkDialog.close();
      else this.chunkDialog.removeAttribute('open');
      this.chunkEditorRecord = null;
    }

    syncChunkScopeField() {
      if (!this.chunkForm) return;
      const kind = this.chunkForm.elements.namedItem('scope_kind');
      const selector = this.chunkForm.elements.namedItem('scope_selector');
      if (!kind || !selector) return;
      selector.required = kind.value !== 'global';
      selector.disabled = kind.value === 'global';
      if (selector.disabled) selector.value = '';
    }

    async saveChunkEditor() {
      if (!this.chunkForm || !this.client) return false;
      if (this.chunkFormError) { this.chunkFormError.hidden = true; this.chunkFormError.textContent = ''; }
      const values = Object.fromEntries(new FormData(this.chunkForm).entries());
      values.review_approved = !!this.chunkForm.elements.namedItem('review_approved').checked;
      let content;
      try { content = chunkContentFromValues(values); }
      catch (error) {
        if (this.chunkFormError) { this.chunkFormError.hidden = false; this.chunkFormError.textContent = String(error && error.message || error); }
        return false;
      }
      if (this.chunkSubmitButton) this.chunkSubmitButton.disabled = true;
      try {
        const existing = this.chunkEditorRecord;
        const response = existing
          ? await this.client.updateChunk(existing.id, {chunk: content, expected_revision: existing.revision.number, reason: 'Edited in Knowledge explorer', review_approved: values.review_approved}, {channel: 'chunk-mutation'})
          : await this.client.createChunk({chunk: content, review_approved: values.review_approved}, {channel: 'chunk-mutation'});
        const chunk = response && response.chunk;
        this.closeChunkEditor();
        if (chunk) {
          this.writeURLState({...this.urlState, query: '', kind: '', scopeKind: '', state: chunk.state === 'archived' ? 'archived' : '', tag: '', objectKind: 'chunk', id: chunk.id}, false);
          await this.refresh();
        }
        return true;
      } catch (error) {
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'Knowledge could not save this chunk.');
        if (this.chunkFormError) { this.chunkFormError.hidden = false; this.chunkFormError.textContent = requestID ? `${message} Audit ID: ${requestID}` : message; }
        return false;
      } finally {
        if (this.chunkSubmitButton) this.chunkSubmitButton.disabled = false;
      }
    }

    async changeChunkLifecycle(action) {
      const chunk = this.inspectedRecord;
      if (!chunk || !this.inspectedObject || this.inspectedObject.kind !== 'chunk') return false;
      const verb = action === 'archive' ? 'archive' : 'restore';
      if (typeof globalThis.confirm === 'function' && !globalThis.confirm(`${verb === 'archive' ? 'Archive' : 'Restore'} “${chunk.title}”?`)) return false;
      return this.runChunkMutation(async () => this.client.chunkLifecycle(chunk.id, action, {
        expected_revision: chunk.revision.number, reason: `${verb === 'archive' ? 'Archived' : 'Restored'} in Knowledge explorer`,
      }, {channel: 'chunk-mutation'}), action);
    }

    async deleteInspectedChunk() {
      const chunk = this.inspectedRecord;
      if (!chunk || chunk.state !== 'archived' || !this.inspectedObject || this.inspectedObject.kind !== 'chunk') return false;
      if (typeof globalThis.confirm === 'function' && !globalThis.confirm(`Delete “${chunk.title}”? This erases it and cannot be undone.`)) return false;
      return this.runChunkMutation(() => this.client.deleteChunk(chunk.id, {
        expected_revision: chunk.revision.number, confirmed: true, cascade: false,
      }, {channel: 'chunk-mutation'}), 'delete');
    }

    async runChunkMutation(operation, action) {
      for (const button of [this.chunkEditButton, this.chunkArchiveButton, this.chunkRestoreButton, this.chunkDeleteButton]) if (button) button.disabled = true;
      if (this.chunkMutationStatus) this.chunkMutationStatus.textContent = `${displayLabel(action)} in progress…`;
      try {
        const response = await operation();
        if (action === 'delete') {
          this.writeURLState({...this.urlState, objectKind: '', id: ''}, false);
        } else if (response && response.chunk) {
          const chunk = response.chunk;
          this.writeURLState({...this.urlState, state: chunk.state === 'archived' ? 'archived' : '', objectKind: 'chunk', id: chunk.id}, false);
        }
        await this.refresh();
        return true;
      } catch (error) {
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'Knowledge could not change this chunk.');
        if (this.chunkMutationStatus) this.chunkMutationStatus.textContent = requestID ? `${message} Audit ID: ${requestID}` : message;
        return false;
      } finally {
        for (const button of [this.chunkEditButton, this.chunkArchiveButton, this.chunkRestoreButton, this.chunkDeleteButton]) if (button) button.disabled = false;
      }
    }

    openEntryEditor(record, chunk) {
      if (!this.entryDialog || !this.entryForm) return false;
      this.entryEditorRecord = record && record.id ? record : null;
      this.entryEditorChunk = this.entryEditorRecord ? null : chunk && chunk.id ? chunk : null;
      const values = entryEditorValues(this.entryEditorRecord, this.entryEditorChunk);
      if (!values.chunk_id) return false;
      for (const [name, value] of Object.entries(values)) {
        const input = this.entryForm.elements.namedItem(name);
        if (!input) continue;
        if (input.type === 'checkbox') input.checked = !!value;
        else input.value = value;
      }
      if (this.entryDialogTitle) this.entryDialogTitle.textContent = this.entryEditorRecord ? 'Edit entry' : 'Create entry';
      if (this.entrySubmitButton) this.entrySubmitButton.textContent = this.entryEditorRecord ? 'Save changes' : 'Create entry';
      if (this.entryFormError) { this.entryFormError.hidden = true; this.entryFormError.textContent = ''; }
      this.syncEntryScopeField();
      if (typeof this.entryDialog.showModal === 'function') this.entryDialog.showModal();
      else this.entryDialog.setAttribute('open', '');
      const title = this.entryForm.elements.namedItem('title');
      if (title) title.focus();
      return true;
    }

    closeEntryEditor() {
      if (!this.entryDialog) return;
      if (typeof this.entryDialog.close === 'function') this.entryDialog.close();
      else this.entryDialog.removeAttribute('open');
      this.entryEditorRecord = null;
      this.entryEditorChunk = null;
    }

    syncEntryScopeField() {
      if (!this.entryForm) return;
      const kind = this.entryForm.elements.namedItem('scope_kind');
      const selector = this.entryForm.elements.namedItem('scope_selector');
      if (!kind || !selector) return;
      selector.required = kind.value !== 'global';
      selector.disabled = kind.value === 'global';
      if (selector.disabled) selector.value = '';
    }

    async saveEntryEditor() {
      if (!this.entryForm || !this.client) return false;
      if (this.entryFormError) { this.entryFormError.hidden = true; this.entryFormError.textContent = ''; }
      const values = Object.fromEntries(new FormData(this.entryForm).entries());
      values.review_approved = !!this.entryForm.elements.namedItem('review_approved').checked;
      let content;
      try { content = entryContentFromValues(values); }
      catch (error) {
        if (this.entryFormError) { this.entryFormError.hidden = false; this.entryFormError.textContent = String(error && error.message || error); }
        return false;
      }
      if (this.entrySubmitButton) this.entrySubmitButton.disabled = true;
      try {
        const existing = this.entryEditorRecord;
        const response = existing
          ? await this.client.updateEntry(existing.id, {entry: content, expected_revision: existing.revision.number, reason: 'Edited in Knowledge explorer', review_approved: values.review_approved}, {channel: 'entry-mutation'})
          : await this.client.createEntry({chunk_id: String(values.chunk_id), entry: content, review_approved: values.review_approved}, {channel: 'entry-mutation'});
        const entry = response && response.entry;
        this.closeEntryEditor();
        if (entry) {
          this.writeURLState({...this.urlState, objectKind: 'entry', id: entry.id}, false);
          await this.refresh();
        }
        return true;
      } catch (error) {
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'Knowledge could not save this entry.');
        if (this.entryFormError) { this.entryFormError.hidden = false; this.entryFormError.textContent = requestID ? `${message} Audit ID: ${requestID}` : message; }
        return false;
      } finally {
        if (this.entrySubmitButton) this.entrySubmitButton.disabled = false;
      }
    }

    async openSupersedeEditor() {
      const entry = this.inspectedRecord;
      if (!entry || entry.state !== 'active' || !this.supersedeDialog || !this.supersedeForm) return false;
      this.supersedeEntryRecord = entry;
      const select = this.supersedeForm.elements.namedItem('replacement_entry_id');
      select.replaceChildren();
      select.disabled = true;
      if (this.supersedeSubmitButton) this.supersedeSubmitButton.disabled = true;
      if (this.supersedeError) { this.supersedeError.hidden = true; this.supersedeError.textContent = ''; }
      if (typeof this.supersedeDialog.showModal === 'function') this.supersedeDialog.showModal();
      else this.supersedeDialog.setAttribute('open', '');
      try {
        const response = await this.client.listEntries({chunk_id: entry.chunk_id, state: 'active', limit: 200}, {channel: 'entry-supersede-options'});
        if (!this.supersedeEntryRecord || this.supersedeEntryRecord.id !== entry.id) return false;
        const entries = (Array.isArray(response && response.entries) ? response.entries : []).filter(item => item.id !== entry.id);
        for (const replacement of entries) {
          const option = this.shell.ownerDocument.createElement('option');
          option.value = replacement.id;
          option.textContent = `${replacement.title} · ${replacement.id}`;
          select.appendChild(option);
        }
        select.disabled = !entries.length;
        if (this.supersedeSubmitButton) this.supersedeSubmitButton.disabled = !entries.length;
        if (!entries.length && this.supersedeError) {
          this.supersedeError.hidden = false;
          this.supersedeError.textContent = 'Create another active entry in this chunk before superseding this one.';
        } else if (response && response.page && response.page.next_cursor && this.supersedeError) {
          this.supersedeError.hidden = false;
          this.supersedeError.textContent = 'Showing the first 200 active replacement candidates.';
        }
        return entries.length > 0;
      } catch (error) {
        if (error && ['canceled', 'stale_response'].includes(error.code)) return false;
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'Replacement entries could not be loaded.');
        if (this.supersedeError) { this.supersedeError.hidden = false; this.supersedeError.textContent = requestID ? `${message} Audit ID: ${requestID}` : message; }
        return false;
      }
    }

    closeSupersedeEditor() {
      if (this.client && typeof this.client.cancel === 'function') this.client.cancel('entry-supersede-options');
      if (this.supersedeDialog) {
        if (typeof this.supersedeDialog.close === 'function') this.supersedeDialog.close();
        else this.supersedeDialog.removeAttribute('open');
      }
      this.supersedeEntryRecord = null;
    }

    async saveSupersedeEditor() {
      const entry = this.supersedeEntryRecord;
      if (!entry || !this.supersedeForm) return false;
      const values = Object.fromEntries(new FormData(this.supersedeForm).entries());
      if (!values.replacement_entry_id) return false;
      if (this.supersedeSubmitButton) this.supersedeSubmitButton.disabled = true;
      const success = await this.runEntryMutation(() => this.client.supersedeEntry(entry.id, {
        replacement_entry_id: values.replacement_entry_id, expected_revision: entry.revision.number,
        reason: String(values.reason || '').trim() || 'Superseded in Knowledge explorer',
      }, {channel: 'entry-mutation'}), 'supersede');
      if (success) this.closeSupersedeEditor();
      else if (this.supersedeSubmitButton) this.supersedeSubmitButton.disabled = false;
      return success;
    }

    async changeEntryLifecycle(action) {
      const entry = this.inspectedRecord;
      if (!entry || !this.inspectedObject || this.inspectedObject.kind !== 'entry') return false;
      const verb = action === 'archive' ? 'archive' : 'restore';
      if (typeof globalThis.confirm === 'function' && !globalThis.confirm(`${verb === 'archive' ? 'Archive' : 'Restore'} “${entry.title}”?`)) return false;
      return this.runEntryMutation(() => this.client.entryLifecycle(entry.id, action, {
        expected_revision: entry.revision.number, reason: `${verb === 'archive' ? 'Archived' : 'Restored'} in Knowledge explorer`,
      }, {channel: 'entry-mutation'}), action);
    }

    async deleteInspectedEntry() {
      const entry = this.inspectedRecord;
      if (!entry || entry.state !== 'archived' || !this.inspectedObject || this.inspectedObject.kind !== 'entry') return false;
      if (typeof globalThis.confirm === 'function' && !globalThis.confirm(`Delete “${entry.title}”? This erases it and cannot be undone.`)) return false;
      return this.runEntryMutation(() => this.client.deleteEntry(entry.id, {
        expected_revision: entry.revision.number, confirmed: true,
      }, {channel: 'entry-mutation'}), 'delete', entry.chunk_id);
    }

    async runEntryMutation(operation, action, parentChunkID) {
      const buttons = [this.entryEditButton, this.entrySupersedeButton, this.entryArchiveButton, this.entryRestoreButton, this.entryDeleteButton];
      for (const button of buttons) if (button) button.disabled = true;
      if (this.entryMutationStatus) this.entryMutationStatus.textContent = `${displayLabel(action)} in progress…`;
      try {
        const response = await operation();
        if (action === 'delete') this.writeURLState({...this.urlState, objectKind: parentChunkID ? 'chunk' : '', id: parentChunkID || ''}, false);
        else if (response && response.entry) this.writeURLState({...this.urlState, objectKind: 'entry', id: response.entry.id}, false);
        await this.refresh();
        return true;
      } catch (error) {
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'Knowledge could not change this entry.');
        const status = this.supersedeDialog && this.supersedeDialog.open ? this.supersedeError : this.entryMutationStatus;
        if (status) { status.hidden = false; status.textContent = requestID ? `${message} Audit ID: ${requestID}` : message; }
        return false;
      } finally {
        for (const button of buttons) if (button) button.disabled = false;
      }
    }

    async expandGraph(direction, selection) {
      const selected = graphObjectForSelection(selection) || this.inspectedObject;
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
      if (this.graphCanvas) {
        this.graphCanvas.removeEventListener('keydown', this.onGraphKeyDown);
        this.graphCanvas.removeEventListener('contextmenu', this.onGraphNativeContext);
      }
      if (this.graphContextMenu) this.graphContextMenu.removeEventListener('keydown', this.onContextMenuKeyDown);
      for (const [button, handler] of this.graphContextClickHandlers) button.removeEventListener('click', handler);
      this.shell.ownerDocument.removeEventListener('pointerdown', this.onDocumentPointerDown, true);
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

  return Object.freeze({panes, states, BrowserApp, normalizePane, normalizeState, stateForError, presentationForState, adjacentPane, safeReturnPath, returnPathFromSearch, chatSelectionFromSearch, browserStateFromSearch, searchForBrowserState, displayLabel, plainTextLabel, graphSnapshotRequest, graphExpansionRequest, graphObjectForSelection, graphKeyboardAction, applicabilityRows, inspectorWarnings, safeExternalURL, commaValues, chunkContentFromValues, chunkEditorValues, localDateTimeValue, entryContentFromValues, entryEditorValues, graphDebugEnabled, supportsWebGL, graphEnvironment, sanitizedMarkdownHTML, mount});
});
