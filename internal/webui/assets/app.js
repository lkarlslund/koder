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
    function sanitizeDiagramSVG(html) {
      if (!window.DOMPurify) return html;
      return DOMPurify.sanitize(html, {
        USE_PROFILES: {svg: true, svgFilters: true},
        ADD_TAGS: ['style'],
        ADD_ATTR: ['class', 'style'],
        FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'form', 'input', 'button', 'foreignObject']
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
    function renderMarkdownDiffBlocks(html) {
      if (!html) return html;
      const template = document.createElement('template');
      template.innerHTML = html;
      template.content.querySelectorAll('pre code.language-diff').forEach(code => {
        const diff = code.textContent || '';
        const wrapper = document.createElement('div');
        wrapper.className = 'tool-result tool-diff-markdown';
        wrapper.innerHTML = renderDiffBlock('Diff', diff);
        const pre = code.closest('pre');
        if (pre) pre.replaceWith(wrapper);
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
    function isEscapedAt(text, index) {
      let slashes = 0;
      for (let i = index - 1; i >= 0 && text[i] === '\\'; i--) slashes++;
      return slashes % 2 === 1;
    }
    function findUnescaped(text, needle, start) {
      for (let i = start; i >= 0 && i < text.length; i++) {
        if (text.startsWith(needle, i) && !isEscapedAt(text, i)) return i;
      }
      return -1;
    }
    function mathTokenAt(text, index) {
      const starts = [
        {open: '$$', close: '$$', display: true},
        {open: '\\[', close: '\\]', display: true},
        {open: '\\(', close: '\\)', display: false},
        {open: '$', close: '$', display: false}
      ];
      for (const spec of starts) {
        if (!text.startsWith(spec.open, index) || isEscapedAt(text, index)) continue;
        const start = index + spec.open.length;
        if (spec.open === '$' && /\s/.test(text[start] || '')) continue;
        const end = findUnescaped(text, spec.close, start);
        if (end < 0) return null;
        const body = text.slice(start, end);
        if (!body.trim()) return null;
        if (spec.open === '$' && /\s/.test(body[body.length - 1] || '')) return null;
        return {start: index, end: end + spec.close.length, body, display: spec.display};
      }
      return null;
    }
    function renderMathHTML(source, displayMode) {
      if (!window.katex) return escapeHTML(source);
      try {
        return katex.renderToString(source, {
          displayMode,
          throwOnError: false,
          strict: 'ignore',
          trust: false,
          output: 'html'
        });
      } catch (_) {
        return escapeHTML(displayMode ? '$$' + source + '$$' : '$' + source + '$');
      }
    }
    function containsMarkdownMath(text) {
      const source = String(text || '');
      if (!/[\\$]/.test(source)) return false;
      for (let i = 0; i < source.length; i++) {
        if (mathTokenAt(source, i)) return true;
      }
      return false;
    }
    function renderMathTextNode(node) {
      const text = node.textContent || '';
      if (!/[\\$]/.test(text)) return;
      const fragment = document.createDocumentFragment();
      let offset = 0;
      let changed = false;
      while (offset < text.length) {
        let token = null;
        let tokenStart = -1;
        for (let i = offset; i < text.length; i++) {
          token = mathTokenAt(text, i);
          if (token) {
            tokenStart = i;
            break;
          }
        }
        if (!token) break;
        if (tokenStart > offset) fragment.append(document.createTextNode(text.slice(offset, tokenStart)));
        const span = document.createElement(token.display ? 'div' : 'span');
        span.className = token.display ? 'koder-math koder-math-display' : 'koder-math koder-math-inline';
        span.innerHTML = renderMathHTML(token.body, token.display);
        fragment.append(span);
        offset = token.end;
        changed = true;
      }
      if (!changed) return;
      if (offset < text.length) fragment.append(document.createTextNode(text.slice(offset)));
      node.replaceWith(fragment);
    }
    function renderMathInHTML(html) {
      if (!html || !window.katex || !containsMarkdownMath(html)) return html;
      const template = document.createElement('template');
      template.innerHTML = html;
      const ignored = 'pre, code, kbd, samp, script, style, .katex, .mermaid-diagram';
      const walker = document.createTreeWalker(template.content, NodeFilter.SHOW_TEXT, {
        acceptNode(node) {
          if (!/[\\$]/.test(node.textContent || '')) return NodeFilter.FILTER_REJECT;
          if (node.parentElement?.closest(ignored)) return NodeFilter.FILTER_REJECT;
          return NodeFilter.FILTER_ACCEPT;
        }
      });
      const nodes = [];
      while (walker.nextNode()) nodes.push(walker.currentNode);
      nodes.forEach(renderMathTextNode);
      return template.innerHTML;
    }
    function byteCount(value) {
      const source = String(value || '');
      if (window.TextEncoder) return new TextEncoder().encode(source).length;
      return unescape(encodeURIComponent(source)).length;
    }
    function formatByteCount(bytes) {
      const value = Number(bytes) || 0;
      if (value < 1024) return value + ' B';
      if (value < 1024 * 1024) return (value / 1024).toFixed(value < 10 * 1024 ? 1 : 0) + ' KB';
      return (value / (1024 * 1024)).toFixed(1) + ' MB';
    }
    function diagramPlaceholder(kind, source) {
      return '\n<div class="diagram-stream-placeholder">' + escapeHTML(kind) + ' received: ' + escapeHTML(formatByteCount(byteCount(source))) + '</div>\n';
    }
    function deferStreamingDiagrams(source) {
      let text = String(source || '');
      text = text.replace(/```mermaid[^\n]*\n[\s\S]*?```/gi, match => diagramPlaceholder('Mermaid diagram', match));
      text = text.replace(/```mermaid[\s\S]*$/i, match => diagramPlaceholder('Mermaid diagram', match));
      text = text.replace(/<svg\b[\s\S]*?<\/svg>/gi, match => diagramPlaceholder('SVG', match));
      text = text.replace(/<svg\b[\s\S]*$/i, match => diagramPlaceholder('SVG', match));
      return text;
    }
    function configureMermaid() {
      if (!window.mermaid) return;
      const dark = document.documentElement.getAttribute('data-bs-theme') !== 'light';
      const theme = dark ? 'koder-dark' : 'koder-light';
      if (window.koderMermaidTheme === theme) return;
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'strict',
        theme: 'base',
        themeVariables: koderMermaidThemeVariables(dark),
        themeCSS: koderMermaidThemeCSS(dark),
        flowchart: {htmlLabels: true, curve: 'basis', useMaxWidth: true}
      });
      window.koderMermaidTheme = theme;
    }
    function koderMermaidThemeVariables(dark) {
      const shared = {
        fontFamily: 'var(--bs-font-sans-serif)',
        fontSize: '16px',
        labelTextColor: dark ? '#f8f9fa' : '#212529',
        nodeTextColor: dark ? '#f8f9fa' : '#212529',
        titleColor: dark ? '#f8f9fa' : '#212529',
        darkTextColor: dark ? '#f8f9fa' : '#212529',
      };
      if (dark) {
        return {...shared,
          background: '#212529',
          mainBkg: '#26313d',
          secondBkg: '#1f2a35',
          tertiaryColor: '#303844',
          primaryColor: '#26313d',
          primaryTextColor: '#f8f9fa',
          primaryBorderColor: '#8bb9fe',
          secondaryColor: '#1f2a35',
          secondaryTextColor: '#f8f9fa',
          secondaryBorderColor: '#7aa2d6',
          tertiaryTextColor: '#f8f9fa',
          tertiaryBorderColor: '#91a7c5',
          textColor: '#f8f9fa',
          lineColor: '#c6d3e1',
          edgeLabelBackground: '#212529',
          clusterBkg: '#161b22',
          clusterBorder: '#8bb9fe',
          clusterTextColor: '#f8f9fa',
          noteBkgColor: '#4a3900',
          noteTextColor: '#fff3cd',
          noteBorderColor: '#ffda6a',
          actorBkg: '#26313d',
          actorBorder: '#8bb9fe',
          actorTextColor: '#f8f9fa',
          signalColor: '#f8f9fa',
          signalTextColor: '#f8f9fa',
          labelBoxBkgColor: '#212529',
          labelBoxBorderColor: '#8bb9fe',
          labelTextColor: '#f8f9fa',
        };
      }
      return {...shared,
        background: '#ffffff',
        mainBkg: '#ffffff',
        secondBkg: '#f6f8fa',
        tertiaryColor: '#e9f2ff',
        primaryColor: '#ffffff',
        primaryTextColor: '#212529',
        primaryBorderColor: '#0d6efd',
        secondaryColor: '#f6f8fa',
        secondaryTextColor: '#212529',
        secondaryBorderColor: '#6c8ebf',
        tertiaryTextColor: '#212529',
        tertiaryBorderColor: '#7aa2d6',
        textColor: '#212529',
        lineColor: '#495057',
        edgeLabelBackground: '#ffffff',
        clusterBkg: '#f8f9fa',
        clusterBorder: '#0d6efd',
        clusterTextColor: '#212529',
        noteBkgColor: '#fff3cd',
        noteTextColor: '#212529',
        noteBorderColor: '#d39e00',
        actorBkg: '#ffffff',
        actorBorder: '#0d6efd',
        actorTextColor: '#212529',
        signalColor: '#212529',
        signalTextColor: '#212529',
        labelBoxBkgColor: '#ffffff',
        labelBoxBorderColor: '#0d6efd',
        labelTextColor: '#212529',
      };
    }
    function koderMermaidThemeCSS(dark) {
      const colors = dark ? {
        canvas: '#212529',
        panel: '#2b3035',
        panelAlt: '#343a40',
        panelWarm: '#4a3900',
        border: '#8ab4f8',
        borderAlt: '#66d9ef',
        text: '#f8f9fa',
        muted: '#c6d3e1',
        noteText: '#fff3cd',
        danger: '#ff8ba0',
        success: '#75d79f',
      } : {
        canvas: '#ffffff',
        panel: '#ffffff',
        panelAlt: '#f1f5f9',
        panelWarm: '#fff3cd',
        border: '#0d6efd',
        borderAlt: '#0aa2c0',
        text: '#212529',
        muted: '#495057',
        noteText: '#212529',
        danger: '#dc3545',
        success: '#198754',
      };
      return `
        .label, .label text, .nodeLabel, .edgeLabel, .edgeLabel p, .cluster-label, .cluster-label text,
        .nodeLabel p, .nodeLabel div, .nodeLabel span, .cluster-label p, .cluster-label div, .cluster-label span,
        .actor, .actor-line, .messageText, .loopText, .noteText, .taskText, .sectionTitle,
        .legend, .legend text, text {
          fill: ${colors.text} !important;
          color: ${colors.text} !important;
          font-size: 16px !important;
        }
        .node rect, .node circle, .node ellipse, .node polygon, .node path,
        .flowchart-label, .label-container, .actor, .state, .er.entityBox {
          fill: ${colors.panel} !important;
          stroke: ${colors.border} !important;
          color: ${colors.text} !important;
        }
        .cluster rect, .section, .grid .tick, .timeline-section {
          fill: ${colors.panelAlt} !important;
          stroke: ${colors.border} !important;
        }
        .edgeLabel, .edgeLabel rect, .labelBkg, .labelBox {
          background-color: ${colors.canvas} !important;
          fill: ${colors.canvas} !important;
          color: ${colors.text} !important;
        }
        .edgePath .path, .flowchart-link, .messageLine0, .messageLine1, .transition,
        .relationshipLine, .commit-id, line, path.path {
          stroke: ${colors.muted} !important;
        }
        marker path, marker polygon {
          fill: ${colors.muted} !important;
          stroke: ${colors.muted} !important;
        }
        .note, .note rect {
          fill: ${colors.panelWarm} !important;
          stroke: ${colors.borderAlt} !important;
        }
        .noteText, .noteText tspan {
          fill: ${colors.noteText} !important;
          color: ${colors.noteText} !important;
        }
        .activation0, .activation1, .activation2 {
          fill: ${colors.panelAlt} !important;
          stroke: ${colors.borderAlt} !important;
        }
        .today, .done0, .done1 {
          fill: ${colors.success} !important;
          stroke: ${colors.success} !important;
        }
        .crit0, .crit1, .active0, .active1 {
          fill: ${colors.danger} !important;
          stroke: ${colors.danger} !important;
        }
      `;
    }
    function diagramExpandButton(title) {
      return '<button type="button" class="media-expand-button" title="Expand ' + escapeHTML(title) + '"><i class="bi bi-arrows-angle-expand"></i></button>';
    }
    async function renderMermaidIn(root) {
      if (!root || !window.mermaid) return;
      configureMermaid();
      const diagrams = root.querySelectorAll('.mermaid-diagram[data-mermaid-state="pending"]');
      for (const diagram of diagrams) {
        const source = (diagram.dataset.mermaidSource || diagram.textContent || '').trim();
        if (!source) continue;
        diagram.dataset.mermaidSource = source;
        diagram.dataset.mermaidState = 'rendering';
        const id = 'mermaid-' + Math.random().toString(36).slice(2);
        try {
          const result = await mermaid.render(id, source);
          diagram.innerHTML = '<div class="mermaid-diagram-content">' + sanitizeMermaidSVG(result.svg || '') + '</div>' + diagramExpandButton('Mermaid diagram');
          diagram.dataset.mermaidState = 'done';
          if (result.bindFunctions) result.bindFunctions(diagram);
        } catch (err) {
          diagram.dataset.mermaidState = 'error';
          diagram.innerHTML = '<div class="mermaid-error">Mermaid render failed</div><pre>' + escapeHTML(source) + '</pre>';
        }
      }
    }
    function markMermaidThemeDirty(root) {
      if (!root) return;
      root.querySelectorAll('.mermaid-diagram[data-mermaid-state="done"]').forEach(diagram => {
        const source = diagram.dataset.mermaidSource || '';
        if (!source) return;
        diagram.dataset.mermaidState = 'pending';
        diagram.innerHTML = '<pre>' + escapeHTML(source) + '</pre>';
      });
    }
    const markdownCache = new Map();
    const timelineMarkdownCache = new Map();
    function markdownCacheKey(source, options) {
      return (options.deferDiagrams ? 'defer' : 'full') + ':' + source;
    }
    function markdownOptionsKey(options) {
      return (options.deferDiagrams ? 'defer' : 'full') + ':' + (options.incremental ? 'incremental' : 'static');
    }
    function cachedMarkdown(source, options, render) {
      const key = markdownCacheKey(source, options);
      if (markdownCache.has(key)) return markdownCache.get(key);
      const html = render();
      markdownCache.set(key, html);
      if (markdownCache.size > 120) markdownCache.delete(markdownCache.keys().next().value);
      return html;
    }
    function markdownCacheSize() {
      return markdownCache.size + timelineMarkdownCache.size;
    }
    function timelineMarkdownCacheKey(item, options) {
      const id = String(item?.id || item?.ID || '').trim();
      if (!id) return '';
      return id + ':' + markdownOptionsKey(options || {});
    }
    function renderTimelineMarkdown(item, text, options = {}) {
      const source = String(text || '');
      if (options.incremental) return renderMarkdown(source, options);
      const key = timelineMarkdownCacheKey(item, options);
      if (!key) return renderMarkdown(source, options);
      const cached = timelineMarkdownCache.get(key);
      if (cached && cached.source === source) return cached.html;
      const html = renderMarkdown(source, options);
      timelineMarkdownCache.set(key, {source, html});
      if (timelineMarkdownCache.size > 240) timelineMarkdownCache.delete(timelineMarkdownCache.keys().next().value);
      return html;
    }
    function websocketPayloadBytes(value) {
      if (typeof value === 'string') return new Blob([value]).size;
      if (value instanceof ArrayBuffer) return value.byteLength;
      if (ArrayBuffer.isView(value)) return value.byteLength;
      if (value instanceof Blob) return value.size;
      return 0;
    }
    const transcriptTailWindowSize = 120;
    const transcriptWindowOverscan = 30;
    const estimatedTimelineItemHeight = 160;
    const timelineStore = new Map();
    function renderMarkdown(text, options = {}) {
      const source = options.deferDiagrams ? deferStreamingDiagrams(text) : String(text || '');
      if (!source.trim()) return '';
      return cachedMarkdown(source, options, () => {
        if (!window.marked) return '<pre>' + escapeHTML(source) + '</pre>';
        marked.setOptions({gfm: true, breaks: false});
        let html = marked.parse(source);
        html = sanitizeHTML(html);
        if (!options.deferDiagrams) html = renderMermaidPlaceholders(html);
        html = renderMathInHTML(html);
        html = highlightMarkdownCode(html);
        html = renderMarkdownDiffBlocks(html);
        return sanitizeHTML(html);
      });
    }
    function stableMarkdownPrefixLength(source) {
      const text = String(source || '');
      let inFence = false;
      let offset = 0;
      let stable = 0;
      const lines = text.split(/(\n)/);
      for (let i = 0; i < lines.length; i += 2) {
        const line = lines[i] || '';
        const newline = lines[i + 1] || '';
        if (/^\s*```/.test(line)) inFence = !inFence;
        offset += line.length + newline.length;
        if (!inFence && line.trim() === '' && newline) stable = offset;
      }
      return stable;
    }
    function renderMarkdownIntoElement(el, text, options = {}) {
      if (!el) return;
      const source = String(text || '');
      const key = JSON.stringify({deferDiagrams: !!options.deferDiagrams, incremental: !!options.incremental});
      if (el._koderMarkdownSource === source && el._koderMarkdownOptions === key) return;
      el._koderMarkdownSource = source;
      el._koderMarkdownOptions = key;
      if (!options.incremental || !source.trim()) {
        el.innerHTML = renderMarkdown(source, options);
        el._koderMarkdownStable = '';
        return;
      }
      let stableNode = el.querySelector(':scope > [data-markdown-stable]');
      let tailNode = el.querySelector(':scope > [data-markdown-tail]');
      if (!stableNode || !tailNode) {
        el.textContent = '';
        stableNode = document.createElement('div');
        stableNode.dataset.markdownStable = 'true';
        tailNode = document.createElement('div');
        tailNode.dataset.markdownTail = 'true';
        el.append(stableNode, tailNode);
      }
      const stableLen = stableMarkdownPrefixLength(source);
      const stableSource = source.slice(0, stableLen);
      const tailSource = source.slice(stableLen);
      if (el._koderMarkdownStable !== stableSource) {
        stableNode.innerHTML = renderMarkdown(stableSource, options);
        el._koderMarkdownStable = stableSource;
      }
      tailNode.innerHTML = renderMarkdown(tailSource, options);
    }
    function renderTimelineMarkdownIntoElement(el, item, text, options = {}) {
      if (!el) return;
      if (options.incremental) {
        renderMarkdownIntoElement(el, text, options);
        return;
      }
      const source = String(text || '');
      const key = timelineMarkdownCacheKey(item, options) || 'inline:' + markdownOptionsKey(options);
      if (el._koderMarkdownSource === source && el._koderMarkdownOptions === key) return;
      el._koderMarkdownSource = source;
      el._koderMarkdownOptions = key;
      el.innerHTML = renderTimelineMarkdown(item, source, options);
      el._koderMarkdownStable = '';
    }
    function firstValue(obj, names) {
      if (!obj) return '';
      for (const name of names) {
        const value = obj[name];
        if (value !== undefined && value !== null && value !== '') return value;
      }
      return '';
    }
    function splitLines(text) {
      const source = String(text || '').replace(/\r\n/g, '\n').replace(/\r/g, '\n');
      if (!source) return [];
      const lines = source.split('\n');
      if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
      return lines;
    }
    function compactLines(lines, head = 2, tail = 2) {
      const clean = Array.isArray(lines) ? lines.map(line => String(line ?? '')) : splitLines(lines);
      if (clean.length <= head + tail + 1) return clean.map(text => ({text}));
      return [
        ...clean.slice(0, head).map(text => ({text})),
        {text: '... ' + (clean.length - head - tail) + ' lines omitted ...', omitted: true},
        ...clean.slice(clean.length - tail).map(text => ({text}))
      ];
    }
    function toolData(tool) {
      return (tool && tool.result && (tool.result.data || tool.result.Data)) || {};
    }
    function toolArgs(tool) {
      return (tool && (tool.args || tool.Args)) || {};
    }
    function toolResultText(tool) {
      const result = (tool && tool.result) || {};
      return firstValue(result, ['text', 'Text']) || firstValue(result, ['diff', 'Diff']);
    }
    function toolErrorText(tool) {
      const err = (tool && (tool.error || tool.Error)) || {};
      return firstValue(err, ['message', 'Message', 'code', 'Code']);
    }
    function toolStatus(tool) {
      return String((tool && (tool.status || tool.Status)) || '').toLowerCase();
    }
    function normalizedToolStatus(tool) {
      const status = toolStatus(tool);
      return status === 'completed' ? 'done' : status;
    }
    function toolExitCode(tool) {
      const data = toolData(tool);
      const direct = firstValue(data, ['exit_code', 'ExitCode']);
      if (direct !== '') return direct;
      const text = [toolErrorText(tool), toolResultText(tool)].join('\n');
      const match = text.match(/exit status\s+(-?\d+)/i) || text.match(/exit code\s+(-?\d+)/i);
      return match ? match[1] : '';
    }
    function toolStatusBadgeText(tool) {
      const kind = String((tool && tool.tool) || '');
      const exitCode = toolExitCode(tool);
      if ((kind === 'bash' || kind.startsWith('exec_')) && exitCode !== '') return 'exit ' + exitCode;
      return normalizedToolStatus(tool);
    }
    function toolStatusBadgeClassName(tool) {
      const status = normalizedToolStatus(tool);
      if (status === 'done') return 'tool-status-badge-done';
      if (status === 'running') return 'tool-status-badge-running';
      if (status === 'awaiting_approval') return 'tool-status-badge-awaiting';
      if (status === 'errored' || status === 'error' || status === 'failed' || status === 'denied') return 'tool-status-badge-error';
      if (status === 'canceled' || status === 'cancelled') return 'tool-status-badge-canceled';
      return 'tool-status-badge-pending';
    }
    function isBareExitStatus(text) {
      return /^bash failed:\s*exit status\s+-?\d+\s*$/i.test(String(text || '').trim());
    }
    function noticeLevel(content) {
      return String(firstValue(content, ['level', 'Level']) || 'info').toLowerCase();
    }
    function noticeIcon(content) {
      const level = noticeLevel(content);
      const kind = String(firstValue(content, ['kind', 'Kind'])).toLowerCase();
      if (kind === 'interrupted') return 'bi-stop-circle';
      if (level === 'warning') return 'bi-exclamation-triangle';
      if (level === 'error' || level === 'danger') return 'bi-x-circle';
      return 'bi-info-circle';
    }
    function noticeText(content) {
      return firstValue(content, ['title', 'Title', 'text', 'Text', 'kind', 'Kind']) || 'Notice';
    }
    function noticeReasonText(reason) {
      switch (String(reason || '').toLowerCase()) {
        case 'user_interrupted': return 'user interrupted';
        case 'process_terminating': return 'process terminating';
        case 'process_restart': return 'process restarting';
        default: return String(reason || '');
      }
    }
    function noticeDetail(content) {
      const parts = [];
      for (const key of ['subtitle', 'Subtitle']) {
        const value = content && content[key];
        if (value) parts.push(String(value));
      }
      const reason = noticeReasonText(firstValue(content, ['reason', 'Reason']));
      if (reason) parts.push(reason);
      return parts.join(' · ');
    }
    function lintText(content) {
      return firstValue(content, ['text', 'Text']) || '';
    }
    function lintFiles(content) {
      const files = firstValue(content, ['files', 'Files']);
      return Array.isArray(files) ? files.filter(Boolean).join(', ') : '';
    }
    function userMessageContent(item) {
      return (item && (item.content || item.Content)) || {};
    }
    function userMessageSourceValue(item) {
      const content = userMessageContent(item);
      const source = String(firstValue(content, ['source', 'Source'])).trim().toLowerCase();
      if (source) return source;
      const text = String(firstValue(content, ['text', 'Text'])).trim();
      if (text.startsWith('The previous turn was interrupted because the koder process was restarting.')) return 'auto_resume';
      if (text.startsWith('A tool call was interrupted by the process restart and has been marked failed.')) return 'auto_resume';
      return 'user';
    }
    function userMessageSourceLabelText(item) {
      switch (userMessageSourceValue(item)) {
        case 'steer': return 'steer';
        case 'queued': return 'queued';
        case 'rejected_steer': return 'rejected steer';
        case 'auto_generated': return 'auto-generated';
        case 'auto_resume': return 'auto-resume';
        case 'subchat': return 'subchat';
        case 'turn_instruction': return 'turn instruction';
        default: return 'user';
      }
    }
    function userMessageSourceQualifierText(item) {
      const label = userMessageSourceLabelText(item);
      return label === 'user' ? '' : label;
    }
    function userMessageIconClass(item) {
      switch (userMessageSourceValue(item)) {
        case 'steer': return 'bi-emoji-smile';
        case 'queued': return 'bi-person-lines-fill';
        case 'rejected_steer': return 'bi-person-exclamation';
        case 'auto_generated': return 'bi-stars';
        case 'auto_resume': return 'bi-arrow-clockwise';
        case 'subchat': return 'bi-diagram-3';
        case 'turn_instruction': return 'bi-signpost-split';
        default: return 'bi-person-circle';
      }
    }
    function toolResultHeader(title) {
      return '<div class="tool-result-header">' + escapeHTML(title) + '</div>';
    }
    function renderCompactBlock(title, lines, extraClass = '') {
      const body = compactLines(lines).map(line => {
        const cls = line.omitted ? 'tool-result-line tool-result-omitted' : 'tool-result-line';
        return '<div class="' + cls + '" title="' + escapeHTML(line.text || '') + '">' + escapeHTML(line.text || ' ') + '</div>';
      }).join('');
      const bodyClass = ['tool-result-body', extraClass].filter(Boolean).join(' ');
      return toolResultHeader(title) + '<div class="' + bodyClass + '">' + body + '</div>';
    }
    function renderKeyValueBlock(title, pairs) {
      const lines = pairs.filter(pair => pair[1] !== undefined && pair[1] !== null && String(pair[1]) !== '').map(pair => pair[0] + ': ' + pair[1]);
      return renderCompactBlock(title, lines);
    }
    function renderDiffBlock(title, diff) {
      const rows = diffRows(diff).map(row => {
        const cls = 'tool-diff-line ' + row.cls;
        return '<div class="' + cls + '" title="' + escapeHTML(row.text || '') + '">' +
          '<span class="tool-diff-line-no">' + escapeHTML(row.oldLine || '') + '</span>' +
          '<span class="tool-diff-line-no">' + escapeHTML(row.newLine || '') + '</span>' +
          '<span class="tool-diff-text">' + escapeHTML(row.text || ' ') + '</span>' +
          '</div>';
      }).join('');
      return toolResultHeader(title) + '<div>' + (rows || '<div class="tool-result-body text-secondary">No diff</div>') + '</div>';
    }
    function diffRows(diff) {
      let oldLine = null;
      let newLine = null;
      const rows = [];
      splitLines(diff).forEach(line => {
        const hunk = parseDiffHunk(line);
        if (hunk) {
          oldLine = hunk.oldStart;
          newLine = hunk.newStart;
          return;
        }
        if (skipDiffFileLine(line)) return;
        const cls = diffLineClass(line);
        const row = {cls, text: line, oldLine: '', newLine: ''};
        if (oldLine !== null && newLine !== null) {
          if (line.startsWith('+') && !line.startsWith('+++')) {
            row.newLine = String(newLine);
            newLine++;
          } else if (line.startsWith('-') && !line.startsWith('---')) {
            row.oldLine = String(oldLine);
            oldLine++;
          } else {
            row.oldLine = String(oldLine);
            row.newLine = String(newLine);
            oldLine++;
            newLine++;
          }
        }
        rows.push(row);
      });
      return rows;
    }
    function parseDiffHunk(line) {
      const match = String(line || '').match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      if (!match) return null;
      return {oldStart: Number(match[1]), newStart: Number(match[2])};
    }
    function skipDiffFileLine(line) {
      const text = String(line || '');
      return text.startsWith('--- ') || text.startsWith('+++ ');
    }
    function diffLineClass(line) {
      const text = String(line || '');
      if (text.startsWith('diff --git ')) return 'tool-diff-file';
      if (text.startsWith('index ') || text.startsWith('new file mode ') || text.startsWith('deleted file mode ') || text.startsWith('similarity index ') || text.startsWith('rename from ') || text.startsWith('rename to ')) return 'tool-diff-index';
      if (text.startsWith('+') && !text.startsWith('+++')) return 'tool-diff-add';
      if (text.startsWith('-') && !text.startsWith('---')) return 'tool-diff-del';
      if (text === '') return 'tool-diff-empty';
      return 'tool-diff-context';
    }
    function imageResultSource(data) {
      const path = firstValue(data, ['path', 'Path']);
      const sourcePath = firstValue(data, ['source_path', 'SourcePath']) || path;
      if (!sourcePath) return {path, sourcePath: '', src: ''};
      return {path, sourcePath, src: '/api/show-image?path=' + encodeURIComponent(sourcePath)};
    }
    function renderImagePreviewBlock(title, data, fallbackText, compact) {
      const image = imageResultSource(data);
      const mime = firstValue(data, ['mime_type', 'MIMEType']);
      const detail = firstValue(data, ['detail', 'Detail']);
      const summary = firstValue(data, ['summary', 'Summary']) || fallbackText || title;
      const meta = [image.path, mime, detail].filter(Boolean).join(' · ');
      if (!image.src) return renderCompactBlock(title, summary);
      const imageClass = compact ? 'tool-image-thumb-img' : 'tool-image-large-img';
      return toolResultHeader(summary || title) +
        '<div class="tool-result-body tool-image-result">' +
          '<button type="button" class="tool-image-preview ' + (compact ? 'tool-image-thumb' : 'tool-image-large') + '" data-lightbox-src="' + escapeHTML(image.src) + '" data-lightbox-title="' + escapeHTML(summary || title) + '" data-lightbox-meta="' + escapeHTML(meta) + '" title="Open image preview">' +
            '<img class="' + imageClass + '" alt="' + escapeHTML(image.path || title) + '" src="' + escapeHTML(image.src) + '">' +
            '<span class="tool-image-zoom"><i class="bi bi-arrows-fullscreen"></i></span>' +
          '</button>' +
          (meta ? '<div class="small text-secondary mt-2">' + escapeHTML(meta) + '</div>' : '') +
        '</div>';
    }
    function renderShowImageBlock(data, fallbackText) {
      return renderImagePreviewBlock('Showed image', data, fallbackText, false);
    }
    function readRangeLabel(args, data) {
      const requestedStart = firstValue(args, ['start_line', 'StartLine']);
      const requestedEnd = firstValue(args, ['end_line', 'EndLine']);
      if (!requestedStart && !requestedEnd) return '';
      const start = requestedStart || firstValue(data, ['start_line', 'StartLine', 'start', 'Start']);
      const end = requestedEnd || firstValue(data, ['end_line', 'EndLine', 'end', 'End']);
      if (start && end) return 'lines ' + start + '-' + end;
      if (start) return 'from line ' + start;
      if (end) return 'through line ' + end;
      return '';
    }
    function readTitle(path, args, data) {
      const base = path ? 'Read ' + path : 'Read';
      const range = readRangeLabel(args, data);
      return range ? base + ', ' + range : base;
    }
    function chatSendMessage(args) {
      return String(firstValue(args || {}, ['message', 'Message']) || '').trim();
    }
    function compactCommandLabel(command) {
      const text = String(command || '').replace(/\s+/g, ' ').trim();
      if (!text) return '';
      const max = 96;
      return text.length > max ? text.slice(0, max - 1) + '…' : text;
    }
    function toolTitleText(tool) {
      const kind = String((tool && tool.tool) || '');
      const data = toolData(tool);
      const args = toolArgs(tool);
      const path = firstValue(data, ['path', 'Path']) || firstValue(args, ['path']);
      const command = firstValue(data, ['command', 'Command']) || firstValue(args, ['command', 'cmd']);
      const comment = firstValue(data, ['comment', 'Comment']) || firstValue(args, ['comment']);
      switch (kind) {
        case 'file_read': return readTitle(path, args, data);
        case 'file_write': return path ? 'Write ' + path : 'Write file';
        case 'file_edit': return path ? 'Edit ' + path : 'Edit file';
        case 'bash': {
          if ((toolStatus(tool) === 'done' || toolStatus(tool) === 'errored') && command) return 'Ran ' + command;
          return 'Run command';
        }
        case 'exec_command': {
          const label = comment || compactCommandLabel(command);
          return label ? 'Start exec ' + label : 'Start exec';
        }
        case 'exec_status': return 'Exec status';
        case 'exec_list': return 'Exec sessions';
        case 'exec_write_stdin': return 'Write exec stdin';
        case 'exec_resize': return 'Resize exec';
        case 'exec_terminate': return 'Terminate exec';
        case 'exec_cleanup_background': return 'Clean exec sessions';
        case 'file_grep': return 'Search ' + (firstValue(data, ['pattern', 'Pattern']) || firstValue(args, ['pattern']));
        case 'file_glob': return 'Glob ' + (firstValue(data, ['pattern', 'Pattern']) || firstValue(args, ['pattern']));
        case 'webfetch': return 'Fetch ' + (firstValue(data, ['url', 'URL']) || firstValue(args, ['url']));
        case 'websearch': return 'Search web ' + (firstValue(data, ['query', 'Query']) || firstValue(args, ['query']));
        case 'show_image': return path ? 'Show image ' + path : 'Show image';
        case 'chat_send': return 'Message chat ' + (firstValue(args, ['chat_id', 'ChatID']) || '');
        default: return kind || 'Tool';
      }
    }
    function toolPreviewText(tool) {
      const args = toolArgs(tool);
      if (String((tool && tool.tool) || '') === 'file_read') return '';
      if (String((tool && tool.tool) || '') === 'bash' && (toolStatus(tool) === 'done' || toolStatus(tool) === 'errored')) return '';
      if (String((tool && tool.tool) || '') === 'chat_send') return chatSendMessage(args);
      if (String((tool && tool.tool) || '') === 'exec_command' && args.comment) return '';
      const values = [];
      if (args.command) values.push(compactCommandLabel(args.command));
      if (args.cmd) values.push(compactCommandLabel(args.cmd));
      if (args.process_id) values.push('process_id=' + args.process_id);
      const timeout = timeoutLabel(args.timeout_ms || args.yield_time_ms);
      if (timeout) values.push((args.yield_time_ms ? 'wait=' : 'timeout=') + timeout);
      for (const key of ['path', 'pattern', 'query', 'url', 'include']) {
        if (args[key]) values.push(key + '=' + args[key]);
      }
      return values.slice(0, 2).join('  ');
    }
    function execResultLines(data, fallback) {
      const output = firstValue(data, ['output', 'Output']);
      if (output) return output;
      const lines = [];
      const processID = firstValue(data, ['process_id', 'ProcessID']);
      const command = firstValue(data, ['command', 'Command']);
      const timeout = timeoutLabel(firstValue(data, ['timeout_ms', 'TimeoutMS']));
      const state = firstValue(data, ['state', 'State']);
      const exitCode = firstValue(data, ['exit_code', 'ExitCode']);
      if (processID) lines.push('process_id: ' + processID);
      if (command) lines.push('command: ' + command);
      if (timeout) lines.push('timeout: ' + timeout);
      if (state) lines.push('state: ' + state);
      if (exitCode !== '') lines.push('exit_code: ' + exitCode);
      return lines.length ? lines : fallback;
    }
    function execStartResultLines(data) {
      const output = firstValue(data, ['output', 'Output']);
      if (!output) return [];
      const lines = splitLines(output);
      if (lines.length <= 1) return lines;
      return [lines[0], '... ' + (lines.length - 1) + ' lines omitted ...'];
    }
    function renderExecStartResult(data) {
      const lines = execStartResultLines(data);
      if (!lines.length) return '';
      const body = lines.map((text, idx) => {
        const cls = idx > 0 && String(text).startsWith('... ') ? 'tool-result-line tool-result-omitted' : 'tool-result-line';
        return '<div class="' + cls + '" title="' + escapeHTML(text || '') + '">' + escapeHTML(text || ' ') + '</div>';
      }).join('');
      return '<div class="tool-result-body tool-result-body-mono">' + body + '</div>';
    }
    function timeoutLabel(value) {
      const ms = Number(value || 0);
      if (!Number.isFinite(ms) || ms <= 0) return '';
      if (ms < 1000) return String(ms) + 'ms';
      if (ms % 60000 === 0) return String(ms / 60000) + 'm';
      if (ms % 1000 === 0) return String(ms / 1000) + 's';
      return String(ms) + 'ms';
    }
    function renderToolResult(tool) {
      const kind = String((tool && tool.tool) || '');
      const result = (tool && tool.result) || {};
      const data = toolData(tool);
      const args = toolArgs(tool);
      const status = firstValue(result, ['status', 'Status']);
      if (status === 'error' || status === 'denied') return renderCompactBlock(status, toolResultText(tool));
      if (kind === 'file_write') {
        const path = firstValue(data, ['path', 'Path']) || firstValue(args, ['path']) || 'file';
        const content = firstValue(data, ['content', 'Content']);
        const summary = firstValue(data, ['summary', 'Summary']) || toolResultText(tool);
        const diagnostics = firstValue(data, ['diagnostics', 'Diagnostics']);
        const body = content ? renderCompactBlock(summary || ('Wrote ' + path), content) : renderCompactBlock('Wrote ' + path, summary);
        return body + (diagnostics ? renderCompactBlock('Diagnostics', diagnostics, 'tool-result-body-mono') : '');
      }
      if (kind === 'file_edit') {
        const title = firstValue(data, ['summary', 'Summary']) || 'Edited file';
        const diff = firstValue(data, ['diff', 'Diff']) || firstValue(result, ['diff', 'Diff']) || toolResultText(tool);
        const diagnostics = firstValue(data, ['diagnostics', 'Diagnostics']);
        return renderDiffBlock(title, diff) + (diagnostics ? renderCompactBlock('Diagnostics', diagnostics, 'tool-result-body-mono') : '');
      }
      if (kind === 'file_read') {
        return '';
      }
      if (kind === 'bash') {
        return renderCompactBlock('Output', firstValue(data, ['output', 'Output']) || toolResultText(tool));
      }
      if (kind === 'exec_command') {
        return renderExecStartResult(data);
      }
      if (kind.startsWith('exec_')) {
        return renderCompactBlock('Result', execResultLines(data, toolResultText(tool)), 'tool-result-body-mono');
      }
      if (kind === 'file_glob') return renderCompactBlock('Matches', data.matches || data.Matches || toolResultText(tool));
      if (kind === 'file_grep') return renderCompactBlock('Matches', firstValue(data, ['output', 'Output']) || toolResultText(tool));
      if (kind === 'lint') {
        const title = firstValue(data, ['summary', 'Summary']) || 'Diagnostics';
        const diagnostics = firstValue(data, ['diagnostics', 'Diagnostics']) || toolResultText(tool);
        return renderCompactBlock(title, diagnostics || 'No diagnostics', 'tool-result-body-mono');
      }
      if (kind === 'webfetch') return renderCompactBlock(firstValue(data, ['final_url', 'FinalURL', 'url', 'URL']) || 'Fetched page', firstValue(data, ['body', 'Body']) || toolResultText(tool));
      if (kind === 'websearch') {
        const items = data.items || data.Items || [];
        return renderCompactBlock('Search results', items.length ? items.map((item, idx) => (idx + 1) + '. ' + (item.title || item.Title || item.url || item.URL || '')) : toolResultText(tool));
      }
      if (kind === 'chat_send') return renderCompactBlock('Sent message', chatSendMessage(args) || toolResultText(tool));
      if (kind === 'view_image') {
        return renderImagePreviewBlock('Viewed image', data, toolResultText(tool), true);
      }
      if (kind === 'show_image') return renderShowImageBlock(data, toolResultText(tool));
      return renderCompactBlock(kind || 'Tool result', toolResultText(tool));
    }
    function renderToolError(tool) {
      const kind = String((tool && tool.tool) || '');
      const data = toolData(tool);
      if (kind === 'bash') {
        const output = firstValue(data, ['output', 'Output']);
        const error = toolErrorText(tool);
        if (!output && isBareExitStatus(error)) return '';
        return renderCompactBlock('Output', output || error, 'tool-result-body-mono');
      }
      if (kind.startsWith('exec_')) {
        return renderCompactBlock('Result', execResultLines(data, toolErrorText(tool)), 'tool-result-body-mono');
      }
      return renderCompactBlock('Error', toolErrorText(tool));
    }
    function koderApp() {
      return {
        ws: null, reconnectTimer: null, connectWatchdog: null, websocketHealthTimer: null, lastWSMessageAt: 0, lastWSMessageBytes: 0, reconnectDelay: 150, reconnectProbe: null, nextID: 1, pending: {}, clientID: '', clientStateTimer: null, state: {}, connected: false, connecting: true, draft: '', showAccess: false, accessDraft: {},
        showModels: false, modelLoading: false, modelQuery: '', modelOptions: [], modelPickerTarget: null, modelSettingsDraft: null, modelSettingsSaving: false, modelSettingsStatus: '', modelSettingsStatusKind: 'secondary',
        showSettings: false, settingsLoading: false, settingsSaving: false, settingsTab: 'general', settings: null, settingsStatus: '', settingsStatusKind: 'secondary',
        showSessions: false, showSessionEditor: false, sessionEditorMode: 'create', sessionLoading: false, hydratingSession: {active: false, id: '', title: '', error: ''}, switchingChat: {active: false, id: '', title: '', startedAt: 0}, sessionState: {project_root: '', sessions: []}, sessionDraft: {id: '', title: '', projectRoot: '', createProjectRoot: false, missingProjectRoot: '', error: ''},
        providerState: {catalog: [], providers: [], drafts: {}}, showProviderEditor: false, providerDraft: null, providerHeadersText: '{}', providerModelOptions: [], providerStatus: '', providerStatusKind: 'secondary', providerTesting: false, providerSaving: false,
        showModelConfigEditor: false, modelConfigDraft: null, modelConfigExtraBodyOpen: false, modelConfigStatus: '', modelConfigStatusKind: 'secondary',
        showMCPEditor: false, mcpDraft: null, mcpHeadersText: '{}', mcpStatus: '', mcpStatusKind: 'secondary',
        timelineAction: {open: false, mode: '', itemID: '', itemLabel: '', forkTitle: '', busy: false, error: ''},
        toolCommandModal: {open: false, command: '', subtitle: '', meta: [], output: ''},
        imageLightbox: {open: false, kind: 'image', src: '', html: '', title: '', meta: '', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0},
        completion: {kind: '', query: '', start: 0, end: 0, items: [], selected: 0}, completionSeq: 0,
        theme: readPreference('theme', 'auto'), sidebarRatio: Number(readPreference('sidebarRatio', '0.22')), resizingSidebar: false, mobileSidebarOpen: false, restoreChatAttempted: false, composerInitialFocusDone: false, transcriptStickToBottom: true, transcriptProgrammaticScroll: false, transcriptUserScrollActive: false, transcriptUserScrollTimer: null, transcriptLastItemObserver: null, transcriptObservedLastItemID: '', transcriptObservedLastItemElement: null, transcriptObservedLastItemHeight: 0, scrollRestoreSeq: 0, timelineRenderWindow: {chatID: '', start: 0, end: 0, overscan: 0}, timelineRenderWindowPending: false, timelineItemHeights: {}, timelineAverageItemHeight: estimatedTimelineItemHeight, timelineLoading: {}, timelineLoadingAll: {}, expandedMilestones: {}, hiddenMilestoneStatuses: readHiddenMilestoneStatuses(), hiddenChatStatuses: readHiddenChatStatuses(), showAllExecProcesses: readPreference('showAllExecProcesses', 'false') === 'true', ttsEnabled: false, ttsSettings: {}, ttsTestText: 'Koder TTS test.', ttsTestBusy: false, ttsSpokenItems: {}, ttsAudio: null, execHover: {open: false, title: '', output: '', x: 0, y: 0}, interruptArmedChatID: '', dragChatID: '', dragQueueID: '', composerAttachments: [], activeComposerDraftKey: '', preserveComposerDraftDuringSend: false, composerSendMenuOpen: false, reasoningViews: {}, restartRequestPending: false, restartAcknowledged: false, restartHardRequested: false, restartAgeTick: Date.now(), restartAgeTimer: null, allowSessionURLSync: false, error: '', toast: '', toastTimer: null,
        init() {
          this.initializeRouteHydration();
          this.clampSidebarRatio();
          this.applyTheme();
          this.$watch('draft', () => this.writeComposerDraft());
          this.$watch('composerAttachments', () => this.writeComposerDraft());
          this.connect();
          window.addEventListener('resize', () => { this.resizeComposer(); if (!this.isMobileLayout()) this.mobileSidebarOpen = false; this.reportClientStateSoon(); });
          window.addEventListener('online', () => this.connectNow());
          window.addEventListener('focus', () => { this.connectNow(); this.reportClientStateSoon(); });
          window.addEventListener('blur', () => this.reportClientStateSoon());
          document.addEventListener('visibilitychange', () => { if (!document.hidden) this.connectNow(); this.reportClientStateSoon(); });
          document.addEventListener('click', event => this.handleMediaPreviewClick(event));
          document.addEventListener('keydown', event => this.handleGlobalKeydown(event));
          this.restartAgeTimer = setInterval(() => { this.restartAgeTick = Date.now(); }, 30000);
          this.websocketHealthTimer = setInterval(() => this.checkWebsocketHealth(), 5000);
          this.$nextTick(() => { this.resizeComposer(); this.updateTranscriptStickiness(); this.renderDiagrams(); this.observeLastTranscriptItem(); });
        },
        initializeRouteHydration() {
          const route = this.selectionFromLocation();
          if (!route.sessionID) return;
          this.hydratingSession = {active: true, id: route.sessionID, title: route.chatID ? 'Loading chat' : 'Loading session', error: ''};
        },
        handleGlobalKeydown(event) {
          if (!event || event.defaultPrevented || event.isComposing) return;
          if (event.key === 'Escape') {
            if (this.mobileSidebarOpen) {
              event.preventDefault();
              this.closeMobileSidebar();
              return;
            }
            if (this.modalOpenName()) return;
            if (!this.chatInterruptible()) return;
            event.preventDefault();
            this.interruptChat();
            return;
          }
          if (!this.shouldFocusComposerForKey(event)) return;
          event.preventDefault();
          this.focusComposerAndInsert(event.key);
        },
        shouldFocusComposerForKey(event) {
          if (!event || event.ctrlKey || event.metaKey || event.altKey) return false;
          if (event.key.length !== 1) return false;
          if (this.mobileSidebarOpen) return false;
          if (this.modalOpenName()) return false;
          if (this.textEntryActive()) return false;
          return !!this.$refs?.composerInput;
        },
        textEntryActive() {
          const el = document.activeElement;
          if (!el || el === document.body || el === document.documentElement) return false;
          if (el.isContentEditable) return true;
          const tag = String(el.tagName || '').toLowerCase();
          if (tag === 'textarea' || tag === 'select') return true;
          if (tag !== 'input') return false;
          const type = String(el.getAttribute('type') || 'text').toLowerCase();
          return !['button', 'checkbox', 'color', 'file', 'hidden', 'image', 'radio', 'range', 'reset', 'submit'].includes(type);
        },
        focusComposerAndInsert(text) {
          const el = this.$refs?.composerInput;
          if (!el) return;
          el.focus();
          this.insertComposerText(text);
        },
        handleMediaPreviewClick(event) {
          const trigger = event.target?.closest?.('[data-lightbox-src], [data-lightbox-svg], .mermaid-diagram .media-expand-button');
          if (!trigger) return;
          event.preventDefault();
          if (trigger.matches('.mermaid-diagram .media-expand-button')) {
            const diagram = trigger.closest('.mermaid-diagram');
            const svg = diagram?.querySelector('.mermaid-diagram-content svg');
            this.openMermaidLightbox(svg ? svg.outerHTML : '', 'Mermaid diagram', 'Drag to pan, wheel or buttons to zoom');
            return;
          }
          if (trigger.dataset.lightboxSvg) {
            this.openSVGLightbox(trigger.dataset.lightboxSvg || '', trigger.dataset.lightboxTitle || 'SVG preview', trigger.dataset.lightboxMeta || 'Drag to pan, wheel or buttons to zoom');
            return;
          }
          this.openImageLightbox(trigger.dataset.lightboxSrc || '', trigger.dataset.lightboxTitle || '', trigger.dataset.lightboxMeta || '');
        },
        openImageLightbox(src, title, meta) {
          if (!src) return;
          this.imageLightbox = {open: true, kind: 'image', src, html: '', title: title || 'Image preview', meta: meta || 'Drag to pan, wheel or buttons to zoom', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0};
        },
        openSVGLightbox(html, title, meta) {
          html = sanitizeDiagramSVG(html || '');
          if (!html) return;
          this.imageLightbox = {open: true, kind: 'svg', src: '', html, title: title || 'SVG preview', meta: meta || 'Drag to pan, wheel or buttons to zoom', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0};
        },
        openMermaidLightbox(html, title, meta) {
          html = sanitizeMermaidSVG(html || '');
          if (!html) return;
          this.imageLightbox = {open: true, kind: 'svg', src: '', html, title: title || 'Mermaid diagram', meta: meta || 'Drag to pan, wheel or buttons to zoom', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0};
        },
        closeImageLightbox() {
          this.imageLightbox = {open: false, kind: 'image', src: '', html: '', title: '', meta: '', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0};
        },
        lightboxTransform() {
          const box = this.imageLightbox || {};
          return 'translate(' + (box.panX || 0) + 'px, ' + (box.panY || 0) + 'px) scale(' + (box.zoom || 1) + ')';
        },
        zoomLightbox(delta) {
          const current = Number(this.imageLightbox.zoom || 1);
          this.imageLightbox.zoom = Math.max(0.25, Math.min(8, current + delta));
        },
        resetLightboxView() {
          this.imageLightbox.zoom = 1; this.imageLightbox.panX = 0; this.imageLightbox.panY = 0;
        },
        onLightboxWheel(event) {
          event.preventDefault();
          const direction = event.deltaY < 0 ? 0.2 : -0.2;
          this.zoomLightbox(direction);
        },
        startLightboxPan(event) {
          if (!this.imageLightbox.open) return;
          this.imageLightbox.dragging = true;
          this.imageLightbox.dragX = event.clientX - (this.imageLightbox.panX || 0);
          this.imageLightbox.dragY = event.clientY - (this.imageLightbox.panY || 0);
        },
        moveLightboxPan(event) {
          if (!this.imageLightbox.dragging) return;
          this.imageLightbox.panX = event.clientX - (this.imageLightbox.dragX || 0);
          this.imageLightbox.panY = event.clientY - (this.imageLightbox.dragY || 0);
        },
        stopLightboxPan() {
          this.imageLightbox.dragging = false;
        },
        applyTheme() {
          const resolved = this.theme === 'auto' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : this.theme;
          const previous = document.documentElement.getAttribute('data-bs-theme') || '';
          document.documentElement.setAttribute('data-bs-theme', resolved);
          configureMermaid();
          if (previous && previous !== resolved) {
            markMermaidThemeDirty(this.transcriptElement());
            this.renderDiagrams();
          }
        },
        appShellStyle() { return '--sidebar-width: ' + this.sidebarWidth() + 'px;'; },
        isMobileLayout() { return (window.innerWidth || 0) <= 900; },
        openMobileSidebar() { this.mobileSidebarOpen = true; this.reportClientStateSoon(); },
        closeMobileSidebar() { this.mobileSidebarOpen = false; this.reportClientStateSoon(); },
        sidebarWidth() {
          const width = window.innerWidth || 1440;
          return Math.round(Math.max(280, Math.min(640, width * this.sidebarRatio)));
        },
        clampSidebarRatio() {
          if (!Number.isFinite(this.sidebarRatio)) this.sidebarRatio = 0.22;
          this.sidebarRatio = Math.max(0.16, Math.min(0.45, this.sidebarRatio));
        },
        startSidebarResize(ev) {
          if (window.innerWidth <= 900) return;
          ev.preventDefault();
          this.resizingSidebar = true;
          const move = event => {
            const width = window.innerWidth || 1;
            this.sidebarRatio = Math.max(0.16, Math.min(0.45, (width - event.clientX) / width));
          };
          const stop = () => {
            this.resizingSidebar = false;
            this.clampSidebarRatio();
            writePreference('sidebarRatio', this.sidebarRatio.toFixed(4));
            window.removeEventListener('pointermove', move);
            window.removeEventListener('pointerup', stop);
            window.removeEventListener('pointercancel', stop);
          };
          window.addEventListener('pointermove', move);
          window.addEventListener('pointerup', stop);
          window.addEventListener('pointercancel', stop);
          move(ev);
        },
        connect() {
          if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.handleSocketOpen(this.ws);
            return;
          }
          if (this.ws && this.ws.readyState === WebSocket.CONNECTING) return;
          if (this.reconnectTimer) {
            clearTimeout(this.reconnectTimer);
            this.reconnectTimer = null;
          }
          const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
          const route = this.selectionFromLocation();
          const params = new URLSearchParams();
          if (route.sessionID) params.set('session', route.sessionID);
          if (route.chatID) params.set('chat', route.chatID);
          const query = params.toString();
          const ws = new WebSocket(proto + '//' + location.host + '/ws' + (query ? '?' + query : ''));
          this.ws = ws;
          this.connecting = true;
          this.connected = false;
          if (this.connectWatchdog) clearTimeout(this.connectWatchdog);
          this.connectWatchdog = setTimeout(() => {
            if (this.ws !== ws) return;
            if (ws.readyState === WebSocket.OPEN && !this.connected) {
              this.handleSocketOpen(ws);
              return;
            }
            if (ws.readyState === WebSocket.CONNECTING) ws.close();
          }, 500);
          ws.onopen = () => this.handleSocketOpen(ws);
          ws.onerror = () => {
            if (this.ws === ws && ws.readyState !== WebSocket.CLOSED) ws.close();
          };
          ws.onclose = () => {
            if (this.ws !== ws) return;
            if (this.connectWatchdog) {
              clearTimeout(this.connectWatchdog);
              this.connectWatchdog = null;
            }
            this.connecting = true;
            this.connected = false;
            this.rejectPending('connection closed');
            this.scheduleReconnect();
          };
          ws.onmessage = ev => {
            if (this.ws !== ws) return;
            this.lastWSMessageAt = Date.now();
            this.lastWSMessageBytes = websocketPayloadBytes(ev.data);
            try {
              this.onMessage(JSON.parse(ev.data));
            } catch (err) {
              console.error('websocket message failed', err, ev.data);
              this.error = (err && err.message) || 'websocket message failed';
              this.reconnectStaleSocket(ws, 'websocket message failed');
            }
          };
          queueMicrotask(() => {
            if (this.ws === ws && ws.readyState === WebSocket.OPEN && !this.connected) this.handleSocketOpen(ws);
          });
        },
        handleSocketOpen(ws) {
          if (this.ws !== ws || this.connected) return;
          if (this.connectWatchdog) {
            clearTimeout(this.connectWatchdog);
            this.connectWatchdog = null;
          }
          this.connecting = false;
          this.connected = true;
          this.lastWSMessageAt = Date.now();
          this.reconnectDelay = 150;
          this.rpcOn(ws, 'hello', {}).then(hello => this.applyHello(hello)).catch(err => {
            this.error = (err && err.message) || 'failed to load session';
          });
        },
        checkWebsocketHealth() {
          if (!this.ws || this.ws.readyState !== WebSocket.OPEN || !this.connected) return;
          if (!this.lastWSMessageAt) {
            this.lastWSMessageAt = Date.now();
            return;
          }
          if (Date.now() - this.lastWSMessageAt <= 45000) return;
          this.reconnectStaleSocket(this.ws, 'connection stale');
        },
        reconnectStaleSocket(ws, reason) {
          if (!ws || this.ws !== ws) return;
          this.ws = null;
          this.connecting = true;
          this.connected = false;
          this.rejectPending(reason || 'connection stale');
          try { ws.close(); } catch (_) {}
          this.connectWhenReady();
        },
        connectNow() {
          if (this.reconnectTimer) {
            clearTimeout(this.reconnectTimer);
            this.reconnectTimer = null;
          }
          this.connectWhenReady();
        },
        scheduleReconnect() {
          if (this.reconnectTimer) return;
          const delay = this.reconnectDelay;
          this.reconnectDelay = Math.min(2000, Math.round(this.reconnectDelay * 1.6));
          this.reconnectTimer = setTimeout(() => {
            this.reconnectTimer = null;
            this.connectWhenReady();
          }, delay);
        },
        connectWhenReady() {
          if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) {
            this.connect();
            return;
          }
          if (this.reconnectProbe) return;
          this.connecting = true;
          this.connected = false;
          const controller = new AbortController();
          this.reconnectProbe = controller;
          const timeout = setTimeout(() => controller.abort(), 1000);
          fetch('/api/health', {cache: 'no-store', signal: controller.signal})
            .then(resp => {
              if (!resp.ok) throw new Error('server not ready');
              this.reconnectProbe = null;
              this.connect();
            })
            .catch(() => {
              if (this.reconnectProbe === controller) {
                this.reconnectProbe = null;
                this.scheduleReconnect();
              }
            })
            .finally(() => clearTimeout(timeout));
        },
        connectionLabel() {
          if (this.connected) return 'connected';
          if (this.connecting) return 'connecting';
          return 'offline';
        },
        restartNeeded() {
          return !!(this.state.restart_needed || this.state.RestartNeeded);
        },
        restartButtonClass() {
          if (this.restartHardRequested) return 'btn-danger';
          if (this.restartAcknowledged) return 'btn-warning';
          return 'btn-outline-warning';
        },
        restartButtonIcon() {
          if (this.restartRequestPending) return 'bi-hourglass-split';
          if (this.restartHardRequested) return 'bi-x-octagon-fill';
          if (this.restartAcknowledged) return 'bi-check-circle-fill';
          return 'bi-arrow-clockwise';
        },
        restartBuildInfo() {
          return this.state.restart_build || this.state.RestartBuild || {};
        },
        currentBuildInfo() {
          return this.state.build || this.state.Build || {};
        },
        shortBuildCommit(build) {
          let commit = String(build.commit || build.Commit || '').trim();
          if (!commit) return '';
          if (commit.length > 12) commit = commit.slice(0, 12);
          if (String(build.dirty || build.Dirty || '').trim() === 'true') commit += '-dirty';
          return commit;
        },
        formatBuildTimestamp(raw, includeDate = false) {
          raw = String(raw || '').trim();
          if (!raw || raw === 'unknown') return '';
          const date = new Date(raw);
          if (!Number.isFinite(date.getTime())) return raw;
          const pad = (value) => String(value).padStart(2, '0');
          const time = pad(date.getHours()) + ':' + pad(date.getMinutes()) + ':' + pad(date.getSeconds());
          if (!includeDate) return time;
          return date.getFullYear() + '-' + pad(date.getMonth() + 1) + '-' + pad(date.getDate()) + ' ' + time;
        },
        currentBuildLabel() {
          const build = this.currentBuildInfo();
          const commit = this.shortBuildCommit(build);
          const built = this.formatBuildTimestamp(build.build_time || build.BuildTime);
          if (commit && built) return commit + ' · ' + built;
          return commit || built;
        },
        currentBuildTitle() {
          const build = this.currentBuildInfo();
          const parts = [];
          const name = String(build.name || build.Name || 'koder').trim();
          const version = String(build.version || build.Version || '').trim();
          const commit = String(build.commit || build.Commit || '').trim();
          const dirty = String(build.dirty || build.Dirty || '').trim();
          const built = this.formatBuildTimestamp(build.build_time || build.BuildTime, true);
          if (version) parts.push(name + ' ' + version);
          if (commit) parts.push('commit: ' + commit + (dirty === 'true' ? ' (dirty)' : ''));
          if (built) parts.push('built: ' + built);
          return parts.join('\n') || 'Current build';
        },
        restartBuildCommitLabel() {
          const build = this.restartBuildInfo();
          let commit = String(build.commit || build.Commit || '').trim();
          const fromBuildID = !commit;
          if (fromBuildID) commit = String(build.build_id || build.BuildID || '').trim().split(/\s+@\s+/)[0] || '';
          if (!commit) return '';
          if (!fromBuildID && String(build.dirty || build.Dirty || '').trim() === 'true') commit += '-dirty';
          return commit;
        },
        restartBuildAgeLabel() {
          const build = this.restartBuildInfo();
          const raw = String(build.build_time || build.BuildTime || '').trim();
          if (!raw || raw === 'unknown') return '';
          const built = Date.parse(raw);
          if (!Number.isFinite(built)) return '';
          const elapsed = Math.max(0, (this.restartAgeTick || Date.now()) - built);
          const second = 1000, minute = 60 * second, hour = 60 * minute, day = 24 * hour;
          if (elapsed < minute) return Math.max(1, Math.floor(elapsed / second)) + 's ago';
          if (elapsed < hour) return Math.floor(elapsed / minute) + 'm ago';
          if (elapsed < day) return Math.floor(elapsed / hour) + 'h ago';
          return Math.floor(elapsed / day) + 'd ago';
        },
        restartBuildLabel() {
          const commit = this.restartBuildCommitLabel();
          if (!commit) return '';
          const age = this.restartBuildAgeLabel();
          return age ? commit + ' (' + age + ')' : commit;
        },
        restartButtonTitle() {
          const build = this.restartBuildLabel();
          const suffix = build ? '\nAvailable build: ' + build : '';
          if (this.restartRequestPending) return (this.restartAcknowledged ? 'Requesting hard restart' : 'Requesting restart') + suffix;
          if (this.restartHardRequested) return 'Hard restart acknowledged' + suffix;
          if (this.restartAcknowledged) return 'Restart acknowledged; press again for hard restart' + suffix;
          return 'New koder build is ready; restart koder' + suffix;
        },
        requestRestart() {
          if (this.restartRequestPending || this.restartHardRequested) return;
          const hard = !!this.restartAcknowledged;
          this.restartRequestPending = true;
          this.rpc('restart_process', {hard}).then(() => {
            this.restartRequestPending = false;
            if (hard) {
              this.restartHardRequested = true;
            } else {
              this.restartAcknowledged = true;
            }
          }).catch(err => {
            this.restartRequestPending = false;
            this.showToast(err.message);
          });
        },
        applyHello(hello) {
          if (hello && hello.asset_hash && window.KODER_ASSET_HASH && hello.asset_hash !== window.KODER_ASSET_HASH) {
            location.reload();
            return;
          }
          this.clientID = (hello && hello.client_id) || this.clientID || '';
          this.applyState((hello && hello.state) || hello || {}, {scrollToBottom: true});
          this.focusComposerAfterInitialLoad();
          this.reportClientStateSoon();
          if (window.performance && performance.mark) {
            performance.clearMarks('koder-ready');
            performance.mark('koder-ready');
          }
        },
        onMessage(msg) {
          if (msg.type) { this.onPush(msg); return; }
          const p = this.pending[msg.id]; if (!p) return; delete this.pending[msg.id];
          msg.ok ? p.resolve(msg.result) : p.reject(new Error(msg.error || 'rpc failed'));
        },
        onPush(msg) {
          if (msg.type === 'heartbeat') return;
          if (msg.type === 'snapshot') this.applyState(msg.payload);
          if (msg.type === 'state_delta') this.applyStateDelta(msg.payload);
          if (msg.type === 'chat_delta') this.applyChatDelta(msg.payload);
          if (msg.type === 'planning_delta') this.applyPlanningDelta(msg.payload);
          if (msg.type === 'tasks_delta') this.applyTasksDelta(msg.payload);
          if (msg.type === 'restart_delta') this.applyRestartDelta(msg.payload);
          if (msg.type === 'session_delta') this.applySessionDelta(msg.payload);
          if (msg.type === 'selection_delta') this.applySelectionDelta(msg.payload);
          if (msg.type === 'workspace_delta') this.applyWorkspaceDelta(msg.payload);
          if (msg.type === 'theme') { this.theme = msg.payload.theme || 'auto'; writePreference('theme', this.theme); this.applyTheme(); }
          if (msg.type === 'tts') this.applyTTSSettings(msg.payload);
        },
        applyStateDelta(delta) {
          if (!delta) return;
          delta = {...delta};
          const incomingSessionID = String(delta.session?.id || delta.session?.ID || delta.Session?.id || delta.Session?.ID || '').trim();
          const currentSessionID = String(this.state.session?.id || this.state.session?.ID || '').trim();
          const sameSession = !incomingSessionID || !currentSessionID || incomingSessionID === currentSessionID;
          delete delta.session; delete delta.Session;
          delete delta.chats; delete delta.Chats;
          delete delta.chat_statuses; delete delta.ChatStatuses;
          delete delta.active_chat_id; delete delta.ActiveChatID;
          delete delta.snapshot; delete delta.Snapshot;
          if (!sameSession) {
            delete delta.milestones; delete delta.Milestones;
            delete delta.tasks; delete delta.Tasks;
            delete delta.tasks_by_milestone; delete delta.TasksByKey;
          }
          delete delta.context_window; delete delta.ContextWindow;
          delete delta.model_info; delete delta.ModelInfo;
          const scroll = this.transcriptScrollState();
          const seq = ++this.scrollRestoreSeq;
          this.state = {...this.state, ...delta};
          this.applyTheme();
          this.error = this.state.error || '';
          this.syncInterruptArmed();
          this.afterTranscriptDOMUpdate(() => {
            if (seq === this.scrollRestoreSeq) this.restoreTranscriptScroll(scroll);
          }, {renderDiagrams: false});
          this.reportClientStateSoon();
        },
        shouldRenderDiagramsAfterChatDelta(delta, previous, next) {
          if (!delta) return false;
          if (this.snapshotIsStreaming(next)) return false;
          const wasStreaming = this.snapshotIsStreaming(previous);
          return !!delta.item || !!delta.transcript_changed || !!delta.TranscriptChanged || wasStreaming;
        },
        applyRestartDelta(delta) {
          if (!delta) return;
          const needed = !!(delta.restart_needed || delta.RestartNeeded);
          this.state.restart_needed = needed;
          this.state.RestartNeeded = needed;
          if (delta.restart_build !== undefined || delta.RestartBuild !== undefined) {
            const build = delta.restart_build || delta.RestartBuild || {};
            this.state.restart_build = build;
            this.state.RestartBuild = build;
          }
          if (!needed) {
            this.restartRequestPending = false;
            this.restartAcknowledged = false;
            this.restartHardRequested = false;
            this.state.restart_build = {};
            this.state.RestartBuild = {};
          }
          this.reportClientStateSoon();
        },
        applyPlanningDelta(delta) {
          if (!delta) return;
          if (delta.milestones !== undefined) { this.state.milestones = delta.milestones; this.state.Milestones = delta.milestones; }
          if (delta.tasks !== undefined) { this.state.tasks = delta.tasks; this.state.Tasks = delta.tasks; }
          if (delta.tasks_by_milestone !== undefined) { this.state.tasks_by_milestone = delta.tasks_by_milestone; this.state.TasksByKey = delta.tasks_by_milestone; }
          this.reportClientStateSoon();
        },
        applyTasksDelta(delta) {
          if (!delta) return;
          if (delta.tasks !== undefined) { this.state.tasks = delta.tasks; this.state.Tasks = delta.tasks; }
          this.reportClientStateSoon();
        },
        applySessionDelta(delta) {
          if (!delta || !delta.session) return;
          const id = String(delta.session.id || delta.session.ID || '').trim();
          const sessions = (this.state.sessions || this.state.Sessions || []).slice();
          const idx = sessions.findIndex(item => String(item.id || item.ID || '') === id);
          if (idx >= 0) sessions[idx] = delta.session; else if (id) sessions.push(delta.session);
          this.state.sessions = sessions;
          this.state.Sessions = sessions;
          if (id && id === this.currentSessionID()) {
            this.state.session = delta.session;
            this.state.Session = delta.session;
          }
          this.reportClientStateSoon();
        },
        applySelectionDelta(delta) {
          if (!delta) return;
          const id = String(delta.active_chat_id || delta.ActiveChatID || '').trim();
          if (!id) return;
          this.state.active_chat_id = id;
          this.state.ActiveChatID = id;
          const snapshots = this.state.snapshots || this.state.Snapshots || {};
          const snapshot = snapshots[id] || snapshots[String(id)];
          if (snapshot) { this.state.snapshot = snapshot; this.state.Snapshot = snapshot; }
          this.writeSelectedChat();
          this.reportClientStateSoon();
        },
        applyWorkspaceDelta(delta) {
          if (!delta) return;
          const sessionID = String(delta.session_id || delta.SessionID || '').trim();
          if (sessionID && sessionID !== this.currentSessionID()) return;
          const status = delta.workspace_status || delta.Workspace || delta.workspace;
          if (status === undefined) return;
          this.state.workspace_status = status;
          this.state.Workspace = status;
          this.reportClientStateSoon();
        },
        applyChatDelta(delta) {
          if (!delta) return;
          const id = String(delta.chat_id || delta.ChatID || delta.chat?.id || delta.chat?.ID || '').trim();
          if (!id) return;
          const active = id === this.activeChatID();
          const scroll = active ? this.transcriptScrollState() : null;
          const seq = active ? ++this.scrollRestoreSeq : this.scrollRestoreSeq;
          const snapshots = {...(this.state.snapshots || this.state.Snapshots || {})};
          const current = snapshots[id] || snapshots[String(id)] || {};
          const wasStreaming = this.snapshotIsStreaming(current);
          const next = {...current};
          if (delta.chat) next.Chat = delta.chat;
          if (delta.approvals !== undefined) next.Approvals = delta.approvals;
          if (delta.queue !== undefined) {
            const queue = Array.isArray(delta.queue) ? delta.queue : [];
            next.QueuedInputs = queue;
            next.queued_inputs = queue;
            next.queue = queue;
          }
          if (delta.exec_processes !== undefined) next.ExecProcesses = delta.exec_processes;
          if (delta.context !== undefined) next.Context = delta.context;
          if (delta.status !== undefined) next.Status = delta.status;
          if (delta.status_text !== undefined) next.StatusText = delta.status_text;
          if (delta.active !== undefined) next.Active = delta.active;
          if (delta.replace_timeline || delta.ReplaceTimeline) {
            const timeline = Array.isArray(delta.timeline || delta.Timeline) ? (delta.timeline || delta.Timeline) : [];
            this.storeTimeline(id, timeline);
            next.TimelineHasMore = false;
            next.TimelineLoadedAll = true;
            next.TimelineBefore = timeline.length ? this.timelineItemID(timeline[0]) : '';
          } else if (delta.item) {
            this.storeTimeline(id, this.patchTimelineItem(this.timelineForChat(id, next), delta.item));
          }
          snapshots[id] = next;
          snapshots[String(id)] = next;
          this.state.snapshots = snapshots;
          this.state.Snapshots = snapshots;
          const deltaSessionID = String(delta.chat?.session_id || delta.chat?.SessionID || next.Chat?.SessionID || next.chat?.session_id || '').trim();
          const currentSessionID = String(this.state.session?.id || this.state.session?.ID || '').trim();
          const currentSessionChat = !deltaSessionID || !currentSessionID || deltaSessionID === currentSessionID;
          if (currentSessionChat && delta.chat) this.patchChatList(delta.chat);
          if (currentSessionChat) this.patchChatStatus(delta);
          if (id === this.activeChatID()) {
            this.state.snapshot = next;
            this.state.Snapshot = next;
          }
          if (delta.error) this.error = delta.error;
          this.syncInterruptArmed();
          if (active && delta.item) this.maybeSpeakTimelineItem(delta.item, next);
          if (active && wasStreaming && !this.snapshotIsStreaming(next)) this.maybeSpeakLatestAssistant(next);
          if (active) {
            const changedItemID = delta.item ? this.timelineItemID(delta.item) : '';
            this.afterTranscriptDOMUpdate(() => {
              if (seq === this.scrollRestoreSeq) this.restoreTranscriptScroll(scroll);
            }, {itemID: changedItemID});
          }
          this.reportClientStateSoon();
        },
        patchTimelineItem(timeline, item) {
          const out = Array.isArray(timeline) ? timeline.slice() : [];
          const id = item.id || item.ID || '';
          if (!id) throw new Error('timeline delta missing item id');
          const idx = out.findIndex(existing => {
            const existingID = existing.id || existing.ID || '';
            return existingID === id;
          });
          if (idx >= 0) out[idx] = item; else out.push(item);
          return out;
        },
        patchChatList(chat) {
          const id = this.chatID(chat);
          const chats = (this.state.chats || this.state.Chats || []).slice();
          const idx = chats.findIndex(existing => this.chatID(existing) === id);
          if (idx >= 0) chats[idx] = chat; else chats.push(chat);
          this.state.chats = chats;
          this.state.Chats = chats;
        },
        patchChatStatus(delta) {
          const id = delta.chat_id || delta.ChatID;
          const statuses = (this.state.chat_statuses || this.state.ChatStatuses || []).slice();
          const status = {
            chat_id: id,
            status: delta.status || 'idle',
            status_text: delta.status_text || '',
            busy: !!delta.active,
            pending_approvals: (delta.approvals || []).length,
            last_error: delta.error || '',
          };
          const idx = statuses.findIndex(existing => (existing.chat_id || existing.ChatID) === id);
          if (idx >= 0) statuses[idx] = {...statuses[idx], ...status}; else statuses.push(status);
          this.state.chat_statuses = statuses;
          this.state.ChatStatuses = statuses;
        },
        rpc(method, params) {
          return this.rpcOn(this.ws, method, params).catch(err => { this.error = err.message; this.showToast(err.message); throw err; });
        },
        rpcOn(ws, method, params) {
          const id = this.nextID++;
          return new Promise((resolve, reject) => {
            if (!ws || ws.readyState !== WebSocket.OPEN) {
              reject(new Error('websocket is not connected'));
              return;
            }
            this.pending[id] = {resolve, reject};
            try {
              ws.send(JSON.stringify({id, method, params}));
            } catch (err) {
              delete this.pending[id];
              reject(err);
            }
          });
        },
        rejectPending(message) {
          const pending = this.pending;
          this.pending = {};
          Object.values(pending).forEach(p => p.reject(new Error(message)));
        },
        transcriptElement() {
          return this.$refs?.transcript || document.querySelector('.transcript');
        },
        onTranscriptScroll() {
          if (this.transcriptUserScrollActive && !this.transcriptProgrammaticScroll) {
            this.markTranscriptUserScrollIntent();
            this.scrollRestoreSeq++;
            this.updateTranscriptStickiness();
          }
          this.scheduleTimelineRenderWindowRecalculation();
          const el = this.transcriptElement();
          if (!el || el.scrollTop > 96) return;
          this.loadOlderTimeline();
        },
        markTranscriptUserScrollIntent() {
          this.transcriptUserScrollActive = true;
          if (this.transcriptUserScrollTimer) clearTimeout(this.transcriptUserScrollTimer);
          this.transcriptUserScrollTimer = setTimeout(() => {
            this.transcriptUserScrollActive = false;
            this.transcriptUserScrollTimer = null;
          }, 250);
        },
        onTranscriptWheel(event) {
          this.markTranscriptUserScrollIntent();
          if (event && Number(event.deltaY || 0) < 0) this.setTranscriptStickToBottom(false);
          requestAnimationFrame(() => this.updateTranscriptStickiness());
        },
        onTranscriptKeydown(event) {
          if (!event || event.defaultPrevented || event.isComposing) return;
          if (event.key === 'End') {
            event.preventDefault();
            this.scrollRestoreSeq++;
            this.scrollTranscriptToBottom();
            return;
          }
          if (event.key === 'Home') {
            event.preventDefault();
            this.scrollRestoreSeq++;
            this.setTranscriptStickToBottom(false);
            this.loadAllTimeline();
            return;
          }
          const scrollKeys = ['ArrowUp', 'ArrowDown', 'PageUp', 'PageDown', ' '];
          if (!scrollKeys.includes(event.key)) return;
          this.markTranscriptUserScrollIntent();
          if (event.key === 'ArrowUp' || event.key === 'PageUp') this.setTranscriptStickToBottom(false);
          requestAnimationFrame(() => this.updateTranscriptStickiness());
        },
        transcriptBottomDistance(el = this.transcriptElement()) {
          if (!el) return 0;
          return el.scrollHeight - el.scrollTop - el.clientHeight;
        },
        transcriptNearBottom(el = this.transcriptElement()) {
          return this.transcriptBottomDistance(el) <= 48;
        },
        setTranscriptStickToBottom(value) {
          const next = !!value;
          if (this.transcriptStickToBottom !== next) {
            this.transcriptStickToBottom = next;
            this.reportClientStateSoon();
          }
          return next;
        },
        updateTranscriptStickiness() {
          const el = this.transcriptElement();
          if (!el) {
            this.setTranscriptStickToBottom(true);
            this.reportClientStateSoon();
            return true;
          }
          this.setTranscriptStickToBottom(this.transcriptNearBottom(el));
          return this.transcriptStickToBottom;
        },
        scrollTranscriptToBottom() {
          const el = this.transcriptElement();
          if (!el) return;
          this.setTranscriptStickToBottom(true);
          this.transcriptProgrammaticScroll = true;
          this.recalculateTimelineRenderWindow();
          el.scrollTop = el.scrollHeight;
          requestAnimationFrame(() => {
            el.scrollTop = el.scrollHeight;
            setTimeout(() => { this.transcriptProgrammaticScroll = false; }, 0);
          });
        },
        restoreTranscriptTop(top) {
          const el = this.transcriptElement();
          if (!el) return;
          this.transcriptProgrammaticScroll = true;
          el.scrollTop = Number(top || 0);
          requestAnimationFrame(() => {
            setTimeout(() => { this.transcriptProgrammaticScroll = false; }, 0);
          });
        },
        timelineHasMore() {
          const snapshot = this.activeSnapshot();
          return !!(snapshot.TimelineHasMore || snapshot.timeline_has_more);
        },
        timelineLoadedAll() {
          const snapshot = this.activeSnapshot();
          return !!(snapshot.TimelineLoadedAll || snapshot.timeline_loaded_all);
        },
        timelineBefore() {
          const snapshot = this.activeSnapshot();
          const explicit = String(snapshot.TimelineBefore || snapshot.timeline_before || '').trim();
          if (explicit) return explicit;
          const timeline = this.timeline();
          return timeline.length ? this.timelineItemID(timeline[0]) : '';
        },
        timelineLoadingActive() {
          const id = this.activeChatID();
          return !!(id && (this.timelineLoading[id] || this.timelineLoadingAll[id]));
        },
        loadOlderTimeline() {
          const chatID = this.activeChatID();
          if (!chatID || this.timelineLoading[chatID] || this.timelineLoadingAll[chatID] || !this.timelineHasMore()) return;
          const before = this.timelineBefore();
          if (!before) return;
          const el = this.transcriptElement();
          const scrollHeight = el ? el.scrollHeight : 0;
          const scrollTop = el ? el.scrollTop : 0;
          this.timelineLoading = {...this.timelineLoading, [chatID]: true};
          this.rpc('load_timeline', {chat_id: chatID, before, limit: 80})
            .then(page => this.mergeTimelinePage(page, {prepend: true, scrollHeight, scrollTop}))
            .catch(err => this.showToast(err.message))
            .finally(() => {
              const next = {...this.timelineLoading};
              delete next[chatID];
              this.timelineLoading = next;
            });
        },
        loadAllTimeline() {
          const chatID = this.activeChatID();
          if (!chatID || this.timelineLoadingAll[chatID] || this.timelineLoadedAll()) return;
          this.timelineLoadingAll = {...this.timelineLoadingAll, [chatID]: true};
          this.rpc('load_timeline', {chat_id: chatID, all: true})
            .then(page => this.mergeTimelinePage(page, {replace: true, scrollTop: 0}))
            .catch(err => this.showToast(err.message))
            .finally(() => {
              const next = {...this.timelineLoadingAll};
              delete next[chatID];
              this.timelineLoadingAll = next;
            });
        },
        mergeTimelinePage(page, options = {}) {
          if (!page) return;
          const chatID = String(page.chat_id || page.ChatID || '').trim();
          if (!chatID) return;
          const items = page.items || page.Items || [];
          const snapshots = {...(this.state.snapshots || this.state.Snapshots || {})};
          const current = snapshots[chatID] || snapshots[String(chatID)] || {};
          const existing = this.timelineForChat(chatID, current);
          let timeline = [];
          if (options.replace) {
            timeline = items.slice();
          } else if (options.prepend) {
            const seen = new Set(items.map(item => this.timelineItemID(item)).filter(Boolean));
            timeline = items.concat(existing.filter(item => !seen.has(this.timelineItemID(item))));
          } else {
            timeline = existing.slice();
            items.forEach(item => { timeline = this.patchTimelineItem(timeline, item); });
          }
          const next = {
            ...current,
            TimelineHasMore: !!(page.has_more || page.HasMore),
            TimelineLoadedAll: !!(page.loaded_all || page.LoadedAll),
            TimelineBefore: String(page.before || page.Before || (timeline[0] && this.timelineItemID(timeline[0])) || '').trim(),
          };
          this.storeTimeline(chatID, timeline);
          snapshots[chatID] = next;
          snapshots[String(chatID)] = next;
          this.state.snapshots = snapshots;
          this.state.Snapshots = snapshots;
          if (chatID === String(this.activeChatID())) {
            this.state.snapshot = next;
            this.state.Snapshot = next;
          }
          this.afterTranscriptDOMUpdate(() => {
            const el = this.transcriptElement();
            if (!el) return;
            if (options.replace) {
              this.setTranscriptStickToBottom(false);
              this.restoreTranscriptTop(Number.isFinite(options.scrollTop) ? options.scrollTop : 0);
              return;
            }
            const previousHeight = Number(options.scrollHeight || 0);
            if (previousHeight > 0) {
              this.restoreTranscriptTop(el.scrollHeight - previousHeight + Number(options.scrollTop || 0));
            }
          });
        },
        transcriptScrollState() {
          const el = this.transcriptElement();
          if (!el) return {el: null, top: 0, nearBottom: true, stickToBottom: true};
          const nearBottom = this.transcriptNearBottom(el);
          return {el, top: el.scrollTop, nearBottom, stickToBottom: !!this.transcriptStickToBottom};
        },
        afterTranscriptDOMUpdate(fn, options = {}) {
          this.$nextTick(() => {
            requestAnimationFrame(() => {
              const run = () => {
                this.measureRenderedTimelineItems();
                this.observeLastTranscriptItem();
                fn();
                requestAnimationFrame(() => {
                  this.observeLastTranscriptItem();
                  fn();
                });
                setTimeout(() => {
                  this.observeLastTranscriptItem();
                  fn();
                }, 0);
              };
              if (options.renderDiagrams === false) {
                run();
                return;
              }
              const rendered = this.renderDiagrams(this.timelineItemElement(options.itemID));
              run();
              Promise.resolve(rendered).then(() => {
                this.observeLastTranscriptItem();
                fn();
                requestAnimationFrame(() => {
                  this.observeLastTranscriptItem();
                  fn();
                });
                setTimeout(() => {
                  this.observeLastTranscriptItem();
                  fn();
                }, 0);
              });
            });
          });
        },
        timelineItemElement(itemID) {
          const id = String(itemID || '').trim();
          if (!id) return null;
          const root = this.transcriptElement();
          if (!root) return null;
          const escaped = window.CSS && CSS.escape ? CSS.escape(id) : id.replace(/["\\]/g, '\\$&');
          return root.querySelector('[data-timeline-item-id="' + escaped + '"]');
        },
        lastTranscriptItemElement() {
          const root = this.transcriptElement();
          if (!root) return null;
          const rows = root.querySelectorAll('.transcript-turn[data-timeline-item-id]');
          return rows.length ? rows[rows.length - 1] : null;
        },
        transcriptItemHeight(row) {
          if (!row) return 0;
          const rect = row.getBoundingClientRect();
          return Math.max(0, Math.ceil(rect.height));
        },
        observeLastTranscriptItem() {
          if (!window.ResizeObserver) return;
          const row = this.lastTranscriptItemElement();
          const itemID = row ? String(row.dataset.timelineItemId || '').trim() : '';
          if (row && row === this.transcriptObservedLastItemElement && itemID === this.transcriptObservedLastItemID) return;
          if (this.transcriptLastItemObserver) this.transcriptLastItemObserver.disconnect();
          this.transcriptLastItemObserver = null;
          this.transcriptObservedLastItemElement = row;
          this.transcriptObservedLastItemID = itemID;
          this.transcriptObservedLastItemHeight = this.transcriptItemHeight(row);
          if (!row || !itemID) return;
          this.transcriptLastItemObserver = new ResizeObserver(() => {
            const nextHeight = this.transcriptItemHeight(row);
            if (nextHeight === this.transcriptObservedLastItemHeight) return;
            this.transcriptObservedLastItemHeight = nextHeight;
            if (this.transcriptStickToBottom) this.scrollTranscriptToBottom();
          });
          this.transcriptLastItemObserver.observe(row);
        },
        renderDiagrams(root = null) {
          root = root || this.transcriptElement();
          this.enhanceDisplayedMedia(root);
          return renderMermaidIn(root).then(() => this.enhanceDisplayedMedia(root));
        },
        enhanceDisplayedMedia(root) {
          if (!root) return;
          root.querySelectorAll('.markdown-body img:not([data-lightbox-enhanced])').forEach(img => {
            img.dataset.lightboxEnhanced = 'true';
            const wrapper = document.createElement('span');
            wrapper.className = 'markdown-media-preview';
            img.parentNode.insertBefore(wrapper, img);
            wrapper.appendChild(img);
            const button = document.createElement('button');
            button.type = 'button';
            button.className = 'media-expand-button';
            button.title = 'Expand image';
            button.dataset.lightboxSrc = img.currentSrc || img.src || '';
            button.dataset.lightboxTitle = img.alt || 'Image preview';
            button.dataset.lightboxMeta = 'Drag to pan, wheel or buttons to zoom';
            button.innerHTML = '<i class="bi bi-arrows-angle-expand"></i>';
            wrapper.appendChild(button);
          });
          root.querySelectorAll('.markdown-body svg:not([data-lightbox-enhanced])').forEach(svg => {
            if (svg.closest('.mermaid-diagram')) return;
            svg.dataset.lightboxEnhanced = 'true';
            const wrapper = document.createElement('span');
            wrapper.className = 'markdown-media-preview markdown-svg-preview';
            svg.parentNode.insertBefore(wrapper, svg);
            wrapper.appendChild(svg);
            const button = document.createElement('button');
            button.type = 'button';
            button.className = 'media-expand-button';
            button.title = 'Expand SVG';
            button.dataset.lightboxSvg = svg.outerHTML;
            button.dataset.lightboxTitle = 'SVG preview';
            button.dataset.lightboxMeta = 'Drag to pan, wheel or buttons to zoom';
            button.innerHTML = '<i class="bi bi-arrows-angle-expand"></i>';
            wrapper.appendChild(button);
          });
        },
        restoreTranscriptScroll(scroll, options = {}) {
          const el = this.transcriptElement();
          if (!el) return;
          if (options.scrollToBottom || scroll.stickToBottom) {
            this.scrollTranscriptToBottom();
            return;
          }
          this.restoreTranscriptTop(scroll.top);
        },
        applyState(s, options = {}) {
          const scroll = this.transcriptScrollState();
          const seq = ++this.scrollRestoreSeq;
          this.state = this.cacheStateTimelines(s || {});
          this.hydratingSession = {active: false, id: '', title: '', error: ''};
          this.clearSwitchingChat();
          if (!this.restartNeeded()) {
            this.restartRequestPending = false;
            this.restartAcknowledged = false;
            this.restartHardRequested = false;
          }
          if (this.welcomeMode()) {
            this.sessionState = this.normalizeSessionState(this.state);
          }
          this.syncSessionURL();
          if (this.state.theme || this.state.Theme) this.theme = this.state.theme || this.state.Theme;
          this.applyTTSSettings(this.state.tts || this.state.TTS || null);
          this.restoreMilestoneExpansion();
          this.applyTheme(); this.error = this.state.error || '';
          this.syncInterruptArmed();
          if (!this.restoreSelectedChat()) this.writeSelectedChat();
          this.restoreComposerDraftForActiveChat();
          this.afterTranscriptDOMUpdate(() => {
            if (seq === this.scrollRestoreSeq) this.restoreTranscriptScroll(scroll, options);
          });
          this.reportClientStateSoon();
        },
        selectionFromLocation() {
          const match = location.pathname.match(/^\/s\/([^/]+)(?:\/c\/([^/]+))?$/);
          return {
            sessionID: match ? decodeURIComponent(match[1]) : '',
            chatID: match && match[2] ? decodeURIComponent(match[2]) : '',
          };
        },
        sessionIDFromLocation() { return this.selectionFromLocation().sessionID; },
        chatIDFromLocation() { return this.selectionFromLocation().chatID; },
        currentSessionID() { return String(this.state.session?.id || this.state.session?.ID || '').trim(); },
        welcomeMode() { return !this.currentSessionID() && !this.hydratingSession.active; },
        hydratingSessionMode() { return !!this.hydratingSession.active; },
        sessionLoadedMode() { return !this.welcomeMode() && !this.hydratingSessionMode(); },
        switchingChatMode() { return !!this.switchingChat.active; },
        welcomeMessage() { return this.state.error || this.state.Error || ''; },
        sessionURL(id) {
          const session = String(id || '').trim();
          return session ? '/s/' + encodeURIComponent(session) : '/';
        },
        sessionFilesURL(id) {
          const session = String(id || this.currentSessionID() || '').trim();
          return session ? this.sessionURL(session) + '/files' : '';
        },
        sessionBoardURL(id) {
          const session = String(id || this.currentSessionID() || '').trim();
          return session ? this.sessionURL(session) + '/board' : '';
        },
        openSessionBoard() {
          this.openURLInNewTab(this.sessionBoardURL());
        },
        openSessionFiles() {
          this.openURLInNewTab(this.sessionFilesURL());
        },
        chatURL(chatID, sessionID) {
          const session = String(sessionID || this.currentSessionID() || '').trim();
          const chat = String(chatID || '').trim();
          if (!session) return '/';
          return chat ? this.sessionURL(session) + '/c/' + encodeURIComponent(chat) : this.sessionURL(session);
        },
        shouldOpenInNewTab(ev) {
          return !!(ev && (ev.ctrlKey || ev.metaKey || ev.button === 1));
        },
        openURLInNewTab(url) {
          if (!url) return;
          window.open(url, '_blank', 'noopener');
        },
        syncSessionURL() {
          const id = this.currentSessionID();
          if (!id) {
            if (/^\/s\/[^/]+(?:\/c\/[^/]+)?$/.test(location.pathname)) history.replaceState(null, '', '/');
            this.allowSessionURLSync = false;
            return;
          }
          const target = this.chatURL(this.activeChatID(), id);
          if (location.pathname === target) {
            this.allowSessionURLSync = false;
            return;
          }
          if (location.pathname === '/' || this.allowSessionURLSync) history.replaceState(null, '', target);
          this.allowSessionURLSync = false;
        },
        syncActiveChatURL() {
          const session = this.currentSessionID();
          const chat = String(this.activeChatID() || '').trim();
          if (!session || !chat) return;
          const current = this.selectionFromLocation();
          if (current.sessionID && current.sessionID !== session) return;
          const target = this.chatURL(chat, session);
          if (location.pathname !== target) history.replaceState(null, '', target);
        },
        selectedChatPreferenceName() { return 'selectedChat.' + encodeURIComponent(this.currentSessionID()); },
        milestoneExpansionPreferenceName() {
          const session = encodeURIComponent(this.currentSessionID());
          return 'expandedMilestones.' + session;
        },
        restoreMilestoneExpansion() {
          this.expandedMilestones = readJSONPreference(this.milestoneExpansionPreferenceName(), {});
        },
        writeMilestoneExpansion() {
          writeJSONPreference(this.milestoneExpansionPreferenceName(), this.expandedMilestones || {});
        },
        activeChatID() { return this.state.active_chat_id || this.state.ActiveChatID || 0; },
        writeSelectedChat() { const id = this.activeChatID(); if (id) writeTabPreference(this.selectedChatPreferenceName(), id); },
        composerDraftPreferenceName() {
          const session = encodeURIComponent(this.currentSessionID());
          const chat = encodeURIComponent(this.activeChatID() || '');
          if (!chat) return '';
          return 'composerDraft.' + session + '.' + chat;
        },
        restoreComposerDraftForActiveChat() {
          const key = this.composerDraftPreferenceName();
          if (!key || key === this.activeComposerDraftKey) return;
          this.activeComposerDraftKey = key;
          const saved = readJSONPreference(key, {});
          this.draft = String(saved.draft || '');
          this.composerAttachments = Array.isArray(saved.attachments) ? saved.attachments : [];
          this.clearCompletions();
          this.$nextTick(() => this.resizeComposer());
        },
        focusComposerAfterInitialLoad() {
          if (this.composerInitialFocusDone) return;
          if (this.welcomeMode()) return;
          this.composerInitialFocusDone = true;
          this.$nextTick(() => {
            const el = this.$refs.composerInput;
            if (!el || document.activeElement === el) return;
            el.focus();
            const pos = String(this.draft || '').length;
            try { el.setSelectionRange(pos, pos); } catch (_) {}
            this.reportClientStateSoon();
          });
        },
        writeComposerDraftPayload(draft, attachments) {
          const key = this.composerDraftPreferenceName();
          if (!key) return;
          const text = String(draft || '');
          const files = Array.isArray(attachments) ? attachments : [];
          if (!text && files.length === 0) {
            removePreference(key);
            return;
          }
          writeJSONPreference(key, {draft: text, attachments: files});
        },
        writeComposerDraft() {
          if (this.preserveComposerDraftDuringSend) return;
          this.writeComposerDraftPayload(this.draft, this.composerAttachments);
        },
        clearComposerDraftStorage() {
          const key = this.composerDraftPreferenceName();
          if (key) removePreference(key);
        },
        restoreSelectedChat() {
          if (this.restoreChatAttempted) return false;
          if (this.chatIDFromLocation()) { this.restoreChatAttempted = true; return false; }
          const raw = readTabPreference(this.selectedChatPreferenceName(), '');
          const id = String(raw || '').trim();
          if (!id) { this.restoreChatAttempted = true; return false; }
          const exists = (this.state.chats || this.state.Chats || []).some(chat => this.chatID(chat) === id);
          this.restoreChatAttempted = true;
          if (!exists || id === this.activeChatID()) return false;
          this.rpc('switch_chat', {chat_id: id}).then(s => {
            this.applyState(s, {scrollToBottom: true});
            this.writeSelectedChat();
            this.syncActiveChatURL();
          });
          return true;
        },
        activeSnapshot() {
          const id = this.activeChatID();
          const snapshots = this.state.snapshots || this.state.Snapshots || {};
          return snapshots[id] || snapshots[String(id)] || this.state.snapshot || this.state.Snapshot || {};
        },
        timeline() {
          const id = String(this.activeChatID() || '');
          return this.timelineForChat(id, this.activeSnapshot());
        },
        timelineForChat(chatID, snapshot = {}) {
          const id = String(chatID || snapshot?.Chat?.ID || snapshot?.Chat?.id || snapshot?.chat?.id || snapshot?.chat?.ID || '').trim();
          if (id && timelineStore.has(id)) return timelineStore.get(id);
          const timeline = snapshot?.Timeline || snapshot?.timeline || [];
          return Array.isArray(timeline) ? timeline : [];
        },
        storeTimeline(chatID, timeline) {
          const id = String(chatID || '').trim();
          if (!id || !Array.isArray(timeline)) return [];
          const stored = timeline.slice();
          timelineStore.set(id, stored);
          return stored;
        },
        stripSnapshotTimeline(chatID, snapshot) {
          if (!snapshot || typeof snapshot !== 'object') return snapshot;
          const timeline = snapshot.Timeline || snapshot.timeline;
          if (!Array.isArray(timeline)) return snapshot;
          this.storeTimeline(chatID || snapshot.Chat?.ID || snapshot.Chat?.id || snapshot.chat?.ID || snapshot.chat?.id, timeline);
          const next = {...snapshot};
          delete next.Timeline;
          delete next.timeline;
          return next;
        },
        cacheStateTimelines(state) {
          if (!state || typeof state !== 'object') return state || {};
          const next = {...state};
          const activeID = String(next.active_chat_id || next.ActiveChatID || '').trim();
          if (next.snapshot || next.Snapshot) {
            const snapshot = this.stripSnapshotTimeline(activeID, next.snapshot || next.Snapshot);
            next.snapshot = snapshot;
            next.Snapshot = snapshot;
          }
          const snapshots = next.snapshots || next.Snapshots;
          if (snapshots && typeof snapshots === 'object') {
            const cached = {};
            Object.entries(snapshots).forEach(([chatID, snapshot]) => {
              cached[chatID] = this.stripSnapshotTimeline(chatID, snapshot);
            });
            next.snapshots = cached;
            next.Snapshots = cached;
          }
          return next;
        },
        renderedTimeline() {
          const timeline = this.timeline();
          const bounds = this.timelineRenderWindowBounds(timeline);
          return timeline.slice(bounds.start, bounds.end);
        },
        timelineRenderWindowBounds(timeline = this.timeline()) {
          const chatID = String(this.activeChatID() || '');
          const length = Array.isArray(timeline) ? timeline.length : 0;
          const current = this.timelineRenderWindow || {};
          const fallback = () => {
            if (!this.transcriptStickToBottom || length <= transcriptTailWindowSize) return {chatID, start: 0, end: length, overscan: 0};
            const start = Math.max(0, length - transcriptTailWindowSize - transcriptWindowOverscan);
            return {chatID, start, end: length, overscan: transcriptWindowOverscan};
          };
          if (current.chatID !== chatID || !length) return fallback();
          const start = Math.max(0, Math.min(Number(current.start || 0), length));
          const end = Math.max(start, Math.min(Number(current.end || length), length));
          const overscan = Math.max(0, Number(current.overscan || 0));
          if (end <= start && length > 0) return fallback();
          if (this.transcriptStickToBottom && length > transcriptTailWindowSize && (end < length || end - start > transcriptTailWindowSize + transcriptWindowOverscan)) return fallback();
          return {chatID, start, end, overscan};
        },
        setTimelineRenderWindow(start, end, overscan = 0) {
          const timeline = this.timeline();
          const length = timeline.length;
          const next = {
            chatID: String(this.activeChatID() || ''),
            start: Math.max(0, Math.min(Number(start || 0), length)),
            end: Math.max(0, Math.min(Number(end || 0), length)),
            overscan: Math.max(0, Number(overscan || 0)),
          };
          if (next.end < next.start) next.end = next.start;
          const current = this.timelineRenderWindow || {};
          if (current.chatID === next.chatID && current.start === next.start && current.end === next.end && current.overscan === next.overscan) {
            return this.timelineRenderWindowBounds(timeline);
          }
          this.timelineRenderWindow = next;
          return this.timelineRenderWindowBounds(timeline);
        },
        timelineIndexAtOffset(timeline, offset) {
          if (!Array.isArray(timeline) || timeline.length === 0) return 0;
          const target = Math.max(0, Number(offset || 0));
          let cursor = 0;
          for (let idx = 0; idx < timeline.length; idx++) {
            cursor += this.timelineItemHeight(timeline[idx]);
            if (cursor >= target) return idx;
          }
          return Math.max(0, timeline.length - 1);
        },
        recalculateTimelineRenderWindow() {
          const timeline = this.timeline();
          const length = timeline.length;
          const chatID = String(this.activeChatID() || '');
          if (!chatID || length <= transcriptTailWindowSize) {
            return this.setTimelineRenderWindow(0, length, 0);
          }
          const el = this.transcriptElement();
          if (this.transcriptStickToBottom || !el) {
            const start = Math.max(0, length - transcriptTailWindowSize - transcriptWindowOverscan);
            return this.setTimelineRenderWindow(start, length, transcriptWindowOverscan);
          }
          const first = this.timelineIndexAtOffset(timeline, el.scrollTop);
          const last = this.timelineIndexAtOffset(timeline, el.scrollTop + el.clientHeight);
          const start = Math.max(0, first - transcriptWindowOverscan);
          const end = Math.min(length, last + transcriptWindowOverscan + 1);
          return this.setTimelineRenderWindow(start, Math.max(end, start + 1), transcriptWindowOverscan);
        },
        scheduleTimelineRenderWindowRecalculation() {
          if (this.timelineRenderWindowPending) return;
          this.timelineRenderWindowPending = true;
          requestAnimationFrame(() => {
            this.timelineRenderWindowPending = false;
            this.recalculateTimelineRenderWindow();
          });
        },
        resetTimelineRenderWindow() {
          const timeline = this.timeline();
          return this.setTimelineRenderWindow(0, timeline.length, 0);
        },
        timelineHeightKey(item) {
          const itemID = this.timelineItemID(item);
          if (!itemID) return '';
          return String(this.activeChatID() || '') + ':' + itemID;
        },
        timelineItemHeight(item) {
          const key = this.timelineHeightKey(item);
          const measured = key ? Number(this.timelineItemHeights[key] || 0) : 0;
          return measured > 0 ? measured : Math.max(1, Number(this.timelineAverageItemHeight || estimatedTimelineItemHeight));
        },
        timelineSpacerHeight(items) {
          if (!Array.isArray(items) || items.length === 0) return 0;
          let height = 0;
          for (const item of items) height += this.timelineItemHeight(item);
          return Math.round(height);
        },
        timelineTopSpacerHeight() {
          const timeline = this.timeline();
          const bounds = this.timelineRenderWindowBounds();
          return this.timelineSpacerHeight(timeline.slice(0, bounds.start));
        },
        timelineBottomSpacerHeight() {
          const timeline = this.timeline();
          const bounds = this.timelineRenderWindowBounds(timeline);
          return this.timelineSpacerHeight(timeline.slice(bounds.end));
        },
        measureRenderedTimelineItems(root = null) {
          root = root || this.transcriptElement();
          if (!root) return;
          const chatID = String(this.activeChatID() || '');
          if (!chatID) return;
          const next = {...(this.timelineItemHeights || {})};
          let changed = false;
          let total = 0;
          let count = 0;
          root.querySelectorAll('.transcript-turn[data-timeline-item-id]').forEach(row => {
            const itemID = String(row.dataset.timelineItemId || '').trim();
            if (!itemID) return;
            const rect = row.getBoundingClientRect();
            const styles = window.getComputedStyle(row);
            const marginTop = Number.parseFloat(styles.marginTop || '0') || 0;
            const marginBottom = Number.parseFloat(styles.marginBottom || '0') || 0;
            const height = Math.max(1, Math.ceil(rect.height + marginTop + marginBottom));
            const key = chatID + ':' + itemID;
            total += height;
            count++;
            if (next[key] !== height) {
              next[key] = height;
              changed = true;
            }
          });
          if (count > 0) {
            const average = Math.max(1, Math.round(total / count));
            if (this.timelineAverageItemHeight !== average) {
              this.timelineAverageItemHeight = average;
            }
          }
          if (changed) this.timelineItemHeights = next;
        },
        approvals() { const snapshot = this.activeSnapshot(); return snapshot.Approvals || snapshot.approvals || []; },
        allExecProcesses() {
          const snapshot = this.activeSnapshot();
          const processes = snapshot.ExecProcesses || snapshot.exec_processes || [];
          return Array.isArray(processes) ? processes : [];
        },
        execProcesses() {
          const processes = this.allExecProcesses();
          if (this.showAllExecProcesses) return processes;
          return processes.filter(process => this.execProcessState(process) === 'running');
        },
        execProcessCount() { return this.allExecProcesses().length; },
        runningExecProcessCount() { return this.allExecProcesses().filter(process => this.execProcessState(process) === 'running').length; },
        execProcessCountLabel() {
          const running = this.runningExecProcessCount();
          const total = this.execProcessCount();
          return this.showAllExecProcesses ? String(total) : String(running);
        },
        execProcessFilterTooltip() {
          const running = this.runningExecProcessCount();
          const total = this.execProcessCount();
          return this.showAllExecProcesses ? 'Showing all processes for this chat (' + running + ' running of ' + total + ')' : 'Showing running processes for this chat (' + running + ' of ' + total + ')';
        },
        toggleExecProcessFilter() {
          this.showAllExecProcesses = !this.showAllExecProcesses;
          writePreference('showAllExecProcesses', this.showAllExecProcesses ? 'true' : 'false');
        },
        activeQueue() { const snapshot = this.activeSnapshot(); return snapshot.QueuedInputs || snapshot.queued_inputs || snapshot.queue || []; },
        pendingText() { const snapshot = this.activeSnapshot(); const p = snapshot.PendingAssistant || snapshot.pending_assistant || {}; return [p.Reasoning || p.reasoning, p.Text || p.text].filter(Boolean).join('\n'); },
        snapshotStatus(snapshot) { return String(snapshot?.Status || snapshot?.status || '').trim(); },
        snapshotIsStreaming(snapshot) {
          const status = this.snapshotStatus(snapshot);
          return status === 'streaming_response' || status === 'streaming_thoughts' || status === 'waiting_llm';
        },
        timelineItemID(item) { return String(item?.id || item?.ID || '').trim(); },
        timelineItemActionLabel(item) {
          const kind = String(item?.kind || item?.Kind || item?.content?.kind || '').trim();
          const time = this.formatItemTime(item);
          return [kind || 'item', time].filter(Boolean).join(' at ');
        },
        timelineItemActionAvailable(item) {
          return !!this.timelineItemID(item);
        },
        openTimelineRollback(item) {
          const itemID = this.timelineItemID(item);
          if (!itemID) return;
          this.timelineAction = {open: true, mode: 'rollback', itemID, itemLabel: this.timelineItemActionLabel(item), forkTitle: '', busy: false, error: ''};
        },
        openTimelineFork(item) {
          const itemID = this.timelineItemID(item);
          if (!itemID) return;
          const chat = this.activeChat();
          const title = String(chat?.Title || chat?.title || 'Chat').trim() || 'Chat';
          this.timelineAction = {open: true, mode: 'fork', itemID, itemLabel: this.timelineItemActionLabel(item), forkTitle: title + ' - fork', busy: false, error: ''};
          this.$nextTick(() => {
            const el = this.$refs.timelineForkTitle;
            if (el) {
              el.focus();
              el.select();
            }
          });
        },
        closeTimelineAction() {
          if (this.timelineAction.busy) return;
          this.timelineAction = {open: false, mode: '', itemID: '', itemLabel: '', forkTitle: '', busy: false, error: ''};
        },
        saveTimelineAction() {
          if (!this.timelineAction.open || this.timelineAction.busy) return;
          const chatID = this.activeChatID();
          const itemID = String(this.timelineAction.itemID || '').trim();
          if (!chatID || !itemID) return;
          const mode = this.timelineAction.mode;
          this.timelineAction.busy = true;
          this.timelineAction.error = '';
          if (mode === 'rollback') {
            this.rpc('rollback_chat', {chat_id: chatID, anchor_item_id: itemID}).then(s => {
              this.applyState(s, {scrollToBottom: true});
              this.timelineAction.busy = false;
              this.closeTimelineAction();
            }).catch(err => {
              this.timelineAction.busy = false;
              this.timelineAction.error = err.message || 'rollback failed';
            });
            return;
          }
          if (mode === 'fork') {
            const title = String(this.timelineAction.forkTitle || '').trim();
            if (!title) {
              this.timelineAction.busy = false;
              this.timelineAction.error = 'Chat name is required.';
              return;
            }
            this.rpc('fork_chat', {chat_id: chatID, anchor_item_id: itemID, title}).then(s => {
              this.applyState(s, {scrollToBottom: true});
              this.writeSelectedChat();
              this.syncActiveChatURL();
              this.timelineAction.busy = false;
              this.closeTimelineAction();
            }).catch(err => {
              this.timelineAction.busy = false;
              this.timelineAction.error = err.message || 'fork failed';
            });
          }
        },
        itemTimestamp(item) {
          return String(item?.created_at || item?.CreatedAt || item?.createdAt || item?.timestamp || '').trim();
        },
        formatItemTime(item) {
          const raw = this.itemTimestamp(item);
          if (!raw) return '';
          const date = new Date(raw);
          if (Number.isNaN(date.getTime())) return '';
          const pad = value => String(value).padStart(2, '0');
          return pad(date.getHours()) + ':' + pad(date.getMinutes()) + ':' + pad(date.getSeconds());
        },
        timelineItemIsLatest(item) {
          const id = this.timelineItemID(item);
          if (!id) return false;
          const timeline = this.timeline();
          const latest = timeline.length ? timeline[timeline.length - 1] : null;
          return this.timelineItemID(latest) === id;
        },
        itemMarkdownOptions(item) {
          const streaming = this.snapshotIsStreaming(this.activeSnapshot()) && this.timelineItemIsLatest(item);
          return {deferDiagrams: streaming, incremental: streaming};
        },
        thinkingLabel(reasoning) {
          const explicit = Number(reasoning?.tokens || reasoning?.Tokens || reasoning?.token_count || reasoning?.TokenCount || 0);
          const tokens = explicit > 0 ? explicit : this.estimateTextTokens(reasoning?.text || reasoning?.Text || '');
          const suffix = this.cavemanThinkingSuffix(reasoning);
          return 'thinking (' + tokens + ' tokens)' + suffix;
        },
        hasCavemanReasoning(reasoning) {
          return String(reasoning?.caveman || reasoning?.Caveman || '').trim().length > 0;
        },
        cavemanThinkingSuffix(reasoning) {
          if (!this.hasCavemanReasoning(reasoning)) return '';
          const explicit = Number(reasoning?.caveman_tokens || reasoning?.CavemanTokens || reasoning?.caveman_token_count || reasoning?.CavemanTokenCount || 0);
          const tokens = explicit > 0 ? explicit : this.estimateTextTokens(reasoning?.caveman || reasoning?.Caveman || '');
          return ' · caveman available (' + tokens + ' tokens)';
        },
        reasoningViewKey(item) {
          return this.timelineItemID(item) || String(item?.id || item?.ID || '');
        },
        reasoningView(item) {
          const key = this.reasoningViewKey(item);
          const view = key ? this.reasoningViews[key] : '';
          return view === 'caveman' ? 'caveman' : 'original';
        },
        setReasoningView(item, view) {
          const key = this.reasoningViewKey(item);
          if (!key) return;
          this.reasoningViews[key] = view === 'caveman' ? 'caveman' : 'original';
        },
        reasoningDisplayText(item) {
          const reasoning = item?.content?.reasoning || item?.Content?.Reasoning || {};
          if (this.reasoningView(item) === 'caveman') {
            return reasoning.caveman || reasoning.Caveman || '';
          }
          return reasoning.text || reasoning.Text || '';
        },
        estimateTextTokens(text) {
          const source = String(text || '').trim();
          if (!source) return 0;
          return Math.max(1, Math.ceil(source.length / 4));
        },
        markdownHTML(text, options = {}) { return renderMarkdown(text, options); },
        timelineMarkdownHTML(item, text, options = {}) { return renderTimelineMarkdown(item, text, options); },
        renderMarkdownElement(el, text, options = {}) { renderMarkdownIntoElement(el, text, options); },
        renderTimelineMarkdownElement(el, item, text, options = {}) { renderTimelineMarkdownIntoElement(el, item, text, options); },
        userMessageSourceQualifier(item) { return userMessageSourceQualifierText(item); },
        userMessageIcon(item) { return userMessageIconClass(item); },
        statusText() { const snapshot = this.activeSnapshot(); return snapshot.StatusText || snapshot.status_text || snapshot.Status || 'idle'; },
        chatInterruptible() {
          const snapshot = this.activeSnapshot();
          return !!(snapshot.Active || snapshot.active);
        },
        interruptArmed() {
          return this.interruptArmedChatID && this.interruptArmedChatID === String(this.activeChatID() || '');
        },
        interruptButtonTitle() {
          if (!this.chatInterruptible()) return 'Koder is idle';
          return this.interruptArmed() ? 'Interrupt immediately' : 'Stop after current turn';
        },
        syncInterruptArmed() {
          if (!this.interruptArmedChatID) return;
          if (!this.chatInterruptible() || this.interruptArmedChatID !== String(this.activeChatID() || '')) {
            this.interruptArmedChatID = '';
          }
        },
        clientDebugState() {
          const transcript = this.transcriptElement();
          const timeline = this.timeline();
          const renderWindow = this.timelineRenderWindowBounds(timeline);
          const renderedTurns = transcript ? transcript.querySelectorAll('.transcript-turn').length : 0;
          const domNodes = transcript ? transcript.querySelectorAll('*').length : 0;
          return {
            selected_session: this.state.session?.id || this.state.session?.ID || this.hydratingSession.id || '',
            selected_chat: String(this.activeChatID() || ''),
            document_visible: !document.hidden,
            window_focused: document.hasFocus(),
            composer_focused: this.$refs.composerInput === document.activeElement,
            viewport_width: window.innerWidth || 0,
            viewport_height: window.innerHeight || 0,
            transcript_scroll_top: transcript ? Math.round(transcript.scrollTop) : 0,
            transcript_scroll_height: transcript ? Math.round(transcript.scrollHeight) : 0,
            transcript_client_height: transcript ? Math.round(transcript.clientHeight) : 0,
            stick_to_bottom: !!this.transcriptStickToBottom,
            timeline_items_loaded: timeline.length,
            timeline_items_rendered: renderedTurns,
            transcript_dom_nodes: domNodes,
            markdown_cache_entries: markdownCacheSize(),
            last_ws_message_bytes: this.lastWSMessageBytes || 0,
            render_window_start: renderWindow.start,
            render_window_end: renderWindow.end,
            render_window_overscan: renderWindow.overscan,
            open_dialog: this.openDialogName(),
            interrupt_visible: this.chatInterruptible(),
            interrupt_armed: !!this.interruptArmed(),
          };
        },
        openDialogName() {
          const modal = this.modalOpenName();
          if (modal) return modal;
          if (this.showAccess) return 'access';
          return '';
        },
        modalOpenName() {
          if (this.imageLightbox?.open) return 'image';
          if (this.showProviderEditor) return 'provider';
          if (this.showModelConfigEditor) return 'model_config';
          if (this.showMCPEditor) return 'mcp';
          if (this.showSessionEditor) return 'session_editor';
          if (this.showModels) return 'models';
          if (this.showSessions) return 'sessions';
          if (this.showSettings) return 'settings';
          return '';
        },
        reportClientStateSoon() {
          if (!this.connected || !this.clientID) return;
          if (this.clientStateTimer) clearTimeout(this.clientStateTimer);
          this.clientStateTimer = setTimeout(() => {
            this.clientStateTimer = null;
            if (!this.connected || !this.clientID) return;
            this.rpcOn(this.ws, 'client_state', this.clientDebugState()).catch(() => {});
          }, 180);
        },
        interruptChat() {
          const id = String(this.activeChatID() || '');
          if (!id || !this.chatInterruptible()) return;
          if (this.interruptArmed()) {
            this.interruptArmedChatID = '';
            this.rpc('stop', {}).catch(() => {});
            return;
          }
          this.interruptArmedChatID = id;
          this.rpc('stop_after_turn', {}).catch(() => {
            if (this.interruptArmedChatID === id) this.interruptArmedChatID = '';
          });
        },
        activeChat() {
          const snapshot = this.activeSnapshot();
          return snapshot.chat || snapshot.Chat || {};
        },
        activeChatRole() {
          const chat = this.activeChat();
          return String(chat.workflow_role || chat.WorkflowRole || chat.role || chat.Role || 'general').trim() || 'general';
        },
        activeChatRoleLabel() {
          return this.activeChatRole().replace(/_/g, ' ');
        },
        activeChatRoleTooltip() {
          const chat = this.activeChat();
          const title = chat.title || chat.Title || '';
          const id = chat.id || chat.ID || this.activeChatID() || '';
          const lines = ['Kind: ' + this.activeChatRoleLabel()];
          if (title) lines.push('Title: ' + title);
          if (id) lines.push('Chat: ' + id);
          return lines.join('\n');
        },
        activeProvider() { return this.activeSnapshot()?.chat?.provider_id || this.activeSnapshot()?.Chat?.ProviderID || this.activeSnapshot()?.chat?.ProviderID || this.state.snapshot?.Chat?.ProviderID || ''; },
        activeModel() { return this.activeSnapshot()?.chat?.model_id || this.activeSnapshot()?.Chat?.ModelID || this.activeSnapshot()?.chat?.ModelID || this.state.snapshot?.Chat?.ModelID || ''; },
        activeModelInfo() { return this.state.model_info || this.state.ModelInfo || {}; },
        formatTokens(value) {
          const n = Number(value || 0);
          if (!Number.isFinite(n) || n <= 0) return 'unknown';
          if (n >= 1000) return (n / 1000).toFixed(n >= 100000 ? 0 : 1).replace(/\.0$/, '') + 'K';
          return String(Math.round(n));
        },
        formatContextTokens(value) {
          const n = Number(value || 0);
          if (!Number.isFinite(n) || n < 0) return 'unknown';
          if (n === 0) return '0';
          return this.formatTokens(n);
        },
        capabilityLabel(value, known) {
          if (value) return 'yes';
          return known ? 'no' : 'unknown';
        },
        activeModelTooltip() {
          const info = this.activeModelInfo();
          const contextWindow = info.context_window || info.ContextWindow || this.state.context_window || this.state.ContextWindow || 0;
          const known = !!(info.capabilities_known || info.CapabilitiesKnown);
          const source = info.capability_source || info.CapabilitySource || '';
          const lines = [
            'Context: ' + this.formatTokens(contextWindow) + ' tokens',
            'Chat: ' + (info.supports_chat === false || info.SupportsChat === false ? 'no' : 'yes'),
            'TTS: ' + this.capabilityLabel(info.supports_tts || info.SupportsTTS, known),
            'Tools: ' + (info.supports_tools === false || info.SupportsTools === false ? 'no' : 'yes'),
            'Images: ' + this.capabilityLabel(info.supports_images || info.SupportsImages, known),
            'PDFs: ' + this.capabilityLabel(info.supports_pdfs || info.SupportsPDFs, known),
          ];
          if (source) lines.push('Source: ' + source);
          return lines.join('\n');
        },
        activeContextTokens() {
          const snapshot = this.activeSnapshot();
          const context = snapshot.Context || snapshot.context || {};
          const total = context.TotalTokens || context.total_tokens || 0;
          if (total > 0) return total;
          const chat = this.activeChat();
          if (chat.ContextTokensKnown || chat.context_tokens_known) {
            return chat.LastKnownContextTokens || chat.last_known_context_tokens || 0;
          }
          return 0;
        },
        usageField(usage, pascal, snake) {
          return Number((usage && (usage[pascal] ?? usage[snake])) || 0);
        },
        activeTokenUsage() {
          const snapshot = this.activeSnapshot();
          const chat = this.activeChat();
          return snapshot.TokenUsage || snapshot.token_usage || chat.TokenUsage || chat.token_usage || {};
        },
        activeTokenUsageLabel() {
          const usage = this.activeTokenUsage();
          const total = this.usageField(usage, 'TotalTokens', 'total_tokens');
          const prompt = this.usageField(usage, 'PromptTokens', 'prompt_tokens');
          const completion = this.usageField(usage, 'CompletionTokens', 'completion_tokens');
          if (!total && !prompt && !completion) return '0 used since compact';
          const parts = [];
          if (total) parts.push(this.formatContextTokens(total) + ' total');
          if (prompt) parts.push(this.formatContextTokens(prompt) + ' in');
          if (completion) parts.push(this.formatContextTokens(completion) + ' out');
          return parts.join(' · ');
        },
        activeCachedTokenLabel() {
          const cached = this.usageField(this.activeTokenUsage(), 'CachedTokens', 'cached_tokens');
          return cached ? this.formatContextTokens(cached) + ' cached' : '0 cached';
        },
        activeContextWindow() {
          const info = this.activeModelInfo();
          return info.context_window || info.ContextWindow || this.state.context_window || this.state.ContextWindow || 0;
        },
        activeContextPercent() {
          const windowSize = this.activeContextWindow();
          const tokens = this.activeContextTokens();
          if (!windowSize || !tokens) return 0;
          return Math.max(0, Math.min(999, Math.round((tokens / windowSize) * 100)));
        },
        activeContextFillPercent() {
          return Math.max(0, Math.min(100, this.activeContextPercent()));
        },
        activeContextLabel() {
          if (!this.activeContextTokens()) return 'Context unknown';
          return 'Context ' + this.activeContextPercent() + '%';
        },
        activeContextTokenLabel() {
          if (!this.activeContextTokens()) return 'unknown / ' + this.formatTokens(this.activeContextWindow());
          return this.formatContextTokens(this.activeContextTokens()) + ' / ' + this.formatTokens(this.activeContextWindow());
        },
        activeContextTooltip() {
          const tokens = this.activeContextTokens();
          const windowSize = this.activeContextWindow();
          if (!tokens) return 'Context: unknown\nKoder only shows provider-reported context usage from successful turns.';
          const pct = this.activeContextPercent();
          const lines = ['Context: ' + this.formatContextTokens(tokens) + ' / ' + this.formatTokens(windowSize) + ' tokens (' + pct + '%)'];
          if (windowSize > 0) lines.push('Remaining: ' + this.formatContextTokens(Math.max(0, windowSize - tokens)) + ' tokens');
          const usage = this.activeTokenUsage();
          const total = this.usageField(usage, 'TotalTokens', 'total_tokens');
          const prompt = this.usageField(usage, 'PromptTokens', 'prompt_tokens');
          const completion = this.usageField(usage, 'CompletionTokens', 'completion_tokens');
          const cached = this.usageField(usage, 'CachedTokens', 'cached_tokens');
          lines.push('Token burn since compact: ' + this.formatContextTokens(total) + ' total, ' + this.formatContextTokens(prompt) + ' input, ' + this.formatContextTokens(completion) + ' output, ' + this.formatContextTokens(cached) + ' cached');
          return lines.join('\n');
        },
        activeContextStyle() {
          return 'width: ' + this.activeContextFillPercent() + '%;';
        },
        activeContextClass() {
          const pct = this.activeContextPercent();
          if (pct >= 90) return 'context-danger';
          if (pct >= 75) return 'context-warn';
          return '';
        },
        milestones() { return this.state.milestones || this.state.Milestones || {}; },
        milestoneItems() { return this.milestones().milestones || this.milestones().Milestones || []; },
        visibleMilestones() {
          const items = this.milestoneItems();
          return items.filter(milestone => this.milestoneStatusFilterEnabled(this.milestoneStatus(milestone)));
        },
        visibleMilestoneTree() {
          const items = this.visibleMilestones();
          const byKey = new Map();
          const nodes = items.map(milestone => ({milestone, children: [], depth: 0, orphan: false}));
          for (const node of nodes) {
            const milestoneKey = this.milestoneKey(node.milestone);
            if (milestoneKey) byKey.set(milestoneKey, node);
          }
          const roots = [];
          for (const node of nodes) {
            const parentKey = this.milestoneDependsOnKey(node.milestone);
            const parent = parentKey ? byKey.get(parentKey) : null;
            if (parent && parent !== node) {
              parent.children.push(node);
              continue;
            }
            node.orphan = !!parentKey && !parent;
            roots.push(node);
          }
          return roots;
        },
        flattenedMilestones() {
          const out = [];
          const visit = (node, depth, seen) => {
            const milestoneKey = this.milestoneKey(node.milestone);
            if (milestoneKey && seen.has(milestoneKey)) return;
            const nextSeen = new Set(seen);
            if (milestoneKey) nextSeen.add(milestoneKey);
            out.push({...node, depth});
            for (const child of node.children) visit(child, depth + 1, nextSeen);
          };
          for (const node of this.visibleMilestoneTree()) visit(node, 0, new Set());
          return out;
        },
        closedMilestoneCount() {
          return this.milestoneItems().filter(milestone => {
            const status = this.milestoneStatus(milestone);
            return status === 'completed' || status === 'cancelled';
          }).length;
        },
        milestoneStatusFilterOptions() {
          return this.statusFilterOptions(this.milestoneItems().map(milestone => this.milestoneStatus(milestone)), this.milestoneFilterStatuses(), status => ({
            status,
            label: this.milestoneStatusLabel(status),
            icon: this.milestoneIcon(status),
            count: 0,
          }));
        },
        milestoneStatusLabel(status) {
          const value = String(status || '').trim();
          if (value === 'completed') return 'Completed';
          return this.statusLabel(value);
        },
        milestoneFilterStatuses() {
          return ['executing', 'decomposing', 'ready', 'pending', 'completed', 'blocked', 'cancelled'];
        },
        milestoneStatusFilterEnabled(status) {
          return !this.hiddenMilestoneStatuses[String(status || 'pending')];
        },
        toggleMilestoneStatusFilter(status) {
          const key = String(status || 'pending');
          this.hiddenMilestoneStatuses = {...this.hiddenMilestoneStatuses, [key]: !this.hiddenMilestoneStatuses[key]};
          writeJSONPreference('hiddenMilestoneStatuses', this.hiddenMilestoneStatuses);
        },
        milestoneStatusFilterClass(status) {
          const key = String(status || 'pending');
          return {
            ['status-filter-' + key.replaceAll('_', '-')]: true,
            active: this.milestoneStatusFilterEnabled(key),
          };
        },
        milestoneStatusFilterTitle(filter) {
          return (this.milestoneStatusFilterEnabled(filter.status) ? 'Hide ' : 'Show ') + filter.label + ' milestones';
        },
        milestoneSummary() { return this.milestones().summary || this.milestones().Summary || ''; },
        milestoneKey(m) { return m.Key || m.key || ''; },
        milestoneTitle(m) { return m.Title || m.title || this.milestoneKey(m); },
        milestoneStatus(m) { return m.Status || m.status || 'pending'; },
        milestoneNotes(m) { return m.Notes || m.notes || ''; },
        milestoneDependsOnKey(m) { return m.DependsOnKey || m.depends_on_key || ''; },
        milestoneTreeTitle(node) {
          const notes = this.milestoneNotes(node.milestone);
          if (!node.orphan) return notes;
          const parentKey = this.milestoneDependsOnKey(node.milestone);
          return (notes ? notes + '\n' : '') + 'Depends on hidden or missing milestone ' + parentKey;
        },
        milestoneExpanded(milestoneKey) { return !!this.expandedMilestones[milestoneKey]; },
        toggleMilestone(milestoneKey) {
          if (!milestoneKey) return;
          this.expandedMilestones = {...this.expandedMilestones, [milestoneKey]: !this.expandedMilestones[milestoneKey]};
          this.writeMilestoneExpansion();
        },
        milestoneIcon(status) {
          if (status === 'completed') return 'bi-check-circle-fill text-success';
          if (status === 'cancelled') return 'bi-x-circle-fill text-secondary';
          if (status === 'blocked') return 'bi-exclamation-octagon-fill text-danger';
          if (status === 'decomposing' || status === 'executing') return 'bi-arrow-repeat text-primary';
          if (status === 'ready') return 'bi-play-circle text-info';
          return 'bi-circle text-secondary';
        },
        milestoneBadge(status) {
          if (status === 'completed') return 'planning-badge-completed';
          if (status === 'cancelled') return 'planning-badge-cancelled';
          if (status === 'blocked') return 'planning-badge-blocked';
          if (status === 'decomposing') return 'planning-badge-decomposing';
          if (status === 'executing') return 'planning-badge-executing';
          if (status === 'ready') return 'planning-badge-ready';
          return 'planning-badge-pending';
        },
        taskItems() { return this.state.tasks || this.state.Tasks || []; },
        tasksByMilestone() { return this.state.tasks_by_milestone || this.state.TasksByKey || {}; },
        taskItemsForMilestone(milestone) {
          const milestoneKey = this.milestoneKey(milestone);
          const grouped = this.tasksByMilestone();
          if (grouped && Object.prototype.hasOwnProperty.call(grouped, milestoneKey)) return grouped[milestoneKey] || [];
          return [];
        },
        milestoneTaskCounts(milestone) {
          const counts = {total: 0, completed: 0, active: 0, failed: 0, cancelled: 0, pending: 0};
          for (const task of this.taskItemsForMilestone(milestone)) {
            counts.total++;
            const status = this.taskStatus(task);
            if (status === 'completed') counts.completed++;
            else if (status === 'in_progress') counts.active++;
            else if (status === 'failed' || status === 'blocked') counts.failed++;
            else if (status === 'cancelled') counts.cancelled++;
            else counts.pending++;
          }
          return counts;
        },
        milestoneTaskSummary(milestone) {
          const counts = this.milestoneTaskCounts(milestone);
          if (!counts.total) return '0 tasks';
          const details = [];
          if (counts.active) details.push(counts.active + ' active');
          if (counts.failed) details.push(counts.failed + ' failed');
          if (counts.cancelled) details.push(counts.cancelled + ' cancelled');
          if (counts.pending) details.push(counts.pending + ' pending');
          const suffix = details.length ? ' · ' + details.join(' · ') : '';
          return counts.completed + '/' + counts.total + ' done' + suffix;
        },
        milestoneProgressPercent(milestone, key) {
          const counts = this.milestoneTaskCounts(milestone);
          if (!counts.total) return key === 'pending' ? 100 : 0;
          return Math.max(0, Math.min(100, ((counts[key] || 0) / counts.total) * 100));
        },
        milestoneProgressStyle(milestone, key) {
          return 'flex-basis: ' + this.milestoneProgressPercent(milestone, key).toFixed(2) + '%;';
        },
        milestoneProgressTitle(milestone) {
          const counts = this.milestoneTaskCounts(milestone);
          if (!counts.total) return '0 tasks';
          const parts = [
            counts.completed + ' done',
            counts.active + ' active',
            counts.failed + ' failed',
            counts.cancelled + ' cancelled',
            counts.pending + ' pending',
          ];
          return parts.join(' · ');
        },
        taskStatus(task) { return task.Status || task.status || 'pending'; },
        taskNote(task) { return String(task?.Note || task?.note || '').trim(); },
        taskTitle(task) {
          const content = String(task?.Content || task?.content || '').trim();
          const note = this.taskNote(task);
          return note ? content + '\n' + note : content;
        },
        taskIcon(status) {
          if (status === 'completed') return 'bi-check-circle-fill text-success';
          if (status === 'in_progress') return 'bi-arrow-repeat text-primary';
          if (status === 'failed' || status === 'blocked') return 'bi-exclamation-octagon-fill text-danger';
          if (status === 'cancelled') return 'bi-x-circle-fill text-secondary';
          return 'bi-circle text-secondary';
        },
        taskBadge(status) {
          if (status === 'completed') return 'planning-badge-completed';
          if (status === 'in_progress') return 'planning-badge-executing';
          if (status === 'failed' || status === 'blocked') return 'planning-badge-blocked';
          if (status === 'cancelled') return 'planning-badge-cancelled';
          return 'planning-badge-pending';
        },
        chatID(chat) { return String(chat?.ID || chat?.id || '').trim(); },
        chatTitle(chat) {
          const title = String(chat?.Title || chat?.title || '').trim();
          return title || 'Chat';
        },
        chatByID(id) {
          const target = String(id || '').trim();
          return (this.state.chats || this.state.Chats || []).find(chat => this.chatID(chat) === target) || null;
        },
        chatArchived(chat) { return !!(chat?.Archived || chat?.archived); },
        visibleChats() {
          const chats = this.state.chats || this.state.Chats || [];
          return chats.filter(chat => this.chatStatusFilterEnabled(this.chatFilterStatus(chat)));
        },
        archivedChatCount() {
          return (this.state.chats || this.state.Chats || []).filter(chat => this.chatArchived(chat)).length;
        },
        chatStatusFilterOptions() {
          return this.statusFilterOptions((this.state.chats || this.state.Chats || []).map(chat => this.chatFilterStatus(chat)), this.chatFilterStatuses(), status => ({
            status,
            label: status === 'archived' ? 'Archived' : this.statusLabel(status),
            icon: status === 'archived' ? 'bi-archive' : this.chatStatusIconForValue(status),
            count: 0,
          }));
        },
        chatFilterStatuses() {
          return ['running_tools', 'streaming_response', 'streaming_thoughts', 'waiting_llm', 'waiting_approval', 'idle', 'error', 'archived'];
        },
        chatFilterStatus(chat) {
          if (this.chatArchived(chat)) return 'archived';
          return this.chatStatusValue(chat);
        },
        chatStatusFilterEnabled(status) {
          return !this.hiddenChatStatuses[String(status || 'idle')];
        },
        toggleChatStatusFilter(status) {
          const key = String(status || 'idle');
          this.hiddenChatStatuses = {...this.hiddenChatStatuses, [key]: !this.hiddenChatStatuses[key]};
          writeJSONPreference('hiddenChatStatuses', this.hiddenChatStatuses);
        },
        chatStatusFilterTitle(filter) {
          return (this.chatStatusFilterEnabled(filter.status) ? 'Hide ' : 'Show ') + filter.label + ' chats';
        },
        chatSnapshot(chat) {
          const id = this.chatID(chat);
          const snapshots = this.state.snapshots || this.state.Snapshots || {};
          return snapshots[id] || snapshots[String(id)] || {};
        },
        chatContextTokens(chat) {
          const snapshot = this.chatSnapshot(chat);
          const context = snapshot.Context || snapshot.context || {};
          const liveTotal = context.TotalTokens || context.total_tokens || 0;
          if (liveTotal > 0) return liveTotal;
          if (chat.ContextTokensKnown || chat.context_tokens_known) {
            return chat.LastKnownContextTokens || chat.last_known_context_tokens || 0;
          }
          return 0;
        },
        chatContextWindow() {
          const info = this.activeModelInfo();
          return info.context_window || info.ContextWindow || this.state.context_window || this.state.ContextWindow || 0;
        },
        chatContextPercent(chat) {
          const windowSize = this.chatContextWindow();
          const tokens = this.chatContextTokens(chat);
          if (!windowSize || !tokens) return 0;
          return Math.max(0, Math.min(999, Math.round((tokens / windowSize) * 100)));
        },
        chatContextLabel(chat) {
          if (!this.chatContextTokens(chat)) return '(ctx unknown)';
          const pct = this.chatContextPercent(chat);
          return '(' + pct + '% ctx)';
        },
        chatContextTooltip(chat) {
          const tokens = this.chatContextTokens(chat);
          const windowSize = this.chatContextWindow();
          if (!tokens) return 'Context: unknown\nKoder only shows provider-reported context usage from successful turns.';
          const pct = this.chatContextPercent(chat);
          const lines = ['Context: ' + this.formatContextTokens(tokens) + ' / ' + this.formatTokens(windowSize) + ' tokens (' + pct + '%)'];
          if (windowSize > 0) {
            lines.push('Remaining: ' + this.formatContextTokens(Math.max(0, windowSize - tokens)) + ' tokens');
          }
          const provider = this.activeProvider();
          const model = this.activeModel();
          if (provider || model) lines.push('Model: ' + [provider, model].filter(Boolean).join(' / '));
          return lines.join('\n');
        },
        chatStatus(chat) {
          const id = this.chatID(chat);
          const statuses = this.state.chat_statuses || this.state.ChatStatuses || [];
          return statuses.find(status => (status.chat_id || status.ChatID) === id) || {chat_id: id, status: 'idle', status_text: 'Idle'};
        },
        chatPendingApprovals(chat) {
          const status = this.chatStatus(chat);
          const value = status.pending_approvals ?? status.PendingApprovals ?? 0;
          return Number(value) || 0;
        },
        chatStatusValue(chat) {
          if (this.chatPendingApprovals(chat) > 0) return 'waiting_approval';
          const status = this.chatStatus(chat);
          return String(status.status || status.Status || 'idle');
        },
        chatStatusLabel(chat) {
          const pending = this.chatPendingApprovals(chat);
          if (pending > 0) return pending === 1 ? 'Waiting for approval' : 'Waiting for ' + pending + ' approvals';
          const status = this.chatStatus(chat);
          const text = status.status_text || status.StatusText || '';
          if (text) return text;
          const value = this.chatStatusValue(chat);
          const labels = {
            idle: 'Idle',
            waiting_llm: 'Waiting for LLM',
            streaming_thoughts: 'Streaming reasoning',
            streaming_response: 'Streaming response',
            running_tools: 'Running tools',
            waiting_approval: 'Waiting for approval',
            running: 'Running',
            completed: 'Completed',
            failed: 'Failed',
            cancelled: 'Cancelled',
            error: 'Error',
          };
          return labels[value] || value.replaceAll('_', ' ');
        },
        statusLabel(status) {
          const value = String(status || '').trim();
          const labels = {
            idle: 'Idle',
            waiting_llm: 'Waiting',
            streaming_thoughts: 'Reasoning',
            streaming_response: 'Streaming',
            running_tools: 'Tools',
            waiting_approval: 'Approval',
            running: 'Running',
            pending: 'Pending',
            ready: 'Ready',
            decomposing: 'Decomposing',
            executing: 'Executing',
            completed: 'Done',
            failed: 'Failed',
            blocked: 'Blocked',
            cancelled: 'Cancelled',
            error: 'Error',
          };
          return labels[value] || value.replaceAll('_', ' ');
        },
        statusFilterOptions(statuses, baseline, build) {
          const counts = new Map();
          for (const raw of statuses) {
            const status = String(raw || '').trim();
            if (!status) continue;
            counts.set(status, (counts.get(status) || 0) + 1);
          }
          const keys = new Set(Array.isArray(baseline) ? baseline.map(status => String(status || '').trim()).filter(Boolean) : []);
          for (const status of counts.keys()) keys.add(status);
          return Array.from(keys).sort((a, b) => this.statusSortIndex(a) - this.statusSortIndex(b) || a.localeCompare(b)).map(status => {
            const option = build(status);
            option.count = counts.get(status) || 0;
            return option;
          });
        },
        statusSortIndex(status) {
          const order = ['running', 'waiting_llm', 'streaming_thoughts', 'streaming_response', 'running_tools', 'waiting_approval', 'executing', 'decomposing', 'ready', 'pending', 'idle', 'completed', 'failed', 'blocked', 'cancelled', 'error', 'archived'];
          const idx = order.indexOf(String(status || ''));
          return idx >= 0 ? idx : order.length;
        },
        chatStatusClass(chat) {
          const value = this.chatStatusValue(chat).replaceAll('_', '-');
          return 'status-' + value;
        },
        chatStatusIcon(chat) {
          return this.chatStatusIconForValue(this.chatStatusValue(chat));
        },
        chatStatusIconForValue(value) {
          if (value === 'waiting_approval') return 'bi-exclamation-triangle-fill';
          if (value === 'error' || value === 'failed') return 'bi-exclamation-triangle-fill';
          if (value === 'cancelled') return 'bi-x-circle-fill';
          if (value === 'completed') return 'bi-check-circle-fill';
          if (value === 'running_tools') return 'bi-tools';
          if (value === 'waiting_llm') return 'bi-hourglass-split';
          if (value === 'streaming_response' || value === 'streaming_thoughts' || value === 'running') return 'bi-arrow-repeat';
          return 'bi-circle';
        },
        gitStatus() { return this.state.workspace_status || this.state.Workspace || {}; },
        gitFiles() { return this.gitStatus().files || this.gitStatus().Files || []; },
        gitTreeRows() {
          const root = {kind: 'dir', name: '', path: '', children: new Map(), file: {additions: 0, deletions: 0, files: 0}};
          for (const file of this.gitFiles()) {
            const path = String(file.path || file.Path || '').trim().replace(/^\/+/, '').replace(/\/+$/, '');
            if (!path) continue;
            const parts = path.split('/').filter(Boolean);
            let node = root;
            parts.forEach((part, idx) => {
              const isLeaf = idx === parts.length - 1;
              const childPath = parts.slice(0, idx + 1).join('/');
              if (!node.children.has(part)) {
                node.children.set(part, {kind: isLeaf ? 'file' : 'dir', name: part, path: childPath, children: new Map(), file: {path: childPath, additions: 0, deletions: 0, files: 0}});
              }
              const child = node.children.get(part);
              if (isLeaf) {
                child.kind = 'file';
                child.file = file;
              }
              node = child;
            });
          }
          this.aggregateGitTreeNode(root);
          const rows = [];
          const walk = (node, depth) => {
            const children = Array.from(node.children.values()).sort((a, b) => {
              if (a.kind !== b.kind) return a.kind === 'dir' ? -1 : 1;
              return a.name.localeCompare(b.name);
            });
            for (const child of children) {
              rows.push({key: child.kind + ':' + child.path, kind: child.kind, name: child.name, path: child.path, depth, file: child.file});
              if (child.kind === 'dir') walk(child, depth + 1);
            }
          };
          walk(root, 0);
          return rows;
        },
        aggregateGitTreeNode(node) {
          if (!node || node.kind !== 'dir') return node?.file || {};
          const summary = {path: node.path, code: '', additions: 0, deletions: 0, files: 0};
          for (const child of node.children.values()) {
            const file = child.kind === 'dir' ? this.aggregateGitTreeNode(child) : child.file;
            summary.additions += this.gitAdditions(file);
            summary.deletions += this.gitDeletions(file);
            summary.files += child.kind === 'dir' ? this.gitFileCount(file) : 1;
          }
          node.file = summary;
          return summary;
        },
        gitFileCode(file) { return file.code || file.Code || ''; },
        gitAdditions(file) { return file.additions ?? file.Additions ?? 0; },
        gitDeletions(file) { return file.deletions ?? file.Deletions ?? 0; },
        gitFileCount(file) { return file.files ?? file.Files ?? 0; },
        gitFileStatsText(file) {
          const parts = [];
          const additions = this.gitAdditions(file);
          const deletions = this.gitDeletions(file);
          const files = this.gitFileCount(file);
          if (additions) parts.push('+' + additions);
          if (deletions) parts.push('-' + deletions);
          if (files > 1) parts.push(files + ' files');
          return parts.join(' ');
        },
        gitFileClass(file) {
          const code = this.gitFileCode(file);
          if (code === '??') return 'git-untracked';
          if (code.includes('D')) return 'git-deleted';
          if (code.includes('A')) return 'git-added';
          if (code.includes('M') || code.includes('R') || code.includes('C')) return 'git-modified';
          return '';
        },
        gitCodeBadge(file) {
          const code = this.gitFileCode(file);
          if (code === '??') return 'text-bg-info';
          if (code.includes('D')) return 'text-bg-danger';
          if (code.includes('A')) return 'text-bg-success';
          if (code.includes('M') || code.includes('R') || code.includes('C')) return 'text-bg-warning';
          return 'text-bg-secondary';
        },
        refreshWorkspace() {
          this.rpc('refresh_workspace', {}).then(result => {
            const status = result?.workspace_status || result?.WorkspaceStatus || result?.workspace || result?.Workspace;
            if (status !== undefined) this.applyWorkspaceDelta({workspace_status: status});
          }).catch(err => this.showToast(err.message || 'refresh workspace failed'));
          this.closeMobileSidebar();
        },
        toolIcon(kind) {
          if (kind === 'file_read' || kind === 'file_write' || kind === 'file_edit') return 'bi-file-earmark-code';
          if (kind === 'bash' || String(kind || '').startsWith('exec_')) return 'bi-terminal';
          if (kind === 'file_grep' || kind === 'file_glob' || kind === 'websearch') return 'bi-search';
          if (kind === 'webfetch') return 'bi-globe';
          if (kind === 'view_image' || kind === 'show_image') return 'bi-image';
          return 'bi-wrench-adjustable';
        },
        toolTitle(tool) { return toolTitleText(tool); },
        toolPreview(tool) { return toolPreviewText(tool); },
        toolStatusBadge(tool) { return toolStatusBadgeText(tool); },
        toolStatusBadgeClass(tool) { return toolStatusBadgeClassName(tool); },
        toolCallID(tool) { return tool?.tool_call_id || tool?.ToolCallID || ''; },
        toolCommandInspectable(tool) {
          return String((tool && tool.tool) || '') === 'exec_command' && this.toolCommandText(tool) !== '';
        },
        toolCommandText(tool) {
          const args = toolArgs(tool);
          const data = toolData(tool);
          return String(firstValue(args, ['cmd', 'command']) || firstValue(data, ['command', 'Command']) || '').trim();
        },
        toolCommandMeta(tool) {
          const args = toolArgs(tool);
          const data = toolData(tool);
          const rows = [];
          const add = (label, value) => {
            const text = String(value ?? '').trim();
            if (text) rows.push({label, value: text});
          };
          add('workdir', firstValue(args, ['workdir']) || firstValue(data, ['workdir', 'Workdir']));
          add('shell', firstValue(args, ['shell']) || firstValue(data, ['shell', 'Shell']));
          const timeout = timeoutLabel(firstValue(args, ['timeout_ms']) || firstValue(data, ['timeout_ms', 'TimeoutMS']));
          const wait = timeoutLabel(firstValue(args, ['yield_time_ms']) || firstValue(data, ['yield_time_ms', 'YieldTimeMS']));
          add('process id', firstValue(data, ['process_id', 'ProcessID']));
          add('state', firstValue(data, ['state', 'State']));
          add('exit code', firstValue(data, ['exit_code', 'ExitCode']));
          if (timeout) add('timeout', timeout);
          if (wait) add('startup wait', wait);
          for (const key of ['tty', 'login']) {
            const value = args[key] ?? data[key] ?? data[key.charAt(0).toUpperCase() + key.slice(1)];
            if (value !== undefined && value !== null && value !== '') add(key, String(value));
          }
          return rows;
        },
        openToolCommandModal(tool) {
          const command = this.toolCommandText(tool);
          if (!command) return;
          const args = toolArgs(tool);
          const comment = String(firstValue(args, ['comment']) || firstValue(toolData(tool), ['comment', 'Comment']) || '').trim();
          this.toolCommandModal = {
            open: true,
            command,
            subtitle: comment || this.toolCallID(tool) || 'Exact command',
            meta: this.toolCommandMeta(tool),
            output: String(firstValue(toolData(tool), ['output', 'Output']) || toolResultText(tool) || toolErrorText(tool) || '').trim(),
          };
        },
        closeToolCommandModal() {
          this.toolCommandModal = {open: false, command: '', subtitle: '', meta: [], output: ''};
        },
        toolApprovalPending(tool) {
          return this.toolCallID(tool) && toolStatus(tool) === 'awaiting_approval';
        },
        toolResultHTML(tool) { return renderToolResult(tool); },
        toolErrorHTML(tool) { return renderToolError(tool); },
        execProcessID(process) { return process?.process_id || process?.ProcessID || ''; },
        execProcessCommand(process) { return process?.command || process?.Command || ''; },
        execProcessState(process) { return String(process?.state || process?.State || '').toLowerCase(); },
        execProcessTerminable(process) { return this.execProcessState(process) === 'running'; },
        execProcessExitCode(process) {
          const value = process?.exit_code ?? process?.ExitCode;
          return value === undefined || value === null ? '' : String(value);
        },
        execProcessTimeout(process) { return timeoutLabel(process?.timeout_ms ?? process?.TimeoutMS); },
        execProcessLabel(process) {
          const id = this.execProcessID(process);
          const state = this.execProcessState(process) || 'unknown';
          const exitCode = this.execProcessExitCode(process);
          return id + ' · ' + (exitCode === '' ? state : state + ' ' + exitCode);
        },
        execProcessClass(process) {
          const state = this.execProcessState(process);
          if (state === 'running') return 'text-bg-primary';
          if (state === 'completed') return 'text-bg-success';
          if (state === 'terminated') return 'text-bg-warning';
          if (state === 'failed' || state === 'lost') return 'text-bg-danger';
          return 'text-bg-secondary';
        },
        execProcessOutput(process) {
          const output = process?.output || process?.Output || '';
          return output || 'No output yet';
        },
        execProcessTooltip(process) {
          const lines = [];
          lines.push(this.execProcessCommand(process) || this.execProcessID(process));
          const workdir = process?.workdir || process?.Workdir || '';
          if (workdir) lines.push(workdir);
          lines.push(this.execProcessLabel(process));
          const timeout = this.execProcessTimeout(process);
          if (timeout) lines.push('timeout: ' + timeout);
          const bytes = process?.output_bytes || process?.OutputBytes || 0;
          if (bytes) lines.push(String(bytes) + ' output bytes captured');
          return lines.filter(Boolean).join('\n');
        },
        showExecProcessTooltip(process, event) {
          this.execHover = {...this.execHover, open: true, title: this.execProcessTooltip(process), output: this.execProcessOutput(process)};
          this.positionExecProcessTooltip(event);
        },
        positionExecProcessTooltip(event) {
          if (!event) return;
          const rect = event.currentTarget?.getBoundingClientRect ? event.currentTarget.getBoundingClientRect() : null;
          const x = rect ? rect.left - 12 : event.clientX;
          const y = rect ? rect.top : event.clientY;
          this.execHover = {...this.execHover, x: Math.max(8, x), y: Math.max(8, y)};
        },
        hideExecProcessTooltip() {
          this.execHover = {...this.execHover, open: false};
        },
        execHoverStyle() {
          const width = Math.min(672, Math.max(280, window.innerWidth - 24));
          const left = Math.max(8, Math.min(this.execHover.x - width, window.innerWidth - width - 8));
          const top = Math.max(8, Math.min(this.execHover.y, window.innerHeight - 120));
          return 'left: ' + left + 'px; top: ' + top + 'px; width: ' + width + 'px;';
        },
        terminateExecProcess(process) {
          const processID = this.execProcessID(process);
          if (!processID || !this.execProcessTerminable(process)) return;
          this.rpc('terminate_exec_process', {chat_id: this.activeChatID(), process_id: processID}).catch(err => this.showToast(err.message || 'terminate process failed'));
        },
        noticeIcon(content) { return noticeIcon(content); },
        noticeLevel(content) { return noticeLevel(content); },
        noticeText(content) { return noticeText(content); },
        noticeDetail(content) { return noticeDetail(content); },
        lintText(content) { return lintText(content); },
        lintFiles(content) { return lintFiles(content); },
        attachmentName(attachment) { return attachment?.name || attachment?.Name || 'image'; },
        attachmentIcon(attachment) {
          const mime = String(attachment?.mime || attachment?.MIME || '').toLowerCase();
          if (mime.startsWith('image/')) return 'bi-image';
          if (mime === 'application/pdf') return 'bi-filetype-pdf';
          return 'bi-paperclip';
        },
        resizeComposer() {
          const el = this.$refs.composerInput; if (!el) return;
          const maxHeight = Math.floor((window.innerHeight || 800) * 0.2);
          el.style.height = 'auto';
          const nextHeight = Math.min(el.scrollHeight, maxHeight);
          el.style.height = nextHeight + 'px';
          el.style.overflowY = el.scrollHeight > maxHeight ? 'auto' : 'hidden';
        },
        onComposerInput() { this.resizeComposer(); this.updateCompletions(); },
        onComposerPaste(ev) {
          const items = Array.from(ev.clipboardData?.items || []);
          const imageItems = items.filter(item => item.kind === 'file' && String(item.type || '').startsWith('image/'));
          if (imageItems.length === 0) {
            this.insertComposerText(ev.clipboardData?.getData('text/plain') || '');
            return;
          }
          imageItems.forEach(item => {
            const file = item.getAsFile();
            if (file) this.uploadComposerImage(file);
          });
        },
        insertComposerText(text) {
          if (!text) return;
          const el = this.$refs.composerInput;
          const start = el ? el.selectionStart ?? this.draft.length : this.draft.length;
          const end = el ? el.selectionEnd ?? start : start;
          this.draft = this.draft.slice(0, start) + text + this.draft.slice(end);
          const cursor = start + text.length;
          this.$nextTick(() => { if (el) { el.focus(); el.setSelectionRange(cursor, cursor); } this.resizeComposer(); this.updateCompletions(); });
        },
        uploadComposerImage(file) {
          const form = new FormData();
          form.append('image', file, file.name || 'clipboard.png');
          fetch('/api/attachments/clipboard-image', {method: 'POST', body: form})
            .then(resp => {
              if (!resp.ok) return resp.text().then(text => { throw new Error(text || resp.statusText); });
              return resp.json();
            })
            .then(draft => {
              this.composerAttachments = [...this.composerAttachments, draft];
              this.showToast('Attached ' + this.attachmentName(draft));
            })
            .catch(err => this.showToast(err.message || 'image paste failed'));
        },
        removeComposerAttachment(attachment) {
          const id = attachment?.id || attachment?.ID;
          this.composerAttachments = this.composerAttachments.filter(item => (item.id || item.ID) !== id);
        },
        queueItemID(item) { return item?.ID || item?.id || ''; },
        queueItemOrigin(item) { return String(item?.Origin || item?.origin || item?.Source || item?.source || 'user').toLowerCase(); },
        queueItemDelivery(item) {
          const delivery = String(item?.Delivery || item?.delivery || '').toLowerCase();
          if (delivery) return delivery;
          const kind = String(item?.Kind || item?.kind || '').toLowerCase();
          if (kind === 'steer') return 'turn_boundary';
          if (kind === 'continue') return 'continue';
          return 'next_turn';
        },
        queueItemKind(item) {
          const origin = this.queueItemOrigin(item).replace('_', ' ');
          const delivery = this.queueItemDelivery(item);
          if (delivery === 'turn_boundary') return origin === 'user' ? 'user steer' : origin;
          if (delivery === 'continue') return 'continue';
          return origin;
        },
        queueItemKindToggleTitle(item) {
          const delivery = this.queueItemDelivery(item);
          if (delivery === 'turn_boundary') return 'Convert queued steer to normal queued user message';
          if (delivery === 'next_turn') return 'Convert queued user message to steer';
          return 'Message type cannot be changed';
        },
        queueItemText(item) { return String(item?.Text || item?.text || item?.Note || item?.note || ''); },
        queueItemPreview(item) {
          const text = this.queueItemText(item).replace(/\s+/g, ' ').trim();
          if (text) return text;
          const count = this.queueAttachmentCount(item);
          if (count > 0) return count + ' attachment' + (count === 1 ? '' : 's');
          return this.queueItemKind(item);
        },
        queueItemTooltip(item) {
          const parts = [this.queueItemKind(item)];
          const text = this.queueItemText(item).trim();
          if (text) parts.push(text);
          const count = this.queueAttachmentCount(item);
          if (count > 0) parts.push(count + ' attachment' + (count === 1 ? '' : 's'));
          return parts.join('\n');
        },
        queueItemAttachments(item) { return item?.Attachments || item?.attachments || []; },
        queueAttachmentCount(item) { return this.queueItemAttachments(item).length; },
        queueAttachmentDraft(attachment) {
          return {
            id: attachment?.id || attachment?.ID || '',
            ID: attachment?.ID || attachment?.id || '',
            name: attachment?.name || attachment?.Name || '',
            Name: attachment?.Name || attachment?.name || '',
            mime: attachment?.mime || attachment?.MIME || '',
            MIME: attachment?.MIME || attachment?.mime || '',
            path: attachment?.path || attachment?.Path || '',
            Path: attachment?.Path || attachment?.path || '',
            size: attachment?.size || attachment?.Size || 0,
            Size: attachment?.Size || attachment?.size || 0,
            source: attachment?.source || attachment?.Source || '',
            Source: attachment?.Source || attachment?.source || '',
            original: attachment?.original || attachment?.Original || '',
            Original: attachment?.Original || attachment?.original || ''
          };
        },
        setActiveQueue(items) {
          const id = String(this.activeChatID() || '');
          if (!id) return;
          const snapshots = {...(this.state.snapshots || this.state.Snapshots || {})};
          const snapshot = {...(snapshots[id] || snapshots[String(id)] || this.state.snapshot || this.state.Snapshot || {})};
          snapshot.QueuedInputs = items.slice();
          snapshot.queued_inputs = items.slice();
          snapshots[id] = snapshot;
          snapshots[String(id)] = snapshot;
          this.state.snapshots = snapshots;
          this.state.Snapshots = snapshots;
          this.state.snapshot = snapshot;
          this.state.Snapshot = snapshot;
        },
        deleteQueueItem(item) {
          const id = this.queueItemID(item);
          if (!id) return;
          const previous = this.activeQueue().slice();
          this.setActiveQueue(previous.filter(existing => this.queueItemID(existing) !== id));
          this.rpc('delete_queue_item', {id}).catch(err => {
            this.showToast(err.message);
            this.setActiveQueue(previous);
          });
        },
        toggledQueueItemKind(item) {
          const delivery = this.queueItemDelivery(item);
          if (delivery === 'turn_boundary') {
            return {...item, Kind: 'queued', kind: 'queued', Delivery: 'next_turn', delivery: 'next_turn'};
          }
          if (delivery === 'next_turn') {
            return {...item, Kind: 'steer', kind: 'steer', Delivery: 'turn_boundary', delivery: 'turn_boundary'};
          }
          return item;
        },
        toggleQueueItemKind(item) {
          const id = this.queueItemID(item);
          if (!id) return;
          const previous = this.activeQueue().slice();
          const updated = previous.map(existing => this.queueItemID(existing) === id ? this.toggledQueueItemKind(existing) : existing);
          this.setActiveQueue(updated);
          this.rpc('toggle_queue_item_kind', {id}).catch(err => {
            this.showToast(err.message);
            this.setActiveQueue(previous);
          });
        },
        moveQueueItemToComposer(item) {
          const text = this.queueItemText(item);
          if (text) {
            const prefix = this.draft.trim() ? this.draft.replace(/\s+$/, '') + '\n' : '';
            this.draft = prefix + text;
          }
          const attachments = this.queueItemAttachments(item).map(attachment => this.queueAttachmentDraft(attachment));
          if (attachments.length > 0) this.composerAttachments = [...this.composerAttachments, ...attachments];
          this.deleteQueueItem(item);
          this.$nextTick(() => { const el = this.$refs.composerInput; if (el) { el.focus(); el.setSelectionRange(this.draft.length, this.draft.length); } this.resizeComposer(); });
        },
        sendQueueItemNow(item) {
          const id = this.queueItemID(item);
          if (!id) return;
          const previous = this.activeQueue().slice();
          const promoted = {...item, Kind: 'queued', kind: 'queued', Delivery: 'next_turn', delivery: 'next_turn'};
          this.setActiveQueue([promoted, ...previous.filter(existing => this.queueItemID(existing) !== id)]);
          this.rpc('send_queue_item_now', {id}).catch(err => {
            this.showToast(err.message);
            this.setActiveQueue(previous);
          });
        },
        abortAndSendQueueItemNow(item) {
          const id = this.queueItemID(item);
          if (!id) return;
          const previous = this.activeQueue().slice();
          const promoted = {...item, Kind: 'queued', kind: 'queued', Delivery: 'next_turn', delivery: 'next_turn'};
          this.setActiveQueue([promoted, ...previous.filter(existing => this.queueItemID(existing) !== id)]);
          this.rpc('abort_and_send_queue_item_now', {id}).catch(err => {
            this.showToast(err.message);
            this.setActiveQueue(previous);
          });
        },
        toggleComposerSendMenu() {
          this.composerSendMenuOpen = !this.composerSendMenuOpen;
          this.reportClientStateSoon();
        },
        closeComposerSendMenu() {
          if (!this.composerSendMenuOpen) return;
          this.composerSendMenuOpen = false;
          this.reportClientStateSoon();
        },
        startQueueDrag(ev, id) {
          if (!id) return;
          this.dragQueueID = id;
          if (ev.dataTransfer) {
            ev.dataTransfer.effectAllowed = 'move';
            ev.dataTransfer.setData('text/plain', id);
          }
        },
        overQueueDrag(ev, id) {
          const sourceID = this.dragQueueID || (ev.dataTransfer && ev.dataTransfer.getData('text/plain')) || '';
          if (!sourceID || sourceID === id) return;
          if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
        },
        dropQueue(ev, targetID) {
          const sourceID = this.dragQueueID || (ev.dataTransfer && ev.dataTransfer.getData('text/plain')) || '';
          this.dragQueueID = '';
          if (!sourceID || !targetID || sourceID === targetID) return;
          const items = this.activeQueue().slice();
          const from = items.findIndex(item => this.queueItemID(item) === sourceID);
          const to = items.findIndex(item => this.queueItemID(item) === targetID);
          if (from < 0 || to < 0) return;
          const [moved] = items.splice(from, 1);
          items.splice(to, 0, moved);
          const previous = this.activeQueue().slice();
          this.setActiveQueue(items);
          this.rpc('reorder_queue', {ids: items.map(item => this.queueItemID(item))}).catch(err => {
            this.showToast(err.message);
            this.setActiveQueue(previous);
          });
        },
        endQueueDrag() { this.dragQueueID = ''; },
        onComposerKeydown(ev) {
          if (this.completion.items.length > 0) {
            if (ev.key === 'ArrowDown') { ev.preventDefault(); this.completion.selected = Math.min(this.completion.items.length - 1, this.completion.selected + 1); return; }
            if (ev.key === 'ArrowUp') { ev.preventDefault(); this.completion.selected = Math.max(0, this.completion.selected - 1); return; }
            if (ev.key === 'Tab' || ev.key === 'Enter') { ev.preventDefault(); this.acceptCompletion(this.completion.selected); return; }
            if (ev.key === 'Escape') { ev.preventDefault(); this.clearCompletions(); return; }
          }
          if (ev.key === 'Enter' && (ev.metaKey || ev.ctrlKey || ev.altKey)) { ev.preventDefault(); this.send({steer: true}); return; }
          if (ev.key === 'Enter' && !ev.shiftKey) { ev.preventDefault(); this.send(); }
        },
        onComposerKeyup(ev) {
          if (['ArrowDown', 'ArrowUp', 'Tab', 'Enter', 'Escape'].includes(ev.key)) return;
          this.updateCompletions();
        },
        updateCompletions() {
          const el = this.$refs.composerInput; if (!el) return;
          const cursor = el.selectionStart ?? this.draft.length;
          const seq = ++this.completionSeq;
          this.rpc('composer_completions', {text: this.draft, cursor}).then(result => {
            if (seq !== this.completionSeq) return;
            const items = result.items || [];
            this.completion = {kind: result.kind || '', query: result.query || '', start: result.start || 0, end: result.end || cursor, items, selected: 0};
          }).catch(() => this.clearCompletions());
        },
        clearCompletions() { this.completion = {kind: '', query: '', start: 0, end: 0, items: [], selected: 0}; },
        acceptCompletion(index) {
          const item = this.completion.items[index]; if (!item) return;
          const before = this.draft.slice(0, this.completion.start);
          const after = this.draft.slice(this.completion.end);
          const insert = item.insert_text || item.label || '';
          this.draft = before + insert + after;
          const cursor = before.length + insert.length;
          this.clearCompletions();
          this.$nextTick(() => { const el = this.$refs.composerInput; if (el) { el.focus(); el.setSelectionRange(cursor, cursor); } this.resizeComposer(); });
        },
        send(options = {}) {
          this.closeComposerSendMenu();
          const text = this.draft.trim();
          const attachments = this.composerAttachments.slice();
          if (!text && attachments.length === 0) return;
          if (text && attachments.length === 0 && this.handleSlash(text)) {
            this.draft = '';
            this.clearCompletions();
            this.clearComposerDraftStorage();
            this.$nextTick(() => this.resizeComposer());
            return;
          }
          this.preserveComposerDraftDuringSend = true;
          this.draft = '';
          this.composerAttachments = [];
          this.clearCompletions();
          this.writeComposerDraftPayload(text, attachments);
          this.$nextTick(() => this.resizeComposer());
          this.rpc('send_prompt', {text, attachments, steer: !!options.steer})
            .then(() => { this.preserveComposerDraftDuringSend = false; this.clearComposerDraftStorage(); })
            .catch(() => { this.preserveComposerDraftDuringSend = false; this.draft = text; this.composerAttachments = attachments; });
        },
        handleSlash(text) {
          if (text === '/permissions') { this.openAccessDialog(); return true; }
          if (text === '/compact' || text.startsWith('/compact ')) { this.rpc('compact', {instructions: text.slice('/compact'.length).trim()}); return true; }
          if (text === '/chat new') { this.newChat(); return true; }
          if (text === '/model') { this.openModelDialog(); return true; }
          if (text === '/providers') { this.openProviderDialog(); return true; }
          if (text === '/sessions') { this.openSessionDialog(); return true; }
          if (text === '/settings') { this.openSettingsDialog(); return true; }
          if (text.startsWith('/')) { this.error = 'Unknown web command: ' + text; return true; }
          return false;
        },
        switchChat(id, ev) {
          if (!id) return;
          if (this.shouldOpenInNewTab(ev)) {
            this.openURLInNewTab(this.chatURL(id));
            return;
          }
          const target = String(id || '').trim();
          if (!target || target === String(this.activeChatID() || '')) return;
          this.beginSwitchingChat(target);
          this.rpc('switch_chat', {chat_id: target}).then(s => { this.applyState(s, {scrollToBottom: true}); this.writeSelectedChat(); this.syncActiveChatURL(); this.closeMobileSidebar(); }).catch(err => { this.clearSwitchingChat(); this.showToast(err.message); });
        },
        beginSwitchingChat(id) {
          const chat = this.chatByID(id);
          this.switchingChat = {active: true, id, title: chat ? this.chatTitle(chat) : 'Loading chat', startedAt: Date.now()};
          this.reportClientStateSoon();
        },
        clearSwitchingChat() {
          if (!this.switchingChat.active) return;
          this.switchingChat = {active: false, id: '', title: '', startedAt: 0};
          this.reportClientStateSoon();
        },
        newChat() { this.rpc('new_chat', {title: 'Chat'}).then(s => { this.applyState(s, {scrollToBottom: true}); this.writeSelectedChat(); this.syncActiveChatURL(); this.closeMobileSidebar(); }).catch(err => this.showToast(err.message)); },
        renameChat(chat) {
          const id = this.chatID(chat);
          if (!id) return;
          const current = this.chatTitle(chat);
          const title = window.prompt('Rename chat', current);
          if (title === null) return;
          const next = String(title || '').trim();
          if (!next) {
            this.showToast('Chat title is required');
            return;
          }
          if (next === current) return;
          this.rpc('rename_chat', {chat_id: id, title: next})
            .then(s => {
              this.applyState(s);
              this.writeSelectedChat();
              this.syncActiveChatURL();
            })
            .catch(err => this.showToast(err.message));
        },
        startChatDrag(ev, id) {
          if (!id) return;
          this.dragChatID = id;
          if (ev.dataTransfer) {
            ev.dataTransfer.effectAllowed = 'move';
            ev.dataTransfer.setData('text/plain', id);
          }
        },
        overChatDrag(ev, id) {
          const sourceID = this.dragChatID || (ev.dataTransfer && ev.dataTransfer.getData('text/plain')) || '';
          if (!sourceID || sourceID === id) return;
          if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
        },
        dropChat(ev, targetID) {
          const sourceID = this.dragChatID || (ev.dataTransfer && ev.dataTransfer.getData('text/plain')) || '';
          this.dragChatID = '';
          if (!sourceID || !targetID || sourceID === targetID) return;
          const allChats = (this.state.chats || this.state.Chats || []).slice();
          const chats = this.visibleChats().slice();
          const from = chats.findIndex(chat => this.chatID(chat) === sourceID);
          const to = chats.findIndex(chat => this.chatID(chat) === targetID);
          if (from < 0 || to < 0) return;
          const [moved] = chats.splice(from, 1);
          chats.splice(to, 0, moved);
          const visibleIDs = new Set(chats.map(chat => this.chatID(chat)));
          const orderedIDs = chats.map(chat => this.chatID(chat));
          allChats.forEach(chat => {
            const id = this.chatID(chat);
            if (!visibleIDs.has(id)) orderedIDs.push(id);
          });
          const previousChats = allChats.slice();
          this.state.chats = [...chats, ...allChats.filter(chat => !visibleIDs.has(this.chatID(chat)))];
          this.state.Chats = this.state.chats;
          this.rpc('reorder_chats', {chat_ids: orderedIDs})
            .catch(err => {
              this.showToast(err.message);
              this.state.chats = previousChats;
              this.state.Chats = previousChats;
            });
        },
        endChatDrag() { this.dragChatID = ''; },
        deleteChat(id) {
          if (!id || !confirm('Archive this chat?')) return;
          this.rpc('delete_chat', {chat_id: id}).then(s => { this.applyState(s, {scrollToBottom: true}); this.writeSelectedChat(); this.syncActiveChatURL(); }).catch(err => this.showToast(err.message));
        },
        showToast(message) {
          this.toast = message || '';
          if (this.toastTimer) clearTimeout(this.toastTimer);
          this.toastTimer = setTimeout(() => { this.toast = ''; this.toastTimer = null; }, 4500);
        },
        ttsButtonTitle() { return this.ttsEnabled ? 'Disable TTS output' : 'Enable TTS output'; },
        toggleTTSOutput() {
          const enabled = !this.ttsEnabled;
          this.rpc('set_tts_enabled', {enabled}).then(settings => {
            this.applyTTSSettings(settings);
            this.showToast(this.ttsEnabled ? 'TTS output enabled' : 'TTS output disabled');
          });
        },
        applyTTSSettings(settings) {
          if (!settings) return;
          this.ttsSettings = Object.assign({}, this.ttsSettings || {}, settings);
          this.ttsEnabled = !!(settings.enabled || settings.Enabled);
          if (this.settings?.ui) this.settings.ui.tts = Object.assign({}, this.settings.ui.tts || {}, this.ttsSettings);
        },
        maybeSpeakTimelineItem(item, snapshot) {
          if (!this.ttsEnabled || !item || this.snapshotIsStreaming(snapshot)) return;
          const kind = String(item.kind || item.Kind || '').trim();
          if (kind !== 'assistant') return;
          const id = this.timelineItemID(item);
          const content = item.content || item.Content || {};
          const text = String(content.text || content.Text || '').trim();
          if (!id || !text || this.ttsSpokenItems[id] === text) return;
          this.ttsSpokenItems = {...this.ttsSpokenItems, [id]: text};
          this.speakText(text);
        },
        maybeSpeakLatestAssistant(snapshot) {
          const chatID = String(snapshot?.Chat?.ID || snapshot?.Chat?.id || snapshot?.chat?.ID || snapshot?.chat?.id || this.activeChatID() || '').trim();
          const timeline = this.timelineForChat(chatID, snapshot);
          for (let i = timeline.length - 1; i >= 0; i--) {
            const item = timeline[i];
            const kind = String(item?.kind || item?.Kind || '').trim();
            if (kind === 'assistant') {
              this.maybeSpeakTimelineItem(item, snapshot);
              return;
            }
          }
        },
        speakText(text) {
          this.rpc('tts_speech', {text}).then(result => {
            this.playTTSAudioResult(result);
          }).catch(err => this.showToast(err.message || 'TTS failed'));
        },
        playTTSAudioResult(result) {
            const audioBase64 = result.audio_base64 || result.AudioBase64 || '';
            if (!audioBase64) return;
            if (this.ttsAudio) {
              this.ttsAudio.pause();
              this.ttsAudio = null;
            }
            const contentType = result.content_type || result.ContentType || 'application/octet-stream';
            const audio = new Audio('data:' + contentType + ';base64,' + audioBase64);
            this.ttsAudio = audio;
            audio.play().catch(err => this.showToast(err.message || 'TTS playback failed'));
        },
        testTTSOutput() {
          const text = String(this.ttsTestText || '').trim();
          if (!text || this.ttsTestBusy) return;
          this.ttsTestBusy = true;
          const tts = Object.assign({}, this.settings?.ui?.tts || {});
          this.rpc('tts_speech', {text, tts}).then(result => {
            this.playTTSAudioResult(result);
            this.showToast('TTS test played');
          }).catch(err => {
            this.showToast(err.message || 'TTS test failed');
          }).finally(() => {
            this.ttsTestBusy = false;
          });
        },
        activeAccessSettings() { return this.state.access?.settings || this.state.Access?.Settings || {}; },
        accessPresets() { return this.state.access?.presets || this.state.Access?.Presets || []; },
        accessSummary(settings) {
          settings = settings || {};
          return (settings.network ? 'net on' : 'net off') + ', project ' + (settings.project || 'readwrite');
        },
        cloneAccessSettings(settings) {
          const src = settings || {};
          return {
            network: !!src.network,
            project: src.project || 'readwrite',
            home: src.home || 'none',
            root: src.root || 'readonly',
            tmp: src.tmp || 'session',
            mounts: Array.isArray(src.mounts) ? src.mounts.map(m => ({path: m.path || '', mode: m.mode || 'readonly'})) : [],
          };
        },
        openAccessDialog() {
          this.accessDraft = this.cloneAccessSettings(this.activeAccessSettings());
          this.showAccess = true;
          this.showModels = false; this.showSettings = false; this.closeMobileSidebar(); this.reportClientStateSoon();
        },
        closeAccessDialog() { this.showAccess = false; this.reportClientStateSoon(); },
        applyAccessPreset(settings) { this.accessDraft = this.cloneAccessSettings(settings); },
        addAccessMount(settings) {
          if (!settings) return;
          if (!Array.isArray(settings.mounts)) settings.mounts = [];
          settings.mounts.push({path: '', mode: 'readonly'});
        },
        deleteAccessMount(settings, index) {
          if (!settings?.mounts) return;
          settings.mounts.splice(index, 1);
        },
        saveAccessSettings() {
          this.rpc('set_access_settings', this.accessDraft).then(() => { this.closeAccessDialog(); }).catch(err => this.showToast(err.message));
        },
        openModelDialog() {
          this.modelPickerTarget = null;
          this.showModels = true; this.modelQuery = ''; this.modelSettingsStatus = ''; this.modelSettingsStatusKind = 'secondary';
          this.closeMobileSidebar();
          this.reportClientStateSoon();
          this.$nextTick(() => this.$refs.modelSearch?.focus());
          this.refreshModelOptions();
          this.loadActiveModelSettings();
        },
        openSettingsModelPicker(target) {
          this.modelPickerTarget = Object.assign({kind: ''}, target || {});
          this.showModels = true; this.modelQuery = ''; this.modelSettingsDraft = null; this.modelSettingsStatus = ''; this.modelSettingsStatusKind = 'secondary';
          this.reportClientStateSoon();
          this.$nextTick(() => this.$refs.modelSearch?.focus());
          this.refreshModelOptions();
        },
        refreshModelOptions() {
          this.modelLoading = true; this.modelOptions = [];
          this.rpc('list_models', {})
            .then(result => { this.modelOptions = result.models || []; })
            .catch(err => { this.showToast(err.message); })
            .finally(() => { this.modelLoading = false; });
        },
        closeModelDialog() { this.showModels = false; this.modelPickerTarget = null; this.modelSettingsDraft = null; this.modelSettingsStatus = ''; this.reportClientStateSoon(); },
        filteredModels() {
          const q = this.modelQuery.trim().toLowerCase();
          const models = this.modelPickerModels();
          if (!q) return models;
          return models.filter(m => [m.provider_id, m.provider_label, m.model_id, m.source_provider_id, m.source_model_id, m.owned_by].filter(Boolean).join(' ').toLowerCase().includes(q));
        },
        selectModel(model) {
          if (this.modelPickerTarget) {
            this.applyModelPickerSelection(model);
            this.closeModelDialog();
            return;
          }
          if (model && model.supports_chat === false) {
            this.showToast('This model supports TTS output, not chat completions.');
            return;
          }
          this.rpc('set_model', {provider_id: model.provider_id, model_id: model.model_id}).then(() => {
            this.modelOptions = (this.modelOptions || []).map(item => Object.assign({}, item, {current: item.provider_id === model.provider_id && item.model_id === model.model_id}));
            this.loadModelSettings(model.provider_id, model.model_id);
          });
        },
        modelPickerTitle() {
          if (!this.modelPickerTarget) return 'Select model';
          return this.modelPickerTarget.title || 'Select model';
        },
        modelPickerSubtitle() {
          if (!this.modelPickerTarget) return this.activeProvider() + ' / ' + this.activeModel();
          return this.modelPickerTarget.subtitle || 'Settings';
        },
        modelPickerCurrentValue() {
          const target = this.modelPickerTarget;
          if (!target) return JSON.stringify([this.activeProvider() || '', this.activeModel() || '']);
          if (target.kind === 'default') return this.defaultModelValue();
          if (target.kind === 'tts') return this.ttsModelValue();
          if (target.kind === 'compaction') return this.compactionModelValue();
          if (target.kind === 'thinking') return this.thinkingModelValue();
          return '';
        },
        modelPickerModels() {
          const models = this.modelOptions || [];
          const target = this.modelPickerTarget;
          if (target?.kind === 'tts') {
            const current = this.modelPickerCurrentValue();
            return models.filter(model => model.supports_tts || this.modelOptionValue(model) === current);
          }
          if (target?.chatOnly) return models.filter(model => model.supports_chat !== false);
          return models;
        },
        modelPickerModelCurrent(model) {
          if (!model) return false;
          return this.modelOptionValue(model) === this.modelPickerCurrentValue();
        },
        applyModelPickerSelection(model) {
          if (!model || !this.modelPickerTarget) return;
          const value = this.modelOptionValue(model);
          if (this.modelPickerTarget.kind === 'default') this.setDefaultModelValue(value);
          if (this.modelPickerTarget.kind === 'tts') this.setTTSModelValue(value);
          if (this.modelPickerTarget.kind === 'compaction') this.setCompactionModelValue(value);
          if (this.modelPickerTarget.kind === 'thinking') this.setThinkingModelValue(value);
        },
        blankableNumber(value) {
          if (value === null || value === undefined || value === '') return null;
          const number = Number(value);
          return Number.isFinite(number) ? number : null;
        },
        normalizeModelSettingsDraft(raw = {}) {
          return Object.assign({
            original_provider_id: raw.provider_id || '',
            original_model_id: raw.model_id || '',
            provider_id: raw.provider_id || '',
            model_id: raw.model_id || '',
            source_provider_id: raw.source_provider_id || raw.provider_id || '',
            source_model_id: raw.source_model_id || raw.model_id || '',
            custom: !!raw.custom,
            editable: !!raw.editable,
            backing_detected: !!raw.backing_detected,
            context_window: raw.context_window || 32768,
            model_preset: raw.model_preset || 'auto',
            temperature: raw.temperature ?? null,
            top_p: raw.top_p ?? null,
            min_p: raw.min_p ?? null,
            top_k: raw.top_k || 0,
            repeat_penalty: raw.repeat_penalty ?? null,
            thinking_mode: raw.thinking_mode || 'auto',
            thinking_budget: raw.thinking_budget || 0,
            extra_body: raw.extra_body || {},
            extra_body_text: this.formatModelExtraBodyText(raw.extra_body),
          }, raw || {});
        },
        modelSettingsEditable() {
          return !!(this.modelSettingsDraft && (this.modelSettingsDraft.editable || this.modelSettingsDraft.custom));
        },
        uniqueCustomModelID(providerID, sourceModelID) {
          const base = String(sourceModelID || 'model').trim() + ' custom';
          const used = new Set([...(this.modelOptions || []), ...(this.settings?.models || []), ...(this.modelConfigRows() || [])]
            .filter(item => item.provider_id === providerID)
            .map(item => String(item.model_id || '').trim())
            .filter(Boolean));
          if (!used.has(base)) return base;
          for (let idx = 2; idx < 1000; idx++) {
            const next = base + ' ' + idx;
            if (!used.has(next)) return next;
          }
          return base + ' ' + Date.now();
        },
        customizeModelSettings() {
          if (!this.modelSettingsDraft) return;
          const sourceProviderID = String(this.modelSettingsDraft.source_provider_id || this.modelSettingsDraft.provider_id || '').trim();
          const sourceModelID = String(this.modelSettingsDraft.source_model_id || this.modelSettingsDraft.model_id || '').trim();
          this.modelSettingsDraft = Object.assign({}, this.modelSettingsDraft, {
            original_provider_id: '',
            original_model_id: '',
            provider_id: sourceProviderID,
            model_id: this.uniqueCustomModelID(sourceProviderID, sourceModelID),
            source_provider_id: sourceProviderID,
            source_model_id: sourceModelID,
            custom: true,
            editable: true,
          });
          this.modelSettingsStatus = 'Customize this model under a new name, then save.'; this.modelSettingsStatusKind = 'secondary';
        },
        activeModelSettingsKey() {
          const info = this.activeModelInfo();
          return {provider_id: info.provider_id || this.activeProvider(), model_id: info.model_id || this.activeModel()};
        },
        loadActiveModelSettings() {
          const key = this.activeModelSettingsKey();
          if (!key.provider_id || !key.model_id) return;
          this.loadModelSettings(key.provider_id, key.model_id);
        },
        loadModelSettings(providerID, modelID) {
          providerID = String(providerID || '').trim(); modelID = String(modelID || '').trim();
          if (!providerID || !modelID) return;
          this.rpc('model_config', {provider_id: providerID, model_id: modelID})
            .then(result => { this.modelSettingsDraft = this.normalizeModelSettingsDraft(result); })
            .catch(err => { this.modelSettingsStatus = err.message; this.modelSettingsStatusKind = 'danger'; });
        },
        saveActiveModelSettings() {
          if (!this.modelSettingsDraft) return;
          const payload = Object.assign({}, this.modelSettingsDraft, {
            context_window: Number(this.modelSettingsDraft.context_window || 0),
            temperature: this.blankableNumber(this.modelSettingsDraft.temperature),
            top_p: this.blankableNumber(this.modelSettingsDraft.top_p),
            min_p: this.blankableNumber(this.modelSettingsDraft.min_p),
            top_k: Number(this.modelSettingsDraft.top_k || 0),
            repeat_penalty: this.blankableNumber(this.modelSettingsDraft.repeat_penalty),
            thinking_budget: Number(this.modelSettingsDraft.thinking_budget || 0),
          });
          this.modelSettingsSaving = true; this.modelSettingsStatus = ''; this.modelSettingsStatusKind = 'secondary';
          this.rpc('save_model_config', payload).then(result => {
            this.modelSettingsDraft = this.normalizeModelSettingsDraft(result);
            this.modelSettingsStatus = 'Saved model settings'; this.modelSettingsStatusKind = 'success';
            return this.rpc('set_model', {provider_id: result.provider_id, model_id: result.model_id}).then(() => this.refreshModelOptions());
          }).catch(err => { this.modelSettingsStatus = err.message; this.modelSettingsStatusKind = 'danger'; }).finally(() => { this.modelSettingsSaving = false; });
        },
        openSessionDialog() {
          this.showSessions = true; this.sessionLoading = true; this.closeSessionEditor();
          this.reportClientStateSoon();
          this.rpc('list_sessions', {}).then(result => { this.sessionState = this.normalizeSessionState(result); }).finally(() => { this.sessionLoading = false; });
        },
        closeSessionDialog() { this.showSessions = false; this.closeSessionEditor(); this.reportClientStateSoon(); },
        loadWelcomeSessions() {
          this.rpc('list_sessions', {}).then(result => {
            this.sessionState = this.normalizeSessionState(result);
            this.state.sessions = this.sessionState.sessions;
            this.state.Sessions = this.state.sessions;
            this.state.project_root = this.sessionState.project_root || this.state.project_root || '';
            this.state.ProjectRoot = this.state.project_root;
          }).catch(err => this.showToast(err.message));
        },
        normalizeSessionState(value) {
          const source = value || {};
          const sessions = source.sessions || source.Sessions || [];
          return {
            project_root: source.project_root || source.ProjectRoot || '',
            sessions: Array.isArray(sessions) ? sessions : [],
          };
        },
        sessionRows() {
          if (this.showSessions && Array.isArray(this.sessionState.sessions)) return this.sessionState.sessions;
          return this.normalizeSessionState(this.state).sessions || this.sessionState.sessions || [];
        },
        activeSessionID() { return this.currentSessionID(); },
        currentSession() { return this.state.session || this.state.Session || {}; },
        sessionID(session) { return session.ID || session.id; },
        sessionTitle(session) { return session.Title || session.title || 'New Session'; },
        workspaceTitleSuffix() {
          const root = String(this.state.project_root || this.state.ProjectRoot || '').trim();
          return root ? `(${root})` : '';
        },
        sessionProjectRoot(session) { return session.ProjectRoot || session.project_root || ''; },
        beginHydratingSession(id) {
          id = String(id || '').trim();
          if (!id) return;
          const session = this.sessionRows().find(row => this.sessionID(row) === id) || {};
          this.hydratingSession = {active: true, id, title: this.sessionTitle(session), error: ''};
          this.showSessions = false;
          this.closeSessionEditor();
          this.state.session = null;
          this.state.Session = null;
          this.state.snapshot = {};
          this.state.Snapshot = {};
          this.state.active_chat_id = '';
          this.state.ActiveChatID = '';
          history.pushState(null, '', this.sessionURL(id));
          this.reportClientStateSoon();
        },
        switchSession(id, ev) {
          if (!id) return;
          if (this.shouldOpenInNewTab(ev)) {
            this.openURLInNewTab(this.sessionURL(id));
            return;
          }
          if (id === this.activeSessionID()) { this.closeSessionDialog(); return; }
          this.beginHydratingSession(id);
          this.allowSessionURLSync = true;
          this.rpc('switch_session', {session_id: id}).then(s => { this.applyState(s); this.closeSessionDialog(); }).catch(err => {
            const message = err.message || 'session hydration failed';
            this.hydratingSession = {...this.hydratingSession, active: false, error: message};
            this.showToast(message);
          });
        },
        beginCreateSessionFromWelcome() {
          this.sessionState = this.normalizeSessionState(this.state);
          this.beginCreateSession();
        },
        beginCreateSession() {
          this.sessionEditorMode = 'create';
          this.sessionDraft = {id: '', title: '', projectRoot: this.state.project_root || '', createProjectRoot: false, missingProjectRoot: '', error: ''};
          this.showSessionEditor = true;
        },
        beginEditSession(session) {
          this.sessionEditorMode = 'edit';
          this.sessionDraft = {id: this.sessionID(session), title: this.sessionTitle(session), projectRoot: this.sessionProjectRoot(session), createProjectRoot: false, missingProjectRoot: '', error: ''};
          this.showSessionEditor = true;
        },
        closeSessionEditor() {
          this.showSessionEditor = false;
          this.sessionDraft = {id: '', title: '', projectRoot: '', createProjectRoot: false, missingProjectRoot: '', error: ''};
        },
        browseProjectFolder() {
          this.rpc('browse_project_folder', {}).then(result => {
            if (result && result.project_root) {
              this.sessionDraft.projectRoot = result.project_root;
              this.sessionDraft.createProjectRoot = false;
              this.sessionDraft.missingProjectRoot = '';
              this.sessionDraft.error = '';
            }
          }).catch(err => this.showToast(err.message));
        },
        saveSessionEditor() {
          const title = String(this.sessionDraft.title || '').trim() || 'New Session';
          if (this.sessionEditorMode === 'edit') {
            const id = this.sessionDraft.id;
            if (!id) return;
            this.rpc('rename_session', {session_id: id, title}).then(s => {
              this.applyState(s);
              return this.rpc('list_sessions', {});
            }).then(result => {
              this.sessionState = this.normalizeSessionState(result || this.sessionState);
              this.closeSessionEditor();
            });
            return;
          }
          const projectRoot = String(this.sessionDraft.projectRoot || '').trim();
          if (projectRoot !== this.sessionDraft.missingProjectRoot) {
            this.sessionDraft.createProjectRoot = false;
            this.sessionDraft.missingProjectRoot = '';
            this.sessionDraft.error = '';
          }
          this.allowSessionURLSync = true;
          this.rpc('new_session', {title, project_root: projectRoot, create_project_root: !!this.sessionDraft.createProjectRoot}).then(s => {
            this.applyState(s);
            this.closeSessionDialog();
          }).catch(err => {
            const message = err.message || 'create session failed';
            if (message.includes('project root does not exist:')) {
              this.sessionDraft.missingProjectRoot = projectRoot;
              this.sessionDraft.createProjectRoot = false;
              this.sessionDraft.error = message;
              return;
            }
            this.showToast(message);
          });
        },
        deleteSession(id) {
          if (!id) return;
          if (!confirm('Delete this session and all chats?')) return;
          this.rpc('delete_session', {session_id: id}).then(s => {
            this.applyState(s);
            return this.rpc('list_sessions', {});
          }).then(result => { this.sessionState = this.normalizeSessionState(result || this.sessionState); });
        },
        openProviderDialog() { this.openSettingsDialog('providers'); },
        providerTemplates() { return this.providerState.catalog || []; },
        providerRows() { return this.providerState.providers || []; },
        setProviderDraft(draft) {
          this.providerDraft = Object.assign({headers: {}}, draft || {});
          this.providerHeadersText = JSON.stringify(this.providerDraft.headers || {}, null, 2);
          this.providerModelOptions = [];
        },
        editProvider(id) {
          const draft = (this.providerState.drafts || {})[id];
          if (draft) { this.setProviderDraft(draft); this.providerStatus = ''; this.providerStatusKind = 'secondary'; this.showProviderEditor = true; }
        },
        addProvider() {
          const first = this.providerTemplates()[0]?.id || 'openai-compatible';
          this.rpc('new_provider_draft', {template_id: first}).then(draft => { this.setProviderDraft(draft); this.providerStatus = ''; this.providerStatusKind = 'secondary'; this.showProviderEditor = true; });
        },
        closeProviderEditor() { this.showProviderEditor = false; this.providerDraft = null; this.providerModelOptions = []; this.providerStatus = ''; this.providerStatusKind = 'secondary'; },
        providerTemplateChanged() {
          if (!this.providerDraft) return;
          const current = this.providerDraft;
          this.rpc('new_provider_draft', {template_id: current.template_id}).then(next => {
            next.original_provider_id = current.original_provider_id || next.original_provider_id;
            next.provider_id = current.provider_id || next.provider_id;
            next.name = current.name || next.name;
            next.api_key = current.api_key || '';
            this.setProviderDraft(next);
          });
        },
        providerDraftPayload() {
          if (!this.providerDraft) return null;
          let headers = {};
          try {
            headers = this.providerHeadersText.trim() ? JSON.parse(this.providerHeadersText) : {};
          } catch (err) {
            this.providerStatus = 'Invalid headers JSON: ' + err.message; this.providerStatusKind = 'danger';
            return null;
          }
          if (!headers || Array.isArray(headers) || typeof headers !== 'object') {
            this.providerStatus = 'Headers JSON must be an object'; this.providerStatusKind = 'danger';
            return null;
          }
          const cleanHeaders = {};
          for (const [key, value] of Object.entries(headers)) cleanHeaders[key] = String(value);
          return Object.assign({}, this.providerDraft, {headers: cleanHeaders});
        },
        testProvider() {
          const payload = this.providerDraftPayload(); if (!payload) return;
          this.providerTesting = true; this.providerStatus = ''; this.providerStatusKind = 'secondary';
          this.rpc('test_provider', payload).then(result => {
            const count = result.model_count || 0;
            const sample = (result.models || []).slice(0, 4).join(', ');
            this.providerModelOptions = result.models || [];
            if (result.selected_model && this.providerDraft) {
              this.providerDraft.model = result.selected_model;
              this.addOrUpdateModelConfig(this.providerDraft.provider_id, result.selected_model);
            }
            if (this.providerDraft) {
              this.providerDraft.prompt_progress_probed = !!result.prompt_progress_probed;
              this.providerDraft.prompt_progress_supported = !!result.prompt_progress_supported;
            }
            const selected = result.selected_model ? ' Selected ' + result.selected_model + '.' : '';
            const progress = result.prompt_progress_probed ? (' Prompt progress ' + (result.prompt_progress_supported ? 'supported.' : 'not supported.')) : '';
            this.providerStatus = 'Test passed: ' + count + ' model' + (count === 1 ? '' : 's') + (sample ? ' (' + sample + ')' : '') + '.' + selected + progress;
            this.providerStatusKind = 'success';
          }).catch(err => { this.providerStatus = err.message; this.providerStatusKind = 'danger'; }).finally(() => { this.providerTesting = false; });
        },
        saveProvider() {
          const payload = this.providerDraftPayload(); if (!payload) return;
          this.providerSaving = true; this.providerStatus = ''; this.providerStatusKind = 'secondary';
          this.rpc('save_provider', payload).then(result => {
            this.providerState = result.providers || result;
            if (result.preferences) this.setSettingsState(result.preferences);
            if (this.settings) this.settings.providers = this.providerState;
            if (this.settings && this.providerDraft?.provider_id && this.providerDraft?.model) this.addOrUpdateModelConfig(this.providerDraft.provider_id, this.providerDraft.model);
            if (result.state) this.applyState(result.state);
            this.providerStatus = 'Saved provider'; this.providerStatusKind = 'success';
            this.showProviderEditor = false;
          }).catch(err => { this.providerStatus = err.message; this.providerStatusKind = 'danger'; }).finally(() => { this.providerSaving = false; });
        },
        deleteProvider(id) {
          if (!id || !confirm('Delete this provider?')) return;
          this.rpc('delete_provider', {provider_id: id}).then(result => {
            this.providerState = result.providers || result;
            if (this.settings) this.settings.providers = this.providerState;
            if (this.settings?.model_configs) this.settings.model_configs = this.modelConfigRows().filter(item => item.provider_id !== id);
            if (this.settings?.general) {
              this.settings.general.default_provider = this.providerState.default_provider || '';
              this.settings.general.default_model = this.providerState.default_model || '';
            }
            if (result.state) this.applyState(result.state);
          }).catch(err => this.showToast(err.message));
        },
        openSettingsDialog(tab = 'general') {
          this.showSettings = true; this.settingsTab = tab; this.settingsLoading = true; this.settingsStatus = ''; this.settingsStatusKind = 'secondary';
          this.reportClientStateSoon();
          this.rpc('preferences_state', {}).then(state => {
            this.setSettingsState(state);
            if (this.settingsTab === 'models') this.ensureDetectedDefaultModel();
          }).finally(() => { this.settingsLoading = false; });
        },
        closeSettingsDialog() {
          this.showSettings = false; this.settings = null; this.settingsStatus = '';
          this.closeProviderEditor(); this.closeModelConfigEditor(); this.closeMCPEditor(); this.reportClientStateSoon();
        },
        setSettingsState(state) {
          this.settings = state || {};
          if (!this.settings.ui) this.settings.ui = {};
          if (!this.settings.ui.tts) this.settings.ui.tts = {enabled: false, provider_id: '', model_id: '', voice: 'alloy', response_format: 'wav', speed: 1, pcm_sample_rate: 24000};
          this.applyTTSSettings(this.settings.ui.tts);
          this.providerState = this.settings.providers || this.providerState;
          if (this.settingsTab === 'models') this.ensureDetectedDefaultModel();
        },
        settingsTabs() { return ['general', 'tts', 'access', 'tools', 'compaction', 'thinking', 'prompts', 'providers', 'models', 'mcp']; },
        selectSettingsTab(tab) {
          this.settingsTab = tab;
          if (tab === 'models') this.ensureDetectedDefaultModel();
        },
        settingsTabLabel(tab) {
          return {general: 'General', tts: 'TTS', access: 'Access', tools: 'Tools', compaction: 'Compaction', thinking: 'Thinking', prompts: 'Prompts', providers: 'Providers', models: 'Models', mcp: 'MCP'}[tab] || tab;
        },
        ttsModelOptions() {
          const models = this.settings?.models || [];
          const current = this.ttsModelValue();
          return models.filter(model => model.supports_tts || this.modelOptionValue(model) === current);
        },
        ttsModelValue() {
          const tts = this.settings?.ui?.tts || {};
          if (!tts.provider_id && !tts.model_id) return '';
          return JSON.stringify([tts.provider_id || '', tts.model_id || '']);
        },
        setTTSModelValue(value) {
          if (!this.settings?.ui) return;
          if (!this.settings.ui.tts) this.settings.ui.tts = {};
          if (!value) {
            this.settings.ui.tts.provider_id = '';
            this.settings.ui.tts.model_id = '';
            return;
          }
          let parts = [];
          try {
            parts = JSON.parse(String(value || '[]'));
          } catch (_) {
            parts = [];
          }
          this.settings.ui.tts.provider_id = parts[0] || '';
          this.settings.ui.tts.model_id = parts[1] || '';
        },
        compactionModelValue() {
          const c = this.settings?.compaction || {};
          if (c.use_chat_model || (!c.provider_id && !c.model_id)) return 'chat';
          return JSON.stringify([c.provider_id || '', c.model_id || '']);
        },
        modelOptionValue(model) {
          return JSON.stringify([model?.provider_id || '', model?.model_id || '']);
        },
        modelOptionLabel(model) {
          if (!model) return '';
          const label = (model.provider_label || model.provider_id || '') + ' / ' + (model.model_id || '');
          const suffix = [];
          if (model.custom) suffix.push('custom');
          if (model.detected) suffix.push('detected');
          if (model.custom && model.source_model_id) suffix.push('uses ' + (model.source_provider_id || model.provider_id || '') + ' / ' + model.source_model_id);
          if (model.custom && model.backing_detected === false) suffix.push('source missing');
          return suffix.length ? label + ' (' + suffix.join(', ') + ')' : label;
        },
        labelForModelValue(value, fallback = '') {
          if (!value) return fallback;
          const model = (this.settings?.models || this.modelOptions || []).find(item => this.modelOptionValue(item) === value);
          if (model) return this.modelOptionLabel(model);
          let parts = [];
          try {
            parts = JSON.parse(String(value || '[]'));
          } catch (_) {
            parts = [];
          }
          return parts[0] || parts[1] ? (parts[0] || '-') + ' / ' + (parts[1] || '-') : fallback;
        },
        ttsModelLabel() { return this.labelForModelValue(this.ttsModelValue(), 'First detected TTS model'); },
        compactionModelLabel() { return this.compactionModelValue() === 'chat' ? 'Chat model' : this.labelForModelValue(this.compactionModelValue(), 'Chat model'); },
        thinkingModelLabel() { return this.thinkingModelValue() === 'chat' ? 'Chat model' : this.labelForModelValue(this.thinkingModelValue(), 'Chat model'); },
        defaultModelLabel() { return this.labelForModelValue(this.defaultModelValue(), 'No default model'); },
        setCompactionModelValue(value) {
          if (!this.settings?.compaction) return;
          if (value === 'chat') {
            this.settings.compaction.use_chat_model = true;
            this.settings.compaction.provider_id = '';
            this.settings.compaction.model_id = '';
            return;
          }
          let parts = [];
          try {
            parts = JSON.parse(String(value || '[]'));
          } catch (_) {
            parts = [];
          }
          this.settings.compaction.use_chat_model = false;
          this.settings.compaction.provider_id = parts[0] || '';
          this.settings.compaction.model_id = parts[1] || '';
        },
        thinkingModelValue() {
          const c = this.settings?.thinking || {};
          if (c.use_chat_model || (!c.provider_id && !c.model_id)) return 'chat';
          return JSON.stringify([c.provider_id || '', c.model_id || '']);
        },
        setThinkingModelValue(value) {
          if (!this.settings?.thinking) return;
          if (value === 'chat') {
            this.settings.thinking.use_chat_model = true;
            this.settings.thinking.provider_id = '';
            this.settings.thinking.model_id = '';
            return;
          }
          let parts = [];
          try {
            parts = JSON.parse(String(value || '[]'));
          } catch (_) {
            parts = [];
          }
          this.settings.thinking.use_chat_model = false;
          this.settings.thinking.provider_id = parts[0] || '';
          this.settings.thinking.model_id = parts[1] || '';
        },
        defaultModelValue() {
          const g = this.settings?.general || {};
          return JSON.stringify([g.default_provider || '', g.default_model || '']);
        },
        setDefaultModelValue(value) {
          if (!this.settings?.general) return;
          let parts = [];
          try {
            parts = JSON.parse(String(value || '[]'));
          } catch (_) {
            parts = [];
          }
          this.settings.general.default_provider = parts[0] || '';
          this.settings.general.default_model = parts[1] || '';
          if (this.settings.providers) {
            this.settings.providers.default_provider = this.settings.general.default_provider;
            this.settings.providers.default_model = this.settings.general.default_model;
          }
        },
        ensureDetectedDefaultModel() {
          if (!this.settings?.general || !Array.isArray(this.settings.models) || this.settings.models.length === 0) return;
          const current = this.defaultModelValue();
          if (this.settings.models.some(model => this.modelOptionValue(model) === current)) return;
          this.setDefaultModelValue(this.modelOptionValue(this.settings.models[0]));
        },
        settingsListRows(kind) {
          if (kind === 'providers') return this.providerRows();
          if (kind === 'models') return this.settings?.model_configs || [];
          if (kind === 'mcp') return this.settings?.mcp_servers || [];
          return [];
        },
        settingsItemTitle(kind, item) {
          if (kind === 'providers') return item.name || item.id || 'Provider';
          if (kind === 'models') return item.model_id || 'Model';
          if (kind === 'mcp') return item.name || item.id || 'MCP server';
          return item?.name || item?.id || 'Item';
        },
        settingsItemSubtitle(kind, item) {
          if (kind === 'providers') return (item.id || '-') + (item.base_url ? ' / ' + item.base_url : '');
          if (kind === 'models') return (item.provider_id || '-') + ' -> ' + ((item.source_provider_id || item.provider_id || '-') + ' / ' + (item.source_model_id || '-')) + ' / ' + (item.context_window || 32768) + ' context' + (item.thinking_mode && item.thinking_mode !== 'auto' ? ' / thinking ' + item.thinking_mode : '');
          if (kind === 'mcp') return (item.id || '-') + (item.url ? ' / ' + item.url : '');
          return '';
        },
        settingsItemBadges(kind, item) {
          const badges = [];
          if (kind === 'providers' && item.default) badges.push('default');
          if (kind === 'providers') badges.push(this.promptProgressBadge(item));
          if (kind === 'models' && this.settings?.general?.default_provider === item.provider_id && this.settings?.general?.default_model === item.model_id) badges.push('default');
          if (kind === 'models') badges.push('custom');
          if (kind === 'models' && item.backing_detected === false) badges.push('source missing');
          if (item.disabled) badges.push('disabled');
          return badges.filter(Boolean);
        },
        promptProgressState(item) {
          if (!item) return {label: 'Unknown', detail: 'Prompt progress has not been checked yet.'};
          const mode = String(item.prompt_progress_mode || 'auto').trim().toLowerCase() || 'auto';
          if (mode === 'disabled') return {label: 'Disabled', detail: 'Prompt progress is disabled for this provider.'};
          if (!item.prompt_progress_probed) return {label: 'Unknown', detail: 'Koder will try prompt progress on the next test, save, or model request.'};
          if (item.prompt_progress_supported) return {label: 'Supported', detail: 'This provider accepts return_progress and can stream prompt preprocessing progress.'};
          return {label: 'Unsupported', detail: 'This provider rejected return_progress; Koder will omit it.'};
        },
        promptProgressBadge(item) {
          const state = this.promptProgressState(item).label.toLowerCase();
          return 'prompt ' + state;
        },
        editSettingsItem(kind, id) {
          if (kind === 'providers') { this.editProvider(id); return; }
          if (kind === 'models') { this.editModelConfig(id); return; }
          if (kind === 'mcp') this.editMCPServer(id);
        },
        addSettingsItem(kind) {
          if (kind === 'providers') { this.addProvider(); return; }
          if (kind === 'models') { this.addModelConfig(); return; }
          if (kind === 'mcp') this.addMCPServer();
        },
        deleteSettingsItem(kind, id) {
          if (kind === 'providers') { this.deleteProvider(id); return; }
          if (kind === 'models') { this.deleteModelConfig(id); return; }
          if (kind === 'mcp') this.deleteMCPServer(id);
        },
        modelConfigRows() { return this.settings?.model_configs || []; },
        modelConfigKey(item) { return JSON.stringify([item?.provider_id || '', item?.model_id || '']); },
        providerIDOptions() {
          const ids = new Set((this.providerRows() || []).map(item => String(item.id || '').trim()).filter(Boolean));
          return Array.from(ids).sort();
        },
        modelIDOptionsForProvider(providerID) {
          const id = String(providerID || '').trim();
          const ids = new Set();
          for (const option of this.settings?.models || []) {
            if ((!id || option.provider_id === id) && option.detected) ids.add(option.model_id);
            if ((!id || option.source_provider_id === id) && option.source_model_id) ids.add(option.source_model_id);
          }
          for (const model of this.providerModelOptions || []) ids.add(model);
          return Array.from(ids).filter(Boolean).sort();
        },
        addOrUpdateModelConfig(providerID, modelID, values = {}) {
          if (!this.settings || !providerID || !modelID) return;
          if (!Array.isArray(this.settings.model_configs)) this.settings.model_configs = [];
          const existing = this.settings.model_configs.find(item => item.provider_id === providerID && item.model_id === modelID);
          const next = Object.assign({
            original_provider_id: providerID,
            original_model_id: modelID,
            provider_id: providerID,
            model_id: modelID,
            source_provider_id: providerID,
            source_model_id: modelID,
            custom: true,
            editable: true,
            context_window: 32768,
            model_preset: 'auto',
            temperature: null,
            top_p: null,
            min_p: null,
            top_k: 0,
            repeat_penalty: null,
            thinking_mode: 'auto',
            thinking_budget: 0,
            extra_body: {},
            extra_body_text: '{}'
          }, existing || {}, values, {provider_id: providerID, model_id: modelID});
          next.extra_body_text = this.formatModelExtraBodyText(next.extra_body);
          if (existing) Object.assign(existing, next); else this.settings.model_configs.push(next);
          this.settings.model_configs.sort((a, b) => (String(a.provider_id || '') + '\0' + String(a.model_id || '')).localeCompare(String(b.provider_id || '') + '\0' + String(b.model_id || '')));
        },
        addModelConfig() {
          const source = (this.settings?.models || []).find(model => model.detected) || {};
          const providerID = source.provider_id || this.settings?.general?.default_provider || this.providerIDOptions()[0] || '';
          const sourceModelID = source.model_id || '';
          this.modelConfigDraft = this.normalizeModelSettingsDraft({original_provider_id: '', original_model_id: '', provider_id: providerID, model_id: this.uniqueCustomModelID(providerID, sourceModelID), source_provider_id: providerID, source_model_id: sourceModelID, custom: true, editable: true, extra_body: {}});
          this.modelConfigDraft.extra_body_text = this.formatModelExtraBodyText(this.modelConfigDraft.extra_body);
          this.modelConfigExtraBodyOpen = false;
          this.modelConfigStatus = ''; this.modelConfigStatusKind = 'secondary'; this.showModelConfigEditor = true;
        },
        editModelConfig(key) {
          const item = this.modelConfigRows().find(row => this.modelConfigKey(row) === key);
          if (!item) return;
          this.modelConfigDraft = JSON.parse(JSON.stringify(Object.assign({
            original_provider_id: item.provider_id || '',
            original_model_id: item.model_id || '',
            context_window: 32768,
            model_preset: 'auto',
            thinking_mode: 'auto',
            extra_body: {}
          }, item)));
          this.modelConfigDraft.extra_body_text = this.formatModelExtraBodyText(this.modelConfigDraft.extra_body);
          this.modelConfigExtraBodyOpen = this.modelExtraBodyHasContent(this.modelConfigDraft.extra_body);
          this.modelConfigStatus = ''; this.modelConfigStatusKind = 'secondary'; this.showModelConfigEditor = true;
        },
        closeModelConfigEditor() { this.showModelConfigEditor = false; this.modelConfigDraft = null; this.modelConfigExtraBodyOpen = false; this.modelConfigStatus = ''; this.modelConfigStatusKind = 'secondary'; },
        saveModelConfig() {
          if (!this.settings || !this.modelConfigDraft) return;
          const providerID = String(this.modelConfigDraft.provider_id || '').trim();
          const modelID = String(this.modelConfigDraft.model_id || '').trim();
          const sourceProviderID = String(this.modelConfigDraft.source_provider_id || providerID).trim();
          const sourceModelID = String(this.modelConfigDraft.source_model_id || '').trim();
          if (!providerID || !modelID || !sourceProviderID || !sourceModelID) {
            this.modelConfigStatus = 'Provider, custom name, and source model are required'; this.modelConfigStatusKind = 'danger';
            return;
          }
          const contextWindow = Number(this.modelConfigDraft.context_window || 0);
          if (contextWindow <= 0) {
            this.modelConfigStatus = 'Context window must be greater than zero'; this.modelConfigStatusKind = 'danger';
            return;
          }
          let extraBody = {};
          try {
            extraBody = this.parseModelExtraBodyText(this.modelConfigDraft.extra_body_text);
          } catch (err) {
            this.modelConfigStatus = err.message || 'Custom request JSON is invalid'; this.modelConfigStatusKind = 'danger';
            this.modelConfigExtraBodyOpen = true;
            return;
          }
          const originalKey = JSON.stringify([this.modelConfigDraft.original_provider_id || providerID, this.modelConfigDraft.original_model_id || modelID]);
          const rows = this.modelConfigRows().filter(item => this.modelConfigKey(item) !== originalKey && this.modelConfigKey(item) !== JSON.stringify([providerID, modelID]));
          rows.push(Object.assign({}, this.modelConfigDraft, {
            original_provider_id: providerID,
            original_model_id: modelID,
            provider_id: providerID,
            model_id: modelID,
            source_provider_id: sourceProviderID,
            source_model_id: sourceModelID,
            custom: true,
            editable: true,
            context_window: contextWindow,
            model_preset: String(this.modelConfigDraft.model_preset || 'auto').trim() || 'auto',
            temperature: this.blankableNumber(this.modelConfigDraft.temperature),
            top_p: this.blankableNumber(this.modelConfigDraft.top_p),
            min_p: this.blankableNumber(this.modelConfigDraft.min_p),
            top_k: Number(this.modelConfigDraft.top_k || 0),
            repeat_penalty: this.blankableNumber(this.modelConfigDraft.repeat_penalty),
            thinking_mode: String(this.modelConfigDraft.thinking_mode || 'auto').trim() || 'auto',
            thinking_budget: Number(this.modelConfigDraft.thinking_budget || 0),
            extra_body: extraBody,
            extra_body_text: this.formatModelExtraBodyText(extraBody)
          }));
          rows.sort((a, b) => (String(a.provider_id || '') + '\0' + String(a.model_id || '')).localeCompare(String(b.provider_id || '') + '\0' + String(b.model_id || '')));
          this.settings.model_configs = rows;
          this.modelConfigStatus = 'Saved model'; this.modelConfigStatusKind = 'success';
          this.showModelConfigEditor = false;
        },
        modelExtraBodyHasContent(value) {
          return !!(value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length);
        },
        formatModelExtraBodyText(value) {
          if (!this.modelExtraBodyHasContent(value)) return '{}';
          return JSON.stringify(value, null, 2);
        },
        parseModelExtraBodyText(text) {
          const raw = String(text || '').trim();
          if (!raw) return {};
          const parsed = JSON.parse(raw);
          if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('Custom request JSON must be an object');
          return parsed;
        },
        deleteModelConfig(key) {
          if (!this.settings || !key || !confirm('Delete this model setting?')) return;
          this.settings.model_configs = this.modelConfigRows().filter(item => this.modelConfigKey(item) !== key);
        },
        mcpRows() { return this.settings?.mcp_servers || []; },
        addMCPServer() {
          this.mcpDraft = {original_id: '', id: '', name: '', url: '', headers: {}, disabled: false, startup_timeout: '', request_timeout: '', disable_standalone_sse: false, bearer_token: '', bearer_token_env: ''};
          this.mcpHeadersText = '{}'; this.mcpStatus = ''; this.mcpStatusKind = 'secondary'; this.showMCPEditor = true;
        },
        editMCPServer(id) {
          const item = this.mcpRows().find(server => server.id === id);
          if (!item) return;
          this.mcpDraft = JSON.parse(JSON.stringify(Object.assign({original_id: id, headers: {}}, item, {original_id: id})));
          this.mcpHeadersText = JSON.stringify(this.mcpDraft.headers || {}, null, 2);
          this.mcpStatus = ''; this.mcpStatusKind = 'secondary'; this.showMCPEditor = true;
        },
        closeMCPEditor() { this.showMCPEditor = false; this.mcpDraft = null; this.mcpStatus = ''; this.mcpStatusKind = 'secondary'; },
        mcpDraftPayload() {
          if (!this.mcpDraft) return null;
          let headers = {};
          try {
            headers = this.mcpHeadersText.trim() ? JSON.parse(this.mcpHeadersText) : {};
          } catch (err) {
            this.mcpStatus = 'Invalid headers JSON: ' + err.message; this.mcpStatusKind = 'danger';
            return null;
          }
          if (!headers || Array.isArray(headers) || typeof headers !== 'object') {
            this.mcpStatus = 'Headers JSON must be an object'; this.mcpStatusKind = 'danger';
            return null;
          }
          const id = String(this.mcpDraft.id || '').trim();
          if (!id) {
            this.mcpStatus = 'Server ID is required'; this.mcpStatusKind = 'danger';
            return null;
          }
          const cleanHeaders = {};
          for (const [key, value] of Object.entries(headers)) cleanHeaders[key] = String(value);
          const payload = Object.assign({}, this.mcpDraft, {id, headers: cleanHeaders});
          delete payload.original_id;
          return payload;
        },
        saveMCPServer() {
          const payload = this.mcpDraftPayload(); if (!payload || !this.settings) return;
          const original = this.mcpDraft.original_id || payload.id;
          const rows = this.mcpRows().filter(item => item.id !== original && item.id !== payload.id);
          rows.push(payload);
          rows.sort((a, b) => String(a.id || '').localeCompare(String(b.id || '')));
          this.settings.mcp_servers = rows;
          this.mcpStatus = 'Saved MCP server'; this.mcpStatusKind = 'success';
          this.showMCPEditor = false;
        },
        deleteMCPServer(id) {
          if (!this.settings || !id || !confirm('Delete this MCP server?')) return;
          this.settings.mcp_servers = this.mcpRows().filter(item => item.id !== id);
        },
        toolDefaultRows() { return this.settings?.tool_defaults || []; },
        toolDefaultTool(item) { return item.tool || item.Tool || ''; },
        toolDefaultLabel(item) { return item.label || item.Label || this.toolDefaultTool(item); },
        toolDefaultGroupID(item) { return item.group || item.Group || this.toolDefaultTool(item); },
        toolDefaultGroupLabel(item) { return item.group_label || item.GroupLabel || this.toolDefaultGroupID(item); },
        toolDefaultEnabled(item) { return item.enabled ?? item.Enabled ?? true; },
        setToolDefaultEnabled(item, enabled) {
          if ('enabled' in item) item.enabled = enabled; else item.Enabled = enabled;
        },
        toolDefaultGroups() {
          const groups = new Map();
          for (const item of this.toolDefaultRows()) {
            const id = this.toolDefaultGroupID(item);
            if (!groups.has(id)) groups.set(id, {id, label: this.toolDefaultGroupLabel(item), items: []});
            groups.get(id).items.push(item);
          }
          return Array.from(groups.values()).map(group => {
            group.items.sort((a, b) => this.toolDefaultTool(a).localeCompare(this.toolDefaultTool(b)));
            return group;
          });
        },
        toolGroupEnabled(group) {
          const items = group?.items || [];
          return items.length > 0 && items.every(item => this.toolDefaultEnabled(item));
        },
        toolGroupPartial(group) {
          const items = group?.items || [];
          if (items.length === 0) return false;
          const enabled = items.filter(item => this.toolDefaultEnabled(item)).length;
          return enabled > 0 && enabled < items.length;
        },
        setToolGroupEnabled(group, enabled) {
          for (const item of group?.items || []) this.setToolDefaultEnabled(item, enabled);
        },
        saveSettings() {
          if (!this.settings) return;
          let payload;
          try {
            payload = JSON.parse(JSON.stringify(this.settings));
          } catch (err) {
            this.settingsStatus = 'Invalid JSON: ' + err.message; this.settingsStatusKind = 'danger';
            return;
          }
          this.settingsSaving = true; this.settingsStatus = ''; this.settingsStatusKind = 'secondary';
          this.rpc('save_preferences', payload).then(state => {
            this.setSettingsState(state);
            this.theme = state?.ui?.theme || this.theme;
            this.applyTheme();
            this.settingsStatus = 'Saved settings'; this.settingsStatusKind = 'success';
          }).catch(err => { this.settingsStatus = err.message; this.settingsStatusKind = 'danger'; }).finally(() => { this.settingsSaving = false; });
        },
        resetPrompt(target) {
          this.rpc('reset_prompt', {target}).then(prompt => {
            const prompts = this.settings?.prompts || [];
            const idx = prompts.findIndex(item => item.target === target);
            if (idx >= 0) prompts[idx] = prompt; else prompts.push(prompt);
            this.settings.prompts = prompts;
            this.settingsStatus = 'Reset ' + target; this.settingsStatusKind = 'success';
          }).catch(err => { this.settingsStatus = err.message; this.settingsStatusKind = 'danger'; });
        }
      }
    }
    function preferenceKey(name) { return 'koder.' + name; }
    function readPreference(name, fallback) {
      try { return localStorage.getItem(preferenceKey(name)) || fallback; } catch (_) { return fallback; }
    }
    function writePreference(name, value) {
      try { localStorage.setItem(preferenceKey(name), String(value)); } catch (_) {}
    }
    function removePreference(name) {
      try { localStorage.removeItem(preferenceKey(name)); } catch (_) {}
    }
    function readTabPreference(name, fallback) {
      try { return sessionStorage.getItem(preferenceKey(name)) || fallback; } catch (_) { return fallback; }
    }
    function writeTabPreference(name, value) {
      try { sessionStorage.setItem(preferenceKey(name), String(value)); } catch (_) {}
    }
    function readJSONPreference(name, fallback) {
      try {
        const raw = localStorage.getItem(preferenceKey(name));
        if (!raw) return fallback;
        return JSON.parse(raw) || fallback;
      } catch (_) {
        return fallback;
      }
    }
    function writeJSONPreference(name, value) {
      try { localStorage.setItem(preferenceKey(name), JSON.stringify(value || {})); } catch (_) {}
    }
    function readHiddenMilestoneStatuses() {
      const saved = readJSONPreference('hiddenMilestoneStatuses', null);
      if (saved && typeof saved === 'object') return saved;
      const hideClosed = readPreference('hideClosedMilestones', 'false') === 'true';
      return hideClosed ? {completed: true, cancelled: true} : {};
    }
    function readHiddenChatStatuses() {
      const saved = readJSONPreference('hiddenChatStatuses', null);
      if (saved && typeof saved === 'object') return saved;
      return {archived: true};
    }
