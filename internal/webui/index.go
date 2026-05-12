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
    .model-trigger { color: inherit; text-decoration: none; }
    .modal-backdrop-lite { position: fixed; inset: 0; background: rgba(0, 0, 0, .45); z-index: 1050; display: grid; place-items: center; padding: 1rem; }
    .model-dialog { width: min(760px, 100%); max-height: min(720px, 90vh); overflow: hidden; display: flex; flex-direction: column; }
    .model-list { overflow: auto; }
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
        <button type="button" class="btn btn-link model-trigger p-0 text-start" @click="openModelDialog()">
          <i class="bi bi-cpu me-1 text-secondary"></i><span x-text="activeProvider()"></span> / <span x-text="activeModel()"></span>
        </button>
      </div>
      <div class="mb-3">
        <div class="small text-secondary">Status</div>
        <div x-text="statusText()"></div>
      </div>
      <div class="mb-3">
        <div class="d-flex align-items-center justify-content-between">
          <div class="small text-secondary">Permissions</div>
          <button class="btn btn-sm btn-outline-secondary" @click="showPermissions = !showPermissions"><i class="bi bi-shield-lock"></i></button>
        </div>
        <div class="mt-1" x-text="permissionLabel(activePermission())"></div>
        <div class="list-group list-group-flush mt-2" x-show="showPermissions">
          <template x-for="profile in permissionProfiles()" :key="profile.Name || profile.name">
            <button class="list-group-item list-group-item-action" :class="{'active': permissionName(profile) === activePermission()}" @click="setPermission(permissionName(profile))">
              <div class="fw-semibold" x-text="profile.Label || profile.label || profile.Name || profile.name"></div>
              <div class="small opacity-75" x-text="profile.Description || profile.description || ''"></div>
            </button>
          </template>
        </div>
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

  <div class="modal-backdrop-lite" x-show="showModels" x-transition @keydown.escape.window="closeModelDialog()" style="display: none;">
    <section class="model-dialog bg-body border rounded shadow">
      <header class="d-flex align-items-center justify-content-between gap-3 p-3 border-bottom">
        <div>
          <div class="fw-semibold"><i class="bi bi-cpu me-1"></i> Select model</div>
          <div class="small text-secondary" x-text="activeProvider() + ' / ' + activeModel()"></div>
        </div>
        <button type="button" class="btn btn-sm btn-outline-secondary" @click="closeModelDialog()"><i class="bi bi-x-lg"></i></button>
      </header>
      <div class="p-3 border-bottom">
        <input class="form-control" type="search" x-model="modelQuery" placeholder="Filter models" x-ref="modelSearch">
      </div>
      <div class="model-list list-group list-group-flush">
        <template x-if="modelLoading">
          <div class="list-group-item text-secondary"><span class="spinner-border spinner-border-sm me-2"></span>Loading models</div>
        </template>
        <template x-if="!modelLoading && filteredModels().length === 0">
          <div class="list-group-item text-secondary">No models match the filter</div>
        </template>
        <template x-for="model in filteredModels()" :key="model.provider_id + '/' + model.model_id">
          <button type="button" class="list-group-item list-group-item-action d-flex align-items-start justify-content-between gap-3" :class="{'active': model.current}" @click="selectModel(model)">
            <span class="text-start">
              <span class="fw-semibold" x-text="model.model_id"></span>
              <span class="d-block small opacity-75">
                <span x-text="model.provider_label || model.provider_id"></span>
                <template x-if="model.owned_by"><span x-text="' · ' + model.owned_by"></span></template>
              </span>
            </span>
            <i class="bi bi-check-lg" x-show="model.current"></i>
          </button>
        </template>
      </div>
    </section>
  </div>

  <script>
    function koderApp() {
      return {
        ws: null, nextID: 1, pending: {}, state: {}, connected: false, draft: '', showPermissions: false,
        showModels: false, modelLoading: false, modelQuery: '', modelOptions: [],
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
        activeProvider() { return this.state.session?.provider_id || this.state.session?.ProviderID || ''; },
        activeModel() { return this.state.session?.model_id || this.state.session?.ModelID || ''; },
        chatID(chat) { return chat.ID || chat.id; },
        formatArgs(args) { return args ? JSON.stringify(args, null, 2) : ''; },
        send() { const text = this.draft.trim(); if (!text) return; if (this.handleSlash(text)) { this.draft = ''; return; } this.draft = ''; this.rpc('send_prompt', {text}); },
        handleSlash(text) {
          if (text === '/permissions') { this.showPermissions = true; return true; }
          if (text === '/compact') { this.rpc('compact', {}); return true; }
          if (text === '/chat new') { this.newChat(); return true; }
          if (text.startsWith('/')) { this.error = 'Unknown web command: ' + text; return true; }
          return false;
        },
        switchChat(id) { if (id) this.rpc('switch_chat', {chat_id: id}).then(s => this.applyState(s)); },
        newChat() { this.rpc('new_chat', {title: 'Chat'}).then(s => this.applyState(s)); },
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
        setTheme(theme) { localStorage.setItem('koder.theme', theme); this.applyTheme(); this.rpc('set_theme', {theme}); }
      }
    }
  </script>
</body>
</html>`
