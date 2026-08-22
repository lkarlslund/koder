'use strict';

const assert = require('assert');
const knowledge = require('../assets/knowledge_api_client.js');

function response(status, body, requestID = 'request-1') {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({'X-Koder-Request-ID': requestID}),
    text: async () => body === null ? '' : JSON.stringify(body),
  };
}

async function testRequestAndCursorEncoding() {
  const requests = [];
  const client = new knowledge.Client({token: 'secret', fetchImpl: async (url, options) => {
    requests.push({url, options});
    return response(200, {api_version: 'knowledge.v1', chunks: [], page: {next_cursor: 'opaque'}});
  }});
  const data = await client.listChunks({kind: ['reference', 'personal'], cursor: 'opaque +/='}, {channel: 'chunks'});
  assert.strictEqual(data.page.next_cursor, 'opaque');
  assert.strictEqual(requests[0].url, '/api/knowledge/v1/chunks?cursor=opaque+%2B%2F%3D&kind=reference&kind=personal');
  assert.strictEqual(requests[0].options.headers.get('Authorization'), 'Bearer secret');
  assert.strictEqual(requests[0].options.credentials, 'same-origin');
  assert.strictEqual(client.generation('chunks'), 1);

  await client.getLink('link-1', {channel: 'link'});
  assert.strictEqual(requests[1].url, '/api/knowledge/v1/links/link-1');

  await client.entryEvidence('entry-1', {limit: 25}, {channel: 'evidence'});
  assert.strictEqual(requests[2].url, '/api/knowledge/v1/entries/entry-1/evidence?limit=25');

  await client.createChunk({chunk: {title: 'Test'}}, {channel: 'chunk-create'});
  assert.strictEqual(requests[3].url, '/api/knowledge/v1/chunks');
  assert.strictEqual(requests[3].options.method, 'POST');
  await client.updateChunk('chunk-1', {expected_revision: 1, chunk: {title: 'Test'}}, {channel: 'chunk-update'});
  assert.strictEqual(requests[4].options.method, 'PUT');
  await client.chunkLifecycle('chunk-1', 'archive', {expected_revision: 2}, {channel: 'chunk-lifecycle'});
  assert.strictEqual(requests[5].url, '/api/knowledge/v1/chunks/chunk-1/archive');
  await client.deleteChunk('chunk-1', {expected_revision: 3, confirmed: true}, {channel: 'chunk-delete'});
  assert.strictEqual(requests[6].options.method, 'DELETE');
  assert.throws(() => client.chunkLifecycle('chunk-1', 'erase', {}), /invalid/);

  await client.createEntry({chunk_id: 'chunk-1', entry: {title: 'Entry'}}, {channel: 'entry-create'});
  assert.strictEqual(requests[7].url, '/api/knowledge/v1/entries');
  await client.updateEntry('entry-1', {expected_revision: 1, entry: {title: 'Entry'}}, {channel: 'entry-update'});
  assert.strictEqual(requests[8].options.method, 'PUT');
  await client.entryLifecycle('entry-1', 'archive', {expected_revision: 2}, {channel: 'entry-lifecycle'});
  assert.strictEqual(requests[9].url, '/api/knowledge/v1/entries/entry-1/archive');
  await client.supersedeEntry('entry-1', {replacement_entry_id: 'entry-2', expected_revision: 3}, {channel: 'entry-supersede'});
  assert.strictEqual(requests[10].url, '/api/knowledge/v1/entries/entry-1/supersede');
  await client.deleteEntry('entry-1', {expected_revision: 4, confirmed: true}, {channel: 'entry-delete'});
  assert.strictEqual(requests[11].options.method, 'DELETE');
  assert.throws(() => client.entryLifecycle('entry-1', 'erase', {}), /invalid/);

  await client.createLink({link: {source: {kind: 'entry', id: 'entry-1'}, target: {kind: 'entry', id: 'entry-2'}, kind: 'related_to'}}, {channel: 'link-create'});
  assert.strictEqual(requests[12].url, '/api/knowledge/v1/links');
  assert.strictEqual(requests[12].options.method, 'POST');
  await client.linkLifecycle('link-1', 'unlink', {expected_revision: 1}, {channel: 'link-lifecycle'});
  assert.strictEqual(requests[13].url, '/api/knowledge/v1/links/link-1/unlink');
  assert.strictEqual(requests[13].options.method, 'POST');
  assert.throws(() => client.linkLifecycle('link-1', 'delete', {}), /invalid/);
  await client.history('entry', 'entry-1', {limit: 20, cursor: 'older'}, {channel: 'history'});
  assert.strictEqual(requests[14].url, '/api/knowledge/v1/entries/entry-1/history?cursor=older&limit=20');
  assert.throws(() => client.history('evidence', 'evidence-1', {}), /invalid/);

  const selection = {session_id: 'session-1', chat_id: 'chat-2', object: {kind: 'entry', id: 'entry-3'}};
  await client.sendToChat(selection, {channel: 'send'});
  assert.strictEqual(requests[15].url, '/api/knowledge/v1/chat-context');
  assert.strictEqual(requests[15].options.method, 'POST');
  assert.deepStrictEqual(JSON.parse(requests[15].options.body), selection);

  await client.listGraphViews({channel: 'views'});
  assert.strictEqual(requests[16].url, '/api/knowledge/v1/views');
  await client.createGraphView({name: 'My view', state: {}}, {channel: 'view-create'});
  assert.strictEqual(requests[17].options.method, 'POST');
  await client.getGraphView('view-1', {channel: 'view'});
  assert.strictEqual(requests[18].url, '/api/knowledge/v1/views/view-1');
  await client.updateGraphView('view-1', {name: 'Updated', state: {}, expected_revision: 1}, {channel: 'view-update'});
  assert.strictEqual(requests[19].options.method, 'PUT');
  await client.deleteGraphView('view-1', {expected_revision: 2}, {channel: 'view-delete'});
  assert.strictEqual(requests[20].options.method, 'DELETE');
}

