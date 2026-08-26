(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderMemoryAPI = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const API_VERSION = 'memory.v1';

  class MemoryAPIError extends Error {
    constructor(message, options) {
      super(String(message || 'Memory request failed'));
      options = options || {};
      this.name = 'MemoryAPIError';
      this.code = String(options.code || 'request_failed');
      this.status = Number(options.status) || 0;
      this.retryable = !!options.retryable;
      this.requestID = String(options.requestID || '');
      this.details = options.details || null;
      if (options.cause !== undefined) this.cause = options.cause;
    }
  }

  class MemoryAPIStaleResponseError extends MemoryAPIError {
    constructor(channel, generation) {
      super('A newer Memory request replaced this result.', {code: 'stale_response'});
      this.name = 'MemoryAPIStaleResponseError';
      this.channel = channel;
      this.generation = generation;
    }
  }

  function normalizeBase(value) {
    value = String(value || '/api/memory/v1').trim().replace(/\/+$/, '');
    if (!/^\/api\/memory\/v[0-9]+$/.test(value)) throw new TypeError('Memory API base must be a local versioned path');
    return value;
  }

  function encodeID(value) {
    value = String(value || '').trim();
    if (!value || value === '.' || value === '..' || value.includes('/')) throw new TypeError('Memory object ID is invalid');
    return encodeURIComponent(value);
  }

  function queryString(values) {
    const query = new URLSearchParams();
    const input = values && typeof values === 'object' ? values : {};
    for (const key of Object.keys(input).sort()) {
      const raw = input[key];
      const items = Array.isArray(raw) ? raw : [raw];
      for (const item of items) {
        if (item === undefined || item === null || item === '') continue;
        if (typeof item === 'object') throw new TypeError('Memory query values must be scalar');
        query.append(key, String(item));
      }
    }
    const encoded = query.toString();
    return encoded ? '?' + encoded : '';
  }

  function attachmentFilename(value, fallback) {
    value = String(value || '');
    fallback = String(fallback || 'memory.kmemory');
    const encoded = value.match(/filename\*=UTF-8''([^;]+)/i);
    const plain = value.match(/filename="([^"]+)"|filename=([^;\s]+)/i);
    let name = plain ? (plain[1] || plain[2]) : fallback;
    if (encoded) {
      try { name = decodeURIComponent(encoded[1]); } catch (_) { name = fallback; }
    }
    name = String(name || fallback).replace(/[\\/\u0000-\u001f\u007f]/g, '-').trim().replace(/^\.+/, '');
    if (!name.toLowerCase().endsWith('.kmemory')) name += '.kmemory';
    return name || fallback;
  }

  async function responseJSON(response) {
    const text = await response.text();
    if (!text.trim()) return null;
    try {
      return JSON.parse(text);
    } catch (cause) {
      throw new MemoryAPIError('Memory returned an invalid response.', {
        code: 'invalid_response', status: response.status, cause,
        requestID: response.headers && response.headers.get ? response.headers.get('X-Koder-Request-ID') : ''
      });
    }
  }

  class Client {
    constructor(options) {
      options = options || {};
      this.base = normalizeBase(options.base);
      this.token = String(options.token || '').trim();
      this.fetch = options.fetchImpl || (typeof fetch === 'function' ? fetch.bind(globalThis) : null);
      if (!this.fetch) throw new TypeError('Memory API fetch implementation is required');
      this.timeoutMS = Math.max(1, Number(options.timeoutMS) || 15000);
      this.active = new Map();
      this.generations = new Map();
    }

    generation(channel) {
      return this.generations.get(String(channel || 'default')) || 0;
    }

    cancel(channel) {
      channel = String(channel || 'default');
      const current = this.active.get(channel);
      if (!current) return false;
      current.controller.abort();
      this.active.delete(channel);
      return true;
    }

    cancelAll() {
      for (const current of this.active.values()) current.controller.abort();
      this.active.clear();
    }

    async request(path, options) {
      options = options || {};
      path = String(path || '').trim();
      if (!path.startsWith('/') || path.startsWith('//') || path.includes('..')) throw new TypeError('Memory API path is invalid');
      const channel = String(options.channel || 'default');
      const previous = this.active.get(channel);
      if (previous) previous.controller.abort();
      const generation = this.generation(channel) + 1;
      this.generations.set(channel, generation);
      const controller = new AbortController();
      const ticket = {controller, generation};
      this.active.set(channel, ticket);

      let timedOut = false;
      const timeout = setTimeout(() => {
        timedOut = true;
        controller.abort();
      }, Math.max(1, Number(options.timeoutMS) || this.timeoutMS));
      const externalSignal = options.signal;
      const externalAbort = () => controller.abort();
      if (externalSignal) {
        if (externalSignal.aborted) externalAbort();
        else externalSignal.addEventListener('abort', externalAbort, {once: true});
      }

      try {
        const headers = new Headers(options.headers || {});
        headers.set('Accept', options.responseType === 'package' ? 'application/vnd.koder.memory+zip' : 'application/json');
        if (this.token) headers.set('Authorization', 'Bearer ' + this.token);
        let body;
        if (options.rawBody !== undefined) {
          headers.set('Content-Type', String(options.contentType || 'application/octet-stream'));
          body = options.rawBody;
        } else if (options.body !== undefined) {
          headers.set('Content-Type', 'application/json');
          body = JSON.stringify(options.body);
        }
        const response = await this.fetch(this.base + path + queryString(options.query), {
          method: String(options.method || 'GET').toUpperCase(), headers, body,
          signal: controller.signal, cache: 'no-store', credentials: 'same-origin'
        });
        if (this.generation(channel) !== generation) throw new MemoryAPIStaleResponseError(channel, generation);
        let data;
        if (!response.ok || options.responseType !== 'package') data = await responseJSON(response);
        if (this.generation(channel) !== generation) throw new MemoryAPIStaleResponseError(channel, generation);
        if (!response.ok) {
          const detail = data && data.error || {};
          throw new MemoryAPIError(detail.message || 'Memory request failed.', {
            code: detail.code || 'http_error', status: response.status, retryable: detail.retryable,
            details: detail.details, requestID: data && data.request_id || response.headers.get('X-Koder-Request-ID')
          });
        }
        if (options.responseType === 'package') {
          const blob = await response.blob();
          if (this.generation(channel) !== generation) throw new MemoryAPIStaleResponseError(channel, generation);
          return {
            blob,
            filename: attachmentFilename(response.headers.get('Content-Disposition'), options.filename),
            contentType: response.headers.get('Content-Type') || '',
            etag: response.headers.get('ETag') || '',
          };
        }
        if (data && data.api_version && data.api_version !== API_VERSION) {
          throw new MemoryAPIError('Memory API version is not compatible.', {
            code: 'incompatible_api', status: response.status, requestID: data.request_id
          });
        }
        return data;
      } catch (cause) {
        if (cause instanceof MemoryAPIStaleResponseError || cause instanceof MemoryAPIError) throw cause;
        if (this.generation(channel) !== generation) throw new MemoryAPIStaleResponseError(channel, generation);
        if (timedOut) {
          throw new MemoryAPIError('Memory request timed out.', {code: 'timeout', retryable: true, cause});
        }
        if (controller.signal.aborted) {
          throw new MemoryAPIError('Memory request was canceled.', {code: 'canceled', cause});
        }
        throw new MemoryAPIError('Memory could not reach Koder.', {code: 'network', retryable: true, cause});
      } finally {
        clearTimeout(timeout);
        if (externalSignal) externalSignal.removeEventListener('abort', externalAbort);
        if (this.active.get(channel) === ticket) this.active.delete(channel);
      }
    }

    listChunks(query, options) { return this.request('/chunks', {...options, query}); }
    getChunk(id, options) { return this.request('/chunks/' + encodeID(id), options); }
    createChunk(body, options) { return this.request('/chunks', {...options, method: 'POST', body}); }
    updateChunk(id, body, options) { return this.request('/chunks/' + encodeID(id), {...options, method: 'PUT', body}); }
    chunkLifecycle(id, action, body, options) {
      action = String(action || '');
      if (!['archive', 'restore'].includes(action)) throw new TypeError('Memory chunk lifecycle action is invalid');
      return this.request('/chunks/' + encodeID(id) + '/' + action, {...options, method: 'POST', body});
    }
    deleteChunk(id, body, options) { return this.request('/chunks/' + encodeID(id), {...options, method: 'DELETE', body}); }
    listEntries(query, options) { return this.request('/entries', {...options, query}); }
    getEntry(id, options) { return this.request('/entries/' + encodeID(id), options); }
    createEntry(body, options) { return this.request('/entries', {...options, method: 'POST', body}); }
    updateEntry(id, body, options) { return this.request('/entries/' + encodeID(id), {...options, method: 'PUT', body}); }
    entryLifecycle(id, action, body, options) {
      action = String(action || '');
      if (!['archive', 'restore'].includes(action)) throw new TypeError('Memory entry lifecycle action is invalid');
      return this.request('/entries/' + encodeID(id) + '/' + action, {...options, method: 'POST', body});
    }
    supersedeEntry(id, body, options) { return this.request('/entries/' + encodeID(id) + '/supersede', {...options, method: 'POST', body}); }
    deleteEntry(id, body, options) { return this.request('/entries/' + encodeID(id), {...options, method: 'DELETE', body}); }
    entryEvidence(id, query, options) { return this.request('/entries/' + encodeID(id) + '/evidence', {...options, query}); }
    getLink(id, options) { return this.request('/links/' + encodeID(id), options); }
    createLink(body, options) { return this.request('/links', {...options, method: 'POST', body}); }
    linkLifecycle(id, action, body, options) {
      action = String(action || '');
      if (!['unlink', 'restore'].includes(action)) throw new TypeError('Memory relationship lifecycle action is invalid');
      return this.request('/links/' + encodeID(id) + '/' + action, {...options, method: 'POST', body});
    }
    history(kind, id, query, options) {
      kind = String(kind || '');
      const collections = {chunk: 'chunks', entry: 'entries', link: 'links'};
      if (!collections[kind]) throw new TypeError('Memory history object kind is invalid');
      return this.request('/' + collections[kind] + '/' + encodeID(id) + '/history', {...options, query});
    }
    search(body, options) { return this.request('/search', {...options, method: 'POST', body}); }
    graphSnapshot(body, options) { return this.request('/graph/snapshot', {...options, method: 'POST', body}); }
    neighbors(body, options) { return this.request('/neighbors', {...options, method: 'POST', body}); }
    status(options) { return this.request('/status', options); }
    sendToChat(body, options) { return this.request('/chat-context', {...options, method: 'POST', body}); }
    listGraphViews(options) { return this.request('/views', options); }
    getGraphView(id, options) { return this.request('/views/' + encodeID(id), options); }
    createGraphView(body, options) { return this.request('/views', {...options, method: 'POST', body}); }
    updateGraphView(id, body, options) { return this.request('/views/' + encodeID(id), {...options, method: 'PUT', body}); }
    deleteGraphView(id, body, options) { return this.request('/views/' + encodeID(id), {...options, method: 'DELETE', body}); }
    previewPackage(file, options) {
      if (!file) throw new TypeError('Memory package file is required');
      return this.request('/packages/preview', {...options, method: 'POST', rawBody: file, contentType: 'application/vnd.koder.memory+zip', timeoutMS: 90000});
    }
    stagePackage(file, values, options) {
      if (!file) throw new TypeError('Memory package file is required');
      return this.request('/packages/stages', {...options, method: 'POST', rawBody: file, contentType: 'application/vnd.koder.memory+zip', query: values, timeoutMS: 90000});
    }
    activatePackage(stageID, options) { return this.request('/packages/stages/' + encodeID(stageID) + '/activate', {...options, method: 'POST', timeoutMS: 90000}); }
    discardPackage(stageID, options) { return this.request('/packages/stages/' + encodeID(stageID), {...options, method: 'DELETE'}); }
    exportPackage(chunkID, options) { return this.request('/packages/export/' + encodeID(chunkID), {...options, responseType: 'package', timeoutMS: 90000, filename: 'memory.kmemory'}); }
	listCurationCandidates(query, options) { return this.request('/curation/candidates', {...options, query}); }
	curationCandidateDecision(id, action, body, options) {
	  action = String(action || '');
	  if (!['accept', 'reject', 'undo'].includes(action)) throw new TypeError('Memory curation action is invalid');
	  return this.request('/curation/candidates/' + encodeID(id) + '/' + action, {...options, method: 'POST', body});
	}

    async *pages(path, options) {
      options = options || {};
      let cursor = String(options.cursor || '');
      const seen = new Set(cursor ? [cursor] : []);
      const maxPages = Math.max(1, Math.min(1000, Number(options.maxPages) || 100));
      for (let pageNumber = 0; pageNumber < maxPages; pageNumber++) {
        const query = {...(options.query || {})};
        if (cursor) query.cursor = cursor;
        const page = await this.request(path, {...options, query, cursor: undefined, maxPages: undefined});
        yield page;
        const next = String(page && page.page && page.page.next_cursor || '');
        if (!next) return;
        if (seen.has(next)) throw new MemoryAPIError('Memory returned a repeated cursor.', {code: 'invalid_cursor'});
        seen.add(next);
        cursor = next;
      }
      throw new MemoryAPIError('Memory pagination exceeded the client safety limit.', {code: 'page_limit'});
    }
  }

  function fromPageConfig(config, options) {
    config = config || {};
    return new Client({...options, base: config.api_base, token: config.token});
  }

  return Object.freeze({API_VERSION, Client, MemoryAPIError, MemoryAPIStaleResponseError, queryString, attachmentFilename, fromPageConfig});
});
