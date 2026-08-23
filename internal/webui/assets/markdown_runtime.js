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

  function renderMermaidPlaceholders(html) {
    const template = document.createElement('template');
    template.innerHTML = html;
    template.content.querySelectorAll('pre > code').forEach(code => {
      const lang = String(code.className || '').toLowerCase();
      if (!/(^|\s)language-mermaid(\s|$)/.test(lang)) return;
      const source = code.textContent || '';
      const diagram = document.createElement('div');
      diagram.className = 'mermaid-diagram';
      diagram.dataset.mermaidState = 'pending';
      const pre = document.createElement('pre');
      pre.textContent = source;
      diagram.appendChild(pre);
      code.closest('pre').replaceWith(diagram);
    });
    return template.innerHTML;
  }

  function diagramExpandButton() {
    return '<button type="button" class="media-expand-button" title="Expand Mermaid diagram"><i class="bi bi-arrows-angle-expand"></i></button>';
  }

  async function renderMermaidDiagrams(root, options = {}) {
    if (!root || !window.mermaid) return;
    if (options.configure) options.configure();
    const diagrams = root.querySelectorAll('.mermaid-diagram[data-mermaid-state="pending"]');
    for (const diagram of diagrams) {
      const source = (diagram.dataset.mermaidSource || diagram.textContent || '').trim();
      if (!source) continue;
      diagram.dataset.mermaidSource = source;
      diagram.dataset.mermaidState = 'rendering';
      const id = (options.idPrefix || 'mermaid') + '-' + Math.random().toString(36).slice(2);
      try {
        const result = await mermaid.render(id, source);
        if (!diagram.isConnected) continue;
        diagram.innerHTML = '<div class="mermaid-diagram-content">' + sanitizeMermaidSVG(result.svg || '') + '</div>' + diagramExpandButton();
        diagram.dataset.mermaidState = 'done';
        if (result.bindFunctions) result.bindFunctions(diagram);
      } catch (err) {
        if (!diagram.isConnected) continue;
        diagram.dataset.mermaidState = 'error';
        diagram.innerHTML = options.errorHTML
          ? options.errorHTML(err, source)
          : '<div class="mermaid-error">Mermaid render failed</div><pre>' + escapeHTML(source) + '</pre>';
      }
    }
  }

  window.KoderMarkdownRuntime = Object.freeze({
    escapeHTML,
    sanitizeHTML,
    sanitizeMermaidSVG,
    highlightMarkdownCode,
    renderMermaidPlaceholders,
    renderMermaidDiagrams,
  });
})();
