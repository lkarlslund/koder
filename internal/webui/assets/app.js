    function escapeHTML(value) {
      return String(value || '').replace(/[&<>"']/g, ch => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[ch]));
    }
    function sanitizeHTML(html) {
      if (!window.DOMPurify) return html;
      return DOMPurify.sanitize(html, {
        ADD_ATTR: ['class'],
        FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed', 'form', 'input', 'button']
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
    function renderMarkdown(text) {
      const source = String(text || '');
      if (!source.trim()) return '';
      if (!window.marked) return '<pre>' + escapeHTML(source) + '</pre>';
      marked.setOptions({gfm: true, breaks: false});
      let html = marked.parse(source);
      html = sanitizeHTML(html);
      html = highlightMarkdownCode(html);
      return sanitizeHTML(html);
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
    function toolStatus(tool) {
      return String((tool && (tool.status || tool.Status)) || '').toLowerCase();
    }
    function toolResultHeader(title) {
      return '<div class="tool-result-header">' + escapeHTML(title) + '</div>';
    }
    function renderCompactBlock(title, lines) {
      const body = compactLines(lines).map(line => {
        const cls = line.omitted ? 'tool-result-line tool-result-omitted' : 'tool-result-line';
        return '<div class="' + cls + '">' + escapeHTML(line.text || ' ') + '</div>';
      }).join('');
      return toolResultHeader(title) + '<div class="tool-result-body">' + body + '</div>';
    }
    function renderKeyValueBlock(title, pairs) {
      const lines = pairs.filter(pair => pair[1] !== undefined && pair[1] !== null && String(pair[1]) !== '').map(pair => pair[0] + ': ' + pair[1]);
      return renderCompactBlock(title, lines);
    }
    function renderDiffBlock(title, diff) {
      const rows = splitLines(diff).map(line => {
        let cls = 'tool-diff-line';
        if (line.startsWith('+') && !line.startsWith('+++')) cls += ' tool-diff-add';
        else if (line.startsWith('-') && !line.startsWith('---')) cls += ' tool-diff-del';
        else if (line.startsWith('@@') || line.startsWith('---') || line.startsWith('+++')) cls += ' tool-diff-meta';
        return '<div class="' + cls + '">' + escapeHTML(line || ' ') + '</div>';
      }).join('');
      return toolResultHeader(title) + '<div>' + (rows || '<div class="tool-result-body text-secondary">No diff</div>') + '</div>';
    }
    function renderShowImageBlock(data, fallbackText) {
      const path = firstValue(data, ['path', 'Path']);
      const sourcePath = firstValue(data, ['source_path', 'SourcePath']) || path;
      const mime = firstValue(data, ['mime_type', 'MIMEType']);
      const summary = firstValue(data, ['summary', 'Summary']) || fallbackText || ('Showed image ' + path);
      if (!sourcePath) return renderCompactBlock('Showed image', summary);
      const src = '/api/show-image?path=' + encodeURIComponent(sourcePath);
      const meta = [path, mime].filter(Boolean).join(' · ');
      return toolResultHeader(summary || 'Showed image') +
        '<div class="tool-result-body">' +
          '<img class="img-fluid rounded border bg-body-tertiary" alt="' + escapeHTML(path || 'shown image') + '" src="' + escapeHTML(src) + '">' +
          (meta ? '<div class="small text-secondary mt-2">' + escapeHTML(meta) + '</div>' : '') +
        '</div>';
    }
    function toolTitleText(tool) {
      const kind = String((tool && tool.tool) || '');
      const data = toolData(tool);
      const args = toolArgs(tool);
      const path = firstValue(data, ['path', 'Path']) || firstValue(args, ['path']);
      const command = firstValue(data, ['command', 'Command']) || firstValue(args, ['command']);
      switch (kind) {
        case 'read': return path ? 'Read ' + path : 'Read';
        case 'write': return path ? 'Write ' + path : 'Write file';
        case 'edit': return path ? 'Edit ' + path : 'Edit file';
        case 'apply_patch': return 'Apply patch';
        case 'bash': return toolStatus(tool) === 'done' && command ? 'Ran ' + command : 'Run command';
        case 'exec_command': return 'Start exec';
        case 'exec_status': return 'Exec status';
        case 'exec_list': return 'Exec sessions';
        case 'exec_write_stdin': return 'Write exec stdin';
        case 'exec_resize': return 'Resize exec';
        case 'exec_terminate': return 'Terminate exec';
        case 'exec_cleanup_background': return 'Clean exec sessions';
        case 'grep': return 'Search ' + (firstValue(data, ['pattern', 'Pattern']) || firstValue(args, ['pattern']));
        case 'glob': return 'Glob ' + (firstValue(data, ['pattern', 'Pattern']) || firstValue(args, ['pattern']));
        case 'webfetch': return 'Fetch ' + (firstValue(data, ['url', 'URL']) || firstValue(args, ['url']));
        case 'websearch': return 'Search web ' + (firstValue(data, ['query', 'Query']) || firstValue(args, ['query']));
        case 'show_image': return path ? 'Show image ' + path : 'Show image';
        default: return kind || 'Tool';
      }
    }
    function toolPreviewText(tool) {
      const args = toolArgs(tool);
      if (String((tool && tool.tool) || '') === 'bash' && toolStatus(tool) === 'done') return '';
      const values = [];
      if (args.command) values.push(args.command);
      for (const key of ['path', 'pattern', 'query', 'url', 'include']) {
        if (args[key]) values.push(key + '=' + args[key]);
      }
      return values.slice(0, 2).join('  ');
    }
    function execResultLines(data, fallback) {
      const output = firstValue(data, ['output', 'Output']);
      if (output) return output;
      const lines = [];
      const message = firstValue(data, ['message', 'Message']);
      const processID = firstValue(data, ['process_id', 'ProcessID']);
      const state = firstValue(data, ['state', 'State']);
      const exitCode = firstValue(data, ['exit_code', 'ExitCode']);
      if (message) lines.push(message);
      if (processID) lines.push('process_id: ' + processID);
      if (state) lines.push('state: ' + state);
      if (exitCode !== '') lines.push('exit_code: ' + exitCode);
      return lines.length ? lines : fallback;
    }
    function renderToolResult(tool) {
      const kind = String((tool && tool.tool) || '');
      const result = (tool && tool.result) || {};
      const data = toolData(tool);
      const args = toolArgs(tool);
      const status = firstValue(result, ['status', 'Status']);
      if (status === 'error' || status === 'denied') return renderCompactBlock(status, toolResultText(tool));
      if (kind === 'write') {
        const path = firstValue(data, ['path', 'Path']) || firstValue(args, ['path']) || 'file';
        const content = firstValue(data, ['content', 'Content']);
        const summary = firstValue(data, ['summary', 'Summary']) || toolResultText(tool);
        if (content) return renderCompactBlock(summary || ('Wrote ' + path), content);
        return renderCompactBlock('Wrote ' + path, summary);
      }
      if (kind === 'edit' || kind === 'apply_patch') {
        const title = firstValue(data, ['summary', 'Summary']) || (kind === 'edit' ? 'Edited file' : 'Applied patch');
        const diff = firstValue(data, ['diff', 'Diff']) || firstValue(result, ['diff', 'Diff']) || toolResultText(tool);
        return renderDiffBlock(title, diff);
      }
      if (kind === 'read') {
        const path = firstValue(data, ['path', 'Path']) || firstValue(args, ['path']) || 'read';
        const storedLines = data.lines || data.Lines || [];
        const lines = storedLines.length ? storedLines.map(line => (line.number || line.Number || '') + ': ' + (line.text || line.Text || '')) : toolResultText(tool);
        return renderCompactBlock(path, lines);
      }
      if (kind === 'bash') {
        return renderCompactBlock('Output', firstValue(data, ['output', 'Output']) || toolResultText(tool));
      }
      if (kind.startsWith('exec_')) {
        return renderCompactBlock('Result', execResultLines(data, toolResultText(tool)));
      }
      if (kind === 'glob') return renderCompactBlock('Matches', data.matches || data.Matches || toolResultText(tool));
      if (kind === 'grep') return renderCompactBlock('Matches', firstValue(data, ['output', 'Output']) || toolResultText(tool));
      if (kind === 'webfetch') return renderCompactBlock(firstValue(data, ['final_url', 'FinalURL', 'url', 'URL']) || 'Fetched page', firstValue(data, ['body', 'Body']) || toolResultText(tool));
      if (kind === 'websearch') {
        const items = data.items || data.Items || [];
        return renderCompactBlock('Search results', items.length ? items.map((item, idx) => (idx + 1) + '. ' + (item.title || item.Title || item.url || item.URL || '')) : toolResultText(tool));
      }
      if (kind === 'view_image') {
        return renderKeyValueBlock('Viewed image', [['path', firstValue(data, ['path', 'Path'])], ['mime', firstValue(data, ['mime_type', 'MIMEType'])], ['detail', firstValue(data, ['detail', 'Detail'])]]);
      }
      if (kind === 'show_image') return renderShowImageBlock(data, toolResultText(tool));
      return renderCompactBlock(kind || 'Tool result', toolResultText(tool));
    }
    function koderApp() {
      return {
        ws: null, nextID: 1, pending: {}, state: {}, connected: false, draft: '', showPermissions: false,
        showModels: false, modelLoading: false, modelQuery: '', modelOptions: [],
        showSessions: false, sessionLoading: false, sessionState: {active_id: 0, workdir: '', sessions: []}, newSessionTitle: '',
        showProviders: false, providerState: {catalog: [], providers: [], drafts: {}}, providerDraft: null, providerHeadersText: '{}', providerStatus: '', providerStatusKind: 'secondary', providerTesting: false, providerSaving: false,
        completion: {kind: '', query: '', start: 0, end: 0, items: [], selected: 0}, completionSeq: 0,
        theme: readPreference('theme', 'auto'), sidebarRatio: Number(readPreference('sidebarRatio', '0.22')), resizingSidebar: false, restoreChatAttempted: false, error: '', toast: '', toastTimer: null,
        init() { this.clampSidebarRatio(); this.applyTheme(); this.connect(); window.addEventListener('resize', () => this.resizeComposer()); this.$nextTick(() => this.resizeComposer()); },
        applyTheme() {
          const resolved = this.theme === 'auto' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : this.theme;
          document.documentElement.setAttribute('data-bs-theme', resolved);
        },
        appShellStyle() { return '--sidebar-width: ' + this.sidebarWidth() + 'px;'; },
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
          if (this.ws && (this.ws.readyState === WebSocket.CONNECTING || this.ws.readyState === WebSocket.OPEN)) return;
          const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
          const ws = new WebSocket(proto + '//' + location.host + '/ws');
          this.ws = ws;
          ws.onopen = () => {
            if (this.ws !== ws) return;
            this.connected = true;
            this.rpcOn(ws, 'hello', {}).then(hello => this.applyHello(hello)).catch(() => {});
          };
          ws.onclose = () => {
            if (this.ws !== ws) return;
            this.connected = false;
            this.rejectPending('connection closed');
            setTimeout(() => this.connect(), 1000);
          };
          ws.onmessage = ev => { if (this.ws === ws) this.onMessage(JSON.parse(ev.data)); };
        },
        applyHello(hello) {
          if (hello && hello.asset_hash && window.KODER_ASSET_HASH && hello.asset_hash !== window.KODER_ASSET_HASH) {
            location.reload();
            return;
          }
          this.applyState((hello && hello.state) || hello || {}, {scrollToBottom: true});
        },
        onMessage(msg) {
          if (msg.type) { this.onPush(msg); return; }
          const p = this.pending[msg.id]; if (!p) return; delete this.pending[msg.id];
          msg.ok ? p.resolve(msg.result) : p.reject(new Error(msg.error || 'rpc failed'));
        },
        onPush(msg) {
          if (msg.type === 'snapshot') this.applyState(msg.payload);
          if (msg.type === 'theme') { this.theme = msg.payload.theme || 'auto'; writePreference('theme', this.theme); this.applyTheme(); }
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
        transcriptScrollState() {
          const el = document.querySelector('.transcript');
          if (!el) return {el: null, top: 0, nearBottom: true};
          const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
          return {el, top: el.scrollTop, nearBottom: distance <= 48};
        },
        afterTranscriptDOMUpdate(fn) {
          this.$nextTick(() => {
            requestAnimationFrame(() => {
              fn();
              setTimeout(fn, 0);
            });
          });
        },
        restoreTranscriptScroll(scroll, options = {}) {
          const el = document.querySelector('.transcript');
          if (!el) return;
          if (options.scrollToBottom || scroll.nearBottom) {
            el.scrollTop = el.scrollHeight;
            return;
          }
          el.scrollTop = scroll.top;
        },
        applyState(s, options = {}) {
          const scroll = this.transcriptScrollState();
          this.state = s || {}; this.applyTheme(); this.error = this.state.error || '';
          if (!this.restoreSelectedChat()) this.writeSelectedChat();
          this.afterTranscriptDOMUpdate(() => this.restoreTranscriptScroll(scroll, options));
        },
        selectedChatPreferenceName() { return 'selectedChat.' + encodeURIComponent(this.state.workdir || this.state.Workdir || ''); },
        activeChatID() { return this.state.active_chat_id || this.state.ActiveChatID || 0; },
        writeSelectedChat() { const id = this.activeChatID(); if (id) writePreference(this.selectedChatPreferenceName(), id); },
        restoreSelectedChat() {
          if (this.restoreChatAttempted) return false;
          const raw = readPreference(this.selectedChatPreferenceName(), '');
          const id = Number(raw);
          if (!id) { this.restoreChatAttempted = true; return false; }
          const exists = (this.state.chats || this.state.Chats || []).some(chat => this.chatID(chat) === id);
          this.restoreChatAttempted = true;
          if (!exists || id === this.activeChatID()) return false;
          this.rpc('switch_chat', {chat_id: id}).then(s => this.applyState(s, {scrollToBottom: true}));
          return true;
        },
        timeline() { return this.state.snapshot?.Timeline || this.state.snapshot?.timeline || []; },
        approvals() { return this.state.snapshot?.Approvals || this.state.snapshot?.approvals || []; },
        pendingText() { const p = this.state.snapshot?.PendingAssistant || this.state.snapshot?.pending_assistant || {}; return [p.Reasoning || p.reasoning, p.Text || p.text].filter(Boolean).join('\n'); },
        markdownHTML(text) { return renderMarkdown(text); },
        statusText() { return this.state.snapshot?.StatusText || this.state.snapshot?.status_text || this.state.snapshot?.Status || 'idle'; },
        activeProvider() { return this.state.session?.provider_id || this.state.session?.ProviderID || ''; },
        activeModel() { return this.state.session?.model_id || this.state.session?.ModelID || ''; },
        milestones() { return this.state.milestones || this.state.Milestones || {}; },
        milestoneItems() { return this.milestones().milestones || this.milestones().Milestones || []; },
        milestoneSummary() { return this.milestones().summary || this.milestones().Summary || ''; },
        milestoneRef(m) { return m.Ref || m.ref || ''; },
        milestoneTitle(m) { return m.Title || m.title || this.milestoneRef(m); },
        milestoneStatus(m) { return m.Status || m.status || 'pending'; },
        milestoneNotes(m) { return m.Notes || m.notes || ''; },
        milestoneBadge(status) {
          if (status === 'completed') return 'text-bg-success';
          if (status === 'blocked') return 'text-bg-danger';
          if (status === 'in_progress' || status === 'decomposing' || status === 'executing') return 'text-bg-primary';
          return 'text-bg-secondary';
        },
        todoItems() { return this.state.todos || this.state.Todos || []; },
        todoStatus(todo) { return todo.Status || todo.status || 'pending'; },
        todoIcon(status) {
          if (status === 'completed') return 'bi-check-circle-fill text-success';
          if (status === 'in_progress') return 'bi-arrow-repeat text-primary';
          return 'bi-circle text-secondary';
        },
        chatID(chat) { return chat.ID || chat.id; },
        chatStatus(chat) {
          const id = this.chatID(chat);
          const statuses = this.state.chat_statuses || this.state.ChatStatuses || [];
          return statuses.find(status => (status.chat_id || status.ChatID) === id) || {chat_id: id, status: 'idle', status_text: 'Idle'};
        },
        chatStatusValue(chat) {
          const status = this.chatStatus(chat);
          return String(status.status || status.Status || 'idle');
        },
        chatStatusLabel(chat) {
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
        chatStatusClass(chat) {
          const value = this.chatStatusValue(chat).replaceAll('_', '-');
          return 'status-' + value;
        },
        chatStatusIcon(chat) {
          const value = this.chatStatusValue(chat);
          if (value === 'waiting_approval') return 'bi-pause-circle-fill';
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
        gitFileCode(file) { return file.code || file.Code || ''; },
        gitAdditions(file) { return file.additions ?? file.Additions ?? 0; },
        gitDeletions(file) { return file.deletions ?? file.Deletions ?? 0; },
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
        refreshWorkspace() { this.rpc('refresh_workspace', {}).then(s => this.applyState(s)); },
        toolIcon(kind) {
          if (kind === 'read' || kind === 'write' || kind === 'edit' || kind === 'apply_patch') return 'bi-file-earmark-code';
          if (kind === 'bash' || String(kind || '').startsWith('exec_')) return 'bi-terminal';
          if (kind === 'grep' || kind === 'glob' || kind === 'websearch') return 'bi-search';
          if (kind === 'webfetch') return 'bi-globe';
          if (kind === 'view_image' || kind === 'show_image') return 'bi-image';
          return 'bi-wrench-adjustable';
        },
        toolTitle(tool) { return toolTitleText(tool); },
        toolPreview(tool) { return toolPreviewText(tool); },
        toolCallID(tool) { return tool?.tool_call_id || tool?.ToolCallID || ''; },
        toolApprovalPending(tool) {
          return this.toolCallID(tool) && toolStatus(tool) === 'awaiting_approval';
        },
        toolResultHTML(tool) { return renderToolResult(tool); },
        resizeComposer() {
          const el = this.$refs.composerInput; if (!el) return;
          const maxHeight = Math.floor((window.innerHeight || 800) * 0.2);
          el.style.height = 'auto';
          const nextHeight = Math.min(el.scrollHeight, maxHeight);
          el.style.height = nextHeight + 'px';
          el.style.overflowY = el.scrollHeight > maxHeight ? 'auto' : 'hidden';
        },
        onComposerInput() { this.resizeComposer(); this.updateCompletions(); },
        onComposerKeydown(ev) {
          if (this.completion.items.length > 0) {
            if (ev.key === 'ArrowDown') { ev.preventDefault(); this.completion.selected = Math.min(this.completion.items.length - 1, this.completion.selected + 1); return; }
            if (ev.key === 'ArrowUp') { ev.preventDefault(); this.completion.selected = Math.max(0, this.completion.selected - 1); return; }
            if (ev.key === 'Tab' || ev.key === 'Enter') { ev.preventDefault(); this.acceptCompletion(this.completion.selected); return; }
            if (ev.key === 'Escape') { ev.preventDefault(); this.clearCompletions(); return; }
          }
          if (ev.key === 'Enter' && !ev.shiftKey) { ev.preventDefault(); this.send(); }
          if (ev.key === 'Enter' && (ev.metaKey || ev.ctrlKey)) { ev.preventDefault(); this.send(); }
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
        send() { const text = this.draft.trim(); if (!text) return; if (this.handleSlash(text)) { this.draft = ''; this.clearCompletions(); this.$nextTick(() => this.resizeComposer()); return; } this.draft = ''; this.clearCompletions(); this.$nextTick(() => this.resizeComposer()); this.rpc('send_prompt', {text}); },
        handleSlash(text) {
          if (text === '/permissions') { this.showPermissions = true; return true; }
          if (text === '/compact') { this.rpc('compact', {}); return true; }
          if (text === '/chat new') { this.newChat(); return true; }
          if (text.startsWith('/')) { this.error = 'Unknown web command: ' + text; return true; }
          return false;
        },
        switchChat(id) { if (id) this.rpc('switch_chat', {chat_id: id}).then(s => { this.applyState(s, {scrollToBottom: true}); this.writeSelectedChat(); }); },
        newChat() { this.rpc('new_chat', {title: 'Chat'}).then(s => { this.applyState(s, {scrollToBottom: true}); this.writeSelectedChat(); }); },
        deleteChat(id) {
          if (!id || !confirm('Delete this chat?')) return;
          this.rpc('delete_chat', {chat_id: id}).then(s => this.applyState(s)).catch(err => this.showToast(err.message));
        },
        showToast(message) {
          this.toast = message || '';
          if (this.toastTimer) clearTimeout(this.toastTimer);
          this.toastTimer = setTimeout(() => { this.toast = ''; this.toastTimer = null; }, 4500);
        },
        permissionProfiles() { return this.state.permissions?.profiles || this.state.Permissions?.Profiles || []; },
        activePermission() { return this.state.permissions?.active || this.state.Permissions?.Active || ''; },
        permissionName(profile) { return profile.Name || profile.name; },
        permissionLabel(name) { const p = this.permissionProfiles().find(p => this.permissionName(p) === name); return p ? (p.Label || p.label || name) : (name || '-'); },
        setPermission(profile) { this.rpc('set_permission_profile', {profile}).then(s => { this.applyState(s); this.showPermissions = false; }); },
        openModelDialog() {
          this.showModels = true; this.modelLoading = true; this.modelQuery = '';
          this.$nextTick(() => this.$refs.modelSearch?.focus());
          this.rpc('list_models', {}).then(result => { this.modelOptions = result.models || []; }).finally(() => { this.modelLoading = false; });
        },
        closeModelDialog() { this.showModels = false; },
        filteredModels() {
          const q = this.modelQuery.trim().toLowerCase();
          const models = this.modelOptions || [];
          if (!q) return models;
          return models.filter(m => [m.provider_id, m.provider_label, m.model_id, m.owned_by].filter(Boolean).join(' ').toLowerCase().includes(q));
        },
        selectModel(model) {
          this.rpc('set_model', {provider_id: model.provider_id, model_id: model.model_id}).then(s => {
            this.applyState(s); this.closeModelDialog();
          });
        },
        openSessionDialog() {
          this.showSessions = true; this.sessionLoading = true; this.newSessionTitle = '';
          this.rpc('list_sessions', {}).then(result => { this.sessionState = result || {active_id: 0, workdir: '', sessions: []}; }).finally(() => { this.sessionLoading = false; });
        },
        closeSessionDialog() { this.showSessions = false; },
        sessionRows() { return this.sessionState.sessions || this.state.sessions || []; },
        activeSessionID() { return this.sessionState.active_id || this.state.session?.ID || this.state.session?.id || 0; },
        sessionID(session) { return session.ID || session.id; },
        sessionTitle(session) { return session.Title || session.title || 'New Session'; },
        sessionModel(session) { return (session.ProviderID || session.provider_id || '-') + ' / ' + (session.ModelID || session.model_id || '-'); },
        switchSession(id) {
          if (!id || id === this.activeSessionID()) { this.closeSessionDialog(); return; }
          this.rpc('switch_session', {session_id: id}).then(s => { this.applyState(s); this.closeSessionDialog(); });
        },
        createSession() {
          this.rpc('new_session', {title: this.newSessionTitle || 'New Session'}).then(s => { this.applyState(s); this.closeSessionDialog(); });
        },
        openProviderDialog() {
          this.showProviders = true; this.providerStatus = ''; this.providerStatusKind = 'secondary';
          this.rpc('provider_state', {}).then(state => {
            this.providerState = state || {catalog: [], providers: [], drafts: {}};
            if (this.providerRows().length > 0) this.editProvider(this.providerRows()[0].id);
            else this.addProvider();
          });
        },
        closeProviderDialog() { this.showProviders = false; this.providerDraft = null; this.providerStatus = ''; },
        providerTemplates() { return this.providerState.catalog || []; },
        providerRows() { return this.providerState.providers || []; },
        setProviderDraft(draft) {
          this.providerDraft = Object.assign({headers: {}}, draft || {});
          this.providerHeadersText = JSON.stringify(this.providerDraft.headers || {}, null, 2);
        },
        editProvider(id) {
          const draft = (this.providerState.drafts || {})[id];
          if (draft) { this.setProviderDraft(draft); this.providerStatus = ''; }
        },
        addProvider() {
          const first = this.providerTemplates()[0]?.id || 'openai-compatible';
          this.rpc('new_provider_draft', {template_id: first}).then(draft => { this.setProviderDraft(draft); this.providerStatus = ''; });
        },
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
            this.providerStatus = 'Test passed: ' + count + ' model' + (count === 1 ? '' : 's') + (sample ? ' (' + sample + ')' : '');
            this.providerStatusKind = 'success';
          }).catch(err => { this.providerStatus = err.message; this.providerStatusKind = 'danger'; }).finally(() => { this.providerTesting = false; });
        },
        saveProvider() {
          const payload = this.providerDraftPayload(); if (!payload) return;
          this.providerSaving = true; this.providerStatus = ''; this.providerStatusKind = 'secondary';
          this.rpc('save_provider', payload).then(result => {
            this.providerState = result.providers || result;
            if (result.state) this.applyState(result.state);
            this.providerStatus = 'Saved provider'; this.providerStatusKind = 'success';
            this.editProvider(payload.provider_id);
          }).catch(err => { this.providerStatus = err.message; this.providerStatusKind = 'danger'; }).finally(() => { this.providerSaving = false; });
        },
        deleteProvider(id) {
          if (!id || !confirm('Delete this provider?')) return;
          this.rpc('delete_provider', {provider_id: id}).then(result => {
            this.providerState = result.providers || result;
            if (result.state) this.applyState(result.state);
            const next = this.providerRows()[0];
            if (next) this.editProvider(next.id); else this.addProvider();
          }).catch(err => this.showToast(err.message));
        },
        setTheme(theme) { writePreference('theme', theme); this.applyTheme(); this.rpc('set_theme', {theme}); }
      }
    }
    function preferenceKey(name) { return 'koder.' + name; }
    function readPreference(name, fallback) {
      try { return localStorage.getItem(preferenceKey(name)) || fallback; } catch (_) { return fallback; }
    }
    function writePreference(name, value) {
      try { localStorage.setItem(preferenceKey(name), String(value)); } catch (_) {}
    }
