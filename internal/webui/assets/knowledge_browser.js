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
  const localPreferencesKey = 'koder.knowledge.explorer.preferences.v1';
  const localPreferencesVersion = 1;
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

  function browserSearchHasState(search) {
    let params;
    try { params = new URLSearchParams(String(search || '')); } catch (_) { return false; }
    return browserURLKeys.some(key => params.has(key));
  }

  function normalizedBrowserState(value) {
    return browserStateFromSearch(searchForBrowserState('', value));
  }

  function browserStatesEqual(left, right) {
    left = normalizedBrowserState(left);
    right = normalizedBrowserState(right);
    return Object.keys(left).every(key => left[key] === right[key]);
  }

  function graphPreferenceObject(value) {
    value = value && typeof value === 'object' ? value : {};
    const kind = String(value.kind || '').trim().toLowerCase();
    const id = boundedText(value.id, 160);
    return ['chunk', 'entry'].includes(kind) && /^[A-Za-z0-9_-]+$/.test(id) ? {kind, id} : null;
  }

  function graphPreferenceKey(value, node) {
    value = boundedText(value, 220);
    const pattern = node ? /^(?:chunk|entry|evidence):[A-Za-z0-9_-]+$/ : /^[A-Za-z0-9_-]+$/;
    return pattern.test(value) ? value : '';
  }

  function uniqueBounded(values, limit, normalize) {
    const result = [];
    const seen = new Set();
    for (const value of Array.isArray(values) ? values : []) {
      const normalized = normalize(value);
      const token = typeof normalized === 'string' ? normalized : JSON.stringify(normalized);
      if (!normalized || seen.has(token)) continue;
      seen.add(token);
      result.push(normalized);
      if (result.length >= limit) break;
    }
    return result;
  }

  function normalizeLocalPreferences(value) {
    value = value && typeof value === 'object' ? value : {};
    const graph = value.graph && typeof value.graph === 'object' ? value.graph : {};
    const pinnedNodes = uniqueBounded(graph.pinnedNodes, 500, item => {
      item = item && typeof item === 'object' ? item : {};
      const key = graphPreferenceKey(item.key, true);
      const x = Number(item.x);
      const y = Number(item.y);
      return key && Number.isFinite(x) && Number.isFinite(y) && Math.abs(x) <= 1000000 && Math.abs(y) <= 1000000
        ? {key, x, y} : null;
    });
    const frontier = uniqueBounded(graph.frontier, 50, item => {
      item = item && typeof item === 'object' ? item : {};
      const object = graphPreferenceObject(item);
      const direction = String(item.direction || '').trim().toLowerCase();
      return object && ['incoming', 'outgoing'].includes(direction) ? {...object, direction} : null;
    });
    return Object.freeze({
      version: localPreferencesVersion,
      savedViewID: /^[A-Za-z0-9_-]+$/.test(boundedText(value.savedViewID, 160)) ? boundedText(value.savedViewID, 160) : '',
      browser: normalizedBrowserState(value.browser),
      mobilePane: normalizePane(value.mobilePane),
      graph: Object.freeze({
        root: graphPreferenceObject(graph.root),
        presentation: String(graph.presentation || '').trim().toLowerCase() === 'table' ? 'table' : 'canvas',
        hiddenNodes: uniqueBounded(graph.hiddenNodes, 1000, item => graphPreferenceKey(item, true)),
        hiddenEdges: uniqueBounded(graph.hiddenEdges, 2000, item => graphPreferenceKey(item, false)),
        pinnedNodes,
        frontier,
      }),
    });
  }

  function loadLocalPreferences(storage, key) {
    if (!storage || typeof storage.getItem !== 'function') return null;
    try {
      const raw = storage.getItem(key || localPreferencesKey);
      if (!raw) return null;
      const value = JSON.parse(raw);
      if (!value || Number(value.version) !== localPreferencesVersion) return null;
      return normalizeLocalPreferences(value);
    } catch (_) { return null; }
  }

  function saveLocalPreferences(storage, value, key) {
    const normalized = normalizeLocalPreferences(value);
    if (!storage || typeof storage.setItem !== 'function') return normalized;
    try { storage.setItem(key || localPreferencesKey, JSON.stringify(normalized)); } catch (_) {}
    return normalized;
  }

  function clearLocalPreferences(storage, key) {
    if (!storage || typeof storage.removeItem !== 'function') return false;
    try { storage.removeItem(key || localPreferencesKey); return true; } catch (_) { return false; }
  }

  function graphViewStateFromPreferences(value) {
    const preferences = normalizeLocalPreferences(value);
    return {
      browser: {
        query: preferences.browser.query, kind: preferences.browser.kind, scope_kind: preferences.browser.scopeKind,
        state: preferences.browser.state, tag: preferences.browser.tag, object_kind: preferences.browser.objectKind, id: preferences.browser.id,
      },
      mobile_pane: preferences.mobilePane,
      root: preferences.graph.root,
      presentation: preferences.graph.presentation,
      hidden_nodes: preferences.graph.hiddenNodes,
      hidden_edges: preferences.graph.hiddenEdges,
      pinned_nodes: preferences.graph.pinnedNodes,
      frontier: preferences.graph.frontier,
      layout: 'force_atlas2',
    };
  }

  function preferencesFromGraphViewState(value, savedViewID) {
    value = value && typeof value === 'object' ? value : {};
    const browser = value.browser && typeof value.browser === 'object' ? value.browser : {};
    return normalizeLocalPreferences({
      savedViewID,
      browser: {
        query: browser.query, kind: browser.kind, scopeKind: browser.scope_kind, state: browser.state,
        tag: browser.tag, objectKind: browser.object_kind, id: browser.id,
      },
      mobilePane: value.mobile_pane,
      graph: {
        root: value.root, presentation: value.presentation, hiddenNodes: value.hidden_nodes, hiddenEdges: value.hidden_edges,
        pinnedNodes: value.pinned_nodes, frontier: value.frontier,
      },
    });
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

  function comparableEditorValue(value) {
    if (value === undefined || value === null) return '';
    if (typeof value === 'boolean') return value ? 'true' : 'false';
    if (Array.isArray(value)) return JSON.stringify(value);
    return String(value).trim();
  }

  function editorConflict(base, draft, latest, options) {
    base = base || {};
    draft = draft || {};
    latest = latest || {};
    options = options || {};
    const ignored = new Set(options.ignore || ['review_approved']);
    const keys = [...new Set([...Object.keys(base), ...Object.keys(draft), ...Object.keys(latest)])].filter(key => !ignored.has(key)).sort();
    const serverFields = [];
    const draftFields = [];
    const overlappingFields = [];
    const merged = {...latest};
    for (const key of keys) {
      const baseValue = comparableEditorValue(base[key]);
      const draftValue = comparableEditorValue(draft[key]);
      const latestValue = comparableEditorValue(latest[key]);
      const serverChanged = latestValue !== baseValue;
      const draftChanged = draftValue !== baseValue;
      if (serverChanged) serverFields.push(key);
      if (draftChanged) draftFields.push(key);
      if (serverChanged && draftChanged && draftValue !== latestValue) overlappingFields.push(key);
      if (draftChanged) merged[key] = draft[key];
    }
    for (const key of ignored) if (Object.hasOwn(draft, key)) merged[key] = key === 'review_approved' ? false : draft[key];
    return {serverFields, draftFields, overlappingFields, merged};
  }

  function historyProjection(objectKind, record) {
    const value = record && record[objectKind];
    if (!value) return null;
    let projection;
    if (objectKind === 'chunk') projection = chunkEditorValues(value);
    else if (objectKind === 'entry') projection = entryEditorValues(value);
    else if (objectKind === 'link') {
      projection = {
        source: value.source && `${value.source.kind}:${value.source.id}`, target: value.target && `${value.target.kind}:${value.target.id}`,
        kind: value.kind || '', label: value.label || '', notes: value.notes || '', evidence_ids: (value.evidence_ids || []).join(', '),
      };
    } else return null;
    delete projection.review_approved;
    return {...projection, state: value.state || '', superseded_by_id: value.superseded_by_id || '', verification: value.verification && value.verification.status || ''};
  }

  function historyChangedFields(previous, current) {
    previous = previous || {};
    current = current || {};
    return [...new Set([...Object.keys(previous), ...Object.keys(current)])]
      .filter(key => comparableEditorValue(previous[key]) !== comparableEditorValue(current[key]))
      .sort();
  }

  function historyTimeline(revisions, objectKind, evidence, options) {
    revisions = Array.isArray(revisions) ? revisions : [];
    evidence = Array.isArray(evidence) ? evidence : [];
    options = options || {};
    const evidenceByID = new Map(evidence.map(item => [String(item && item.id || ''), item]));
    return revisions.map((record, index) => {
      const value = record && record[objectKind] || {};
      const revision = value.revision || {};
      const older = index + 1 < revisions.length ? historyProjection(objectKind, revisions[index + 1]) : null;
      const current = historyProjection(objectKind, record) || {};
      const isCreation = !older && !options.hasOlder;
      const changedFields = older ? historyChangedFields(older, current) : [];
      const evidenceIDs = Array.isArray(value.evidence_ids) ? value.evidence_ids.map(String) : [];
      const snippetSource = objectKind === 'entry' ? value.summary || value.body : objectKind === 'chunk' ? value.description : value.notes;
      return {
        revision: Number(revision.number) || 0, revisionID: String(revision.id || ''), reason: String(revision.reason || ''),
        actor: [revision.actor && revision.actor.kind, revision.actor && revision.actor.id].filter(Boolean).join(':'),
        timestamp: String(revision.created_at || value.updated_at || ''), state: String(value.state || ''),
        title: String(value.title || value.label || displayLabel(value.kind) || `${displayLabel(objectKind)} revision`),
        action: String(revision.reason || (isCreation ? 'Created' : `Revision ${revision.number || ''}`)).trim(),
        changedFields, isCreation, snippet: plainTextLabel(snippetSource, 320), evidenceIDs,
        evidence: evidenceIDs.map(id => evidenceByID.get(id)).filter(Boolean),
      };
    });
  }

  function deletionAssessment(objectKind, record, details) {
    record = record || {};
    details = details || {};
    const groups = [];
    const add = (label, kind, ids, count) => {
      ids = [...new Set((Array.isArray(ids) ? ids : []).map(String).filter(Boolean))];
      count = Math.max(ids.length, Number(count) || 0);
      if (count) groups.push({label, kind, ids, count});
    };
    if (objectKind === 'chunk') {
      const blockers = details.chunk_blockers || {};
      const counts = blockers.reported_counts || record.counts || {};
      add('Contained entries', 'entry', blockers.entry_ids, counts.entries);
      add('Touching relationships', 'link', blockers.link_ids, counts.links);
      add('Exclusively owned evidence', 'evidence', blockers.evidence_ids, counts.evidence);
      add('Chunk dependencies', 'chunk', blockers.dependency_ids || record.dependency_ids);
      add('Chunks depending on this one', 'chunk', blockers.dependent_chunk_ids);
    } else if (objectKind === 'entry') {
      const blockers = details.entry_blockers || {};
      add('Touching relationships', 'link', blockers.link_ids);
      add('Superseded entries using this replacement', 'entry', blockers.superseded_entry_ids);
    }
    const noun = objectKind === 'entry' ? 'entry' : 'chunk';
    return {
      blocked: groups.length > 0, groups,
      explanation: `Deleting this ${noun} permanently removes its current content and all stored revisions. Archive is reversible; deletion is not.`,
    };
  }

  const relationshipKinds = new Set(['related_to', 'part_of', 'requires', 'alternative_to', 'applies_to', 'supersedes', 'contradicts', 'caused_by', 'supported_by', 'derived_from']);
  const symmetricRelationshipKinds = new Set(['related_to', 'alternative_to', 'contradicts']);

  function relationshipShapeError(kind, source, target) {
    kind = String(kind || '').trim();
    source = source || {};
    target = target || {};
    if (!relationshipKinds.has(kind)) return 'Choose a known relationship type.';
    if (!['chunk', 'entry'].includes(source.kind) || !String(source.id || '').trim()) return 'Choose a chunk or entry as the source.';
    if (!['chunk', 'entry'].includes(target.kind) || !String(target.id || '').trim()) return 'Choose a chunk or entry as the target.';
    if (source.kind === target.kind && source.id === target.id) return 'A knowledge object cannot relate to itself.';
    if (kind === 'alternative_to' && source.kind !== target.kind) return 'Alternatives must connect two objects of the same type.';
    if (kind === 'part_of' && target.kind !== 'chunk') return 'Part of must point to a chunk.';
    if (['applies_to', 'supported_by'].includes(kind) && source.kind !== 'entry') return `${displayLabel(kind)} must start at an entry.`;
    if (['supersedes', 'contradicts', 'caused_by'].includes(kind) && (source.kind !== 'entry' || target.kind !== 'entry')) return `${displayLabel(kind)} requires two entries.`;
    return '';
  }

  function linkContentFromValues(values) {
    values = values || {};
    const source = {kind: String(values.source_kind || '').trim(), id: String(values.source_id || '').trim()};
    const target = {kind: String(values.target_kind || '').trim(), id: String(values.target_id || '').trim()};
    const kind = String(values.kind || '').trim();
    const error = relationshipShapeError(kind, source, target);
    if (error) throw new TypeError(error);
    const content = {source, target, kind};
    for (const name of ['label', 'notes']) {
      const value = String(values[name] || '').trim();
      if (value) content[name] = value;
    }
    const evidence = commaValues(values.evidence_ids);
    if (evidence.length) content.evidence_ids = evidence;
    return content;
  }

  function relationPreview(values, labels) {
    values = values || {};
    labels = labels || {};
    const source = {kind: String(values.source_kind || ''), id: String(values.source_id || '')};
    const target = {kind: String(values.target_kind || ''), id: String(values.target_id || '')};
    const kind = String(values.kind || '');
    const error = relationshipShapeError(kind, source, target);
    const sourceLabel = String(labels.source || source.id || 'Source');
    const targetLabel = String(labels.target || target.id || 'Target');
    const arrow = symmetricRelationshipKinds.has(kind) ? '↔' : '→';
    const detail = symmetricRelationshipKinds.has(kind)
      ? 'This symmetric relationship can be traversed in either direction.'
      : 'This directed relationship will become part of normal knowledge traversal.';
    return {valid: !error, path: `${sourceLabel} ${arrow} ${displayLabel(kind) || 'Relationship'} ${arrow} ${targetLabel}`, detail: error || detail};
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
      this.graphTable = options.graphTable || null;
      this.chunks = [];
      this.matches = [];
      this.page = null;
      this.lastError = null;
      this.resultMode = 'chunks';
      const search = globalThis.location && globalThis.location.search;
      if (Object.prototype.hasOwnProperty.call(options, 'preferenceStorage')) this.preferenceStorage = options.preferenceStorage;
      else {
        try { this.preferenceStorage = globalThis.localStorage || null; } catch (_) { this.preferenceStorage = null; }
      }
      this.savedPreferences = loadLocalPreferences(this.preferenceStorage);
      const explicitBrowserState = browserSearchHasState(search);
      const requestedBrowserState = browserStateFromSearch(search);
      const restoreSavedView = !!this.savedPreferences && (!explicitBrowserState || browserStatesEqual(requestedBrowserState, this.savedPreferences.browser));
      this.urlState = explicitBrowserState || !this.savedPreferences ? requestedBrowserState : this.savedPreferences.browser;
      this.graphRoot = !restoreSavedView
        ? graphPreferenceObject({kind: this.urlState.objectKind, id: this.urlState.id})
        : (this.savedPreferences && this.savedPreferences.graph.root) || graphPreferenceObject({kind: this.urlState.objectKind, id: this.urlState.id});
      this.graphFrontier = restoreSavedView ? [...this.savedPreferences.graph.frontier] : [];
      this.restoredGraphPreferences = restoreSavedView ? this.savedPreferences.graph : normalizeLocalPreferences({}).graph;
      this.graphPresentation = this.graphRenderer ? this.restoredGraphPreferences.presentation : 'table';
      this.activeSavedViewID = restoreSavedView ? this.savedPreferences.savedViewID : '';
      this.savedViews = [];
      this.preferenceTimer = 0;
      this.suppressPreferenceWrites = false;
      this.graphLoadGeneration = 0;
      if (!explicitBrowserState && this.savedPreferences && globalThis.history && globalThis.location) {
        const restoredSearch = searchForBrowserState(search, this.urlState);
        globalThis.history.replaceState(null, '', globalThis.location.pathname + restoredSearch);
      }
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
      this.preferencesResetButton = shell.querySelector('[data-knowledge-preferences-reset]');
      this.graphTableToggle = shell.querySelector('[data-knowledge-graph-table-toggle]');
      this.graphTableToggleLabel = shell.querySelector('[data-knowledge-graph-table-toggle-label]');
      this.savedViewSelect = shell.querySelector('[data-knowledge-saved-view]');
      this.savedViewCreateButton = shell.querySelector('[data-knowledge-saved-view-create]');
      this.savedViewUpdateButton = shell.querySelector('[data-knowledge-saved-view-update]');
      this.savedViewDeleteButton = shell.querySelector('[data-knowledge-saved-view-delete]');
      this.savedViewStatus = shell.querySelector('[data-knowledge-saved-view-status]');
      this.savedViewDialog = shell.querySelector('[data-knowledge-saved-view-dialog]');
      this.savedViewForm = shell.querySelector('[data-knowledge-saved-view-form]');
      this.savedViewDialogTitle = shell.querySelector('[data-knowledge-saved-view-dialog-title]');
      this.savedViewSubmitButton = shell.querySelector('[data-knowledge-saved-view-submit]');
      this.savedViewError = shell.querySelector('[data-knowledge-saved-view-error]');
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
      this.chunkConflictUI = this.conflictUI('chunk');
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
      this.entryConflictUI = this.conflictUI('entry');
      this.supersedeDialog = shell.querySelector('[data-knowledge-supersede-dialog]');
      this.supersedeForm = shell.querySelector('[data-knowledge-supersede-form]');
      this.supersedeError = shell.querySelector('[data-knowledge-supersede-error]');
      this.supersedeSubmitButton = shell.querySelector('[data-knowledge-supersede-submit]');
      this.linkCreateButton = shell.querySelector('[data-knowledge-link-create]');
      this.linkDialog = shell.querySelector('[data-knowledge-link-dialog]');
      this.linkForm = shell.querySelector('[data-knowledge-link-form]');
      this.linkFormError = shell.querySelector('[data-knowledge-link-form-error]');
      this.linkSubmitButton = shell.querySelector('[data-knowledge-link-submit]');
      this.linkSourceLabel = shell.querySelector('[data-knowledge-link-source-label]');
      this.linkSourceRef = shell.querySelector('[data-knowledge-link-source-ref]');
      this.linkTargetLabel = shell.querySelector('[data-knowledge-link-target-label]');
      this.linkTargetRef = shell.querySelector('[data-knowledge-link-target-ref]');
      this.linkPreview = shell.querySelector('[data-knowledge-link-preview]');
      this.linkPreviewPath = shell.querySelector('[data-knowledge-link-preview-path]');
      this.linkPreviewDetail = shell.querySelector('[data-knowledge-link-preview-detail]');
      this.linkActions = shell.querySelector('[data-knowledge-link-actions]');
      this.linkUnlinkButton = shell.querySelector('[data-knowledge-link-unlink]');
      this.linkRestoreButton = shell.querySelector('[data-knowledge-link-restore]');
      this.linkMutationStatus = shell.querySelector('[data-knowledge-link-mutation-status]');
      this.deleteDialog = shell.querySelector('[data-knowledge-delete-dialog]');
      this.deleteForm = shell.querySelector('[data-knowledge-delete-form]');
      this.deleteDialogTitle = shell.querySelector('[data-knowledge-delete-dialog-title]');
      this.deleteObjectTitle = shell.querySelector('[data-knowledge-delete-object-title]');
      this.deleteObjectRef = shell.querySelector('[data-knowledge-delete-object-ref]');
      this.deleteExplanation = shell.querySelector('[data-knowledge-delete-explanation]');
      this.deleteBlockers = shell.querySelector('[data-knowledge-delete-blockers]');
      this.deleteBlockerList = shell.querySelector('[data-knowledge-delete-blocker-list]');
      this.deleteError = shell.querySelector('[data-knowledge-delete-error]');
      this.deleteSubmitButton = shell.querySelector('[data-knowledge-delete-submit]');
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
        history: shell.querySelector('[data-knowledge-inspector-history]'),
        historyList: shell.querySelector('[data-knowledge-history-list]'),
        historyCount: shell.querySelector('[data-knowledge-history-count]'),
        historyStatus: shell.querySelector('[data-knowledge-history-status]'),
        historyMore: shell.querySelector('[data-knowledge-history-more]'),
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
      if (this.preferencesResetButton) this.preferencesResetButton.addEventListener('click', () => this.resetLocalPreferences());
      if (this.graphTableToggle) this.graphTableToggle.addEventListener('click', () => this.setGraphPresentation(this.graphPresentation === 'table' ? 'canvas' : 'table'));
      if (this.savedViewSelect) this.savedViewSelect.addEventListener('change', () => this.loadNamedGraphView(this.savedViewSelect.value));
      if (this.savedViewCreateButton) this.savedViewCreateButton.addEventListener('click', () => this.openSavedViewEditor());
      if (this.savedViewUpdateButton) this.savedViewUpdateButton.addEventListener('click', () => this.openSavedViewEditor(this.activeSavedViewID));
      if (this.savedViewDeleteButton) this.savedViewDeleteButton.addEventListener('click', () => this.deleteNamedGraphView());
      for (const button of shell.querySelectorAll('[data-knowledge-saved-view-cancel]')) button.addEventListener('click', () => this.closeSavedViewEditor());
      if (this.savedViewForm) this.savedViewForm.addEventListener('submit', event => { event.preventDefault(); this.saveNamedGraphView(); });
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
      if (this.chunkConflictUI.reload) this.chunkConflictUI.reload.addEventListener('click', () => this.resolveEditorConflict('chunk', 'reload'));
      if (this.chunkConflictUI.rebase) this.chunkConflictUI.rebase.addEventListener('click', () => this.resolveEditorConflict('chunk', 'rebase'));
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
      if (this.entryConflictUI.reload) this.entryConflictUI.reload.addEventListener('click', () => this.resolveEditorConflict('entry', 'reload'));
      if (this.entryConflictUI.rebase) this.entryConflictUI.rebase.addEventListener('click', () => this.resolveEditorConflict('entry', 'rebase'));
      for (const button of shell.querySelectorAll('[data-knowledge-supersede-cancel]')) button.addEventListener('click', () => this.closeSupersedeEditor());
      if (this.supersedeForm) this.supersedeForm.addEventListener('submit', event => { event.preventDefault(); this.saveSupersedeEditor(); });
      if (this.linkCreateButton) this.linkCreateButton.addEventListener('click', () => this.openLinkEditor());
      for (const button of shell.querySelectorAll('[data-knowledge-link-cancel]')) button.addEventListener('click', () => this.closeLinkEditor());
      const linkSwap = shell.querySelector('[data-knowledge-link-swap]');
      if (linkSwap) linkSwap.addEventListener('click', () => this.swapLinkEndpoints());
      if (this.linkForm) {
        this.linkForm.addEventListener('submit', event => { event.preventDefault(); this.saveLinkEditor(); });
        this.linkForm.elements.namedItem('kind').addEventListener('change', () => this.syncLinkPreview());
      }
      if (this.linkUnlinkButton) this.linkUnlinkButton.addEventListener('click', () => this.changeLinkLifecycle('unlink'));
      if (this.linkRestoreButton) this.linkRestoreButton.addEventListener('click', () => this.changeLinkLifecycle('restore'));
      for (const button of shell.querySelectorAll('[data-knowledge-delete-cancel]')) button.addEventListener('click', () => this.closeDeleteDialog());
      if (this.deleteForm) {
        this.deleteForm.addEventListener('submit', event => { event.preventDefault(); this.confirmKnowledgeDelete(); });
        this.deleteForm.elements.namedItem('acknowledge').addEventListener('change', () => this.syncDeleteConfirmation());
      }
      if (this.inspector.historyMore) this.inspector.historyMore.addEventListener('click', () => this.loadOlderHistory());
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
        this.schedulePreferenceSave();
      };
      this.shell.addEventListener('koder:knowledge-pane', this.onGraphPane);
      if (this.graphAdapter) {
        this.graphUnsubscribe = this.graphAdapter.subscribe(event => {
          if (event.type === 'selection') this.syncGraphSelection(event.detail);
          if (event.type === 'change' && this.graphTable) this.graphTable.refresh();
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
          if (this.graphTable) this.graphTable.refresh();
          this.schedulePreferenceSave();
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
            this.schedulePreferenceSave();
          }
        });
      }
      if (this.graphTable) {
        this.graphTableUnsubscribe = this.graphTable.subscribe(event => this.runGraphTableAction(event));
      }
      this.onPageHide = () => this.flushPreferenceSave();
      globalThis.addEventListener('pagehide', this.onPageHide);
      this.syncControls();
      this.updateGraphViewControls();
      this.setGraphPresentation(this.graphPresentation, {save: false});
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
      this.schedulePreferenceSave();
    }

    graph() {
      return this.graphAdapter && this.graphAdapter.target && this.graphAdapter.target.graph || null;
    }

    localPreferenceSnapshot() {
      const graph = this.graph();
      let hiddenNodes = this.restoredGraphPreferences.hiddenNodes || [];
      let hiddenEdges = this.restoredGraphPreferences.hiddenEdges || [];
      let pinnedNodes = this.restoredGraphPreferences.pinnedNodes || [];
      if (graph && graph.order) {
        if (this.graphView) ({hiddenNodes, hiddenEdges} = this.graphView.snapshot());
        pinnedNodes = graph.filterNodes((key, attributes) => !!attributes.pinned).map(key => ({
          key, x: Number(graph.getNodeAttribute(key, 'x')), y: Number(graph.getNodeAttribute(key, 'y')),
        }));
      }
      return normalizeLocalPreferences({
        savedViewID: this.activeSavedViewID,
        browser: this.urlState,
        mobilePane: this.shell.dataset.mobilePane,
        graph: {root: this.graphRoot, presentation: this.graphPresentation, hiddenNodes, hiddenEdges, pinnedNodes, frontier: this.graphFrontier},
      });
    }

    schedulePreferenceSave() {
      if (this.suppressPreferenceWrites || !this.preferenceStorage) return;
      clearTimeout(this.preferenceTimer);
      this.preferenceTimer = setTimeout(() => this.flushPreferenceSave(), 120);
    }

    flushPreferenceSave() {
      clearTimeout(this.preferenceTimer);
      this.preferenceTimer = 0;
      if (this.suppressPreferenceWrites || !this.preferenceStorage) return null;
      const saved = saveLocalPreferences(this.preferenceStorage, this.localPreferenceSnapshot());
      this.savedPreferences = saved;
      this.restoredGraphPreferences = saved.graph;
      return saved;
    }

    setGraphRoot(objectKind, id) {
      this.graphRoot = graphPreferenceObject({kind: objectKind, id});
      this.graphFrontier = [];
      this.restoredGraphPreferences = normalizeLocalPreferences({graph: {root: this.graphRoot}}).graph;
    }

    resetLocalPreferences() {
      this.suppressPreferenceWrites = true;
      clearTimeout(this.preferenceTimer);
      this.preferenceTimer = 0;
      clearLocalPreferences(this.preferenceStorage);
      this.savedPreferences = null;
      this.activeSavedViewID = '';
      this.graphRoot = null;
      this.graphFrontier = [];
      this.restoredGraphPreferences = normalizeLocalPreferences({}).graph;
      const graph = this.graph();
      if (this.graphView) this.graphView.reset();
      if (graph) graph.updateEachNodeAttributes((key, attributes) => ({...attributes, pinned: false}), {attributes: ['pinned']});
      this.setGraphPresentation(this.graphRenderer ? 'canvas' : 'table', {save: false});
      this.urlState = browserStateFromSearch('');
      const search = searchForBrowserState(globalThis.location && globalThis.location.search, this.urlState);
      globalThis.history.replaceState(null, '', globalThis.location.pathname + search);
      this.setMobilePane('graph');
      this.syncControls();
      this.suppressPreferenceWrites = false;
      this.renderSavedGraphViews();
      const status = this.shell.querySelector('[data-knowledge-status-label]');
      if (status) status.textContent = 'Local explorer preferences reset';
      this.refresh();
      return true;
    }

    setMobilePane(value) {
      const pane = normalizePane(value);
      this.shell.dataset.mobilePane = pane;
      for (const tab of this.shell.querySelectorAll('[data-knowledge-tab]')) {
        const selected = tab.dataset.knowledgeTab === pane;
        tab.setAttribute('aria-selected', selected ? 'true' : 'false');
        tab.tabIndex = selected ? 0 : -1;
      }
      this.shell.dispatchEvent(new CustomEvent('koder:knowledge-pane', {detail: {pane}}));
    }

    setGraphPresentation(value, options) {
      value = String(value || '').trim().toLowerCase();
      if (value !== 'table' && value !== 'canvas') value = 'canvas';
      if (value === 'canvas' && !this.graphRenderer) value = 'table';
      this.graphPresentation = value;
      const stage = this.shell.querySelector('#knowledge-graph');
      if (stage) stage.dataset.presentation = value;
      const table = this.shell.querySelector('[data-knowledge-graph-table]');
      if (table) table.hidden = value !== 'table';
      if (this.graphCanvas) this.graphCanvas.setAttribute('aria-hidden', value === 'table' ? 'true' : 'false');
      if (this.graphTableToggle) {
        const showingTable = value === 'table';
        const label = showingTable ? 'Show visual graph canvas' : 'Show accessible graph table';
        this.graphTableToggle.setAttribute('aria-pressed', showingTable ? 'true' : 'false');
        this.graphTableToggle.title = label;
        if (this.graphTableToggleLabel) this.graphTableToggleLabel.textContent = label;
      }
      if (this.graphCenterButton) this.graphCenterButton.disabled = value === 'table' || !this.graphViewport;
      if (this.graphFitButton) this.graphFitButton.disabled = value === 'table' || !this.graphViewport;
      if (value === 'table' && this.graphTable) this.graphTable.refresh();
      if (value === 'canvas' && this.graphViewport) setTimeout(() => this.graphViewport && this.graphViewport.fit({animate: false}), 0);
      if (!options || options.save !== false) this.schedulePreferenceSave();
      return value;
    }

    runGraphTableAction(event) {
      const item = event && event.item;
      const action = String(event && event.action || '');
      if (!item || !['node', 'edge'].includes(item.kind)) return false;
      if (action === 'select') return this.selectGraphObject(item.kind, item.key, {additive: true});
      if (action === 'inspect') return this.selectGraphObject(item.kind, item.key);
      if (action === 'incoming' || action === 'outgoing') {
        this.selectGraphObject(item.kind, item.key);
        return this.expandGraph(action, item);
      }
      if (action === 'hide') {
        this.selectGraphObject(item.kind, item.key);
        return this.applyGraphViewAction('hide');
      }
      return false;
    }

    savedViewRecord(id) {
      id = String(id || '');
      return this.savedViews.find(view => String(view && view.id || '') === id) || null;
    }

    renderSavedGraphViews() {
      if (!this.savedViewSelect) return;
      const document = this.shell.ownerDocument;
      this.savedViewSelect.replaceChildren();
      const placeholder = document.createElement('option');
      placeholder.value = '';
      placeholder.textContent = this.savedViews.length ? 'Named views' : 'No named views';
      this.savedViewSelect.appendChild(placeholder);
      for (const view of this.savedViews) {
        const option = document.createElement('option');
        option.value = String(view.id || '');
        option.textContent = String(view.name || view.id || 'Untitled view');
        this.savedViewSelect.appendChild(option);
      }
      const selected = !!this.savedViewRecord(this.activeSavedViewID);
      if (!selected) this.activeSavedViewID = '';
      this.savedViewSelect.value = this.activeSavedViewID;
      if (this.savedViewUpdateButton) this.savedViewUpdateButton.disabled = !selected;
      if (this.savedViewDeleteButton) this.savedViewDeleteButton.disabled = !selected;
    }

    async loadSavedGraphViews() {
      if (!this.client || typeof this.client.listGraphViews !== 'function') return false;
      if (this.savedViewSelect) this.savedViewSelect.disabled = true;
      try {
        const response = await this.client.listGraphViews({channel: 'saved-views'});
        this.savedViews = Array.isArray(response && response.views) ? response.views : [];
        this.renderSavedGraphViews();
        if (this.savedViewStatus) this.savedViewStatus.textContent = `${this.savedViews.length} named ${this.savedViews.length === 1 ? 'view' : 'views'} available.`;
        return true;
      } catch (error) {
        if (error && ['canceled', 'stale_response'].includes(error.code)) return false;
        this.savedViews = [];
        this.renderSavedGraphViews();
        const message = String(error && error.message || 'Named views could not load.');
        if (this.savedViewStatus) this.savedViewStatus.textContent = message;
        const status = this.shell.querySelector('[data-knowledge-status-label]');
        if (status) status.textContent = 'Named views unavailable';
        return false;
      } finally {
        if (this.savedViewSelect) this.savedViewSelect.disabled = false;
      }
    }

    loadNamedGraphView(id) {
      const view = this.savedViewRecord(id);
      if (!view) {
        this.activeSavedViewID = '';
        this.renderSavedGraphViews();
        this.schedulePreferenceSave();
        return false;
      }
      const preferences = preferencesFromGraphViewState(view.state, view.id);
      this.activeSavedViewID = String(view.id);
      this.urlState = preferences.browser;
      this.graphRoot = preferences.graph.root;
      this.graphPresentation = this.graphRenderer ? preferences.graph.presentation : 'table';
      this.graphFrontier = [...preferences.graph.frontier];
      this.restoredGraphPreferences = preferences.graph;
      const search = searchForBrowserState(globalThis.location && globalThis.location.search, this.urlState);
      globalThis.history.pushState(null, '', globalThis.location.pathname + search);
      this.setMobilePane(preferences.mobilePane);
      this.setGraphPresentation(this.graphPresentation, {save: false});
      this.syncControls();
      saveLocalPreferences(this.preferenceStorage, preferences);
      this.renderSavedGraphViews();
      if (this.savedViewStatus) this.savedViewStatus.textContent = `Loaded named view ${view.name}.`;
      this.refresh();
      return true;
    }

    openSavedViewEditor(id) {
      if (!this.savedViewDialog || !this.savedViewForm) return false;
      const view = this.savedViewRecord(id);
      this.savedViewEditorID = view ? String(view.id) : '';
      this.savedViewForm.elements.namedItem('name').value = view ? String(view.name || '') : '';
      if (this.savedViewDialogTitle) this.savedViewDialogTitle.textContent = view ? 'Update named view' : 'Save named view';
      if (this.savedViewSubmitButton) this.savedViewSubmitButton.textContent = view ? 'Update view' : 'Save view';
      if (this.savedViewError) { this.savedViewError.hidden = true; this.savedViewError.textContent = ''; }
      if (typeof this.savedViewDialog.showModal === 'function') this.savedViewDialog.showModal();
      else this.savedViewDialog.setAttribute('open', '');
      this.savedViewForm.elements.namedItem('name').focus();
      return true;
    }

    closeSavedViewEditor() {
      if (!this.savedViewDialog) return;
      if (typeof this.savedViewDialog.close === 'function') this.savedViewDialog.close();
      else this.savedViewDialog.removeAttribute('open');
      this.savedViewEditorID = '';
    }

    async saveNamedGraphView() {
      if (!this.savedViewForm || !this.client) return false;
      const name = String(this.savedViewForm.elements.namedItem('name').value || '').trim();
      if (!name) return false;
      const current = this.savedViewRecord(this.savedViewEditorID);
      const body = {name, state: graphViewStateFromPreferences(this.localPreferenceSnapshot())};
      if (current) body.expected_revision = Number(current.revision);
      if (this.savedViewSubmitButton) this.savedViewSubmitButton.disabled = true;
      if (this.savedViewError) { this.savedViewError.hidden = true; this.savedViewError.textContent = ''; }
      try {
        const response = current
          ? await this.client.updateGraphView(current.id, body, {channel: 'saved-view-mutation'})
          : await this.client.createGraphView(body, {channel: 'saved-view-mutation'});
        const view = response && response.view;
        if (!view) throw new Error('Koder returned no named view.');
        this.savedViews = [...this.savedViews.filter(item => item.id !== view.id), view]
          .sort((left, right) => String(left.name || '').localeCompare(String(right.name || '')));
        this.activeSavedViewID = String(view.id);
        this.closeSavedViewEditor();
        this.renderSavedGraphViews();
        this.flushPreferenceSave();
        if (this.savedViewStatus) this.savedViewStatus.textContent = `${current ? 'Updated' : 'Saved'} named view ${view.name}.`;
        return true;
      } catch (error) {
        if (error && error.code === 'conflict') await this.loadSavedGraphViews();
        const requestID = String(error && error.requestID || '');
        const message = error && error.code === 'conflict' ? 'This named view changed or its name is already used. Review the latest views and try again.' : String(error && error.message || 'Named view could not be saved.');
        if (this.savedViewError) { this.savedViewError.hidden = false; this.savedViewError.textContent = requestID ? `${message} Audit ID: ${requestID}` : message; }
        return false;
      } finally {
        if (this.savedViewSubmitButton) this.savedViewSubmitButton.disabled = false;
      }
    }

    async deleteNamedGraphView() {
      const view = this.savedViewRecord(this.activeSavedViewID);
      if (!view || !this.client) return false;
      if (typeof globalThis.confirm === 'function' && !globalThis.confirm(`Delete named view “${view.name}”? Knowledge content is not affected.`)) return false;
      if (this.savedViewDeleteButton) this.savedViewDeleteButton.disabled = true;
      try {
        await this.client.deleteGraphView(view.id, {expected_revision: Number(view.revision)}, {channel: 'saved-view-mutation'});
        this.savedViews = this.savedViews.filter(item => item.id !== view.id);
        this.activeSavedViewID = '';
        this.renderSavedGraphViews();
        this.flushPreferenceSave();
        if (this.savedViewStatus) this.savedViewStatus.textContent = `Deleted named view ${view.name}. The current graph remains as an unnamed local view.`;
        return true;
      } catch (error) {
        if (error && error.code === 'conflict') await this.loadSavedGraphViews();
        const status = this.shell.querySelector('[data-knowledge-status-label]');
        if (status) status.textContent = String(error && error.message || 'Named view could not be deleted.');
        return false;
      } finally {
        if (this.savedViewDeleteButton) this.savedViewDeleteButton.disabled = !this.savedViewRecord(this.activeSavedViewID);
      }
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
      this.setGraphRoot(objectKind, id);
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
      const relationReady = items.length === 2 && items.every(item => item.kind === 'node' && graphObjectForSelection(item));
      if (this.graphRenderer) this.graphRenderer.setSelections(items);
      if (this.graphTable) this.graphTable.setSelection(items);
      if (this.graphSelectionCount) {
        this.graphSelectionCount.hidden = items.length < 2;
        this.graphSelectionCount.textContent = items.length < 2 ? '' : `${items.length} selected`;
      }
      if (this.linkCreateButton) {
        this.linkCreateButton.disabled = !relationReady;
        this.linkCreateButton.title = relationReady ? 'Preview a relationship between the selected nodes' : 'Select exactly two graph nodes to create a relationship';
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
      const relationReady = selection.length === 2 && selection.every(item => item.kind === 'node' && graphObjectForSelection(item));
      for (const button of this.graphContextButtons) {
        const action = button.dataset.knowledgeContextAction;
        button.hidden = action === 'relate' ? !relationReady : ['incoming', 'outgoing'].includes(action) ? !object : ['center', 'pin'].includes(action) ? !isNode : false;
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
      if (action === 'relate') {
        this.closeGraphContextMenu();
        return this.openLinkEditor();
      }
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
        if (this.linkActions) this.linkActions.hidden = true;
      }
    }

    cancelInspectorSupport() {
      if (!this.client || typeof this.client.cancel !== 'function') return;
      this.client.cancel('inspector-evidence');
      this.client.cancel('inspector-relations');
      this.client.cancel('inspector-history');
      this.client.cancel('inspector-history-more');
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
      const stage = this.shell.querySelector('#knowledge-graph');
      if (stage) stage.dataset.graphState = state;
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
      if (!this.graphAdapter) return;
      const generation = ++this.graphLoadGeneration;
      this.client.cancel('graph-restore');
      const root = this.graphRoot || graphPreferenceObject({kind: this.urlState.objectKind, id: this.urlState.id});
      const request = root && graphSnapshotRequest(root.kind, root.id);
      if (!request) {
        this.client.cancel('graph');
        if (this.graphLayout) this.graphLayout.stop('selection_cleared');
        if (this.graphRenderer) this.graphRenderer.setSelection(null, null);
        if (this.graphTable) this.graphTable.refresh();
        this.setGraphState('empty');
        return;
      }
      if (this.graphLayout) this.graphLayout.stop('selection_changed');
      this.setGraphState('loading');
      try {
        const response = await this.client.graphSnapshot(request, {channel: 'graph', timeoutMS: 10000});
        if (generation !== this.graphLoadGeneration) return;
        this.graphRoot = {...request.root};
        const preferences = this.graphPreferencesForRoot(request.root);
        this.suppressPreferenceWrites = true;
        this.graphAdapter.replaceSnapshot(response, preferences);
        if (this.graphView) this.graphView.reset({preserveVisibility: true});
        let restoredExpansions = 0;
        for (const expansion of preferences.frontier) {
          if (generation !== this.graphLoadGeneration) return;
          const expansionRequest = graphExpansionRequest(expansion.kind, expansion.id, expansion.direction);
          if (!expansionRequest) continue;
          try {
            const expanded = await this.client.graphSnapshot(expansionRequest, {channel: 'graph-restore', timeoutMS: 10000});
            if (generation !== this.graphLoadGeneration) return;
            const merged = this.graphAdapter.mergeSnapshot(expanded, preferences);
            if (!merged.result || merged.result.action === 'applied') restoredExpansions++;
          } catch (error) {
            if (error && ['canceled', 'stale_response'].includes(error.code)) return;
            break;
          }
        }
        this.applyLocalGraphPreferences();
        const rootKey = `${request.root.kind}:${request.root.id}`;
        const selectedKey = ['chunk', 'entry', 'evidence'].includes(this.urlState.objectKind) ? `${this.urlState.objectKind}:${this.urlState.id}` : '';
        if (!(selectedKey && this.graphAdapter.select('node', selectedKey))) this.graphAdapter.select('node', rootKey);
        const counts = this.graphAdapter.counts();
        const truncated = !!(response && response.page && response.page.truncated);
        const restored = restoredExpansions ? ` Restored ${restoredExpansions} local ${restoredExpansions === 1 ? 'expansion' : 'expansions'}.` : '';
        const detail = `${counts.nodes} ${counts.nodes === 1 ? 'node' : 'nodes'} and ${counts.edges} ${counts.edges === 1 ? 'relationship' : 'relationships'}.${restored}`;
        this.setGraphState(truncated ? 'truncated' : 'ready', detail);
        if (this.graphLayout) this.graphLayout.request();
        else if (this.graphViewport) this.graphViewport.fit({animate: false});
      } catch (error) {
        if (error && (error.code === 'canceled' || error.code === 'stale_response')) return;
        const requestID = String(error && error.requestID || '');
        const detail = String(error && error.message || 'This neighborhood could not be loaded.');
        this.setGraphState(error && error.code === 'invalid_cursor' ? 'stale' : 'error', requestID ? `${detail} Audit ID: ${requestID}` : detail);
      } finally {
        if (generation === this.graphLoadGeneration) {
          this.suppressPreferenceWrites = false;
          this.schedulePreferenceSave();
        }
      }
    }

    graphPreferencesForRoot(root) {
      const savedRoot = this.restoredGraphPreferences && this.restoredGraphPreferences.root;
      if (!savedRoot || !root || savedRoot.kind !== root.kind || savedRoot.id !== root.id) {
        return normalizeLocalPreferences({graph: {root}}).graph;
      }
      return this.restoredGraphPreferences;
    }

    applyLocalGraphPreferences() {
      if (this.graphRenderer) this.graphRenderer.scheduleRefresh(false);
      this.updateGraphViewControls();
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
      this.historyState = {objectKind, id: String(record.id || ''), revisions: [], evidence: [], nextCursor: '', loading: true, error: ''};
      this.renderHistoryTimeline();
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
      const linkSelected = objectKind === 'link';
      const archivedLink = linkSelected && record.state === 'archived';
      if (this.linkActions) this.linkActions.hidden = !linkSelected;
      if (this.linkUnlinkButton) this.linkUnlinkButton.hidden = !linkSelected || archivedLink;
      if (this.linkRestoreButton) this.linkRestoreButton.hidden = !archivedLink;
      if (this.linkMutationStatus) this.linkMutationStatus.textContent = '';
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
      const historyTask = ['chunk', 'entry', 'link'].includes(objectKind) && typeof this.client.history === 'function'
        ? this.client.history(objectKind, id, {limit: 20}, {channel: 'inspector-history'}).then(response => ({response})).catch(error => ({error}))
        : Promise.resolve({response: {revisions: [], page: {}}});
      const [evidenceResult, relationsResult, historyResult] = await Promise.all([evidenceTask, relationsTask, historyTask]);
      if (!this.inspectedObject || this.inspectedObject.kind !== objectKind || this.inspectedObject.id !== id) return;
      const evidence = Array.isArray(evidenceResult.response && evidenceResult.response.evidence) ? evidenceResult.response.evidence : [];
      const neighbors = Array.isArray(relationsResult.response && relationsResult.response.neighbors) ? relationsResult.response.neighbors : [];
      const errors = [evidenceResult.error, relationsResult.error].filter(error => error && !['canceled', 'stale_response'].includes(error.code));
      const truncated = !!(evidenceResult.response && evidenceResult.response.page && evidenceResult.response.page.next_cursor) ||
        !!(relationsResult.response && relationsResult.response.page && relationsResult.response.page.next_cursor);
      this.renderInspectorSupport(objectKind, record, evidence, neighbors, errors, truncated);
      const historyError = historyResult.error && !['canceled', 'stale_response'].includes(historyResult.error.code) ? historyResult.error : null;
      const historyResponse = historyResult.response || {};
      this.historyState = {
        objectKind, id, revisions: Array.isArray(historyResponse.revisions) ? historyResponse.revisions : [], evidence,
        nextCursor: String(historyResponse.page && historyResponse.page.next_cursor || ''), loading: false,
        error: historyError ? String(historyError.message || 'Revision history could not be loaded.') : '',
        requestID: historyError ? String(historyError.requestID || '') : '',
      };
      this.renderHistoryTimeline();
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

    renderHistoryTimeline() {
      const state = this.historyState || {};
      const list = this.inspector.historyList;
      if (!list) return;
      list.replaceChildren();
      const revisions = Array.isArray(state.revisions) ? state.revisions : [];
      if (this.inspector.historyCount) this.inspector.historyCount.textContent = revisions.length ? `${revisions.length}${state.nextCursor ? '+' : ''}` : '';
      if (this.inspector.historyStatus) {
        const request = state.requestID ? ` Audit ID: ${state.requestID}` : '';
        this.inspector.historyStatus.textContent = state.loading ? 'Loading revision history…'
          : state.error ? state.error + request
            : !revisions.length ? 'No revision history is available.'
              : state.nextCursor ? `Showing the newest ${revisions.length} revisions.` : `Showing all ${revisions.length} revisions.`;
      }
      if (this.inspector.historyMore) {
        this.inspector.historyMore.hidden = !state.nextCursor || !!state.loading;
        this.inspector.historyMore.disabled = !!state.loading;
      }
      for (const item of historyTimeline(revisions, state.objectKind, state.evidence, {hasOlder: !!state.nextCursor})) {
        const row = this.shell.ownerDocument.createElement('li');
        row.className = 'knowledge-history-item';
        const marker = this.shell.ownerDocument.createElement('span');
        marker.className = 'knowledge-history-marker';
        marker.setAttribute('aria-hidden', 'true');
        const card = this.shell.ownerDocument.createElement('article');
        card.className = 'knowledge-history-card';
        const header = this.shell.ownerDocument.createElement('header');
        const action = this.shell.ownerDocument.createElement('strong');
        action.textContent = item.action;
        const timestamp = this.shell.ownerDocument.createElement('time');
        timestamp.dateTime = item.timestamp;
        timestamp.textContent = formatTimestamp(item.timestamp);
        header.append(action, timestamp);
        const meta = this.shell.ownerDocument.createElement('div');
        meta.className = 'knowledge-history-meta';
        meta.textContent = [`Revision ${item.revision}`, item.actor, displayLabel(item.state)].filter(Boolean).join(' · ');
        card.append(header, meta);
        const changes = this.shell.ownerDocument.createElement('div');
        changes.className = 'knowledge-history-changes';
        const fields = item.isCreation ? ['Initial version'] : item.changedFields.map(displayLabel);
        for (const field of fields) {
          const badge = this.shell.ownerDocument.createElement('span');
          badge.textContent = field;
          changes.appendChild(badge);
        }
        if (fields.length) card.appendChild(changes);
        if (item.snippet) {
          const snippet = this.shell.ownerDocument.createElement('p');
          snippet.className = 'knowledge-history-snippet';
          snippet.textContent = item.snippet;
          card.appendChild(snippet);
        }
        if (item.evidenceIDs.length) {
          const evidence = this.shell.ownerDocument.createElement('div');
          evidence.className = 'knowledge-history-evidence';
          const heading = this.shell.ownerDocument.createElement('span');
          heading.textContent = `${item.evidenceIDs.length} evidence ${item.evidenceIDs.length === 1 ? 'record' : 'records'} on this revision`;
          evidence.appendChild(heading);
          const details = new Map(item.evidence.map(value => [String(value.id), value]));
          for (const id of item.evidenceIDs) {
            const value = details.get(id);
            const line = this.shell.ownerDocument.createElement(value ? 'span' : 'code');
            line.textContent = value
              ? [value.source && (value.source.title || value.source.id), displayLabel(value.quality)].filter(Boolean).join(' · ')
              : id;
            evidence.appendChild(line);
          }
          card.appendChild(evidence);
        }
        row.append(marker, card);
        list.appendChild(row);
      }
    }

    async loadOlderHistory() {
      const state = this.historyState;
      if (!state || !state.nextCursor || state.loading || !this.client) return false;
      state.loading = true;
      state.error = '';
      this.renderHistoryTimeline();
      try {
        const response = await this.client.history(state.objectKind, state.id, {limit: 20, cursor: state.nextCursor}, {channel: 'inspector-history-more'});
        if (this.historyState !== state || !this.inspectedObject || this.inspectedObject.kind !== state.objectKind || this.inspectedObject.id !== state.id) return false;
        const seen = new Set(state.revisions.map(record => {
          const value = record && record[state.objectKind];
          return String(value && value.revision && value.revision.id || '');
        }));
        for (const record of Array.isArray(response && response.revisions) ? response.revisions : []) {
          const value = record && record[state.objectKind];
          const revisionID = String(value && value.revision && value.revision.id || '');
          if (!revisionID || seen.has(revisionID)) continue;
          seen.add(revisionID);
          state.revisions.push(record);
        }
        state.nextCursor = String(response && response.page && response.page.next_cursor || '');
        return true;
      } catch (error) {
        if (error && ['canceled', 'stale_response'].includes(error.code)) return false;
        state.error = String(error && error.message || 'Older revisions could not be loaded.');
        state.requestID = String(error && error.requestID || '');
        return false;
      } finally {
        if (this.historyState === state) {
          state.loading = false;
          this.renderHistoryTimeline();
        }
      }
    }

    conflictUI(kind) {
      const prefix = `[data-knowledge-${kind}-conflict`;
      return {
        container: this.shell.querySelector(`${prefix}]`), revision: this.shell.querySelector(`${prefix}-revision]`),
        server: this.shell.querySelector(`${prefix}-server]`), draft: this.shell.querySelector(`${prefix}-draft]`),
        overlap: this.shell.querySelector(`${prefix}-overlap]`), detail: this.shell.querySelector(`${prefix}-detail]`),
        reload: this.shell.querySelector(`${prefix}-reload]`), rebase: this.shell.querySelector(`${prefix}-rebase]`),
      };
    }

    clearEditorConflict(kind) {
      const ui = kind === 'chunk' ? this.chunkConflictUI : this.entryConflictUI;
      if (ui && ui.container) ui.container.hidden = true;
      this[`${kind}EditorConflict`] = null;
    }

    editorValuesFor(kind, record) {
      return kind === 'chunk' ? chunkEditorValues(record) : entryEditorValues(record);
    }

    editorFormFor(kind) {
      return kind === 'chunk' ? this.chunkForm : this.entryForm;
    }

    async handleEditorConflict(kind, original, draftValues, error) {
      if (!original || !original.id || error && error.code !== 'conflict') return false;
      const ui = kind === 'chunk' ? this.chunkConflictUI : this.entryConflictUI;
      const formError = kind === 'chunk' ? this.chunkFormError : this.entryFormError;
      try {
        const response = kind === 'chunk'
          ? await this.client.getChunk(original.id, {channel: 'chunk-conflict'})
          : await this.client.getEntry(original.id, {channel: 'entry-conflict'});
        const latest = response && response[kind];
        const currentEditor = this[`${kind}EditorRecord`];
        if (!latest || !currentEditor || currentEditor.id !== original.id) return false;
        if (latest.revision && original.revision && latest.revision.number === original.revision.number) return false;
        const base = this.editorValuesFor(kind, original);
        const form = this.editorFormFor(kind);
        const liveValues = form ? Object.fromEntries(new FormData(form).entries()) : {};
        if (form && form.elements.namedItem('review_approved')) liveValues.review_approved = !!form.elements.namedItem('review_approved').checked;
        const draft = {...base, ...draftValues, ...liveValues};
        const current = this.editorValuesFor(kind, latest);
        const comparison = editorConflict(base, draft, current, {ignore: kind === 'entry' ? ['review_approved', 'chunk_id'] : ['review_approved']});
        const editable = kind === 'chunk' ? latest.state !== 'archived' : latest.state === 'active';
        this[`${kind}EditorConflict`] = {latest, comparison, editable};
        const fieldList = fields => fields.length ? fields.map(displayLabel).join(', ') : 'None';
        if (ui.revision) ui.revision.textContent = `You opened revision ${original.revision.number}; revision ${latest.revision.number} is current (${displayLabel(latest.state)}).`;
        if (ui.server) ui.server.textContent = fieldList(comparison.serverFields);
        if (ui.draft) ui.draft.textContent = fieldList(comparison.draftFields);
        if (ui.overlap) ui.overlap.textContent = fieldList(comparison.overlappingFields);
        if (ui.detail) ui.detail.textContent = !editable
          ? `The current ${kind} is ${displayLabel(latest.state)} and can no longer be edited. Reload it to inspect the latest version.`
          : comparison.overlappingFields.length
            ? 'Merging keeps your choices for fields changed in both versions. Review those fields carefully before saving again.'
            : 'Merging keeps your draft changes and adopts newer server values for fields you did not edit.';
        if (ui.rebase) ui.rebase.disabled = !editable;
        if (ui.container) { ui.container.hidden = false; ui.container.scrollIntoView({block: 'nearest'}); }
        if (formError) { formError.hidden = true; formError.textContent = ''; }
        return true;
      } catch (loadError) {
        const requestID = String(loadError && loadError.requestID || error && error.requestID || '');
        const message = loadError && loadError.code === 'not_found'
          ? `This ${kind} was deleted while you were editing it.`
          : `Knowledge changed, but the latest ${kind} could not be loaded for comparison.`;
        if (formError) { formError.hidden = false; formError.textContent = requestID ? `${message} Audit ID: ${requestID}` : message; }
        return false;
      }
    }

    resolveEditorConflict(kind, action) {
      const conflict = this[`${kind}EditorConflict`];
      const form = this.editorFormFor(kind);
      if (!conflict || !form || !['reload', 'rebase'].includes(action)) return false;
      if (action === 'reload') {
        if (conflict.comparison.draftFields.length && typeof globalThis.confirm === 'function' && !globalThis.confirm('Discard your draft and load the latest saved version?')) return false;
        if (kind === 'chunk') { this.closeChunkEditor(); return this.openChunkEditor(conflict.latest); }
        this.closeEntryEditor();
        return this.openEntryEditor(conflict.latest);
      }
      if (!conflict.editable) return false;
      for (const [name, value] of Object.entries(conflict.comparison.merged)) {
        if (!name) continue;
        const input = form.elements.namedItem(name);
        if (!input) continue;
        if (input.type === 'checkbox') input.checked = !!value;
        else input.value = value;
      }
      this[`${kind}EditorRecord`] = conflict.latest;
      if (kind === 'chunk') {
        this.syncChunkScopeField();
        if (this.chunkDialogTitle) this.chunkDialogTitle.textContent = `Edit chunk · revision ${conflict.latest.revision.number}`;
      } else {
        this.syncEntryScopeField();
        if (this.entryDialogTitle) this.entryDialogTitle.textContent = `Edit entry · revision ${conflict.latest.revision.number}`;
      }
      this.clearEditorConflict(kind);
      return true;
    }

    openChunkEditor(record) {
      if (!this.chunkDialog || !this.chunkForm) return false;
      this.clearEditorConflict('chunk');
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
      if (this.client && typeof this.client.cancel === 'function') this.client.cancel('chunk-conflict');
      if (typeof this.chunkDialog.close === 'function') this.chunkDialog.close();
      else this.chunkDialog.removeAttribute('open');
      this.chunkEditorRecord = null;
      this.clearEditorConflict('chunk');
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
        const existing = this.chunkEditorRecord;
        if (existing && error && error.code === 'conflict' && await this.handleEditorConflict('chunk', existing, values, error)) return false;
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
      return this.openDeleteDialog('chunk', chunk);
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
      this.clearEditorConflict('entry');
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
      if (this.client && typeof this.client.cancel === 'function') this.client.cancel('entry-conflict');
      if (typeof this.entryDialog.close === 'function') this.entryDialog.close();
      else this.entryDialog.removeAttribute('open');
      this.entryEditorRecord = null;
      this.entryEditorChunk = null;
      this.clearEditorConflict('entry');
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
        const existing = this.entryEditorRecord;
        if (existing && error && error.code === 'conflict' && await this.handleEditorConflict('entry', existing, values, error)) return false;
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
      return this.openDeleteDialog('entry', entry);
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

    openDeleteDialog(objectKind, record) {
      if (!this.deleteDialog || !this.deleteForm || !['chunk', 'entry'].includes(objectKind) || !record || record.state !== 'archived') return false;
      this.deleteTarget = {objectKind, record};
      this.deleteAssessment = deletionAssessment(objectKind, record);
      this.deleteForm.reset();
      if (this.deleteError) { this.deleteError.hidden = true; this.deleteError.textContent = ''; }
      if (this.deleteDialogTitle) this.deleteDialogTitle.textContent = `Delete ${objectKind}`;
      if (this.deleteObjectTitle) this.deleteObjectTitle.textContent = String(record.title || record.id);
      if (this.deleteObjectRef) this.deleteObjectRef.textContent = `${objectKind}:${record.id}`;
      if (this.deleteExplanation) this.deleteExplanation.textContent = this.deleteAssessment.explanation;
      this.renderDeleteBlockers();
      this.syncDeleteConfirmation();
      if (typeof this.deleteDialog.showModal === 'function') this.deleteDialog.showModal();
      else this.deleteDialog.setAttribute('open', '');
      return true;
    }

    closeDeleteDialog() {
      if (this.client && typeof this.client.cancel === 'function') this.client.cancel('delete-mutation');
      if (this.deleteDialog) {
        if (typeof this.deleteDialog.close === 'function') this.deleteDialog.close();
        else this.deleteDialog.removeAttribute('open');
      }
      this.deleteTarget = null;
      this.deleteAssessment = null;
    }

    renderDeleteBlockers() {
      const assessment = this.deleteAssessment || {blocked: false, groups: []};
      if (this.deleteBlockers) this.deleteBlockers.hidden = !assessment.blocked;
      if (!this.deleteBlockerList) return;
      this.deleteBlockerList.replaceChildren();
      for (const group of assessment.groups) {
        const section = this.shell.ownerDocument.createElement('section');
        section.className = 'knowledge-delete-blocker-group';
        const heading = this.shell.ownerDocument.createElement('span');
        heading.textContent = `${group.label}: ${group.count}`;
        section.appendChild(heading);
        if (group.ids.length) {
          const identifiers = this.shell.ownerDocument.createElement('div');
          identifiers.className = 'knowledge-delete-blocker-ids';
          for (const id of group.ids.slice(0, 50)) {
            const inspectable = ['chunk', 'entry', 'link'].includes(group.kind);
            const item = this.shell.ownerDocument.createElement(inspectable ? 'button' : 'code');
            if (inspectable) {
              item.type = 'button';
              item.title = `Inspect ${group.kind}`;
              item.addEventListener('click', () => {
                this.closeDeleteDialog();
                this.selectObject(group.kind, id, {title: id});
              });
            }
            item.textContent = id;
            identifiers.appendChild(item);
          }
          if (group.ids.length > 50) {
            const remainder = this.shell.ownerDocument.createElement('code');
            remainder.textContent = `+${group.ids.length - 50} more`;
            identifiers.appendChild(remainder);
          }
          section.appendChild(identifiers);
        }
        this.deleteBlockerList.appendChild(section);
      }
    }

    syncDeleteConfirmation() {
      if (!this.deleteForm || !this.deleteSubmitButton) return false;
      const acknowledged = !!this.deleteForm.elements.namedItem('acknowledge').checked;
      const blocked = !!(this.deleteAssessment && this.deleteAssessment.blocked);
      this.deleteSubmitButton.disabled = !acknowledged || blocked || !!this.deleteInFlight;
      return !this.deleteSubmitButton.disabled;
    }

    async confirmKnowledgeDelete() {
      const target = this.deleteTarget;
      if (!target || !this.syncDeleteConfirmation() || !this.client) return false;
      const {objectKind, record} = target;
      this.deleteInFlight = true;
      this.syncDeleteConfirmation();
      if (this.deleteError) { this.deleteError.hidden = true; this.deleteError.textContent = ''; }
      try {
        if (objectKind === 'chunk') {
          await this.client.deleteChunk(record.id, {expected_revision: record.revision.number, confirmed: true, cascade: false}, {channel: 'delete-mutation'});
        } else {
          await this.client.deleteEntry(record.id, {expected_revision: record.revision.number, confirmed: true}, {channel: 'delete-mutation'});
        }
        const parentChunkID = objectKind === 'entry' ? String(record.chunk_id || '') : '';
        this.closeDeleteDialog();
        this.writeURLState({...this.urlState, objectKind: parentChunkID ? 'chunk' : '', id: parentChunkID}, false);
        await this.refresh();
        return true;
      } catch (error) {
        const requestID = String(error && error.requestID || '');
        if (error && error.code === 'dependency') {
          this.deleteAssessment = deletionAssessment(objectKind, record, error.details);
          this.renderDeleteBlockers();
          this.deleteForm.elements.namedItem('acknowledge').checked = false;
        }
        const message = error && error.code === 'dependency'
          ? 'Deletion was refused because dependent knowledge must be handled first.'
          : String(error && error.message || `Knowledge could not delete this ${objectKind}.`);
        if (this.deleteError) { this.deleteError.hidden = false; this.deleteError.textContent = requestID ? `${message} Audit ID: ${requestID}` : message; }
        return false;
      } finally {
        this.deleteInFlight = false;
        this.syncDeleteConfirmation();
      }
    }

    linkEndpointFromSelection(selection) {
      const reference = graphObjectForSelection(selection);
      if (!reference) return null;
      const node = this.graphAdapter && this.graphAdapter.get('node', selection.key);
      const attributes = node && node.attributes || {};
      return {kind: reference.kind, id: reference.id, label: String(attributes.title || `${displayLabel(reference.kind)} ${reference.id}`)};
    }

    openLinkEditor() {
      if (!this.linkDialog || !this.linkForm || !this.graphAdapter) return false;
      const selected = this.graphAdapter.selectionSnapshot().items;
      if (selected.length !== 2 || selected.some(item => item.kind !== 'node')) return false;
      const endpoints = selected.map(item => this.linkEndpointFromSelection(item));
      if (endpoints.some(endpoint => !endpoint)) return false;
      this.linkEditorEndpoints = endpoints;
      this.linkForm.reset();
      if (this.linkFormError) { this.linkFormError.hidden = true; this.linkFormError.textContent = ''; }
      this.syncLinkEndpointFields();
      if (typeof this.linkDialog.showModal === 'function') this.linkDialog.showModal();
      else this.linkDialog.setAttribute('open', '');
      this.syncLinkPreview();
      return true;
    }

    closeLinkEditor() {
      if (!this.linkDialog) return;
      if (typeof this.linkDialog.close === 'function') this.linkDialog.close();
      else this.linkDialog.removeAttribute('open');
      this.linkEditorEndpoints = null;
    }

    syncLinkEndpointFields() {
      if (!this.linkForm || !Array.isArray(this.linkEditorEndpoints) || this.linkEditorEndpoints.length !== 2) return false;
      const [source, target] = this.linkEditorEndpoints;
      this.linkForm.elements.namedItem('source_kind').value = source.kind;
      this.linkForm.elements.namedItem('source_id').value = source.id;
      this.linkForm.elements.namedItem('target_kind').value = target.kind;
      this.linkForm.elements.namedItem('target_id').value = target.id;
      if (this.linkSourceLabel) this.linkSourceLabel.textContent = source.label;
      if (this.linkSourceRef) this.linkSourceRef.textContent = `${source.kind}:${source.id}`;
      if (this.linkTargetLabel) this.linkTargetLabel.textContent = target.label;
      if (this.linkTargetRef) this.linkTargetRef.textContent = `${target.kind}:${target.id}`;
      return true;
    }

    swapLinkEndpoints() {
      if (!Array.isArray(this.linkEditorEndpoints) || this.linkEditorEndpoints.length !== 2) return false;
      this.linkEditorEndpoints.reverse();
      this.syncLinkEndpointFields();
      this.syncLinkPreview();
      return true;
    }

    syncLinkPreview() {
      if (!this.linkForm) return false;
      const values = Object.fromEntries(new FormData(this.linkForm).entries());
      const endpoints = this.linkEditorEndpoints || [];
      const preview = relationPreview(values, {source: endpoints[0] && endpoints[0].label, target: endpoints[1] && endpoints[1].label});
      if (this.linkPreview) this.linkPreview.classList.toggle('is-invalid', !preview.valid);
      if (this.linkPreviewPath) this.linkPreviewPath.textContent = preview.path;
      if (this.linkPreviewDetail) this.linkPreviewDetail.textContent = preview.detail;
      if (this.linkSubmitButton) this.linkSubmitButton.disabled = !preview.valid;
      return preview.valid;
    }

    async saveLinkEditor() {
      if (!this.linkForm || !this.client) return false;
      if (this.linkFormError) { this.linkFormError.hidden = true; this.linkFormError.textContent = ''; }
      const values = Object.fromEntries(new FormData(this.linkForm).entries());
      values.review_approved = !!this.linkForm.elements.namedItem('review_approved').checked;
      let content;
      try { content = linkContentFromValues(values); }
      catch (error) {
        if (this.linkFormError) { this.linkFormError.hidden = false; this.linkFormError.textContent = String(error && error.message || error); }
        return false;
      }
      if (this.linkSubmitButton) this.linkSubmitButton.disabled = true;
      try {
        const response = await this.client.createLink({link: content, review_approved: values.review_approved}, {channel: 'link-mutation'});
        const link = response && response.link;
        this.closeLinkEditor();
        if (link) {
          this.writeURLState({...this.urlState, objectKind: 'link', id: link.id}, false);
          await this.refreshGraphAround(link.source, link.id);
          await this.loadSelection();
        }
        return true;
      } catch (error) {
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'Knowledge could not create this relationship.');
        if (this.linkFormError) { this.linkFormError.hidden = false; this.linkFormError.textContent = requestID ? `${message} Audit ID: ${requestID}` : message; }
        return false;
      } finally {
        if (this.linkSubmitButton) this.linkSubmitButton.disabled = false;
      }
    }

    async changeLinkLifecycle(action) {
      const link = this.inspectedRecord;
      if (!link || !this.inspectedObject || this.inspectedObject.kind !== 'link' || !['unlink', 'restore'].includes(action)) return false;
      const verb = action === 'unlink' ? 'Unlink' : 'Restore';
      if (typeof globalThis.confirm === 'function' && !globalThis.confirm(`${verb} “${link.label || displayLabel(link.kind)}”?`)) return false;
      const buttons = [this.linkUnlinkButton, this.linkRestoreButton];
      for (const button of buttons) if (button) button.disabled = true;
      if (this.linkMutationStatus) this.linkMutationStatus.textContent = `${verb} in progress…`;
      try {
        const response = await this.client.linkLifecycle(link.id, action, {
          expected_revision: link.revision.number, reason: action === 'unlink' ? 'Unlinked in Knowledge explorer' : 'Restored in Knowledge explorer',
        }, {channel: 'link-mutation'});
        const updated = response && response.link;
        if (updated) {
          await this.refreshGraphAround(updated.source, action === 'restore' ? updated.id : '');
          this.renderInspector('link', updated);
        }
        return true;
      } catch (error) {
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || `Knowledge could not ${action} this relationship.`);
        if (this.linkMutationStatus) this.linkMutationStatus.textContent = requestID ? `${message} Audit ID: ${requestID}` : message;
        return false;
      } finally {
        for (const button of buttons) if (button) button.disabled = false;
      }
    }

    async refreshGraphAround(reference, selectedLinkID) {
      const request = reference && graphSnapshotRequest(reference.kind, reference.id);
      if (!request || !this.graphAdapter) return false;
      try {
        const response = await this.client.graphSnapshot({...request, max_depth: 1, max_nodes: 100, max_edges: 200, time_limit_ms: 2000}, {channel: 'graph', timeoutMS: 10000});
        this.graphAdapter.replaceSnapshot(response);
        if (this.graphView) this.graphView.reset();
        if (selectedLinkID && this.graphAdapter.has('edge', selectedLinkID)) this.graphAdapter.select('edge', selectedLinkID);
        const counts = this.graphAdapter.counts();
        this.setGraphState(response && response.page && response.page.truncated ? 'truncated' : 'ready', `${counts.nodes} ${counts.nodes === 1 ? 'node' : 'nodes'} and ${counts.edges} ${counts.edges === 1 ? 'relationship' : 'relationships'}.`);
        if (this.graphLayout) this.graphLayout.request();
        else if (this.graphViewport) this.graphViewport.fit({animate: false});
        return true;
      } catch (error) {
        if (error && ['canceled', 'stale_response'].includes(error.code)) return false;
        const requestID = String(error && error.requestID || '');
        const message = String(error && error.message || 'The relationship changed, but the graph could not refresh.');
        this.setGraphState('stale', requestID ? `${message} Audit ID: ${requestID}` : message);
        return false;
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
        const expansion = {kind: request.root.kind, id: request.root.id, direction};
        const token = JSON.stringify(expansion);
        this.graphFrontier = [...this.graphFrontier.filter(item => JSON.stringify(item) !== token), expansion].slice(-50);
        this.schedulePreferenceSave();
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
      this.flushPreferenceSave();
      globalThis.removeEventListener('popstate', this.onPopState);
      globalThis.removeEventListener('pagehide', this.onPageHide);
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
      if (this.graphTableUnsubscribe) this.graphTableUnsubscribe();
      if (this.graphLayout) this.graphLayout.destroy();
      if (this.graphViewport) this.graphViewport.destroy();
      if (this.graphRenderer) this.graphRenderer.destroy();
      if (this.graphTable) this.graphTable.destroy();
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
    let initialPreferences = null;
    try { initialPreferences = loadLocalPreferences(globalThis.localStorage); } catch (_) {}
    if (initialPreferences) shell.dataset.mobilePane = initialPreferences.mobilePane;
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
            ? 'Reduced motion is enabled. The accessible graph table is active instead of the animated canvas.'
            : 'WebGL is unavailable. The accessible graph table provides the same graph objects and actions.';
        }
      }
      if (globalThis.KoderKnowledgeGraph && globalThis.KoderKnowledgeGraphAdapter) {
        const graphStore = new globalThis.KoderKnowledgeGraph.Store();
        runtime.graphAdapter = new globalThis.KoderKnowledgeGraphAdapter.Adapter(graphStore);
        if (globalThis.KoderKnowledgeGraphInteractions) {
          runtime.graphView = new globalThis.KoderKnowledgeGraphInteractions.LocalViewHistory({graph: graphStore.graph});
        }
        const tableContainer = shell.querySelector('[data-knowledge-graph-table]');
        const tableViewport = shell.querySelector('[data-knowledge-graph-table-viewport]');
        const tableBody = shell.querySelector('[data-knowledge-graph-table-body]');
        if (globalThis.KoderKnowledgeGraphTable && tableContainer && tableViewport && tableBody) {
          runtime.graphTable = new globalThis.KoderKnowledgeGraphTable.GraphTable({
            graph: graphStore.graph, container: tableContainer, viewport: tableViewport, body: tableBody,
            table: shell.querySelector('[data-knowledge-graph-table-element]'), summary: shell.querySelector('[data-knowledge-graph-table-summary]'),
          });
        }
        if (canvas && environment.available && globalThis.KoderKnowledgeGraphRendering && globalThis.KoderKnowledgeGraphRenderer && globalThis.KoderKnowledgeGraphViewport && globalThis.Sigma) {
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
      }
      const app = new BrowserApp(shell, client, runtime);
      shell.__koderKnowledgeApp = app;
      app.refresh();
      app.loadSavedGraphViews();
      return app;
    } catch (error) {
      const unavailable = new BrowserApp(shell, {cancelAll() {}});
      unavailable.setState('error', {detail: String(error && error.message || error)});
      shell.__koderKnowledgeApp = unavailable;
      return unavailable;
    }
  }

  return Object.freeze({panes, states, localPreferencesKey, BrowserApp, normalizePane, normalizeState, stateForError, presentationForState, adjacentPane, safeReturnPath, returnPathFromSearch, chatSelectionFromSearch, browserStateFromSearch, searchForBrowserState, browserSearchHasState, browserStatesEqual, normalizeLocalPreferences, loadLocalPreferences, saveLocalPreferences, clearLocalPreferences, graphViewStateFromPreferences, preferencesFromGraphViewState, displayLabel, plainTextLabel, graphSnapshotRequest, graphExpansionRequest, graphObjectForSelection, graphKeyboardAction, applicabilityRows, inspectorWarnings, safeExternalURL, commaValues, chunkContentFromValues, chunkEditorValues, localDateTimeValue, entryContentFromValues, entryEditorValues, editorConflict, historyProjection, historyChangedFields, historyTimeline, deletionAssessment, relationshipShapeError, linkContentFromValues, relationPreview, graphDebugEnabled, supportsWebGL, graphEnvironment, sanitizedMarkdownHTML, mount});
});
