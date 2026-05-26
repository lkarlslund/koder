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
    function toolErrorText(tool) {
      const err = (tool && (tool.error || tool.Error)) || {};
      return firstValue(err, ['message', 'Message', 'code', 'Code']);
    }
    function toolStatus(tool) {
      return String((tool && (tool.status || tool.Status)) || '').toLowerCase();
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
      return toolStatus(tool);
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
    function toolResultHeader(title) {
      return '<div class="tool-result-header">' + escapeHTML(title) + '</div>';
    }
    function renderCompactBlock(title, lines, extraClass = '') {
      const body = compactLines(lines).map(line => {
        const cls = line.omitted ? 'tool-result-line tool-result-omitted' : 'tool-result-line';
        return '<div class="' + cls + '">' + escapeHTML(line.text || ' ') + '</div>';
      }).join('');
      const bodyClass = ['tool-result-body', extraClass].filter(Boolean).join(' ');
      return toolResultHeader(title) + '<div class="' + bodyClass + '">' + body + '</div>';
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
        case 'bash': {
          if ((toolStatus(tool) === 'done' || toolStatus(tool) === 'errored') && command) return 'Ran ' + command;
          return 'Run command';
        }
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
      if (String((tool && tool.tool) || '') === 'bash' && (toolStatus(tool) === 'done' || toolStatus(tool) === 'errored')) return '';
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
      if (kind === 'edit') {
        const title = firstValue(data, ['summary', 'Summary']) || 'Edited file';
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
        return renderCompactBlock('Result', execResultLines(data, toolResultText(tool)), 'tool-result-body-mono');
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
        ws: null, reconnectTimer: null, connectWatchdog: null, reconnectDelay: 150, reconnectProbe: null, nextID: 1, pending: {}, clientID: '', clientStateTimer: null, state: {}, connected: false, connecting: true, draft: '', showPermissions: false,
        showModels: false, modelLoading: false, modelQuery: '', modelOptions: [],
        showSettings: false, settingsLoading: false, settingsSaving: false, settingsTab: 'general', settings: null, settingsStatus: '', settingsStatusKind: 'secondary', selectedPermissionProfile: '',
        showSessions: false, sessionLoading: false, sessionState: {active_id: 0, workdir: '', sessions: []}, newSessionTitle: '',
        providerState: {catalog: [], providers: [], drafts: {}}, showProviderEditor: false, providerDraft: null, providerHeadersText: '{}', providerStatus: '', providerStatusKind: 'secondary', providerTesting: false, providerSaving: false,
        showMCPEditor: false, mcpDraft: null, mcpHeadersText: '{}', mcpStatus: '', mcpStatusKind: 'secondary',
        completion: {kind: '', query: '', start: 0, end: 0, items: [], selected: 0}, completionSeq: 0,
        theme: readPreference('theme', 'auto'), sidebarRatio: Number(readPreference('sidebarRatio', '0.22')), resizingSidebar: false, restoreChatAttempted: false, transcriptStickToBottom: true, scrollRestoreSeq: 0, expandedMilestones: {}, interruptArmedChatID: '', dragChatID: '', composerAttachments: [], error: '', toast: '', toastTimer: null,
        init() {
          this.clampSidebarRatio();
          this.applyTheme();
          this.connect();
          window.addEventListener('resize', () => { this.resizeComposer(); this.reportClientStateSoon(); });
          window.addEventListener('online', () => this.connectNow());
          window.addEventListener('focus', () => { this.connectNow(); this.reportClientStateSoon(); });
          window.addEventListener('blur', () => this.reportClientStateSoon());
          document.addEventListener('visibilitychange', () => { if (!document.hidden) this.connectNow(); this.reportClientStateSoon(); });
          this.$nextTick(() => { this.resizeComposer(); this.updateTranscriptStickiness(); });
        },
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
          const ws = new WebSocket(proto + '//' + location.host + '/ws');
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
          ws.onmessage = ev => { if (this.ws === ws) this.onMessage(JSON.parse(ev.data)); };
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
          this.reconnectDelay = 150;
          this.rpcOn(ws, 'hello', {}).then(hello => this.applyHello(hello)).catch(() => {});
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
        applyHello(hello) {
          if (hello && hello.asset_hash && window.KODER_ASSET_HASH && hello.asset_hash !== window.KODER_ASSET_HASH) {
            location.reload();
            return;
          }
          this.clientID = (hello && hello.client_id) || this.clientID || '';
          this.applyState((hello && hello.state) || hello || {}, {scrollToBottom: true});
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
          if (msg.type === 'snapshot') this.applyState(msg.payload);
          if (msg.type === 'state_delta') this.applyStateDelta(msg.payload);
          if (msg.type === 'chat_delta') this.applyChatDelta(msg.payload);
          if (msg.type === 'chat_update') this.applyChatUpdate(msg.payload);
          if (msg.type === 'theme') { this.theme = msg.payload.theme || 'auto'; writePreference('theme', this.theme); this.applyTheme(); }
        },
        applyStateDelta(delta) {
          if (!delta) return;
          const scroll = this.transcriptScrollState();
          const seq = ++this.scrollRestoreSeq;
          this.state = {...this.state, ...delta};
          this.applyTheme();
          this.error = this.state.error || '';
          this.syncInterruptArmed();
          this.afterTranscriptDOMUpdate(() => {
            if (seq === this.scrollRestoreSeq) this.restoreTranscriptScroll(scroll);
          });
          this.reportClientStateSoon();
        },
        applyChatDelta(delta) {
          if (!delta) return;
          const id = String(delta.chat_id || delta.ChatID || delta.chat?.id || delta.chat?.ID || '').trim();
          if (!id) return;
          const scroll = this.transcriptScrollState();
          const seq = ++this.scrollRestoreSeq;
          const snapshots = {...(this.state.snapshots || this.state.Snapshots || {})};
          const current = snapshots[id] || snapshots[String(id)] || {};
          const next = {...current};
          if (delta.chat) next.Chat = delta.chat;
          if (delta.approvals !== undefined) next.Approvals = delta.approvals;
          if (delta.queue !== undefined) next.QueuedInputs = delta.queue;
          if (delta.context !== undefined) next.Context = delta.context;
          if (delta.status !== undefined) next.Status = delta.status;
          if (delta.status_text !== undefined) next.StatusText = delta.status_text;
          if (delta.active !== undefined) next.Active = delta.active;
          if (delta.item) next.Timeline = this.patchTimelineItem(next.Timeline || next.timeline || [], delta.item);
          snapshots[id] = next;
          snapshots[String(id)] = next;
          this.state.snapshots = snapshots;
          this.state.Snapshots = snapshots;
          if (delta.chat) this.patchChatList(delta.chat);
          this.patchChatStatus(delta);
          if (id === this.activeChatID()) {
            this.state.snapshot = next;
            this.state.Snapshot = next;
          }
          if (delta.error) this.error = delta.error;
          this.syncInterruptArmed();
          this.afterTranscriptDOMUpdate(() => {
            if (seq === this.scrollRestoreSeq) this.restoreTranscriptScroll(scroll);
          });
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
        applyChatUpdate(update) {
          const snapshot = update?.Snapshot || update?.snapshot;
          const chat = snapshot?.Chat || snapshot?.chat || {};
          const id = chat.ID || chat.id || update?.chat_id || update?.ChatID;
          if (!id) return;
          const snapshots = {...(this.state.snapshots || this.state.Snapshots || {})};
          snapshots[id] = snapshot;
          this.state.snapshots = snapshots;
          this.state.Snapshots = snapshots;
          if (id === this.activeChatID()) {
            this.state.snapshot = snapshot;
            this.state.Snapshot = snapshot;
          }
          this.syncInterruptArmed();
          this.reportClientStateSoon();
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
        updateTranscriptStickiness() {
          const el = this.transcriptElement();
          if (!el) {
            this.transcriptStickToBottom = true;
            this.reportClientStateSoon();
            return true;
          }
          const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
          this.transcriptStickToBottom = distance <= 48;
          this.reportClientStateSoon();
          return this.transcriptStickToBottom;
        },
        transcriptScrollState() {
          const el = this.transcriptElement();
          if (!el) return {el: null, top: 0, nearBottom: true, stickToBottom: true};
          const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
          const nearBottom = distance <= 48;
          return {el, top: el.scrollTop, nearBottom, stickToBottom: this.transcriptStickToBottom || nearBottom};
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
          const el = this.transcriptElement();
          if (!el) return;
          if (options.scrollToBottom || scroll.stickToBottom) {
            el.scrollTop = el.scrollHeight;
            this.transcriptStickToBottom = true;
            return;
          }
          el.scrollTop = scroll.top;
          this.updateTranscriptStickiness();
        },
        applyState(s, options = {}) {
          const scroll = this.transcriptScrollState();
          const seq = ++this.scrollRestoreSeq;
          this.state = s || {};
          if (this.state.theme || this.state.Theme) this.theme = this.state.theme || this.state.Theme;
          this.applyTheme(); this.error = this.state.error || '';
          this.syncInterruptArmed();
          if (!this.restoreSelectedChat()) this.writeSelectedChat();
          this.afterTranscriptDOMUpdate(() => {
            if (seq === this.scrollRestoreSeq) this.restoreTranscriptScroll(scroll, options);
          });
          this.reportClientStateSoon();
        },
        selectedChatPreferenceName() { return 'selectedChat.' + encodeURIComponent(this.state.workdir || this.state.Workdir || ''); },
        activeChatID() { return this.state.active_chat_id || this.state.ActiveChatID || 0; },
        writeSelectedChat() { const id = this.activeChatID(); if (id) writePreference(this.selectedChatPreferenceName(), id); },
        restoreSelectedChat() {
          if (this.restoreChatAttempted) return false;
          const raw = readPreference(this.selectedChatPreferenceName(), '');
          const id = String(raw || '').trim();
          if (!id) { this.restoreChatAttempted = true; return false; }
          const exists = (this.state.chats || this.state.Chats || []).some(chat => this.chatID(chat) === id);
          this.restoreChatAttempted = true;
          if (!exists || id === this.activeChatID()) return false;
          this.rpc('switch_chat', {chat_id: id}).then(s => this.applyState(s, {scrollToBottom: true}));
          return true;
        },
        activeSnapshot() {
          const id = this.activeChatID();
          const snapshots = this.state.snapshots || this.state.Snapshots || {};
          return snapshots[id] || snapshots[String(id)] || this.state.snapshot || this.state.Snapshot || {};
        },
        timeline() { const snapshot = this.activeSnapshot(); return snapshot.Timeline || snapshot.timeline || []; },
        approvals() { const snapshot = this.activeSnapshot(); return snapshot.Approvals || snapshot.approvals || []; },
        pendingText() { const snapshot = this.activeSnapshot(); const p = snapshot.PendingAssistant || snapshot.pending_assistant || {}; return [p.Reasoning || p.reasoning, p.Text || p.text].filter(Boolean).join('\n'); },
        thinkingLabel(reasoning) {
          const explicit = Number(reasoning?.tokens || reasoning?.Tokens || reasoning?.token_count || reasoning?.TokenCount || 0);
          const tokens = explicit > 0 ? explicit : this.estimateTextTokens(reasoning?.text || reasoning?.Text || '');
          return 'thinking (' + tokens + ' tokens)';
        },
        estimateTextTokens(text) {
          const source = String(text || '').trim();
          if (!source) return 0;
          return Math.max(1, Math.ceil(source.length / 4));
        },
        markdownHTML(text) { return renderMarkdown(text); },
        statusText() { const snapshot = this.activeSnapshot(); return snapshot.StatusText || snapshot.status_text || snapshot.Status || 'idle'; },
        chatInterruptible() {
          const snapshot = this.activeSnapshot();
          return !!(snapshot.Active || snapshot.active);
        },
        interruptArmed() {
          return this.interruptArmedChatID && this.interruptArmedChatID === String(this.activeChatID() || '');
        },
        interruptButtonTitle() {
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
          return {
            selected_session: this.state.session?.id || this.state.session?.ID || '',
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
            open_dialog: this.openDialogName(),
            interrupt_visible: this.chatInterruptible(),
            interrupt_armed: !!this.interruptArmed(),
          };
        },
        openDialogName() {
          if (this.showPermissions) return 'permissions';
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
        activeProvider() { return this.state.session?.provider_id || this.state.session?.ProviderID || ''; },
        activeModel() { return this.state.session?.model_id || this.state.session?.ModelID || ''; },
        activeModelInfo() { return this.state.model_info || this.state.ModelInfo || {}; },
        formatTokens(value) {
          const n = Number(value || 0);
          if (!Number.isFinite(n) || n <= 0) return 'unknown';
          if (n >= 1000) return (n / 1000).toFixed(n >= 100000 ? 0 : 1).replace(/\.0$/, '') + 'K';
          return String(Math.round(n));
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
            'Tools: ' + (info.supports_tools === false || info.SupportsTools === false ? 'no' : 'yes'),
            'Images: ' + this.capabilityLabel(info.supports_images || info.SupportsImages, known),
            'PDFs: ' + this.capabilityLabel(info.supports_pdfs || info.SupportsPDFs, known),
          ];
          if (source) lines.push('Source: ' + source);
          return lines.join('\n');
        },
        milestones() { return this.state.milestones || this.state.Milestones || {}; },
        milestoneItems() { return this.milestones().milestones || this.milestones().Milestones || []; },
        milestoneSummary() { return this.milestones().summary || this.milestones().Summary || ''; },
        milestoneRef(m) { return m.Ref || m.ref || ''; },
        milestoneTitle(m) { return m.Title || m.title || this.milestoneRef(m); },
        milestoneStatus(m) { return m.Status || m.status || 'pending'; },
        milestoneNotes(m) { return m.Notes || m.notes || ''; },
        milestoneExpanded(ref) { return !!this.expandedMilestones[ref]; },
        toggleMilestone(ref) {
          if (!ref) return;
          this.expandedMilestones = {...this.expandedMilestones, [ref]: !this.expandedMilestones[ref]};
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
          if (status === 'completed') return 'text-bg-success';
          if (status === 'cancelled') return 'text-bg-secondary';
          if (status === 'blocked') return 'text-bg-danger';
          if (status === 'decomposing' || status === 'executing') return 'text-bg-primary';
          if (status === 'ready') return 'text-bg-info';
          return 'text-bg-secondary';
        },
        todoItems() { return this.state.todos || this.state.Todos || []; },
        todosByMilestone() { return this.state.todos_by_milestone || this.state.TodosByRef || {}; },
        todoItemsForMilestone(milestone) {
          const ref = this.milestoneRef(milestone);
          const grouped = this.todosByMilestone();
          if (grouped && Object.prototype.hasOwnProperty.call(grouped, ref)) return grouped[ref] || [];
          return [];
        },
        milestoneTodoCounts(milestone) {
          const counts = {total: 0, completed: 0, active: 0, pending: 0};
          for (const todo of this.todoItemsForMilestone(milestone)) {
            counts.total++;
            const status = this.todoStatus(todo);
            if (status === 'completed') counts.completed++;
            else if (status === 'in_progress') counts.active++;
            else counts.pending++;
          }
          return counts;
        },
        milestoneTodoSummary(milestone) {
          const counts = this.milestoneTodoCounts(milestone);
          if (!counts.total) return '0 todos';
          const details = [];
          if (counts.active) details.push(counts.active + ' active');
          if (counts.pending) details.push(counts.pending + ' pending');
          const suffix = details.length ? ' · ' + details.join(' · ') : '';
          return counts.completed + '/' + counts.total + ' done' + suffix;
        },
        todoStatus(todo) { return todo.Status || todo.status || 'pending'; },
        todoIcon(status) {
          if (status === 'completed') return 'bi-check-circle-fill text-success';
          if (status === 'in_progress') return 'bi-arrow-repeat text-primary';
          return 'bi-circle text-secondary';
        },
        chatID(chat) { return chat.ID || chat.id; },
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
          return chat.LastKnownContextTokens || chat.last_known_context_tokens || 0;
        },
        chatContextLabel(chat) {
          const windowSize = this.state.context_window || this.state.ContextWindow || 0;
          const tokens = this.chatContextTokens(chat);
          if (!windowSize || !tokens) return '(0% ctx)';
          const pct = Math.max(0, Math.min(999, Math.round((tokens / windowSize) * 100)));
          return '(' + pct + '% ctx)';
        },
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
          if (kind === 'read' || kind === 'write' || kind === 'edit') return 'bi-file-earmark-code';
          if (kind === 'bash' || String(kind || '').startsWith('exec_')) return 'bi-terminal';
          if (kind === 'grep' || kind === 'glob' || kind === 'websearch') return 'bi-search';
          if (kind === 'webfetch') return 'bi-globe';
          if (kind === 'view_image' || kind === 'show_image') return 'bi-image';
          return 'bi-wrench-adjustable';
        },
        toolTitle(tool) { return toolTitleText(tool); },
        toolPreview(tool) { return toolPreviewText(tool); },
        toolStatusBadge(tool) { return toolStatusBadgeText(tool); },
        toolCallID(tool) { return tool?.tool_call_id || tool?.ToolCallID || ''; },
        toolApprovalPending(tool) {
          return this.toolCallID(tool) && toolStatus(tool) === 'awaiting_approval';
        },
        toolResultHTML(tool) { return renderToolResult(tool); },
        toolErrorHTML(tool) { return renderToolError(tool); },
        noticeIcon(content) { return noticeIcon(content); },
        noticeLevel(content) { return noticeLevel(content); },
        noticeText(content) { return noticeText(content); },
        noticeDetail(content) { return noticeDetail(content); },
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
        send() {
          const text = this.draft.trim();
          const attachments = this.composerAttachments.slice();
          if (!text && attachments.length === 0) return;
          if (text && attachments.length === 0 && this.handleSlash(text)) {
            this.draft = '';
            this.clearCompletions();
            this.$nextTick(() => this.resizeComposer());
            return;
          }
          this.draft = '';
          this.composerAttachments = [];
          this.clearCompletions();
          this.$nextTick(() => this.resizeComposer());
          this.rpc('send_prompt', {text, attachments}).catch(() => { this.draft = text; this.composerAttachments = attachments; });
        },
        handleSlash(text) {
          if (text === '/permissions') { this.showPermissions = true; return true; }
          if (text === '/compact') { this.rpc('compact', {}); return true; }
          if (text === '/chat new') { this.newChat(); return true; }
          if (text === '/model') { this.openModelDialog(); return true; }
          if (text === '/providers') { this.openProviderDialog(); return true; }
          if (text === '/sessions') { this.openSessionDialog(); return true; }
          if (text === '/settings') { this.openSettingsDialog(); return true; }
          if (text.startsWith('/')) { this.error = 'Unknown web command: ' + text; return true; }
          return false;
        },
        switchChat(id) { if (id) this.rpc('switch_chat', {chat_id: id}).then(s => { this.applyState(s, {scrollToBottom: true}); this.writeSelectedChat(); }); },
        newChat() { this.rpc('new_chat', {title: 'Chat'}).then(s => { this.applyState(s, {scrollToBottom: true}); this.writeSelectedChat(); }); },
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
          const chats = (this.state.chats || this.state.Chats || []).slice();
          const from = chats.findIndex(chat => this.chatID(chat) === sourceID);
          const to = chats.findIndex(chat => this.chatID(chat) === targetID);
          if (from < 0 || to < 0) return;
          const [moved] = chats.splice(from, 1);
          chats.splice(to, 0, moved);
          this.state.chats = chats;
          this.state.Chats = chats;
          this.rpc('reorder_chats', {chat_ids: chats.map(chat => this.chatID(chat))})
            .then(s => this.applyState(s))
            .catch(err => {
              this.showToast(err.message);
              this.rpc('get_state', {}).then(s => this.applyState(s)).catch(() => {});
            });
        },
        endChatDrag() { this.dragChatID = ''; },
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
        setPermission(profile) { this.rpc('set_permission_profile', {profile}).then(s => { this.applyState(s); this.showPermissions = false; this.reportClientStateSoon(); }); },
        openModelDialog() {
          this.showModels = true; this.modelLoading = true; this.modelQuery = '';
          this.reportClientStateSoon();
          this.$nextTick(() => this.$refs.modelSearch?.focus());
          this.rpc('list_models', {}).then(result => { this.modelOptions = result.models || []; }).finally(() => { this.modelLoading = false; });
        },
        closeModelDialog() { this.showModels = false; this.reportClientStateSoon(); },
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
          this.reportClientStateSoon();
          this.rpc('list_sessions', {}).then(result => { this.sessionState = result || {active_id: 0, workdir: '', sessions: []}; }).finally(() => { this.sessionLoading = false; });
        },
        closeSessionDialog() { this.showSessions = false; this.reportClientStateSoon(); },
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
        openProviderDialog() { this.openSettingsDialog('providers'); },
        providerTemplates() { return this.providerState.catalog || []; },
        providerRows() { return this.providerState.providers || []; },
        setProviderDraft(draft) {
          this.providerDraft = Object.assign({headers: {}}, draft || {});
          this.providerHeadersText = JSON.stringify(this.providerDraft.headers || {}, null, 2);
        },
        editProvider(id) {
          const draft = (this.providerState.drafts || {})[id];
          if (draft) { this.setProviderDraft(draft); this.providerStatus = ''; this.providerStatusKind = 'secondary'; this.showProviderEditor = true; }
        },
        addProvider() {
          const first = this.providerTemplates()[0]?.id || 'openai-compatible';
          this.rpc('new_provider_draft', {template_id: first}).then(draft => { this.setProviderDraft(draft); this.providerStatus = ''; this.providerStatusKind = 'secondary'; this.showProviderEditor = true; });
        },
        closeProviderEditor() { this.showProviderEditor = false; this.providerDraft = null; this.providerStatus = ''; this.providerStatusKind = 'secondary'; },
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
            if (result.selected_model && this.providerDraft) this.providerDraft.model = result.selected_model;
            const selected = result.selected_model ? ' Selected ' + result.selected_model + '.' : '';
            this.providerStatus = 'Test passed: ' + count + ' model' + (count === 1 ? '' : 's') + (sample ? ' (' + sample + ')' : '') + '.' + selected;
            this.providerStatusKind = 'success';
          }).catch(err => { this.providerStatus = err.message; this.providerStatusKind = 'danger'; }).finally(() => { this.providerTesting = false; });
        },
        saveProvider() {
          const payload = this.providerDraftPayload(); if (!payload) return;
          this.providerSaving = true; this.providerStatus = ''; this.providerStatusKind = 'secondary';
          this.rpc('save_provider', payload).then(result => {
            this.providerState = result.providers || result;
            if (this.settings) this.settings.providers = this.providerState;
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
            if (result.state) this.applyState(result.state);
          }).catch(err => this.showToast(err.message));
        },
        openSettingsDialog(tab = 'general') {
          this.showSettings = true; this.settingsTab = tab; this.settingsLoading = true; this.settingsStatus = ''; this.settingsStatusKind = 'secondary';
          this.reportClientStateSoon();
          this.rpc('preferences_state', {}).then(state => {
            this.setSettingsState(state);
          }).finally(() => { this.settingsLoading = false; });
        },
        closeSettingsDialog() {
          this.showSettings = false; this.settings = null; this.settingsStatus = '';
          this.closeProviderEditor(); this.closeMCPEditor(); this.reportClientStateSoon();
        },
        setSettingsState(state) {
          this.settings = state || {};
          this.providerState = this.settings.providers || this.providerState;
          const profiles = this.permissionSettingsProfiles();
          this.selectedPermissionProfile = this.settings?.permissions?.active || profiles[0]?.name || '';
        },
        settingsTabs() { return ['general', 'compaction', 'prompts', 'providers', 'mcp', 'permissions']; },
        settingsTabLabel(tab) {
          return {general: 'General', compaction: 'Compaction', prompts: 'Prompts', providers: 'Providers', mcp: 'MCP', permissions: 'Permissions'}[tab] || tab;
        },
        compactionModelValue() {
          const c = this.settings?.compaction || {};
          if (c.use_chat_model || (!c.provider_id && !c.model_id)) return 'chat';
          return JSON.stringify([c.provider_id || '', c.model_id || '']);
        },
        modelOptionValue(model) {
          return JSON.stringify([model?.provider_id || '', model?.model_id || '']);
        },
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
        settingsListRows(kind) {
          if (kind === 'providers') return this.providerRows();
          if (kind === 'mcp') return this.settings?.mcp_servers || [];
          return [];
        },
        settingsItemTitle(kind, item) {
          if (kind === 'providers') return item.name || item.id || 'Provider';
          if (kind === 'mcp') return item.name || item.id || 'MCP server';
          return item?.name || item?.id || 'Item';
        },
        settingsItemSubtitle(kind, item) {
          if (kind === 'providers') return (item.id || '-') + ' / ' + (item.default_model || '-');
          if (kind === 'mcp') return (item.id || '-') + (item.url ? ' / ' + item.url : '');
          return '';
        },
        settingsItemBadges(kind, item) {
          const badges = [];
          if (kind === 'providers' && item.default) badges.push('default');
          if (item.disabled) badges.push('disabled');
          return badges;
        },
        editSettingsItem(kind, id) {
          if (kind === 'providers') { this.editProvider(id); return; }
          if (kind === 'mcp') this.editMCPServer(id);
        },
        addSettingsItem(kind) {
          if (kind === 'providers') { this.addProvider(); return; }
          if (kind === 'mcp') this.addMCPServer();
        },
        deleteSettingsItem(kind, id) {
          if (kind === 'providers') { this.deleteProvider(id); return; }
          if (kind === 'mcp') this.deleteMCPServer(id);
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
        permissionSettingsProfiles() { return this.settings?.permissions?.profiles || []; },
        activePermissionProfile() {
          const profiles = this.permissionSettingsProfiles();
          return profiles.find(profile => profile.name === this.selectedPermissionProfile) || profiles[0] || null;
        },
        selectPermissionProfile(name) {
          this.selectedPermissionProfile = name || '';
        },
        permissionProfileSummary(profile) {
          if (!profile) return '';
          const network = profile.network ? 'network on' : 'network off';
          return network + ', root ' + (profile.root || 'readonly') + ', workspace ' + (profile.workspace || 'readwrite');
        },
        setActivePermissionProfile(name) {
          if (this.settings?.permissions) this.settings.permissions.active = name || '';
          this.selectPermissionProfile(name);
        },
        renameActivePermissionProfile(name) {
          const profile = this.activePermissionProfile();
          if (!profile) return;
          const next = String(name || '').trim();
          if (!next) return;
          const wasActive = this.settings?.permissions?.active === profile.name;
          profile.name = next;
          this.selectedPermissionProfile = next;
          if (wasActive && this.settings?.permissions) this.settings.permissions.active = next;
        },
        addPermissionProfile() {
          if (!this.settings) return;
          if (!this.settings.permissions) this.settings.permissions = {active: '', profiles: []};
          const profiles = this.permissionSettingsProfiles();
          let idx = profiles.length + 1;
          let name = 'custom';
          while (profiles.some(profile => profile.name === name)) name = 'custom-' + idx++;
          profiles.push({name, network: false, root: 'readonly', workspace: 'readwrite', mounts: []});
          this.setActivePermissionProfile(name);
        },
        deletePermissionProfile(name) {
          if (!this.settings?.permissions || !name || !confirm('Delete this permission profile?')) return;
          const profiles = this.permissionSettingsProfiles().filter(profile => profile.name !== name);
          this.settings.permissions.profiles = profiles;
          this.selectPermissionProfile(profiles[0]?.name || '');
        },
        addPermissionMount(profile) {
          if (!profile) return;
          if (!Array.isArray(profile.mounts)) profile.mounts = [];
          profile.mounts.push({path: '', mode: 'readonly'});
        },
        deletePermissionMount(profile, index) {
          if (!profile?.mounts) return;
          profile.mounts.splice(index, 1);
        },
        permissionToolOptions() {
          const tools = new Set((this.settings?.tool_defaults || []).map(item => String(item.tool || item.Tool || '').trim()).filter(Boolean));
          return Array.from(tools).sort();
        },
        toolDefaultRows() { return this.settings?.tool_defaults || []; },
        toolDefaultTool(item) { return item.tool || item.Tool || ''; },
        toolDefaultEnabled(item) { return item.enabled ?? item.Enabled ?? true; },
        setToolDefaultEnabled(item, enabled) {
          if ('enabled' in item) item.enabled = enabled; else item.Enabled = enabled;
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
