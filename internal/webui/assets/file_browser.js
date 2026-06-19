(function () {
  function escapeHTML(value) {
    return String(value || '').replace(/[&<>"']/g, ch => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[ch]));
  }

  function formatBytes(bytes) {
    const value = Number(bytes) || 0;
    if (value < 1024) return value + ' B';
    if (value < 1024 * 1024) return (value / 1024).toFixed(value < 10 * 1024 ? 1 : 0) + ' KB';
    return (value / (1024 * 1024)).toFixed(1) + ' MB';
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

  function diagramExpandButton(title) {
    return '<button type="button" class="media-expand-button" title="Expand ' + escapeHTML(title) + '"><i class="bi bi-arrows-angle-expand"></i></button>';
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

  function configureMermaid() {
    if (!window.mermaid || window.koderFilesMermaidConfigured) return;
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: 'dark',
      flowchart: {htmlLabels: true, curve: 'basis', useMaxWidth: true}
    });
    window.koderFilesMermaidConfigured = true;
  }

  function errorDetails(err) {
    if (!err) return 'Unknown error';
    if (typeof err === 'string') return err;
    const message = String(err.message || err.str || err.name || err);
    const hash = String(err.hash?.text || err.hash?.token || '').trim();
    return hash ? message + '\nNear: ' + hash : message;
  }

  window.koderFilesApp = function () {
    return {
      sessionID: '',
      projectRoot: '',
      treeLoading: false,
      treeError: '',
      selectedPath: '',
      file: null,
      fileLoading: false,
      fileError: '',
      viewMode: 'preview',
      childrenByPath: {},
      expanded: {},
      imageLightbox: {open: false, kind: 'svg', src: '', html: '', title: '', meta: '', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0},

      init() {
        const match = location.pathname.match(/^\/s\/([^/]+)\/files$/);
        this.sessionID = match ? decodeURIComponent(match[1]) : '';
        configureMermaid();
        this.loadTree('').then(() => {
          const path = this.pathFromURL();
          if (path) this.openPathFromURL(path);
        });
        window.addEventListener('popstate', () => {
          const path = this.pathFromURL();
          if (path) {
            this.openPathFromURL(path, {replaceURL: true});
          } else {
            this.selectedPath = '';
            this.file = null;
            this.fileError = '';
          }
        });
        document.addEventListener('click', event => this.handleMediaPreviewClick(event));
      },

      sessionURL() {
        return this.sessionID ? '/s/' + encodeURIComponent(this.sessionID) : '/';
      },

      filesURL(path) {
        const base = this.sessionURL() + '/files';
        const value = String(path || '').trim();
        return value ? base + '?path=' + encodeURIComponent(value) : base;
      },

      pathFromURL() {
        return String(new URLSearchParams(location.search).get('path') || '').trim();
      },

      setFileURL(path, options = {}) {
        const target = this.filesURL(path);
        if (location.pathname + location.search === target) return;
        if (options.replaceURL) history.replaceState(null, '', target);
        else history.pushState(null, '', target);
      },

      nodeIndent(depth) {
        return {'padding-left': (0.55 + Number(depth || 0) * 1.05) + 'rem'};
      },

      nodeIcon(node) {
        if (!node.dir) return 'bi-file-earmark-text';
        return this.expanded[node.path || ''] ? 'bi-folder2-open' : 'bi-folder';
      },

      visibleNodes() {
        const output = [];
        const append = (parent, depth) => {
          (this.childrenByPath[parent] || []).forEach(child => {
            output.push({...child, depth});
            if (child.dir && this.expanded[child.path || '']) append(child.path || '', depth + 1);
          });
        };
        append('', 0);
        return output;
      },

      refresh() {
        this.childrenByPath = {};
        this.expanded = {};
        this.loadTree('');
      },

      async openNode(node) {
        if (!node) return;
        if (node.dir) {
          const key = node.path || '';
          this.expanded[key] = !this.expanded[key];
          if (this.expanded[key] && !this.childrenByPath[key]) await this.loadTree(key);
          return;
        }
        await this.loadFile(node.path);
      },

      async openPathFromURL(path, options = {}) {
        const clean = String(path || '').replace(/^\/+/, '');
        if (!clean) return;
        await this.expandParents(clean);
        await this.loadFile(clean, {replaceURL: options.replaceURL});
      },

      async expandParents(path) {
        const parts = String(path || '').split('/').filter(Boolean);
        let parent = '';
        for (let i = 0; i < parts.length - 1; i++) {
          parent = parent ? parent + '/' + parts[i] : parts[i];
          this.expanded[parent] = true;
          if (!this.childrenByPath[parent]) await this.loadTree(parent);
        }
      },

      async loadTree(path) {
        if (!this.sessionID) return;
        this.treeLoading = true;
        this.treeError = '';
        try {
          const response = await fetch('/api/sessions/' + encodeURIComponent(this.sessionID) + '/files/tree?path=' + encodeURIComponent(path || ''), {cache: 'no-store'});
          if (!response.ok) throw new Error(await response.text() || 'tree load failed');
          const data = await response.json();
          this.projectRoot = data.project_root || this.projectRoot;
          this.childrenByPath[data.path || ''] = Array.isArray(data.entries) ? data.entries : [];
        } catch (err) {
          this.treeError = err.message || String(err);
        } finally {
          this.treeLoading = false;
        }
      },

      async loadFile(path, options = {}) {
        if (!this.sessionID || !path) return;
        this.selectedPath = path;
        this.fileLoading = true;
        this.fileError = '';
        this.file = null;
        this.setFileURL(path, options);
        try {
          const response = await fetch('/api/sessions/' + encodeURIComponent(this.sessionID) + '/files/read?path=' + encodeURIComponent(path), {cache: 'no-store'});
          if (!response.ok) throw new Error(await response.text() || 'file load failed');
          this.file = await response.json();
          this.projectRoot = this.file.project_root || this.projectRoot;
          this.viewMode = this.file.markdown ? 'preview' : 'source';
        } catch (err) {
          this.fileError = err.message || String(err);
        } finally {
          this.fileLoading = false;
        }
      },

      fileMeta() {
        if (!this.file) return '';
        const parts = [formatBytes(this.file.size)];
        if (this.file.language) parts.push(this.file.language);
        if (this.file.modified) parts.push(new Date(this.file.modified).toLocaleString());
        return parts.join(' · ');
      },

      highlightedContent() {
        const text = this.file?.content || '';
        const language = this.file?.language || '';
        if (!window.hljs) return escapeHTML(text);
        try {
          if (language && hljs.getLanguage(language)) return hljs.highlight(text, {language, ignoreIllegals: true}).value;
          return hljs.highlightAuto(text).value;
        } catch (_) {
          return escapeHTML(text);
        }
      },

      markdownHTML(source) {
        const text = String(source || '');
        if (!text.trim()) return '';
        if (!window.marked) return '<pre>' + escapeHTML(text) + '</pre>';
        marked.setOptions({gfm: true, breaks: false});
        let html = marked.parse(text);
        html = sanitizeHTML(html);
        html = renderMermaidPlaceholders(html);
        html = highlightMarkdownCode(html);
        return sanitizeHTML(html);
      },

      handleMediaPreviewClick(event) {
        const trigger = event.target?.closest?.('.mermaid-diagram .media-expand-button');
        if (!trigger) return;
        event.preventDefault();
        const diagram = trigger.closest('.mermaid-diagram');
        const svg = diagram?.querySelector('.mermaid-diagram-content svg');
        this.openMermaidLightbox(svg ? svg.outerHTML : '', 'Mermaid diagram', 'Drag to pan, wheel or buttons to zoom');
      },

      openMermaidLightbox(html, title, meta) {
        html = sanitizeMermaidSVG(html || '');
        if (!html) return;
        this.imageLightbox = {open: true, kind: 'svg', src: '', html, title: title || 'Mermaid diagram', meta: meta || 'Drag to pan, wheel or buttons to zoom', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0};
      },

      closeImageLightbox() {
        this.imageLightbox = {open: false, kind: 'svg', src: '', html: '', title: '', meta: '', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0};
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

      renderDiagrams(root) {
        if (!root || !window.mermaid) return;
        configureMermaid();
        root.querySelectorAll('.mermaid-diagram[data-mermaid-state="pending"]').forEach(async diagram => {
          const source = (diagram.textContent || '').trim();
          if (!source) return;
          diagram.dataset.mermaidState = 'rendering';
          const id = 'files-mermaid-' + Math.random().toString(36).slice(2);
          try {
            const result = await mermaid.render(id, source);
            diagram.innerHTML = '<div class="mermaid-diagram-content">' + sanitizeMermaidSVG(result.svg || '') + '</div>' + diagramExpandButton('Mermaid diagram');
            diagram.dataset.mermaidState = 'done';
            if (result.bindFunctions) result.bindFunctions(diagram);
          } catch (err) {
            const details = errorDetails(err);
            diagram.dataset.mermaidState = 'error';
            diagram.innerHTML = '<div class="mermaid-error">Mermaid render failed</div><pre class="mermaid-error-detail">' + escapeHTML(details) + '</pre><pre>' + escapeHTML(source) + '</pre>';
          }
        });
      },
    };
  };
})();
