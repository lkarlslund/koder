(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeGraphTable = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  function displayLabel(value) {
    value = String(value || '').replaceAll('_', ' ').trim();
    return value ? value.charAt(0).toUpperCase() + value.slice(1) : '';
  }

  function tableWindow(total, scrollTop, viewportHeight, rowHeight, overscan) {
    total = Math.max(0, Math.floor(Number(total) || 0));
    rowHeight = Math.max(24, Number(rowHeight) || 62);
    overscan = Math.max(0, Math.min(100, Math.floor(Number(overscan) || 6)));
    const first = Math.max(0, Math.floor(Math.max(0, Number(scrollTop) || 0) / rowHeight) - overscan);
    const visible = Math.max(1, Math.ceil(Math.max(rowHeight, Number(viewportHeight) || rowHeight) / rowHeight));
    const end = Math.min(total, first + visible + overscan * 2);
    return Object.freeze({start: first, end, top: first * rowHeight, bottom: Math.max(0, (total - end) * rowHeight)});
  }

  function graphTableItems(graph) {
    if (!graph || typeof graph.mapNodes !== 'function' || typeof graph.mapEdges !== 'function') return [];
    const visibleNodes = new Map();
    const nodes = graph.mapNodes((key, attributes) => {
      if (attributes.hidden) return null;
      const item = {
        kind: 'node', key: String(key), title: String(attributes.title || key),
        type: displayLabel(attributes.semanticKind || attributes.objectKind || 'node'),
        detail: [displayLabel(attributes.scopeKind), displayLabel(attributes.verification)].filter(Boolean).join(' · '),
        state: displayLabel(attributes.state), objectKind: String(attributes.objectKind || ''), objectID: String(attributes.objectID || ''),
      };
      visibleNodes.set(String(key), item);
      return item;
    }).filter(Boolean);
    const edges = graph.mapEdges((key, attributes, source, target) => {
      if (attributes.hidden || !visibleNodes.has(String(source)) || !visibleNodes.has(String(target))) return null;
      const sourceItem = visibleNodes.get(String(source));
      const targetItem = visibleNodes.get(String(target));
      return {
        kind: 'edge', key: String(key), title: `${sourceItem.title} → ${targetItem.title}`,
        type: 'Relationship', detail: String(attributes.label || displayLabel(attributes.relationshipKind)),
        state: displayLabel(attributes.state), source: String(source), target: String(target),
      };
    }).filter(Boolean);
    const compare = (left, right) => left.title.localeCompare(right.title) || left.key.localeCompare(right.key);
    nodes.sort(compare);
    edges.sort(compare);
    return [...nodes, ...edges];
  }

  class GraphTable {
    constructor(options) {
      options = options || {};
      if (!options.graph || !options.container || !options.viewport || !options.body) {
        throw new TypeError('Knowledge graph table requires a graph and table elements');
      }
      this.graph = options.graph;
      this.container = options.container;
      this.viewport = options.viewport;
      this.body = options.body;
      this.table = options.table || this.body.closest && this.body.closest('table');
      this.summary = options.summary || null;
      this.rowHeight = Math.max(44, Number(options.rowHeight) || 62);
      this.overscan = Math.max(1, Math.min(30, Number(options.overscan) || 7));
      this.requestFrame = options.requestAnimationFrame || globalThis.requestAnimationFrame.bind(globalThis);
      this.cancelFrame = options.cancelAnimationFrame || globalThis.cancelAnimationFrame.bind(globalThis);
      this.listeners = new Set();
      this.selection = new Set();
      this.items = [];
      this.frame = 0;
      this.focusIndex = -1;
      this.destroyed = false;
      this.onScroll = () => this.schedule();
      this.onClick = event => this.handleClick(event);
      this.onChange = event => this.handleChange(event);
      this.onKeyDown = event => this.handleKeyDown(event);
      this.viewport.addEventListener('scroll', this.onScroll, {passive: true});
      this.body.addEventListener('click', this.onClick);
      this.body.addEventListener('change', this.onChange);
      this.body.addEventListener('keydown', this.onKeyDown);
      this.refresh();
    }

    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('Knowledge graph table listener must be a function');
      this.listeners.add(listener);
      return () => this.listeners.delete(listener);
    }

    emit(action, item) {
      const event = Object.freeze({action, item: Object.freeze({...item}), table: this});
      for (const listener of [...this.listeners]) listener(event);
      return event;
    }

    setSelection(items) {
      this.selection = new Set((items || []).map(item => `${String(item.kind)}:${String(item.key)}`));
      this.schedule();
    }

    refresh() {
      if (this.destroyed) return;
      this.items = graphTableItems(this.graph);
      if (this.focusIndex >= this.items.length) this.focusIndex = this.items.length - 1;
      if (this.focusIndex < 0 && this.items.length) this.focusIndex = 0;
      if (this.summary) {
        const nodes = this.items.filter(item => item.kind === 'node').length;
        const edges = this.items.length - nodes;
        this.summary.textContent = `${nodes} visible ${nodes === 1 ? 'node' : 'nodes'} and ${edges} visible ${edges === 1 ? 'relationship' : 'relationships'}. Only the rows near the scroll position are rendered.`;
      }
      if (this.table) this.table.setAttribute('aria-rowcount', String(this.items.length + 1));
      this.schedule();
    }

    schedule() {
      if (this.destroyed || this.frame) return;
      this.frame = this.requestFrame(() => { this.frame = 0; this.render(); });
    }

    spacer(height) {
      if (height <= 0) return null;
      const row = this.body.ownerDocument.createElement('tr');
      row.setAttribute('aria-hidden', 'true');
      const cell = this.body.ownerDocument.createElement('td');
      cell.colSpan = 5;
      cell.style.height = `${height}px`;
      cell.style.padding = '0';
      cell.style.border = '0';
      row.appendChild(cell);
      return row;
    }

    actionButton(label, action, title) {
      const button = this.body.ownerDocument.createElement('button');
      button.type = 'button';
      button.dataset.graphTableAction = action;
      button.className = 'knowledge-table-action';
      button.textContent = label;
      button.title = title || label;
      return button;
    }

    itemRow(item, index) {
      const document = this.body.ownerDocument;
      const row = document.createElement('tr');
      row.dataset.graphTableIndex = String(index);
      row.dataset.graphKind = item.kind;
      row.dataset.graphKey = item.key;
      row.tabIndex = this.focusIndex === index ? 0 : -1;
      row.setAttribute('aria-rowindex', String(index + 2));
      row.classList.toggle('is-selected', this.selection.has(`${item.kind}:${item.key}`));

      const selectCell = document.createElement('td');
      const checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.dataset.graphTableSelect = 'true';
      checkbox.checked = this.selection.has(`${item.kind}:${item.key}`);
      checkbox.setAttribute('aria-label', `Select ${item.kind} ${item.title}`);
      selectCell.appendChild(checkbox);

      const objectCell = document.createElement('th');
      objectCell.scope = 'row';
      const inspect = this.actionButton(item.title, 'inspect', `Inspect ${item.title}`);
      inspect.classList.add('knowledge-table-title');
      const badge = document.createElement('span');
      badge.className = `knowledge-table-kind is-${item.kind}`;
      badge.textContent = item.kind === 'node' ? item.type : 'Relationship';
      objectCell.append(inspect, badge);

      const detailCell = document.createElement('td');
      detailCell.textContent = item.detail || '—';
      const stateCell = document.createElement('td');
      stateCell.textContent = item.state || '—';
      const actionsCell = document.createElement('td');
      actionsCell.className = 'knowledge-table-actions';
      if (item.kind === 'node') {
        actionsCell.append(this.actionButton('In', 'incoming', 'Expand incoming relationships'), this.actionButton('Out', 'outgoing', 'Expand outgoing relationships'));
      }
      actionsCell.appendChild(this.actionButton('Hide', 'hide', 'Hide locally'));
      row.append(selectCell, objectCell, detailCell, stateCell, actionsCell);
      return row;
    }

    render() {
      if (this.destroyed) return;
      const active = this.body.ownerDocument.activeElement;
      const activeIndex = active && active.closest && active.closest('[data-graph-table-index]')
        ? Number(active.closest('[data-graph-table-index]').dataset.graphTableIndex) : -1;
      if (activeIndex >= 0) this.focusIndex = activeIndex;
      const window = tableWindow(this.items.length, this.viewport.scrollTop, this.viewport.clientHeight, this.rowHeight, this.overscan);
      const fragment = this.body.ownerDocument.createDocumentFragment();
      const top = this.spacer(window.top);
      if (top) fragment.appendChild(top);
      for (let index = window.start; index < window.end; index++) fragment.appendChild(this.itemRow(this.items[index], index));
      const bottom = this.spacer(window.bottom);
      if (bottom) fragment.appendChild(bottom);
      this.body.replaceChildren(fragment);
    }

    itemForTarget(target) {
      const row = target && target.closest && target.closest('[data-graph-table-index]');
      const index = row ? Number(row.dataset.graphTableIndex) : -1;
      return Number.isSafeInteger(index) && index >= 0 && index < this.items.length ? {item: this.items[index], index, row} : null;
    }

    handleClick(event) {
      const found = this.itemForTarget(event.target);
      const button = event.target && event.target.closest && event.target.closest('[data-graph-table-action]');
      if (!found || !button) return;
      this.focusIndex = found.index;
      this.emit(button.dataset.graphTableAction, found.item);
    }

    handleChange(event) {
      const found = this.itemForTarget(event.target);
      if (!found || !(event.target && event.target.dataset.graphTableSelect)) return;
      this.focusIndex = found.index;
      this.emit('select', found.item);
    }

    handleKeyDown(event) {
      const found = this.itemForTarget(event.target);
      if (!found) return;
      let next = -1;
      if (event.key === 'ArrowDown') next = Math.min(this.items.length - 1, found.index + 1);
      if (event.key === 'ArrowUp') next = Math.max(0, found.index - 1);
      if (event.key === 'Home') next = 0;
      if (event.key === 'End') next = this.items.length - 1;
      if (next >= 0) {
        event.preventDefault();
        this.focusItem(next);
        return;
      }
      const actions = {Enter: 'inspect', ' ': 'select', Delete: 'hide', Backspace: 'hide', '[': 'incoming', ']': 'outgoing'};
      const action = actions[event.key];
      if (!action || found.item.kind === 'edge' && ['incoming', 'outgoing'].includes(action)) return;
      event.preventDefault();
      this.emit(action, found.item);
    }

    focusItem(index) {
      index = Math.max(0, Math.min(this.items.length - 1, Number(index) || 0));
      this.focusIndex = index;
      const top = index * this.rowHeight;
      const bottom = top + this.rowHeight;
      if (top < this.viewport.scrollTop) this.viewport.scrollTop = top;
      else if (bottom > this.viewport.scrollTop + this.viewport.clientHeight) this.viewport.scrollTop = Math.max(0, bottom - this.viewport.clientHeight);
      this.render();
      this.requestFrame(() => {
        const row = this.body.querySelector(`[data-graph-table-index="${index}"]`);
        if (row) row.focus({preventScroll: true});
      });
    }

    destroy() {
      if (this.destroyed) return;
      this.destroyed = true;
      if (this.frame) this.cancelFrame(this.frame);
      this.viewport.removeEventListener('scroll', this.onScroll);
      this.body.removeEventListener('click', this.onClick);
      this.body.removeEventListener('change', this.onChange);
      this.body.removeEventListener('keydown', this.onKeyDown);
      this.listeners.clear();
      this.body.replaceChildren();
    }
  }

  return Object.freeze({GraphTable, tableWindow, graphTableItems, displayLabel});
});