async function testNewGenerationCancelsStaleRequest() {
  let calls = 0;
  const client = new knowledge.Client({fetchImpl: (url, options) => {
    calls++;
    if (calls === 2) return Promise.resolve(response(200, {api_version: 'knowledge.v1', value: 'new'}));
    return new Promise((resolve, reject) => options.signal.addEventListener('abort', () => {
      const error = new Error('aborted'); error.name = 'AbortError'; reject(error);
    }, {once: true}));
  }});
  const stale = client.request('/status', {channel: 'status'});
  const current = client.request('/status', {channel: 'status'});
  await assert.rejects(stale, error => error instanceof knowledge.KnowledgeAPIStaleResponseError && error.generation === 1);
  assert.strictEqual((await current).value, 'new');
  assert.strictEqual(client.generation('status'), 2);
}

async function testStructuredErrors() {
  const client = new knowledge.Client({fetchImpl: async () => response(409, {
    api_version: 'knowledge.v1', request_id: 'audit-9',
    error: {code: 'conflict', message: 'Refresh.', retryable: true, details: {revision: 4}},
  })});
  await assert.rejects(client.getChunk('chunk-1'), error => {
    assert(error instanceof knowledge.KnowledgeAPIError);
    assert.strictEqual(error.code, 'conflict');
    assert.strictEqual(error.status, 409);
    assert.strictEqual(error.requestID, 'audit-9');
    assert.strictEqual(error.details.revision, 4);
    return true;
  });
}

async function testPaginationUsesOpaqueCursors() {
  const urls = [];
  const pages = [
    {api_version: 'knowledge.v1', chunks: [{id: 'one'}], page: {next_cursor: 'cursor/1 +'}},
    {api_version: 'knowledge.v1', chunks: [{id: 'two'}], page: {}},
  ];
  const client = new knowledge.Client({fetchImpl: async url => {
    urls.push(url);
    return response(200, pages.shift());
  }});
  const ids = [];
  for await (const page of client.pages('/chunks', {query: {limit: 1}, channel: 'pages'})) ids.push(page.chunks[0].id);
  assert.deepStrictEqual(ids, ['one', 'two']);
  assert.deepStrictEqual(urls, [
    '/api/knowledge/v1/chunks?limit=1',
    '/api/knowledge/v1/chunks?cursor=cursor%2F1+%2B&limit=1',
  ]);
}

async function testCancelAndValidation() {
  const client = new knowledge.Client({fetchImpl: (url, options) => new Promise((resolve, reject) => {
    options.signal.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), {name: 'AbortError'})), {once: true});
  })});
  const pending = client.status({channel: 'cancel'});
  assert.strictEqual(client.cancel('cancel'), true);
  await assert.rejects(pending, error => error.code === 'canceled');
  assert.throws(() => new knowledge.Client({base: 'https://example.com/api', fetchImpl: async () => {}}), /local versioned path/);
  assert.throws(() => client.getChunk('../secret'), /invalid/);
  assert.throws(() => knowledge.queryString({scope: {kind: 'global'}}), /scalar/);
}

async function testRepeatedCursorIsRejected() {
  const client = new knowledge.Client({fetchImpl: async () => response(200, {
    api_version: 'knowledge.v1', chunks: [], page: {next_cursor: 'same'},
  })});
  const iterator = client.pages('/chunks', {cursor: 'same'});
  await iterator.next();
  await assert.rejects(iterator.next(), error => error.code === 'invalid_cursor');
}

Promise.resolve()
  .then(testRequestAndCursorEncoding)
  .then(testNewGenerationCancelsStaleRequest)
  .then(testStructuredErrors)
  .then(testPaginationUsesOpaqueCursors)
  .then(testCancelAndValidation)
  .then(testRepeatedCursorIsRejected)
  .catch(error => { console.error(error); process.exitCode = 1; });
