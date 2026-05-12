package webui

const indexHTML = `<!doctype html>
<html lang="en" x-data="koderApp()" x-init="init()" :data-bs-theme="theme">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>koder</title>
  <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.3/dist/css/bootstrap.min.css" rel="stylesheet">
  <link href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.11.3/font/bootstrap-icons.css" rel="stylesheet">
  <script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.14.8/dist/cdn.min.js"></script>
  <style>
    :root { color-scheme: light dark; }
    html, body { height: 100%; }
    body { overflow: hidden; }
    .app-shell { height: 100vh; display: grid; grid-template-columns: minmax(0, 1fr) 340px; grid-template-rows: auto minmax(0, 1fr) auto; }
    .topbar { grid-column: 1 / 3; }
    .transcript { min-height: 0; overflow: auto; }
    .sidebar { min-height: 0; overflow: auto; border-left: 1px solid var(--bs-border-color); }
    .composer { grid-column: 1 / 3; border-top: 1px solid var(--bs-border-color); }
    .message { border-radius: .75rem; }
    .message.user { background: var(--bs-primary-bg-subtle); }
    .message.assistant { background: var(--bs-tertiary-bg); }
    .tool { border-left: 3px solid var(--bs-info); }
    .reasoning { color: var(--bs-secondary-color); }
    pre { white-space: pre-wrap; word-break: break-word; margin: 0; }
    @media (max-width: 900px) { .app-shell { grid-template-columns: 1fr; } .sidebar { display: none; } .topbar, .composer { grid-column: 1; } }
  </style>
</head>
<body class="bg-body text-body">
  <div class="app-shell">
    <nav class="topbar navbar bg-body-tertiary px-3">
      <div class="d-flex align-items-center gap-2">
        <i class="bi bi-terminal-fill text-primary"></i>
        <span class="fw-semibold">koder</span>
        <span class="text-secondary small" x-text="state.session?.title || 'New Session'"></span>
      </div>
      <div class="d-flex align-items-center gap-2">
        <span class="badge text-bg-secondary" x-text="connected ? 'connected' : 'offline'"></span>
        <select class="form-select form-select-sm" x-model="theme" @change="setTheme(theme)" style="width: 8rem">
          <option value="auto">auto</option>
          <option value="dark">dark</option>
          <option value="light">light</option>
        </select>
        <button class="btn btn-sm btn-outline-danger" @click="rpc('stop', {})"><i class="bi bi-stop-fill"></i></button>
      </div>
    </nav>

    <main class="transcript p-3">
      <template x-if="error">
        <div class="alert alert-danger" x-text="error"></div>
      </template>
      <template x-for="item in timeline()" :key="item.id || item.seq">
        <section class="mb-3">
          <template x-if="item.kind === 'user'">
            <div class="message user p-3 ms-auto" style="max-width: 78%;">
              <div class="small text-secondary mb-1"><i class="bi bi-person"></i> You</div>
              <pre x-text="item.content?.text || ''"></pre>
            </div>
          </template>
          <template x-if="item.kind === 'assistant'">
            <div class="message assistant p-3" style="max-width: 86%;">
              <div class="small text-secondary mb-2"><i class="bi bi-stars"></i> Assistant</div>
              <template x-if="item.content?.reasoning?.text">
                <details class="reasoning mb-2">
                  <summary>thinking</summary>
                  <pre x-text="item.content.reasoning.text"></pre>
                </details>
              </template>
              <pre x-text="item.content?.text || ''"></pre>
              <template x-for="tool in item.content?.tools || []" :key="tool.tool_call_id">
                <div class="tool mt-3 ps-3">
                  <div class="small fw-semibold"><i class="bi bi-wrench-adjustable"></i> <span x-text="tool.tool"></span> <span class="badge text-bg-secondary" x-text="tool.status"></span></div>
                  <pre class="small text-secondary" x-text="formatArgs(tool.args)"></pre>
                  <template x-if="tool.result"><pre class="small mt-2" x-text="tool.result.text || tool.result.diff || ''"></pre></template>
                  <template x-if="tool.error"><pre class="small text-danger mt-2" x-text="tool.error.message || tool.error.code || 'tool error'"></pre></template>
                </div>
              </template>
            </div>
          </template>
          <template x-if="item.kind !== 'user' && item.kind !== 'assistant'">
            <div class="message p-3 bg-body-tertiary">
              <div class="small text-secondary"><i class="bi bi-info-circle"></i> <span x-text="item.kind"></span></div>
              <pre x-text="JSON.stringify(item.content, null, 2)"></pre>
            </div>
          </template>
        </section>
      </template>
      <template x-if="pendingText()">
        <section class="message assistant p-3 mb-3" style="max-width: 86%;">
          <div class="small text-secondary mb-2"><i class="bi bi-broadcast"></i> streaming</div>
          <pre x-text="pendingText()"></pre>
        </section>
      </template>
    </main>

    <aside class="sidebar p-3 bg-body-tertiary">
      <div class="mb-3">
        <div class="small text-secondary">Model</div>
        <div><span x-text="state.session?.provider_id || state.session?.ProviderID || ''"></span> / <span x-text="state.session?.model_id || state.session?.ModelID || ''"></span></div>
      </div>
      <div class="mb-3">
        <div class="small text-secondary">Status</div>
        <div x-text="statusText()"></div>
      </div>
      <div class="mb-3">
        <div class="d-flex align-items-center justify-content-between">
          <div class="small text-secondary">Chats</div>
          <button class="btn btn-sm btn-outline-primary" @click="newChat()"><i class="bi bi-plus-lg"></i></button>
        </div>
        <div class="list-group list-group-flush mt-2">
          <template x-for="chat in state.chats || []" :key="chat.ID || chat.id">
            <button class="list-group-item list-group-item-action" :class="{'active': chatID(chat) === state.active_chat_id}" @click="switchChat(chatID(chat))">
              <i class="bi bi-chat-left-text"></i> <span x-text="chat.Title || chat.title || 'Chat'"></span>
            </button>
          </template>
        </div>
      </div>
      <div class="mb-3">
        <div class="small text-secondary">Approvals</div>
        <template x-for="approval in approvals()" :key="approval.ID || approval.id">
          <div class="border rounded p-2 my-2">
            <div class="small fw-semibold" x-text="approval.Tool || approval.tool"></div>
            <div class="small text-secondary" x-text="approval.Command || approval.command"></div>
            <div class="btn-group btn-group-sm mt-2">
              <button class="btn btn-success" @click="rpc('approve', {id: approval.ID || approval.id})"><i class="bi bi-check-lg"></i></button>
              <button class="btn btn-outline-danger" @click="rpc('deny', {id: approval.ID || approval.id})"><i class="bi bi-x-lg"></i></button>
            </div>
          </div>
        </template>
      </div>
      <div class="small text-secondary">Workspace</div>
      <div class="text-break" x-text="state.workdir"></div>
    </aside>

    <form class="composer p-3 bg-body" @submit.prevent="send()">
      <div class="input-group">
        <textarea class="form-control" rows="2" x-model="draft" placeholder="Ask koder or type / for commands" @keydown.enter.exact.prevent="send()" @keydown.meta.enter.prevent="send()" @keydown.ctrl.enter.prevent="send()"></textarea>
        <button class="btn btn-primary" type="submit"><i class="bi bi-send-fill"></i></button>
      </div>
    </form>
  </div>

  <script>
    function koderApp() {
      return {
        ws: null, nextID: 1, pending: {}, state: {}, connected: false, draft: '',
        theme: localStorage.getItem('koder.theme') || 'auto', error: '',
        init() { this.applyTheme(); this.connect(); },
        applyTheme() {
          const resolved = this.theme === 'auto' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : this.theme;
          document.documentElement.setAttribute('data-bs-theme', resolved);
        },
        connect() {
          const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
          this.ws = new WebSocket(proto + '//' + location.host + '/ws');
          this.ws.onopen = () => { this.connected = true; this.rpc('hello', {}).then(s => this.applyState(s)); };
          this.ws.onclose = () => { this.connected = false; setTimeout(() => this.connect(), 1000); };
          this.ws.onmessage = ev => this.onMessage(JSON.parse(ev.data));
        },
        onMessage(msg) {
          if (msg.type) { this.onPush(msg); return; }
          const p = this.pending[msg.id]; if (!p) return; delete this.pending[msg.id];
          msg.ok ? p.resolve(msg.result) : p.reject(new Error(msg.error || 'rpc failed'));
        },
        onPush(msg) {
          if (msg.type === 'snapshot') this.applyState(msg.payload);
          if (msg.type === 'theme') { this.theme = msg.payload.theme || 'auto'; this.applyTheme(); }
        },
        rpc(method, params) {
          const id = this.nextID++;
          this.ws.send(JSON.stringify({id, method, params}));
          return new Promise((resolve, reject) => this.pending[id] = {resolve, reject}).catch(err => { this.error = err.message; throw err; });
        },
        applyState(s) { this.state = s || {}; this.theme = this.state.theme || this.theme; this.applyTheme(); this.error = this.state.error || ''; this.$nextTick(() => { const el = document.querySelector('.transcript'); if (el) el.scrollTop = el.scrollHeight; }); },
        timeline() { return this.state.snapshot?.Timeline || this.state.snapshot?.timeline || []; },
        approvals() { return this.state.snapshot?.Approvals || this.state.snapshot?.approvals || []; },
        pendingText() { const p = this.state.snapshot?.PendingAssistant || this.state.snapshot?.pending_assistant || {}; return [p.Reasoning || p.reasoning, p.Text || p.text].filter(Boolean).join('\n'); },
        statusText() { return this.state.snapshot?.StatusText || this.state.snapshot?.status_text || this.state.snapshot?.Status || 'idle'; },
        chatID(chat) { return chat.ID || chat.id; },
        formatArgs(args) { return args ? JSON.stringify(args, null, 2) : ''; },
        send() { const text = this.draft.trim(); if (!text) return; this.draft = ''; this.rpc('send_prompt', {text}); },
        switchChat(id) { if (id) this.rpc('switch_chat', {chat_id: id}).then(s => this.applyState(s)); },
        newChat() { this.rpc('new_chat', {title: 'Chat'}).then(s => this.applyState(s)); },
        setTheme(theme) { localStorage.setItem('koder.theme', theme); this.applyTheme(); this.rpc('set_theme', {theme}); }
      }
    }
  </script>
</body>
</html>`
