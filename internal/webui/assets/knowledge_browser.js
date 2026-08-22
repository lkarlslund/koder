(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeBrowser = api;
  if (root && root.document) api.mount(root.document);
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const panes = Object.freeze(['sources', 'graph', 'inspector']);

  function normalizePane(value) {
    value = String(value || '').trim().toLowerCase();
    return panes.includes(value) ? value : 'graph';
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

  function mount(document) {
    const shell = document.getElementById('knowledge-browser');
    if (!shell || shell.dataset.shellMounted === 'true') return;
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
  }

  return Object.freeze({panes, normalizePane, adjacentPane, safeReturnPath, returnPathFromSearch, mount});
});
