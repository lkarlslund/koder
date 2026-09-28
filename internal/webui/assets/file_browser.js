(function () {
  const {escapeHTML, sanitizeHTML, sanitizeMermaidSVG, highlightMarkdownCode, renderMermaidPlaceholders, renderMermaidDiagrams} = window.KoderMarkdownRuntime;

  function formatBytes(bytes) {
    const value = Number(bytes) || 0;
    if (value < 1024) return value + ' B';
    if (value < 1024 * 1024) return (value / 1024).toFixed(value < 10 * 1024 ? 1 : 0) + ' KB';
    return (value / (1024 * 1024)).toFixed(1) + ' MB';
  }

  function readFileBrowserPreference(name, fallback) {
    try { return localStorage.getItem('koder.' + name) ?? fallback; } catch (_) { return fallback; }
  }

  function writeFileBrowserPreference(name, value) {
    try { localStorage.setItem('koder.' + name, String(value)); } catch (_) {}
  }

  function isExternalResourceURL(value) {
    const src = String(value || '').trim();
    return !src || src.startsWith('#') || src.startsWith('//') || /^[a-z][a-z0-9+.-]*:/i.test(src);
  }

  function resolveMarkdownAssetPath(src, basePath) {
    src = String(src || '').trim();
    if (isExternalResourceURL(src)) return '';
    const cleanSrc = src.split('#')[0].split('?')[0].replace(/\\/g, '/');
    if (!cleanSrc) return '';
    if (cleanSrc.startsWith('/')) return cleanSrc.replace(/^\/+/, '');
    const base = String(basePath || '').replace(/\\/g, '/');
    const baseDir = base.includes('/') ? base.slice(0, base.lastIndexOf('/')) : '';
    const parts = (baseDir ? baseDir + '/' + cleanSrc : cleanSrc).split('/');
    const stack = [];
    for (const part of parts) {
      if (!part || part === '.') continue;
      if (part === '..') {
        if (!stack.length) return '';
        stack.pop();
        continue;
      }
      stack.push(part);
    }
    return stack.join('/');
  }

  function rewriteMarkdownImageSources(html, basePath, rawURLForPath) {
    const template = document.createElement('template');
    template.innerHTML = html;
    template.content.querySelectorAll('img').forEach(img => {
      const original = img.getAttribute('src') || '';
      const resolved = resolveMarkdownAssetPath(original, basePath);
      if (!resolved) return;
      img.setAttribute('src', rawURLForPath(resolved));
      img.setAttribute('loading', 'lazy');
      img.setAttribute('decoding', 'async');
      img.setAttribute('data-source-path', resolved);
      img.classList.add('file-browser-markdown-image');
      img.removeAttribute('srcset');
      if (!img.getAttribute('title')) img.setAttribute('title', resolved);
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
      chatID: '',
      projectRoot: '',
      treeLoading: false,
      treeError: '',
      selectedPath: '',
      selectedNode: null,
      currentDir: '',
      mutationBusy: false,
      mutationStatus: '',
      mutationError: '',
      fileRequest: 0,
      dropTarget: null,
      dragPath: null,
      fileDialog: null,
      file: null,
      fileLoading: false,
      fileError: '',
      sendStatus: '',
      sendStatusKind: 'secondary',
      sendBusy: false,
      viewMode: 'preview',
      treeRatio: Number(readFileBrowserPreference('fileTreeRatio', '0.26')),
      resizingTree: false,
      childrenByPath: {},
      expanded: {},
      imageLightbox: {open: false, kind: 'svg', src: '', html: '', title: '', meta: '', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0, pointers: {}, pinchDistance: 0, pinchZoom: 1},

      init() {
        const match = location.pathname.match(/^\/s\/([^/]+)\/files$/);
        this.sessionID = match ? decodeURIComponent(match[1]) : '';
        this.chatID = String(new URLSearchParams(location.search).get('chat') || '').trim();
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
            this.fileRequest++;
            this.selectedPath = '';
            this.selectedNode = null;
            this.currentDir = '';
            this.file = null;
            this.fileError = '';
            this.fileLoading = false;
          }
        });
        document.addEventListener('click', event => this.handleMediaPreviewClick(event));
        // Dropping a local file outside a target must not navigate away.
        window.addEventListener('dragover', event => {
          if (Array.from(event.dataTransfer?.types || []).includes('Files')) event.preventDefault();
        });
        window.addEventListener('drop', event => {
          if (Array.from(event.dataTransfer?.types || []).includes('Files')) event.preventDefault();
          this.dropTarget = null;
        });
        this.clampTreeRatio();
      },

      fileBrowserLayoutStyle() {
        const width = window.innerWidth || 1440;
        const treeWidth = Math.round(Math.max(240, Math.min(720, width * this.treeRatio)));
        return '--file-tree-width: ' + treeWidth + 'px;';
      },

      clampTreeRatio() {
        if (!Number.isFinite(this.treeRatio)) this.treeRatio = 0.26;
        this.treeRatio = Math.max(0.15, Math.min(0.55, this.treeRatio));
      },

      startTreeResize(event) {
        if ((window.innerWidth || 0) <= 760) return;
        event.preventDefault();
        this.resizingTree = true;
        const layout = event.currentTarget?.parentElement;
        const move = pointerEvent => {
          const rect = layout?.getBoundingClientRect();
          if (!rect || rect.width <= 0) return;
          this.treeRatio = Math.max(0.15, Math.min(0.55, (pointerEvent.clientX - rect.left) / rect.width));
        };
        const stop = () => {
          this.resizingTree = false;
          this.clampTreeRatio();
          writeFileBrowserPreference('fileTreeRatio', this.treeRatio.toFixed(4));
          window.removeEventListener('pointermove', move);
          window.removeEventListener('pointerup', stop);
          window.removeEventListener('pointercancel', stop);
        };
        window.addEventListener('pointermove', move);
        window.addEventListener('pointerup', stop);
        window.addEventListener('pointercancel', stop);
        move(event);
      },

      sessionURL() {
        if (!this.sessionID) return '/';
        const base = '/s/' + encodeURIComponent(this.sessionID);
        return this.chatID ? base + '/c/' + encodeURIComponent(this.chatID) : base;
      },

      filesURL(path) {
        const base = this.sessionID ? '/s/' + encodeURIComponent(this.sessionID) + '/files' : '/files';
        const value = String(path || '').trim();
        const params = new URLSearchParams();
        if (this.chatID) params.set('chat', this.chatID);
        if (value) params.set('path', value);
        const query = params.toString();
        return query ? base + '?' + query : base;
      },

      rawFileURL(path) {
        return '/api/sessions/' + encodeURIComponent(this.sessionID) + '/files/raw?path=' + encodeURIComponent(String(path || '').replace(/^\/+/, ''));
      },

      downloadFileURL(path) {
        return '/api/sessions/' + encodeURIComponent(this.sessionID) + '/files/download?path=' + encodeURIComponent(String(path || '').replace(/^\/+/, ''));
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
      selectedText() {
        const text = String(window.getSelection?.().toString() || '').trim();
        return text.length > 0 ? text : '';
      },

      async sendFileToChat(options = {}) {
        if (!this.file || this.sendBusy) return;
        if (!this.chatID) {
          this.sendStatus = 'Open this file browser from a chat to send file context.';
          this.sendStatusKind = 'danger';
          return;
        }
        const selected = this.selectedText();
        const payload = {
          chat_id: this.chatID,
          path: this.file.path,
          text: options.selection ? selected : '',
          include_content: !!options.content,
          steer: !!options.steer
        };
        if (options.selection && !payload.text) {
          this.sendStatus = 'Select text in the file preview first.';
          this.sendStatusKind = 'danger';
          return;
        }
        this.sendBusy = true;
        this.sendStatus = '';
        this.sendStatusKind = 'secondary';
        try {
          const resp = await fetch('/api/sessions/' + encodeURIComponent(this.sessionID) + '/files/send', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(payload)
          });
          if (!resp.ok) throw new Error(await resp.text());
          this.sendStatus = 'Queued in chat';
          this.sendStatusKind = 'success';
        } catch (err) {
          this.sendStatus = String(err.message || err || 'Send failed').trim();
          this.sendStatusKind = 'danger';
        } finally {
          this.sendBusy = false;
        }
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

      async refresh() {
        const open = Object.keys(this.expanded).filter(key => this.expanded[key]).sort((a, b) => a.split('/').length - b.split('/').length);
        this.childrenByPath = {};
        await this.loadTree('');
        for (const key of open) {
          if (this.visibleNodes().some(node => node.path === key && node.dir)) await this.loadTree(key);
          else delete this.expanded[key];
        }
      },

      parentPath(path) {
        const index = path.lastIndexOf('/');
        return index < 0 ? '' : path.slice(0, index);
      },

      selectRoot() {
        if (this.mutationBusy) return;
        this.currentDir = '';
        this.selectedNode = null;
      },

      beginFileDialog(action) {
        if (this.mutationBusy || (action !== 'mkdir' && !this.selectedNode)) return;
        this.fileDialog = {
          action, path: this.selectedNode?.path || '', dir: !!this.selectedNode?.dir,
          value: action === 'mkdir' ? (this.currentDir ? this.currentDir + '/' : '') : this.selectedNode.path,
          error: ''
        };
        this.$nextTick(() => {
          const input = this.$refs.fileDialogInput;
          if (action !== 'delete' && input) { input.focus(); input.select(); }
          else this.$refs.fileDialogCancel?.focus();
        });
      },

      async submitFileDialog() {
        const dialog = this.fileDialog;
        if (!dialog || this.mutationBusy) return;
        const payload = dialog.action === 'mkdir' ? {path: dialog.value}
          : {path: dialog.path, destination: dialog.value, recursive: dialog.dir};
        if (await this.changeFile(dialog.action, payload)) this.fileDialog = null;
        else dialog.error = this.mutationError;
      },

      async changeFile(action, payload) {
        if (this.mutationBusy) return false;
        this.mutationBusy = true;
        this.mutationError = '';
        this.mutationStatus = '';
        try {
          const response = await fetch('/api/sessions/' + encodeURIComponent(this.sessionID) + '/files/' + action, {
            method: 'POST', headers: {'Content-Type': 'application/json', 'X-Koder-File-Action': '1'}, body: JSON.stringify(payload)
          });
          if (!response.ok) throw new Error(await response.text());
          const affects = value => value === payload.path || value.startsWith(payload.path + '/');
          if (action === 'move' || action === 'delete') {
            for (const key of Object.keys(this.expanded)) if (affects(key)) delete this.expanded[key];
            if (affects(this.currentDir)) this.currentDir = action === 'move' ? payload.destination + this.currentDir.slice(payload.path.length) : this.parentPath(payload.path);
            if (this.selectedNode && affects(this.selectedNode.path)) {
              const next = payload.destination + this.selectedNode.path.slice(payload.path.length);
              this.selectedNode = action === 'move' ? {...this.selectedNode, path: next, name: next.split('/').pop()} : null;
            }
            if (affects(this.selectedPath)) {
              const next = action === 'move' ? payload.destination + this.selectedPath.slice(payload.path.length) : '';
              this.fileRequest++;
              this.selectedPath = next;
              this.file = null;
              this.fileError = '';
              this.fileLoading = false;
              this.setFileURL(next, {replaceURL: true});
            }
          }
          await this.refresh();
          if (action === 'move') await this.expandParents(payload.destination);
          if (this.selectedPath && !this.file) await this.loadFile(this.selectedPath, {replaceURL: true});
          this.mutationStatus = action === 'delete' ? 'Deleted ' + payload.path : action === 'move' ? 'Moved to ' + payload.destination : 'Created ' + payload.path;
          return true;
        } catch (err) {
          this.mutationError = String(err.message || err).trim();
          return false;
        } finally {
          this.mutationBusy = false;
        }
      },

      async uploadFiles(files, directory = this.currentDir) {
        files = Array.from(files || []);
        if (!files.length || this.mutationBusy) return;
        this.mutationBusy = true;
        this.mutationError = '';
        let completed = 0;
        const failures = [];
        try {
          for (const file of files) {
            const path = (directory ? directory + '/' : '') + file.name;
            this.mutationStatus = 'Uploading ' + (completed + failures.length + 1) + '/' + files.length + ': ' + file.name;
            try {
              if (file.size > 256 * 1024 * 1024) throw new Error('maximum size is 256 MB per file');
              const response = await fetch('/api/sessions/' + encodeURIComponent(this.sessionID) + '/files/upload?path=' + encodeURIComponent(path), {
                method: 'POST', headers: {'Content-Type': 'application/octet-stream', 'X-Koder-File-Action': '1'}, body: file
              });
              if (!response.ok) throw new Error(await response.text());
              completed++;
            } catch (err) { failures.push(file.name + ': ' + String(err.message || err).trim()); }
          }
          if (directory) {
            await this.expandParents(directory + '/_');
            this.expanded[directory] = true;
          }
          await this.refresh();
          this.mutationStatus = 'Uploaded ' + completed + '/' + files.length + ' files to ' + (directory || 'project root');
          this.mutationError = failures.join('\n');
        } finally { this.mutationBusy = false; }
      },

      startFileDrag(event, node) {
        if (this.mutationBusy) { event.preventDefault(); return; }
        this.dragPath = node.path;
        event.dataTransfer.effectAllowed = 'move';
        event.dataTransfer.setData('application/x-koder-file', JSON.stringify({session: this.sessionID, path: node.path}));
      },

      fileDragOver(event, directory) {
        const types = Array.from(event.dataTransfer?.types || []);
        if (this.mutationBusy || (!types.includes('Files') && !types.includes('application/x-koder-file'))) return;
        event.preventDefault();
        event.stopPropagation();
        const invalid = this.dragPath !== null && (directory === this.dragPath || directory.startsWith(this.dragPath + '/') || directory === this.parentPath(this.dragPath));
        event.dataTransfer.dropEffect = invalid ? 'none' : types.includes('Files') ? 'copy' : 'move';
        this.dropTarget = invalid ? null : directory;
      },

      async dropFiles(event, directory) {
        event.preventDefault();
        event.stopPropagation();
        this.dropTarget = null;
        if (this.mutationBusy) return;
        const transfer = event.dataTransfer;
        if (Array.from(transfer.types).includes('Files')) {
          if (Array.from(transfer.items || []).some(item => item.webkitGetAsEntry?.()?.isDirectory)) {
            this.mutationError = 'Drop files, not folders. You can create a folder first using New folder.';
            return;
          }
          await this.uploadFiles(transfer.files, directory);
          return;
        }
        try {
          const data = JSON.parse(transfer.getData('application/x-koder-file'));
          if (data.session !== this.sessionID || typeof data.path !== 'string') throw new Error('Move files within the same session.');
          if (directory === this.parentPath(data.path)) return;
          await this.changeFile('move', {path: data.path, destination: (directory ? directory + '/' : '') + data.path.split('/').pop()});
        } catch (err) { this.mutationError = String(err.message || err); }
      },

      async openNode(node) {
        if (!node || this.mutationBusy) return;
        this.selectedNode = node;
        this.currentDir = node.dir ? node.path : this.parentPath(node.path);
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
        const request = ++this.fileRequest;
        this.selectedPath = path;
        this.selectedNode = {path, name: path.split('/').pop(), dir: false};
        this.currentDir = this.parentPath(path);
        this.fileLoading = true;
        this.fileError = '';
        this.file = null;
        this.setFileURL(path, options);
        try {
          const response = await fetch('/api/sessions/' + encodeURIComponent(this.sessionID) + '/files/read?path=' + encodeURIComponent(path), {cache: 'no-store'});
          if (!response.ok) throw new Error(await response.text() || 'file load failed');
          const file = await response.json();
          if (request !== this.fileRequest) return;
          this.file = file;
          this.projectRoot = this.file.project_root || this.projectRoot;
          this.viewMode = this.file.markdown ? 'preview' : 'source';
        } catch (err) {
          if (request === this.fileRequest) this.fileError = err.message || String(err);
        } finally {
          if (request === this.fileRequest) this.fileLoading = false;
        }
      },

      fileMeta() {
        if (!this.file) return '';
        const parts = [formatBytes(this.file.size)];
        if (this.file.language) parts.push(this.file.language);
        if (this.file.modified) parts.push(new Date(this.file.modified).toLocaleString());
        return parts.join(' · ');
      },

      isImageFile() {
        const mime = String(this.file?.mime || '').toLowerCase();
        return Boolean(this.file?.image || mime.startsWith('image/'));
      },

      imagePreviewURL() {
        return this.file?.path ? this.rawFileURL(this.file.path) : '';
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

      markdownHTML(source, filePath) {
        const text = String(source || '');
        if (!text.trim()) return '';
        if (!window.marked) return '<pre>' + escapeHTML(text) + '</pre>';
        marked.setOptions({gfm: true, breaks: false});
        let html = marked.parse(text);
        html = rewriteMarkdownImageSources(html, filePath || this.file?.path || '', path => this.rawFileURL(path));
        html = sanitizeHTML(html);
        html = renderMermaidPlaceholders(html);
        html = highlightMarkdownCode(html);
        return sanitizeHTML(html);
      },

      handleMediaPreviewClick(event) {
        const mermaidTrigger = event.target?.closest?.('.mermaid-diagram .media-expand-button');
        if (mermaidTrigger) {
          event.preventDefault();
          const diagram = mermaidTrigger.closest('.mermaid-diagram');
          const svg = diagram?.querySelector('.mermaid-diagram-content svg');
          this.openMermaidLightbox(svg ? svg.outerHTML : '', 'Mermaid diagram', 'Drag to pan, pinch, wheel or buttons to zoom');
          return;
        }
        const imageTrigger = event.target?.closest?.('.file-browser-image-preview .media-expand-button, .file-browser-markdown img');
        if (!imageTrigger) return;
        event.preventDefault();
        const img = imageTrigger.matches('img') ? imageTrigger : imageTrigger.closest('.file-browser-image-preview')?.querySelector('img');
        if (!img?.src) return;
        this.openImageLightbox(img.src, img.getAttribute('data-source-path') || img.getAttribute('alt') || this.file?.path || 'Image', 'Drag to pan, pinch, wheel or buttons to zoom');
      },

      openMermaidLightbox(html, title, meta) {
        html = sanitizeMermaidSVG(html || '');
        if (!html) return;
        this.imageLightbox = {open: true, kind: 'svg', src: '', html, title: title || 'Mermaid diagram', meta: meta || 'Drag to pan, pinch, wheel or buttons to zoom', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0, pointers: {}, pinchDistance: 0, pinchZoom: 1};
      },

      openImageLightbox(src, title, meta) {
        src = String(src || '').trim();
        if (!src) return;
        this.imageLightbox = {open: true, kind: 'image', src, html: '', title: title || 'Image', meta: meta || 'Drag to pan, pinch, wheel or buttons to zoom', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0, pointers: {}, pinchDistance: 0, pinchZoom: 1};
      },

      closeImageLightbox() {
        this.imageLightbox = {open: false, kind: 'svg', src: '', html: '', title: '', meta: '', zoom: 1, panX: 0, panY: 0, dragging: false, dragX: 0, dragY: 0, pointers: {}, pinchDistance: 0, pinchZoom: 1};
      },

      lightboxTransform() {
        const box = this.imageLightbox || {};
        return 'translate(' + (box.panX || 0) + 'px, ' + (box.panY || 0) + 'px) scale(' + (box.zoom || 1) + ')';
      },

      zoomLightbox(delta) {
        const current = Number(this.imageLightbox.zoom || 1);
        this.imageLightbox.zoom = this.clampLightboxZoom(current + delta);
      },

      clampLightboxZoom(value) {
        const zoom = Number(value || 1);
        return Math.max(0.25, Math.min(8, zoom));
      },

      resetLightboxView() {
        this.imageLightbox.zoom = 1; this.imageLightbox.panX = 0; this.imageLightbox.panY = 0; this.imageLightbox.pinchDistance = 0; this.imageLightbox.pinchZoom = 1;
      },

      onLightboxWheel(event) {
        event.preventDefault();
        const direction = event.deltaY < 0 ? 0.2 : -0.2;
        this.zoomLightbox(direction);
      },

      startLightboxPan(event) {
        if (!this.imageLightbox.open) return;
        event.preventDefault();
        event.currentTarget?.setPointerCapture?.(event.pointerId);
        const pointers = Object.assign({}, this.imageLightbox.pointers || {});
        pointers[event.pointerId] = {x: event.clientX, y: event.clientY};
        this.imageLightbox.pointers = pointers;
        const active = Object.values(pointers);
        if (active.length >= 2) {
          this.imageLightbox.dragging = false;
          this.imageLightbox.pinchDistance = this.lightboxPointerDistance(active[0], active[1]);
          this.imageLightbox.pinchZoom = Number(this.imageLightbox.zoom || 1);
          return;
        }
        this.imageLightbox.dragging = true;
        this.imageLightbox.dragX = event.clientX - (this.imageLightbox.panX || 0);
        this.imageLightbox.dragY = event.clientY - (this.imageLightbox.panY || 0);
      },

      moveLightboxPan(event) {
        const pointers = Object.assign({}, this.imageLightbox.pointers || {});
        if (pointers[event.pointerId]) {
          pointers[event.pointerId] = {x: event.clientX, y: event.clientY};
          this.imageLightbox.pointers = pointers;
          const active = Object.values(pointers);
          if (active.length >= 2) {
            event.preventDefault();
            const distance = this.lightboxPointerDistance(active[0], active[1]);
            const baseDistance = Number(this.imageLightbox.pinchDistance || distance);
            const baseZoom = Number(this.imageLightbox.pinchZoom || this.imageLightbox.zoom || 1);
            if (baseDistance > 0) this.imageLightbox.zoom = this.clampLightboxZoom(baseZoom * (distance / baseDistance));
            return;
          }
        }
        if (!this.imageLightbox.dragging) return;
        this.imageLightbox.panX = event.clientX - (this.imageLightbox.dragX || 0);
        this.imageLightbox.panY = event.clientY - (this.imageLightbox.dragY || 0);
      },

      stopLightboxPan(event) {
        const pointers = Object.assign({}, this.imageLightbox.pointers || {});
        if (event?.pointerId !== undefined) delete pointers[event.pointerId];
        this.imageLightbox.pointers = pointers;
        const active = Object.values(pointers);
        if (active.length >= 2) {
          this.imageLightbox.dragging = false;
          this.imageLightbox.pinchDistance = this.lightboxPointerDistance(active[0], active[1]);
          this.imageLightbox.pinchZoom = Number(this.imageLightbox.zoom || 1);
          return;
        }
        if (active.length === 1) {
          this.imageLightbox.dragging = true;
          this.imageLightbox.dragX = active[0].x - (this.imageLightbox.panX || 0);
          this.imageLightbox.dragY = active[0].y - (this.imageLightbox.panY || 0);
          this.imageLightbox.pinchDistance = 0;
          this.imageLightbox.pinchZoom = Number(this.imageLightbox.zoom || 1);
          return;
        }
        this.imageLightbox.dragging = false;
        this.imageLightbox.pinchDistance = 0;
        this.imageLightbox.pinchZoom = Number(this.imageLightbox.zoom || 1);
      },

      lightboxPointerDistance(a, b) {
        const dx = Number(a?.x || 0) - Number(b?.x || 0);
        const dy = Number(a?.y || 0) - Number(b?.y || 0);
        return Math.hypot(dx, dy);
      },

      renderDiagrams(root) {
        return renderMermaidDiagrams(root, {
          configure: configureMermaid,
          idPrefix: 'files-mermaid',
          errorHTML(err, source) {
            const details = errorDetails(err);
            return '<div class="mermaid-error">Mermaid render failed</div><pre class="mermaid-error-detail">' + escapeHTML(details) + '</pre><pre>' + escapeHTML(source) + '</pre>';
          }
        });
      },
    };
  };
})();
