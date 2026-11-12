// file_changes.js marks what changed in a file: inserted and deleted words
// and blocks in rendered Markdown, and added and removed lines in source.
// It also finds the most recent edit, so the page can scroll to it.
(function () {
  'use strict';

  // diff returns runs {op, a0, a1, b0, b1} that turn a into b, where op is
  // '=' (same), '-' (only in a) or '+' (only in b). It returns null when
  // more than maxEdits elements differ, to bound the work.
  function diff(a, b, maxEdits = 1500) {
    let start = 0;
    while (start < a.length && start < b.length && a[start] === b[start]) start++;
    let endA = a.length;
    let endB = b.length;
    while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
      endA--;
      endB--;
    }
    const middle = myers(a.slice(start, endA), b.slice(start, endB), maxEdits);
    if (!middle) return null;
    const runs = [];
    if (start) runs.push({op: '=', a0: 0, a1: start, b0: 0, b1: start});
    for (const run of middle) runs.push({op: run.op, a0: run.a0 + start, a1: run.a1 + start, b0: run.b0 + start, b1: run.b1 + start});
    if (endA < a.length) runs.push({op: '=', a0: endA, a1: a.length, b0: endB, b1: b.length});
    return mergeRuns(runs);
  }

  // myers is the O((N+M)D) shortest edit script of Myers (1986).
  function myers(a, b, maxEdits) {
    const n = a.length;
    const m = b.length;
    if (!n && !m) return [];
    if (!n) return [{op: '+', a0: 0, a1: 0, b0: 0, b1: m}];
    if (!m) return [{op: '-', a0: 0, a1: n, b0: 0, b1: 0}];
    const max = Math.min(n + m, maxEdits);
    const offset = max + 1;
    const v = new Int32Array(2 * max + 3);
    const trace = [];
    for (let d = 0; d <= max; d++) {
      // The state before step d, for k in [-d-1, d+1], at index k + d + 1.
      trace.push(v.slice(offset - d - 1, offset + d + 2));
      for (let k = -d; k <= d; k += 2) {
        let x = (k === -d || (k !== d && v[offset + k - 1] < v[offset + k + 1])) ? v[offset + k + 1] : v[offset + k - 1] + 1;
        let y = x - k;
        while (x < n && y < m && a[x] === b[y]) {
          x++;
          y++;
        }
        v[offset + k] = x;
        if (x >= n && y >= m) return backtrack(trace, d, n, m);
      }
    }
    return null;
  }

  function backtrack(trace, last, n, m) {
    const runs = [];
    let x = n;
    let y = m;
    for (let d = last; d > 0; d--) {
      const before = trace[d];
      const at = k => before[k + d + 1];
      const k = x - y;
      const down = k === -d || (k !== d && at(k - 1) < at(k + 1));
      const prevK = down ? k + 1 : k - 1;
      const prevX = at(prevK);
      const prevY = prevX - prevK;
      const snakeX = down ? prevX : prevX + 1;
      if (x > snakeX) runs.push({op: '=', a0: snakeX, a1: x, b0: snakeX - k, b1: y});
      if (down) runs.push({op: '+', a0: prevX, a1: prevX, b0: prevY, b1: prevY + 1});
      else runs.push({op: '-', a0: prevX, a1: prevX + 1, b0: prevY, b1: prevY});
      x = prevX;
      y = prevY;
    }
    if (x > 0) runs.push({op: '=', a0: 0, a1: x, b0: 0, b1: y});
    return runs.reverse();
  }

  function mergeRuns(runs) {
    const out = [];
    for (const run of runs) {
      const last = out[out.length - 1];
      if (last && last.op === run.op && last.a1 === run.a0 && last.b1 === run.b0) {
        last.a1 = run.a1;
        last.b1 = run.b1;
      } else if (run.a1 > run.a0 || run.b1 > run.b0) {
        out.push({...run});
      }
    }
    return out;
  }

  // firstChange is the index in b where the first difference starts.
  function firstChange(runs, length) {
    const run = (runs || []).find(item => item.op !== '=');
    if (!run) return -1;
    return Math.min(run.b0, Math.max(0, length - 1));
  }

  function tokenize(text) {
    return String(text || '').match(/\s+|[\p{L}\p{N}_]+|[^\s\p{L}\p{N}_]/gu) || [];
  }

  function escapeHTML(value) {
    return String(value ?? '').replace(/[&<>"']/g, ch => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[ch]));
  }

  function elementBlocks(html) {
    const template = document.createElement('template');
    template.innerHTML = html;
    return {root: template.content, blocks: Array.from(template.content.children)};
  }

  function ghostOf(element) {
    const ghost = element.cloneNode(true);
    ghost.removeAttribute('id');
    ghost.querySelectorAll('[id]').forEach(node => node.removeAttribute('id'));
    ghost.classList.add('change-removed');
    ghost.setAttribute('aria-hidden', 'true');
    return ghost;
  }

  function textNodes(element) {
    const nodes = [];
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) nodes.push(walker.currentNode);
    return nodes;
  }

  // markWords marks inserted and deleted words of an edited block in place.
  // It returns false when most of the block changed, which reads better as a
  // removed block followed by an added one.
  function markWords(oldElement, newElement) {
    const oldTokens = tokenize(oldElement.textContent);
    const nodes = textNodes(newElement);
    const newTokens = [];
    const owners = [];
    nodes.forEach((node, index) => {
      for (const token of tokenize(node.nodeValue)) {
        newTokens.push(token);
        owners.push(index);
      }
    });
    if (!nodes.length) return false;
    const runs = diff(oldTokens, newTokens, 600);
    if (!runs) return false;
    const words = tokens => tokens.filter(token => token.trim()).length;
    let changed = 0;
    for (const run of runs) {
      if (run.op === '-') changed += words(oldTokens.slice(run.a0, run.a1));
      if (run.op === '+') changed += words(newTokens.slice(run.b0, run.b1));
    }
    if (changed > 0.6 * Math.max(words(oldTokens), words(newTokens), 1)) return false;
    const inserted = new Array(newTokens.length).fill(false);
    const deletedBefore = new Map();
    for (const run of runs) {
      if (run.op === '+') for (let i = run.b0; i < run.b1; i++) inserted[i] = !!newTokens[i].trim();
      if (run.op === '-') {
        const text = oldTokens.slice(run.a0, run.a1).join('');
        if (text.trim()) deletedBefore.set(run.b0, (deletedBefore.get(run.b0) || '') + text);
      }
    }
    let index = 0;
    nodes.forEach((node, nodeIndex) => {
      const fragment = document.createDocumentFragment();
      let insertion = null;
      const flush = () => { insertion = null; };
      while (index < newTokens.length && owners[index] === nodeIndex) {
        if (deletedBefore.has(index)) {
          flush();
          const del = document.createElement('del');
          del.className = 'change-del';
          del.textContent = deletedBefore.get(index);
          fragment.append(del);
        }
        if (inserted[index]) {
          if (!insertion) {
            insertion = document.createElement('ins');
            insertion.className = 'change-ins';
            fragment.append(insertion);
          }
          insertion.append(newTokens[index]);
        } else if (insertion && !newTokens[index].trim() && inserted[index + 1] && owners[index + 1] === nodeIndex) {
          insertion.append(newTokens[index]); // keep "two words" in one mark
        } else {
          flush();
          fragment.append(newTokens[index]);
        }
        index++;
      }
      if (nodeIndex === nodes.length - 1 && deletedBefore.has(newTokens.length)) {
        const del = document.createElement('del');
        del.className = 'change-del';
        del.textContent = deletedBefore.get(newTokens.length);
        fragment.append(del);
      }
      node.replaceWith(fragment);
    });
    newElement.classList.add('change-edited');
    return true;
  }

  // decoratePreview marks rendered Markdown. baseHTML is the version marks
  // compare against; prevHTML is the version shown before the latest edit,
  // whose first difference gets data-latest-change. Either may be null.
  function decoratePreview(newHTML, options = {}) {
    const current = elementBlocks(newHTML);
    const keys = current.blocks.map(block => block.outerHTML);
    if (options.prevHTML != null && options.prevHTML !== newHTML) {
      const before = elementBlocks(options.prevHTML).blocks.map(block => block.outerHTML);
      const at = firstChange(diff(before, keys), keys.length);
      if (at >= 0 && current.blocks[at]) current.blocks[at].setAttribute('data-latest-change', '');
    }
    if (options.marks && options.baseHTML != null && options.baseHTML !== newHTML) {
      const base = elementBlocks(options.baseHTML).blocks;
      const runs = diff(base.map(block => block.outerHTML), keys);
      if (runs) {
        for (let i = 0; i < runs.length;) {
          if (runs[i].op === '=') {
            i++;
            continue;
          }
          const removed = [];
          const added = [];
          while (i < runs.length && runs[i].op !== '=') {
            const run = runs[i++];
            for (let j = run.a0; j < run.a1; j++) removed.push(base[j]);
            for (let j = run.b0; j < run.b1; j++) added.push(current.blocks[j]);
          }
          const anchorIndex = i < runs.length ? runs[i].b0 : keys.length;
          const pairs = Math.min(removed.length, added.length);
          for (let j = 0; j < pairs; j++) {
            if (removed[j].tagName === added[j].tagName && markWords(removed[j], added[j])) continue;
            added[j].before(ghostOf(removed[j]));
            added[j].classList.add('change-added');
          }
          for (let j = pairs; j < added.length; j++) added[j].classList.add('change-added');
          const anchor = current.blocks[anchorIndex];
          for (let j = pairs; j < removed.length; j++) {
            if (anchor) anchor.before(ghostOf(removed[j]));
            else current.root.append(ghostOf(removed[j]));
          }
        }
      }
    }
    const holder = document.createElement('div');
    holder.append(current.root);
    return holder.innerHTML;
  }

  // splitHighlightedLines splits highlight.js output into lines, closing
  // spans at each line end and opening them again on the next line.
  function splitHighlightedLines(html) {
    const lines = [];
    const open = [];
    let line = '';
    const pattern = /(<span[^>]*>)|(<\/span>)|(\n)|([^<\n]+|<)/g;
    let match;
    while ((match = pattern.exec(html))) {
      if (match[1]) {
        open.push(match[1]);
        line += match[1];
      } else if (match[2]) {
        open.pop();
        line += match[2];
      } else if (match[3]) {
        lines.push(line + '</span>'.repeat(open.length));
        line = open.join('');
      } else {
        line += match[4];
      }
    }
    lines.push(line);
    return lines;
  }

  // decorateSource renders source text one span per line, marking added
  // lines and showing removed lines struck through where they were.
  function decorateSource(text, highlightedHTML, options = {}) {
    const lines = String(text || '').split('\n');
    let html = splitHighlightedLines(highlightedHTML);
    if (html.length !== lines.length) html = lines.map(escapeHTML);
    let latest = -1;
    if (options.prevText != null && options.prevText !== text) latest = firstChange(diff(String(options.prevText).split('\n'), lines), lines.length);
    const classes = new Array(lines.length).fill('');
    const removedBefore = new Map();
    if (options.marks && options.baseText != null && options.baseText !== text) {
      const base = String(options.baseText).split('\n');
      for (const run of diff(base, lines) || []) {
        if (run.op === '+') for (let i = run.b0; i < run.b1; i++) classes[i] = ' change-added';
        if (run.op === '-') removedBefore.set(run.b0, (removedBefore.get(run.b0) || []).concat(base.slice(run.a0, run.a1)));
      }
    }
    const out = [];
    const ghosts = at => {
      for (const line of removedBefore.get(at) || []) out.push('<span class="code-line change-removed" aria-hidden="true">' + escapeHTML(line) + '</span>');
    };
    for (let i = 0; i < lines.length; i++) {
      ghosts(i);
      out.push('<span class="code-line' + classes[i] + '"' + (i === latest ? ' data-latest-change' : '') + '>' + html[i] + '</span>');
    }
    ghosts(lines.length);
    return out.join('');
  }

  window.KoderFileChanges = {diff, tokenize, decoratePreview, decorateSource, splitHighlightedLines};
})();
