(function () {
  'use strict';

  function escapeHTML(value) {
    return String(value || '').replace(/[&<>"']/g, ch => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[ch]));
  }

  function sanitizeHTML(html) {
    if (!window.DOMPurify) return html;
    return DOMPurify.sanitize(html, {
      ADD_ATTR: ['class'],
      FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed', 'form', 'input', 'button', 'foreignObject']
    });
  }

  function sanitizeMermaidSVG(html) {
    if (!window.DOMPurify) return html;
    return DOMPurify.sanitize(html, {
      ADD_TAGS: ['foreignobject'],
      ADD_ATTR: ['dominant-baseline'],
      HTML_INTEGRATION_POINTS: {foreignobject: true},
      FORBID_CONTENTS: ['script', 'iframe', 'object', 'embed', 'form', 'input', 'button'],
      FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'form', 'input', 'button']
    });
  }

  function highlightMarkdownCode(html) {
    if (!window.hljs || !html) return html;
    const template = document.createElement('template');
    template.innerHTML = html;
    template.content.querySelectorAll('pre code').forEach(code => {
      const raw = code.textContent || '';
      const langMatch = String(code.className || '').match(/(?:^|\s)language-([A-Za-z0-9_+-]+)/);
      try {
        const highlighted = langMatch && hljs.getLanguage(langMatch[1])
          ? hljs.highlight(raw, {language: langMatch[1], ignoreIllegals: true}).value
          : hljs.highlightAuto(raw).value;
        code.innerHTML = highlighted;
        code.classList.add('hljs');
      } catch (_) {
        code.textContent = raw;
      }
    });
    return template.innerHTML;
  }

  // Rendered diagrams by theme and source, so a re-rendered transcript row
  // shows a diagram at once, and a streaming diagram can keep showing its
  // last rendered version while the next one renders.
  const mermaidResults = new Map();
  const mermaidResultLimit = 60;
  const streamingRenderInterval = 500;
  let lastStreamingRender = 0;

  function mermaidResultKey(source) {
    return (window.koderMermaidTheme || '') + '\n' + source;
  }

  function rememberMermaidResult(source, result) {
    const key = mermaidResultKey(source);
    mermaidResults.delete(key);
    mermaidResults.set(key, result);
    if (mermaidResults.size > mermaidResultLimit) mermaidResults.delete(mermaidResults.keys().next().value);
  }

  // latestStreamingSVG finds the newest rendered SVG for an earlier version
  // of a diagram that is still streaming in: its base, the source before
  // blocks were closed, is a prefix of base.
  function latestStreamingSVG(base) {
    const prefix = (window.koderMermaidTheme || '') + '\n';
    let best = null;
    let bestLength = 0;
    for (const [key, result] of mermaidResults) {
      if (!result.svg || !key.startsWith(prefix)) continue;
      const earlier = result.base ?? key.slice(prefix.length);
      if (earlier.length > bestLength && base.startsWith(earlier)) {
        best = result.svg;
        bestLength = earlier.length;
      }
    }
    return best;
  }

  // streamingMermaidSource makes a diagram that is still streaming in
  // renderable: it drops the unfinished last line and closes blocks that
  // were opened but not yet ended.
  function streamingMermaidBase(source) {
    // A code block's text ends in a newline even when its last line is
    // still arriving, so that newline does not mark the line complete.
    const text = String(source || '').replace(/\n$/, '');
    const cut = text.lastIndexOf('\n');
    return (cut >= 0 ? text.slice(0, cut) : '').trim();
  }

  function streamingMermaidSource(source) {
    const lines = streamingMermaidBase(source).split('\n');
    // Flowcharts and sequence diagrams close blocks with "end"; state, class
    // and ER diagrams with "}". Braces inside a line, such as a {decision}
    // node, balance on that line.
    const closers = [];
    const opener = /^\s*(subgraph|loop|alt|opt|par|critical|break|rect|box)\b/;
    for (const line of lines) {
      if (opener.test(line)) closers.push('end');
      else if (/^\s*end\s*$/.test(line) && closers.at(-1) === 'end') closers.pop();
      const unquoted = line.replace(/"[^"]*"/g, '');
      for (const ch of unquoted) {
        if (ch === '{') closers.push('}');
        else if (ch === '}' && closers.at(-1) === '}') closers.pop();
      }
    }
    while (closers.length) lines.push(closers.pop());
    return lines.join('\n').trim();
  }

  function diagramContentHTML(svg) {
    return '<div class="mermaid-diagram-content">' + sanitizeMermaidSVG(svg) + '</div>' + diagramExpandButton();
  }

  // renderMermaidPlaceholders turns mermaid code blocks into diagram slots.
  // With options.streaming the last block may still be arriving
  // (options.partialLast). The slots hold no SVG, because the HTML is
  // sanitized afterwards; fillKnownMermaidDiagrams fills them once they are in
  // the page, and renderMermaidDiagrams renders the rest.
  function renderMermaidPlaceholders(html, options = {}) {
    const template = document.createElement('template');
    template.innerHTML = html;
    const codes = Array.from(template.content.querySelectorAll('pre > code')).filter(code =>
      /(^|\s)language-mermaid(\s|$)/.test(String(code.className || '').toLowerCase()));
    codes.forEach((code, index) => {
      let source = (code.textContent || '').trim();
      let base = source;
      const partial = !!options.streaming && !!options.partialLast && index === codes.length - 1;
      if (partial) {
        base = streamingMermaidBase(code.textContent || '');
        source = streamingMermaidSource(code.textContent || '');
      }
      // The sources travel as text, not attributes: the sanitizer drops any
      // attribute holding "-->", which nearly every diagram does.
      // adoptMermaidSources moves them into attributes once in the page.
      const diagram = document.createElement('div');
      diagram.className = 'mermaid-diagram';
      diagram.dataset.mermaidState = 'pending';
      const pre = document.createElement('pre');
      pre.className = 'mermaid-source';
      pre.textContent = source;
      if (options.streaming) {
        diagram.dataset.mermaidStreaming = 'true';
        diagram.innerHTML = '<div class="diagram-stream-placeholder">Mermaid diagram: drawing…</div>';
        pre.classList.add('mermaid-hidden-source');
        const basePre = document.createElement('pre');
        basePre.className = 'mermaid-base mermaid-hidden-source';
        basePre.textContent = base;
        diagram.append(pre, basePre);
      } else {
        diagram.appendChild(pre);
      }
      code.closest('pre').replaceWith(diagram);
    });
    return template.innerHTML;
  }

  function diagramExpandButton() {
    return '<button type="button" class="media-expand-button" title="Expand Mermaid diagram"><i class="bi bi-arrows-angle-expand"></i></button>';
  }

  function repairMermaidSource(source) {
    const text = String(source || '');
    if (!/^\s*(?:flowchart|graph)\b/im.test(text)) return text;
    return text.replace(/^(\s*subgraph\s+)([^"\n\[]*[^\w\s"\n\[][^"\n\[]*)$/gm, (match, head, title) =>
      head + '"' + title.trim() + '"').replace(/\b([A-Za-z_][A-Za-z0-9_-]*)\[([^\]\n]+)\]/g, (match, id, label) => {
      const trimmed = label.trim();
      if (!/[()]/.test(trimmed) || /^['"]/.test(trimmed) || (/^\(/.test(trimmed) && /\)$/.test(trimmed))) return match;
      return id + '["' + trimmed.replaceAll('"', '&quot;') + '"]';
    });
  }

  function mermaidErrorHTML(err, source) {
    const message = String(err?.message || err || 'Unknown Mermaid error').trim().slice(0, 2000);
    return '<div class="mermaid-error"><strong>Mermaid render failed</strong>' +
      (message ? '<pre class="mermaid-error-detail">' + escapeHTML(message) + '</pre>' : '') +
      '</div><pre>' + escapeHTML(source) + '</pre>';
  }

  function removeMermaidErrorArtifact(id) {
    document.getElementById('d' + id)?.remove();
  }

  async function renderMermaidSource(id, source) {
    try {
      return await mermaid.render(id, source);
    } catch (originalError) {
      removeMermaidErrorArtifact(id);
      const repaired = repairMermaidSource(source);
      if (repaired === source) throw originalError;
      try {
        return await mermaid.render(id + '-repaired', repaired);
      } catch (_) {
        removeMermaidErrorArtifact(id + '-repaired');
        throw originalError;
      }
    }
  }

  // fillKnownMermaidDiagrams fills slots under root from earlier renders
  // without waiting: a slot whose source has rendered is done, and a
  // streaming slot shows the newest render of an earlier version of itself.
  function adoptMermaidSources(diagram) {
    if (diagram.dataset.mermaidSource !== undefined) return;
    const source = diagram.querySelector(':scope > .mermaid-source');
    const base = diagram.querySelector(':scope > .mermaid-base');
    diagram.dataset.mermaidSource = (source ? source.textContent : diagram.textContent || '').trim();
    if (base) diagram.dataset.mermaidBase = base.textContent;
    diagram.querySelectorAll(':scope > .mermaid-hidden-source').forEach(node => node.remove());
  }

  function fillKnownMermaidDiagrams(root) {
    if (!root) return;
    root.querySelectorAll('.mermaid-diagram[data-mermaid-state="pending"]').forEach(diagram => {
      adoptMermaidSources(diagram);
      const source = diagram.dataset.mermaidSource || '';
      const known = mermaidResults.get(mermaidResultKey(source));
      if (known?.svg) {
        diagram.innerHTML = diagramContentHTML(known.svg);
        diagram.dataset.mermaidState = 'done';
        return;
      }
      if (diagram.dataset.mermaidStreaming !== 'true') return;
      const earlier = latestStreamingSVG(diagram.dataset.mermaidBase ?? source);
      if (earlier) diagram.innerHTML = diagramContentHTML(earlier);
    });
  }

  // renderMermaidDiagrams renders pending diagram slots under root. Streaming
  // slots render at most every 500 ms; options.onThrottled(delay) is called
  // when some were left for later. A streaming slot that fails to render keeps
  // its last version: a half-written diagram often does not parse yet.
  async function renderMermaidDiagrams(root, options = {}) {
    if (!root || !window.mermaid) return;
    if (options.configure) options.configure();
    const diagrams = root.querySelectorAll('.mermaid-diagram[data-mermaid-state="pending"]');
    let throttled = false;
    for (const diagram of diagrams) {
      adoptMermaidSources(diagram);
      const streaming = diagram.dataset.mermaidStreaming === 'true';
      const source = diagram.dataset.mermaidSource || '';
      if (!source) continue;
      const known = mermaidResults.get(mermaidResultKey(source));
      if (known?.svg) {
        diagram.innerHTML = diagramContentHTML(known.svg);
        diagram.dataset.mermaidState = 'done';
        continue;
      }
      if (streaming && known?.error) continue;
      if (streaming) {
        const wait = lastStreamingRender + streamingRenderInterval - Date.now();
        if (wait > 0) {
          throttled = true;
          continue;
        }
        lastStreamingRender = Date.now();
      }
      diagram.dataset.mermaidSource = source;
      diagram.dataset.mermaidState = 'rendering';
      const id = (options.idPrefix || 'mermaid') + '-' + Math.random().toString(36).slice(2);
      try {
        const result = await renderMermaidSource(id, source);
        rememberMermaidResult(source, {svg: result.svg || '', base: diagram.dataset.mermaidBase});
        if (!diagram.isConnected) continue;
        diagram.innerHTML = diagramContentHTML(result.svg || '');
        diagram.dataset.mermaidState = 'done';
        if (result.bindFunctions) result.bindFunctions(diagram);
      } catch (err) {
        if (streaming) {
          rememberMermaidResult(source, {error: true});
          if (diagram.isConnected) diagram.dataset.mermaidState = 'stale';
          continue;
        }
        if (!diagram.isConnected) continue;
        diagram.dataset.mermaidState = 'error';
        diagram.innerHTML = options.errorHTML
          ? options.errorHTML(err, source)
          : mermaidErrorHTML(err, source);
      }
    }
    if (throttled && options.onThrottled) {
      options.onThrottled(Math.max(0, lastStreamingRender + streamingRenderInterval - Date.now()));
    }
  }

  window.KoderMarkdownRuntime = Object.freeze({
    escapeHTML,
    sanitizeHTML,
    sanitizeMermaidSVG,
    highlightMarkdownCode,
    renderMermaidPlaceholders,
    renderMermaidDiagrams,
    fillKnownMermaidDiagrams,
    repairMermaidSource,
  });
})();
