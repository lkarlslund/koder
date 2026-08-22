(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeAPI = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const API_VERSION = 'knowledge.v1';

  class KnowledgeAPIError extends Error {
    constructor(message, options) {
      super(String(message || 'Knowledge request failed'));
      options = options || {};
      this.name = 'KnowledgeAPIError';
      this.code = String(options.code || 'request_failed');
      this.status = Number(options.status) || 0;
      this.retryable = !!options.retryable;
      this.requestID = String(options.requestID || '');
      this.details = options.details || null;
      if (options.cause !== undefined) this.cause = options.cause;
    }
  }

  class KnowledgeAPIStaleResponseError extends KnowledgeAPIError {
    constructor(channel, generation) {
      super('A newer Knowledge request replaced this result.', {code: 'stale_response'});
      this.name = 'KnowledgeAPIStaleResponseError';
      this.channel = channel;
      this.generation = generation;
    }
  }

  function normalizeBase(value) {
    value = String(value || '/api/knowledge/v1').trim().replace(/\/+$/, '');
    if (!/^\/api\/knowledge\/v[0-9]+$/.test(value)) throw new TypeError('Knowledge API base must be a local versioned path');
    return value;
  }

  function encodeID(value) {
    value = String(value || '').trim();
    if (!value || value === '.' || value === '..' || value.includes('/')) throw new TypeError('Knowledge object ID is invalid');
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
        if (typeof item === 'object') throw new TypeError('Knowledge query values must be scalar');
        query.append(key, String(item));
      }
    }
    const encoded = query.toString();
    return encoded ? '?' + encoded : '';
  }

  async function responseJSON(response) {
    const text = await response.text();
    if (!text.trim()) return null;
    try {
      return JSON.parse(text);
    } catch (cause) {
      throw new KnowledgeAPIError('Knowledge returned an invalid response.', {
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
      if (!this.fetch) throw new TypeError('Knowledge API fetch implementation is required');
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
      if (!path.startsWith('/') || path.startsWith('//') || path.includes('..')) throw new TypeError('Knowledge API path is invalid');
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
        headers.set('Accept', 'application/json');
        if (this.token) headers.set('Authorization', 'Bearer ' + this.token);
        let body;
        if (options.body !== undefined) {
          headers.set('Content-Type', 'application/json');
          body = JSON.stringify(options.body);
        }
        const response = await this.fetch(this.base + path + queryString(options.query), {
          method: String(options.method || 'GET').toUpperCase(), headers, body,
          signal: controller.signal, cache: 'no-store', credentials: 'same-origin'
        });
        if (this.generation(channel) !== generation) throw new KnowledgeAPIStaleResponseError(channel, generation);
        const data = await responseJSON(response);
        if (this.generation(channel) !== generation) throw new KnowledgeAPIStaleResponseError(channel, generation);
        if (!response.ok) {
          const detail = data && data.error || {};
          throw new KnowledgeAPIError(detail.message || 'Knowledge request failed.', {
            code: detail.code || 'http_error', status: response.status, retryable: detail.retryable,
            details: detail.details, requestID: data && data.request_id || response.headers.get('X-Koder-Request-ID')
          });
        }
        if (data && data.api_version && data.api_version !== API_VERSION) {
          throw new KnowledgeAPIError('Knowledge API version is not compatible.', {
            code: 'incompatible_api', status: response.status, requestID: data.request_id
          });
        }
        return data;
      } catch (cause) {
        if (cause instanceof KnowledgeAPIStaleResponseError || cause instanceof KnowledgeAPIError) throw cause;
        if (this.generation(channel) !== generation) throw new KnowledgeAPIStaleResponseError(channel, generation);
        if (timedOut) {
          throw new KnowledgeAPIError('Knowledge request timed out.', {code: 'timeout', retryable: true, cause});
        }
        if (controller.signal.aborted) {
          throw new KnowledgeAPIError('Knowledge request was canceled.', {code: 'canceled', cause});
        }
        throw new KnowledgeAPIError('Knowledge could not reach Koder.', {code: 'network', retryable: true, cause});
      } finally {
        clearTimeout(timeout);
        if (externalSignal) externalSignal.removeEventListener('abort', externalAbort);
        if (this.active.get(channel) === ticket) this.active.delete(channel);
      }
    }

    listChunks(query, options) { return this.request('/chunks', {...options, query}); }
    getChunk(id, options) { return this.request('/chunks/' + encodeID(id), options); }
    listEntries(query, options) { return this.request('/entries', {...options, query}); }
    getEntry(id, options) { return this.request('/entries/' + encodeID(id), options); }
    entryEvidence(id, query, options) { return this.request('/entries/' + encodeID(id) + '/evidence', {...options, query}); }
    getLink(id, options) { return this.request('/links/' + encodeID(id), options); }
    search(body, options) { return this.request('/search', {...options, method: 'POST', body}); }
    graphSnapshot(body, options) { return this.request('/graph/snapshot', {...options, method: 'POST', body}); }
    neighbors(body, options) { return this.request('/neighbors', {...options, method: 'POST', body}); }
    status(options) { return this.request('/status', options); }
    sendToChat(body, options) { return this.request('/chat-context', {...options, method: 'POST', body}); }

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
        if (seen.has(next)) throw new KnowledgeAPIError('Knowledge returned a repeated cursor.', {code: 'invalid_cursor'});
        seen.add(next);
        cursor = next;
      }
      throw new KnowledgeAPIError('Knowledge pagination exceeded the client safety limit.', {code: 'page_limit'});
    }
  }

  function fromPageConfig(config, options) {
    config = config || {};
    return new Client({...options, base: config.api_base, token: config.token});
  }

  return Object.freeze({API_VERSION, Client, KnowledgeAPIError, KnowledgeAPIStaleResponseError, queryString, fromPageConfig});
});
