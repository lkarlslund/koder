(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  if (root) root.KoderKnowledgeGraphRendering = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  const kindStyles = Object.freeze({
    'chunk:reference': {color: '#5279c7', shape: 'hexagon', glyph: 'R', size: 15},
    'chunk:personal': {color: '#a866c7', shape: 'hexagon', glyph: 'P', size: 15},
    'chunk:project': {color: '#327f9d', shape: 'hexagon', glyph: '⌂', size: 15},
    'chunk:environment': {color: '#5c8c63', shape: 'hexagon', glyph: 'E', size: 15},
    'entry:fact': {color: '#4c8dd8', shape: 'circle', glyph: 'i', size: 8},
    'entry:procedure': {color: '#36a69a', shape: 'diamond', glyph: '→', size: 9},
    'entry:concept': {color: '#7986cb', shape: 'circle', glyph: 'C', size: 9},
    'entry:warning': {color: '#db6b66', shape: 'triangle', glyph: '!', size: 10},
    'entry:preference': {color: '#ac75c5', shape: 'heart', glyph: '♥', size: 9},
    'entry:decision': {color: '#d28a45', shape: 'diamond', glyph: 'D', size: 10},
    'entry:reference': {color: '#5e91b5', shape: 'square', glyph: '↗', size: 8},
    default: {color: '#718096', shape: 'circle', glyph: '·', size: 8},
  });

  const stateStyles = Object.freeze({
    active: {opacity: 1, pattern: 'solid', label: 'Active'},
    draft: {opacity: 0.72, pattern: 'dashed', label: 'Draft'},
    archived: {opacity: 0.4, pattern: 'faded', label: 'Archived'},
    superseded: {opacity: 0.48, pattern: 'struck', label: 'Superseded'},
    invalid: {opacity: 0.75, pattern: 'crosshatched', label: 'Invalid'},
    deleted: {opacity: 0.25, pattern: 'crossed', label: 'Deleted'},
  });

  const scopeStyles = Object.freeze({
    global: {color: '#8da2b8', glyph: '◎', label: 'Global'},
    personal: {color: '#cc83d6', glyph: '●', label: 'Personal'},
    project: {color: '#54b5cf', glyph: '◆', label: 'Project'},
    session: {color: '#e5a95d', glyph: '◒', label: 'Session'},
    environment: {color: '#78b981', glyph: '⬡', label: 'Environment'},
  });

  const verificationStyles = Object.freeze({
    verified: {color: '#42c77a', pattern: 'solid', glyph: '✓', label: 'Verified', width: 2.4},
    partially_verified: {color: '#e5b14d', pattern: 'dashed', glyph: '◐', label: 'Partially verified', width: 2.4},
    unverified: {color: '#9aa6b2', pattern: 'dotted', glyph: '?', label: 'Unverified', width: 2},
    disputed: {color: '#ef6461', pattern: 'double', glyph: '!', label: 'Disputed', width: 3},
  });

  const edgeStyles = Object.freeze({
    related_to: {label: 'Related to', color: '#8292a6', dash: 'solid', width: 1.2, arrow: 'forward', order: 10},
    part_of: {label: 'Part of', color: '#5f9ee8', dash: 'solid', width: 1.8, arrow: 'forward', order: 20},
    requires: {label: 'Requires', color: '#e89b48', dash: 'solid', width: 2.2, arrow: 'forward', order: 30},
    alternative_to: {label: 'Alternative to', color: '#ae78d1', dash: 'dashed', width: 1.6, arrow: 'forward', order: 40},
    applies_to: {label: 'Applies to', color: '#57afc9', dash: 'dotted', width: 1.5, arrow: 'forward', order: 50},
    supersedes: {label: 'Supersedes', color: '#efbd4e', dash: 'double', width: 2.5, arrow: 'forward', order: 60},
    contradicts: {label: 'Contradicts', color: '#ef6461', dash: 'warning', width: 2.8, arrow: 'forward', order: 70},
    caused_by: {label: 'Caused by', color: '#9a7bd1', dash: 'dashed', width: 1.7, arrow: 'forward', order: 80},
    supported_by: {label: 'Supported by', color: '#56b881', dash: 'solid', width: 1.8, arrow: 'forward', order: 90},
    derived_from: {label: 'Derived from', color: '#52adbd', dash: 'dotted', width: 1.6, arrow: 'forward', order: 100},
    default: {label: 'Relationship', color: '#7e8a98', dash: 'solid', width: 1.2, arrow: 'forward', order: 1000},
  });

  function safeKey(value) {
    return String(value || '').trim().toLowerCase();
  }

  function plainLabel(value, limit) {
    value = String(value || '')
      .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
      .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
      .replace(/<[^>]*>/g, ' ')
      .replace(/[`*_~#>|]/g, ' ')
      .replace(/\s+/g, ' ').trim();
    limit = Math.max(1, Number(limit) || 80);
    return value.length > limit ? value.slice(0, limit - 1).trimEnd() + '…' : value;
  }

  function kindStyle(attributes) {
    const key = `${safeKey(attributes && attributes.objectKind)}:${safeKey(attributes && attributes.semanticKind)}`;
    return {...(kindStyles[key] || kindStyles.default), key};
  }

  function nodeStyle(attributes, context) {
    attributes = attributes || {};
    context = context || {};
    const kind = kindStyle(attributes);
    const state = {...(stateStyles[safeKey(attributes.state)] || stateStyles.active)};
    const scope = {...(scopeStyles[safeKey(attributes.scopeKind)] || scopeStyles.global)};
    const verificationKey = safeKey(attributes.verification);
    const verification = verificationKey ? {...(verificationStyles[verificationKey] || verificationStyles.unverified)} : null;
    const selected = !!context.selected;
    const hovered = !!context.hovered;
    const searchHit = !!context.searchHit;
    const stale = !!context.stale;
    const pinned = !!attributes.pinned;
    const badges = [
      {kind: 'scope', glyph: scope.glyph, label: scope.label, color: scope.color},
      verification && {kind: 'verification', glyph: verification.glyph, label: verification.label, color: verification.color},
      state.pattern !== 'solid' && {kind: 'state', glyph: state.pattern === 'dashed' ? '◌' : '×', label: state.label, color: '#aab4bf'},
      pinned && {kind: 'layout', glyph: '⌖', label: 'Pinned locally', color: '#73d7f2'},
    ].filter(Boolean);
    const borderColor = stale ? '#f16f6f' : selected ? '#f5fbff' : verification ? verification.color : scope.color;
    const borderPattern = stale ? 'warning' : selected ? 'selected' : verification ? verification.pattern : state.pattern;
    const label = plainLabel(attributes.title || attributes.objectID || 'Untitled knowledge');
    const size = kind.size + (selected ? 3 : hovered ? 1.5 : searchHit ? 1 : 0);
    const opacity = Math.max(0.2, state.opacity * (context.hidden ? 0.2 : 1));
    const ariaParts = [label, kind.key.replace(':', ' '), scope.label, state.label];
    if (verification) ariaParts.push(verification.label);
    if (stale) ariaParts.push('Stale');
    if (pinned) ariaParts.push('Pinned locally');
    return {
      kind: kind.key, shape: kind.shape, glyph: kind.glyph, color: kind.color, size, opacity,
      label, scopeColor: scope.color, borderColor, borderWidth: selected ? 3.5 : verification ? verification.width : 1.7,
      borderPattern, statePattern: state.pattern, badges, selected, hovered, searchHit, stale, pinned,
      ariaLabel: ariaParts.join(', '),
      sigma: {
        label, color: kind.color, size, borderColor, borderSize: selected ? 0.22 : 0.14,
        forceLabel: selected || hovered || searchHit, highlighted: selected || hovered, hidden: !!context.hidden,
        zIndex: selected ? 4 : hovered ? 3 : searchHit ? 2 : 1,
      },
    };
  }

  function styledNodeAttributes(attributes, context) {
    const style = nodeStyle(attributes, context);
    return {...attributes, ...style.sigma, knowledgeStyle: style};
  }

  function edgeStyle(attributes, context) {
    attributes = attributes || {};
    context = context || {};
    const kind = safeKey(attributes.relationshipKind);
    const base = edgeStyles[kind] || edgeStyles.default;
    const archived = safeKey(attributes.state) === 'archived';
    const selected = !!context.selected;
    const hovered = !!context.hovered;
    const hidden = !!context.hidden;
    const width = base.width + (selected ? 2 : hovered ? 1 : 0);
    const opacity = hidden ? 0 : archived ? 0.3 : context.dimmed ? 0.22 : 0.82;
    return {
      kind: kind || 'unspecified', label: base.label, color: base.color, dash: archived ? 'faded' : base.dash,
      width, arrow: base.arrow, opacity, selected, hovered, archived,
      ariaLabel: `${plainLabel(attributes.label || base.label)}, ${base.label}${archived ? ', Archived' : ''}`,
      sigma: {
        label: plainLabel(attributes.label || ''), color: base.color, size: width,
        type: base.arrow === 'forward' ? 'arrow' : 'line', hidden,
        zIndex: selected ? 4 : hovered ? 3 : kind === 'contradicts' ? 2 : 1,
      },
    };
  }

  function styledEdgeAttributes(attributes, context) {
    const style = edgeStyle(attributes, context);
    return {...attributes, ...style.sigma, knowledgeStyle: style};
  }

  function legendForEdges(edges, options) {
    options = options || {};
    const enabled = options.enabledKinds ? new Set([...options.enabledKinds].map(safeKey)) : null;
    const counts = new Map();
    for (const edge of edges || []) {
      const attributes = edge && edge.attributes || edge || {};
      const kind = safeKey(attributes.relationshipKind) || 'unspecified';
      if (enabled && !enabled.has(kind)) continue;
      counts.set(kind, (counts.get(kind) || 0) + 1);
    }
    return [...counts].map(([kind, count]) => {
      const style = edgeStyles[kind] || edgeStyles.default;
      return {kind, label: style.label, color: style.color, dash: style.dash, width: style.width, arrow: style.arrow, count, order: style.order};
    }).sort((left, right) => left.order - right.order || left.label.localeCompare(right.label));
  }

  function renderLegend(container, entries) {
    if (!container) return;
    container.replaceChildren();
    for (const entry of entries || []) {
      const item = container.ownerDocument.createElement('span');
      item.className = 'knowledge-legend-item';
      item.title = `${entry.label} (${entry.count})`;
      const swatch = container.ownerDocument.createElement('span');
      swatch.className = `knowledge-legend-line is-${entry.dash}`;
      swatch.style.setProperty('--knowledge-edge-color', entry.color);
      const label = container.ownerDocument.createElement('span');
      label.textContent = `${entry.label} · ${entry.count}`;
      item.append(swatch, label);
      container.appendChild(item);
    }
    container.hidden = !container.childElementCount;
  }

  return Object.freeze({
    kindStyles, stateStyles, scopeStyles, verificationStyles, edgeStyles,
    plainLabel, kindStyle, nodeStyle, styledNodeAttributes, edgeStyle, styledEdgeAttributes, legendForEdges, renderLegend,
  });
});
