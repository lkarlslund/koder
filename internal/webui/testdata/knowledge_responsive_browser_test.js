'use strict';

const assert = require('assert');
const childProcess = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const targetURL = process.env.KODER_KNOWLEDGE_TEST_URL;
const chromium = process.env.KODER_CHROMIUM;
assert(targetURL, 'KODER_KNOWLEDGE_TEST_URL is required');
assert(chromium, 'KODER_CHROMIUM is required');

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));

async function waitFor(check, description, attempts = 100) {
  for (let attempt = 0; attempt < attempts; attempt++) {
    const value = await check();
    if (value) return value;
    await delay(50);
  }
  throw new Error(`Timed out waiting for ${description}`);
}

async function main() {
  const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'koder-knowledge-responsive-'));
  const processHandle = childProcess.spawn(chromium, [
    '--headless=new', '--enable-unsafe-swiftshader', '--no-first-run', '--no-default-browser-check',
    '--disable-background-networking', '--disable-component-update', '--remote-debugging-port=0',
    `--user-data-dir=${profile}`, targetURL,
  ], {stdio: ['ignore', 'ignore', 'pipe']});
  const processExited = new Promise(resolve => processHandle.once('exit', resolve));
  let stderr = '';
  processHandle.stderr.setEncoding('utf8');
  processHandle.stderr.on('data', chunk => { stderr += chunk; });

  let socket;
  try {
    const debuggerURL = await waitFor(() => {
      const match = stderr.match(/DevTools listening on (ws:\/\/[^\s]+)/);
      return match && match[1];
    }, 'Chromium DevTools');
    const debuggerAddress = new URL(debuggerURL);
    const targetsURL = `http://${debuggerAddress.host}/json`;
    const page = await waitFor(async () => {
      try {
        const targets = await (await fetch(targetsURL)).json();
        return targets.find(target => target.type === 'page' && target.url.startsWith(targetURL));
      } catch (_) {
        return null;
      }
    }, 'Knowledge browser target');

    socket = new WebSocket(page.webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      socket.addEventListener('open', resolve, {once: true});
      socket.addEventListener('error', reject, {once: true});
    });
    let sequence = 0;
    const pending = new Map();
    const exceptions = [];
    socket.addEventListener('message', event => {
      const message = JSON.parse(event.data);
      if (message.id && pending.has(message.id)) {
        const request = pending.get(message.id);
        pending.delete(message.id);
        if (message.error) request.reject(new Error(message.error.message));
        else request.resolve(message.result);
      } else if (message.method === 'Runtime.exceptionThrown') {
        exceptions.push(message.params.exceptionDetails.exception?.description || message.params.exceptionDetails.text);
      }
    });
    const send = (method, params = {}) => new Promise((resolve, reject) => {
      const id = ++sequence;
      pending.set(id, {resolve, reject});
      socket.send(JSON.stringify({id, method, params}));
    });
    const evaluate = async expression => {
      const response = await send('Runtime.evaluate', {expression, awaitPromise: true, returnByValue: true});
      if (response.exceptionDetails) {
        throw new Error(response.exceptionDetails.exception?.description || response.exceptionDetails.text);
      }
      return response.result.value;
    };
    const setViewport = async (width, height, mobile) => {
      await send('Emulation.setDeviceMetricsOverride', {width, height, deviceScaleFactor: 1, mobile});
      await delay(100);
    };
    const tap = async selector => {
      const point = await evaluate(`(() => {
        const element = document.querySelector(${JSON.stringify(selector)});
        if (!element) return null;
        const rect = element.getBoundingClientRect();
        return {x: rect.left + rect.width / 2, y: rect.top + rect.height / 2};
      })()`);
      assert(point, `missing touch target ${selector}`);
      await send('Input.dispatchTouchEvent', {type: 'touchStart', touchPoints: [{x: point.x, y: point.y, radiusX: 4, radiusY: 4}]});
      await send('Input.dispatchTouchEvent', {type: 'touchEnd', touchPoints: []});
      await delay(100);
    };

    await send('Runtime.enable');
    await send('Emulation.setTouchEmulationEnabled', {enabled: true, maxTouchPoints: 5});
    await setViewport(390, 844, true);
    await waitFor(() => evaluate(`Boolean(document.getElementById('knowledge-browser')?.__koderKnowledgeApp && document.querySelector('[data-object-kind="chunk"]'))`), 'mobile Knowledge shell');

    const phone = await evaluate(`(() => {
      const visible = selector => getComputedStyle(document.querySelector(selector)).display !== 'none';
      const tabs = [...document.querySelectorAll('[data-knowledge-tab]')].map(tab => tab.getBoundingClientRect());
      const action = document.querySelector('.knowledge-graph-toolbar-actions .knowledge-icon-button').getBoundingClientRect();
      return {
        mobileTabs: visible('.knowledge-mobile-tabs'), sources: visible('.knowledge-sources-pane'),
        graph: visible('.knowledge-graph-pane'), inspector: visible('.knowledge-inspector-pane'),
        bodyFits: document.documentElement.scrollWidth <= innerWidth,
        tabTargets: tabs.every(rect => rect.height >= 44 && rect.width >= 44),
        actionTarget: action.width >= 44 && action.height >= 44,
      };
    })()`);
    assert.deepStrictEqual(phone, {
      mobileTabs: true, sources: false, graph: true, inspector: false,
      bodyFits: true, tabTargets: true, actionTarget: true,
    });

    await tap('[data-knowledge-tab="sources"]');
    assert.strictEqual(await evaluate(`document.getElementById('knowledge-browser').dataset.mobilePane`), 'sources');
    await tap('[data-object-kind="chunk"]');
    await waitFor(() => evaluate(`!document.querySelector('[data-knowledge-inspector-content]').hidden`), 'touch-selected inspector');
    assert.strictEqual(await evaluate(`document.getElementById('knowledge-browser').dataset.mobilePane`), 'inspector');

    await tap('[data-knowledge-tab="graph"]');
    await waitFor(() => evaluate(`document.querySelector('#knowledge-graph').dataset.graphState === 'ready'`), 'touch-selected graph');
    assert.strictEqual(await evaluate(`getComputedStyle(document.querySelector('.knowledge-graph-pane')).display`), 'flex');

    await setViewport(844, 390, true);
    const landscape = await evaluate(`({pane: document.getElementById('knowledge-browser').dataset.mobilePane, bodyFits: document.documentElement.scrollWidth <= innerWidth, graphVisible: getComputedStyle(document.querySelector('.knowledge-graph-pane')).display === 'flex'})`);
    assert.deepStrictEqual(landscape, {pane: 'graph', bodyFits: true, graphVisible: true});

    await setViewport(1200, 800, false);
    const desktop = await evaluate(`(() => {
      const visible = selector => getComputedStyle(document.querySelector(selector)).display !== 'none';
      return {mobileTabs: visible('.knowledge-mobile-tabs'), sources: visible('.knowledge-sources-pane'), graph: visible('.knowledge-graph-pane'), inspector: visible('.knowledge-inspector-pane'), bodyFits: document.documentElement.scrollWidth <= innerWidth};
    })()`);
    assert.deepStrictEqual(desktop, {mobileTabs: false, sources: true, graph: true, inspector: true, bodyFits: true});
    assert.deepStrictEqual(exceptions, []);
  } finally {
    if (socket) socket.close();
    processHandle.kill('SIGTERM');
    await new Promise(resolve => {
      const timeout = setTimeout(() => {
        processHandle.kill('SIGKILL');
        resolve();
      }, 2000);
      processExited.then(() => {
        clearTimeout(timeout);
        resolve();
      });
    });
    fs.rmSync(profile, {recursive: true, force: true});
  }
}

main().catch(error => {
  console.error(error.stack || error);
  process.exitCode = 1;
});
