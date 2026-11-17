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

  // Image sizes. Markdown itself has none; these are the two common
  // extensions: ![alt](src =WxH) from Typora and markdown-it-imsize (=40x,
  // =x40 and =40 too), and ![alt](src){width=40 height=30} from Pandoc.
  // Raw <img width="40"> works as in HTML.
  const imageLink = String.raw`!\[((?:\\[\s\S]|[^\\\]])*)\]\(\s*(<[^>\n]*>|[^\s()]+)(?:\s+("(?:\\[\s\S]|[^"\\])*"|'(?:\\[\s\S]|[^'\\])*'))?`;
  const sizeToken = String.raw`(\d+(?:\.\d+)?(?:px|%|em|rem|vw)?)`;
  const typoraImage = new RegExp('^' + imageLink + String.raw`\s+=` + sizeToken + '?(?:x' + sizeToken + String.raw`?)?\s*\)`);
  const pandocImage = new RegExp('^' + imageLink + String.raw`\s*\)\{([^{}\n]*)\}`);

  function unescapeMarkdown(text) {
    return String(text || '').replace(/\\([!-/:-@[-`{-~])/g, '$1');
  }

  function sizedImageToken(raw, alt, src, title, width, height, classes) {
    if (!width && !height) return undefined;
    return {
      type: 'sizedImage',
      raw,
      alt: unescapeMarkdown(alt),
      src: unescapeMarkdown(String(src).replace(/^<|>$/g, '')),
      title: title ? unescapeMarkdown(title.slice(1, -1)) : '',
      width: width || '',
      height: height || '',
      classes: classes || [],
    };
  }

  // sizeAttribute writes a plain or px length as an attribute and any other
  // unit as a style, since width="50%" is not valid on img.
  function sizeAttribute(name, value) {
    if (!value) return '';
    const plain = /^(\d+(?:\.\d+)?)(px)?$/.exec(value);
    return plain ? ' ' + name + '="' + plain[1] + '"' : ' style="' + name + ':' + escapeHTML(value) + '"';
  }

  const sizedImageExtension = {
    name: 'sizedImage',
    level: 'inline',
    start(src) {
      const at = src.indexOf('![');
      return at < 0 ? undefined : at;
    },
    tokenizer(src) {
      let match = typoraImage.exec(src);
      if (match) return sizedImageToken(match[0], match[1], match[2], match[3], match[4], match[5]);
      match = pandocImage.exec(src);
      if (!match) return undefined;
      let width = '';
      let height = '';
      const classes = [];
      for (const part of match[4].matchAll(/\.([\w-]+)|([\w-]+)=("[^"]*"|'[^']*'|[^\s}]+)/g)) {
        if (part[1]) classes.push(part[1]);
        const value = String(part[3] || '').replace(/^["']|["']$/g, '');
        if (part[2] === 'width') width = value;
        if (part[2] === 'height') height = value;
      }
      return sizedImageToken(match[0], match[1], match[2], match[3], width, height, classes);
    },
    renderer(token) {
      const width = sizeAttribute('width', token.width);
      const height = sizeAttribute('height', token.height);
      // Two style attributes would collide; merge them.
      const styles = [width, height].filter(attr => attr.startsWith(' style=')).map(attr => attr.slice(8, -1));
      const attrs = [width, height].filter(attr => !attr.startsWith(' style=')).join('') + (styles.length ? ' style="' + styles.join(';') + '"' : '');
      return '<img src="' + escapeHTML(token.src) + '" alt="' + escapeHTML(token.alt) + '"' +
        (token.title ? ' title="' + escapeHTML(token.title) + '"' : '') + attrs +
        (token.classes.length ? ' class="' + escapeHTML(token.classes.join(' ')) + '"' : '') + '>';
    },
  };
  if (window.marked?.use) marked.use({extensions: [sizedImageExtension]});

  function normalizePath(parts) {
    const stack = [];
    for (const part of parts) {
      if (!part || part === '.') continue;
      if (part === '..') {
        if (!stack.length) return null;
        stack.pop();
      } else {
        stack.push(part);
      }
    }
    return stack.join('/');
  }

  // resolveImageSource maps an image src to a URL the page can load, and
  // the file it shows. Links with a scheme (https:, data:) stay as they are.
  // Paths inside the project load through context.fileURL; other absolute
  // paths and file:// URLs load through /api/show-image, which serves image
  // files only. Relative paths are relative to context.basePath's folder.
  function resolveImageSource(src, context = {}) {
    let value = String(src || '').trim();
    if (!value || value.startsWith('#') || value.startsWith('//')) return null;
    if (/^file:\/\//i.test(value)) value = value.replace(/^file:\/\/(localhost)?/i, '');
    else if (/^[a-z][a-z0-9+.-]*:/i.test(value)) return null;
    value = value.split('#')[0].split('?')[0].replace(/\\/g, '/');
    try { value = decodeURIComponent(value); } catch (_) {}
    if (!value) return null;
    const root = String(context.projectRoot || '').replace(/\/+$/, '');
    const fileURL = context.fileURL;
    const showImage = path => ({url: '/api/show-image?path=' + encodeURIComponent(path), path});
    if (value.startsWith('/')) {
      if (root && fileURL && value.startsWith(root + '/')) {
        const rel = normalizePath(value.slice(root.length + 1).split('/'));
        if (rel) return {url: fileURL(rel), path: rel};
      }
      if (fileURL && context.isRootRelative?.(value)) {
        const rel = normalizePath(value.split('/'));
        if (rel) return {url: fileURL(rel), path: rel};
      }
      return showImage(value);
    }
    const base = String(context.basePath || '').replace(/\\/g, '/');
    const dir = base.includes('/') ? base.slice(0, base.lastIndexOf('/')) : '';
    const rel = normalizePath((dir ? dir + '/' + value : value).split('/'));
    if (rel !== null && rel !== '' && fileURL) return {url: fileURL(rel), path: rel};
    if (root) {
      const absolute = normalizePath((root + '/' + (dir ? dir + '/' : '') + value).split('/'));
      if (absolute) return showImage('/' + absolute);
    }
    return null;
  }

  // sizeImage applies an img's height attribute. Stylesheets give images
  // height:auto so a width keeps their shape, which also overrides the
  // height attribute; a height given alone, or with a width, needs a style.
  function sizeImage(img) {
    const width = img.getAttribute('width');
    const height = img.getAttribute('height');
    if (!height || !/^\d+(?:\.\d+)?$/.test(height)) return;
    img.style.height = height + 'px';
    if (width && /^\d+(?:\.\d+)?$/.test(width)) {
      img.style.width = width + 'px';
      img.style.objectFit = 'contain';
    } else {
      img.style.width = 'auto';
    }
  }

  // rewriteImageSources points every img at a URL the page can load and
  // applies its size. It returns the HTML and calls decorate for extra
  // attributes each page wants.
  function rewriteImageSources(html, context = {}, decorate) {
    if (!html || !html.includes('<img')) return html;
    const template = document.createElement('template');
    template.innerHTML = html;
    template.content.querySelectorAll('img').forEach(img => {
      const resolved = resolveImageSource(img.getAttribute('src'), context);
      if (resolved) {
        img.setAttribute('src', resolved.url);
        img.setAttribute('data-source-path', resolved.path);
        img.removeAttribute('srcset');
        if (!img.getAttribute('title')) img.setAttribute('title', resolved.path);
      }
      img.setAttribute('loading', 'lazy');
      img.setAttribute('decoding', 'async');
      if (img.hasAttribute('width') || img.hasAttribute('height') || img.style.width || img.style.height) img.classList.add('markdown-sized-image');
      sizeImage(img);
      if (decorate) decorate(img, resolved);
    });
    return template.innerHTML;
  }

  window.KoderMarkdownRuntime = Object.freeze({
    resolveImageSource,
    rewriteImageSources,
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
