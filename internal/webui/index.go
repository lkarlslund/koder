package webui

const indexHTML = `<!doctype html>
<html lang="en" x-data="koderApp()" x-init="init()" :data-bs-theme="theme">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>koder</title>
  <link href="/assets/vendor/bootstrap/bootstrap.min.css" rel="stylesheet">
  <link href="/assets/vendor/bootstrap-icons/font/bootstrap-icons.css" rel="stylesheet">
  <link href="/assets/vendor/highlight/github-dark.min.css" rel="stylesheet">
  <script defer src="/assets/vendor/marked/marked.umd.js"></script>
  <script defer src="/assets/vendor/dompurify/purify.min.js"></script>
  <script defer src="/assets/vendor/highlight/highlight.min.js"></script>
  <script defer src="/assets/vendor/alpine/cdn.min.js"></script>
  <style>
    :root { color-scheme: light dark; }
    html, body { height: 100%; }
    body { overflow: hidden; }
    .app-shell { height: 100vh; display: grid; grid-template-columns: minmax(0, 1fr) 6px var(--sidebar-width, 340px); grid-template-rows: auto minmax(0, 1fr) auto; }
    .topbar { grid-column: 1 / 4; }
    .transcript { min-height: 0; overflow: auto; }
    .sidebar-resizer { min-height: 0; cursor: col-resize; background: var(--bs-border-color); opacity: .55; touch-action: none; }
    .sidebar-resizer:hover, .sidebar-resizer.resizing { background: var(--bs-primary); opacity: 1; }
    .sidebar { min-height: 0; overflow: auto; border-left: 1px solid var(--bs-border-color); }
    .chat-status-icon { flex: 0 0 auto; font-size: .9rem; line-height: 1; }
    .chat-status-icon.status-idle { color: var(--bs-secondary-color); opacity: .65; }
    .chat-status-icon.status-running, .chat-status-icon.status-waiting-llm, .chat-status-icon.status-streaming-response, .chat-status-icon.status-streaming-thoughts, .chat-status-icon.status-running-tools { color: var(--bs-primary); animation: chat-status-spin 1s linear infinite; }
    .chat-status-icon.status-waiting-approval { color: var(--bs-warning); animation: chat-status-pulse 1s ease-in-out infinite; }
    .chat-status-icon.status-error, .chat-status-icon.status-failed, .chat-status-icon.status-cancelled { color: var(--bs-danger); animation: chat-status-pulse 1.1s ease-in-out infinite; }
    .chat-status-icon.status-completed { color: var(--bs-success); animation: chat-status-fade 1.5s ease-in-out infinite; }
    @keyframes chat-status-spin { to { transform: rotate(360deg); } }
    @keyframes chat-status-pulse { 0%, 100% { opacity: .45; transform: scale(.94); } 50% { opacity: 1; transform: scale(1.08); } }
    @keyframes chat-status-fade { 0%, 100% { opacity: .55; } 50% { opacity: 1; } }
    .composer { grid-column: 1 / 4; border-top: 1px solid var(--bs-border-color); }
    .composer-input { max-height: 20vh; overflow-y: hidden; resize: none; }
    .composer-menu { max-height: 32vh; overflow: auto; }
    .message { border-radius: .75rem; }
    .message.user { background: var(--bs-primary-bg-subtle); }
    .message.assistant { background: var(--bs-tertiary-bg); }
    .markdown-body { overflow-wrap: anywhere; }
    .markdown-body > :first-child { margin-top: 0; }
    .markdown-body > :last-child { margin-bottom: 0; }
    .markdown-body h1, .markdown-body h2, .markdown-body h3 { margin: .45rem 0 .65rem; line-height: 1.2; }
    .markdown-body h1 { font-size: 1.35rem; border-bottom: 1px solid var(--bs-border-color); padding-bottom: .35rem; }
    .markdown-body h2 { font-size: 1.15rem; }
    .markdown-body h3 { font-size: 1rem; }
    .markdown-body p, .markdown-body ul, .markdown-body ol, .markdown-body table, .markdown-body pre { margin-bottom: .75rem; }
    .markdown-body ul, .markdown-body ol { padding-left: 1.4rem; }
    .markdown-body li + li { margin-top: .2rem; }
    .markdown-body table { width: 100%; border-collapse: collapse; display: block; overflow-x: auto; }
    .markdown-body th, .markdown-body td { border: 1px solid var(--bs-border-color); padding: .35rem .5rem; vertical-align: top; }
    .markdown-body th { background: var(--bs-secondary-bg); font-weight: 600; }
    .markdown-body code { color: var(--bs-code-color); background: var(--bs-secondary-bg); border-radius: .25rem; padding: .1rem .25rem; }
    .markdown-body pre { background: #0d1117; color: #c9d1d9; border-radius: .45rem; padding: .75rem; overflow: auto; white-space: pre; }
    .markdown-body pre code { background: transparent; color: inherit; padding: 0; border-radius: 0; }
    .markdown-body blockquote { border-left: 3px solid var(--bs-border-color); color: var(--bs-secondary-color); padding-left: .75rem; margin: .75rem 0; }
    .tool { border-left: 3px solid var(--bs-info); }
    .tool-result { border: 1px solid var(--bs-border-color); border-radius: .45rem; overflow: hidden; background: var(--bs-body-bg); }
    .tool-result-header { padding: .35rem .55rem; background: var(--bs-secondary-bg); border-bottom: 1px solid var(--bs-border-color); font-size: .82rem; font-weight: 600; }
    .tool-result-body { padding: .45rem .55rem; font-family: var(--bs-font-monospace); font-size: .82rem; line-height: 1.35; white-space: pre-wrap; overflow-wrap: anywhere; }
    .tool-result-line { min-height: 1.35em; }
    .tool-result-omitted { color: var(--bs-secondary-color); font-style: italic; }
    .tool-diff-line { padding: .05rem .45rem; font-family: var(--bs-font-monospace); font-size: .82rem; line-height: 1.35; white-space: pre-wrap; overflow-wrap: anywhere; }
    .tool-diff-add { background: rgba(var(--bs-success-rgb), .18); color: var(--bs-success-text-emphasis); }
    .tool-diff-del { background: rgba(var(--bs-danger-rgb), .18); color: var(--bs-danger-text-emphasis); }
    .tool-diff-meta { background: var(--bs-secondary-bg); color: var(--bs-secondary-color); }
    .reasoning { color: var(--bs-secondary-color); }
    .model-trigger { color: inherit; text-decoration: none; }
    .git-file { border-left: 3px solid transparent; }
    .git-file.git-added, .diff-add { color: var(--bs-success-text-emphasis); }
    .git-file.git-modified { border-left-color: var(--bs-warning); }
    .git-file.git-deleted, .diff-del { color: var(--bs-danger-text-emphasis); }
    .git-file.git-untracked { color: var(--bs-info-text-emphasis); }
    .modal-backdrop-lite { position: fixed; inset: 0; background: rgba(0, 0, 0, .45); z-index: 1050; display: grid; place-items: center; padding: 1rem; }
    .model-dialog { width: min(760px, 100%); max-height: min(720px, 90vh); overflow: hidden; display: flex; flex-direction: column; }
    .model-list { overflow: auto; }
    .session-dialog { width: min(820px, 100%); max-height: min(720px, 90vh); overflow: hidden; display: flex; flex-direction: column; }
    .session-list { overflow: auto; }
    .provider-dialog { width: min(980px, 100%); max-height: min(820px, 92vh); overflow: hidden; display: flex; flex-direction: column; }
    .provider-body { min-height: 0; display: grid; grid-template-columns: 280px minmax(0, 1fr); }
    .provider-list, .provider-form { min-height: 0; overflow: auto; }
    .toast-stack { position: fixed; right: 1rem; bottom: 1rem; z-index: 1100; max-width: min(420px, calc(100vw - 2rem)); }
    pre { white-space: pre-wrap; word-break: break-word; margin: 0; }
    @media (max-width: 900px) { .app-shell { grid-template-columns: 1fr; } .sidebar, .sidebar-resizer { display: none; } .topbar, .composer { grid-column: 1; } .provider-body { grid-template-columns: 1fr; } }
  </style>
</head>
<body class="bg-body text-body">
  <div class="app-shell" :style="appShellStyle()">
    <nav class="topbar navbar bg-body-tertiary px-3">
      <div class="d-flex align-items-center gap-2">
        <i class="bi bi-terminal-fill text-primary"></i>
        <span class="fw-semibold">koder</span>
        <span class="text-secondary small" x-text="state.session?.title || 'New Session'"></span>
      </div>
      <div class="d-flex align-items-center gap-2">
        <span class="badge text-bg-secondary" x-text="connected ? 'connected' : 'offline'"></span>
        <button class="btn btn-sm btn-outline-secondary" title="Sessions" @click="openSessionDialog()"><i class="bi bi-collection"></i></button>
        <button class="btn btn-sm btn-outline-secondary" title="Providers" @click="openProviderDialog()"><i class="bi bi-plug"></i></button>
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
              <div class="markdown-body" x-html="markdownHTML(item.content?.text || '')"></div>
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
              <div class="markdown-body" x-html="markdownHTML(item.content?.text || '')"></div>
              <template x-for="tool in item.content?.tools || []" :key="tool.tool_call_id">
                <div class="tool mt-3 ps-3">
                  <div class="small fw-semibold"><i class="bi" :class="toolIcon(tool.tool)"></i> <span x-text="toolTitle(tool)"></span> <span class="badge text-bg-secondary" x-text="tool.status"></span></div>
                  <div class="small text-secondary" x-show="toolPreview(tool)" x-text="toolPreview(tool)"></div>
                  <template x-if="tool.result"><div class="tool-result mt-2" x-html="toolResultHTML(tool)"></div></template>
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
          <div class="markdown-body" x-html="markdownHTML(pendingText())"></div>
        </section>
      </template>
    </main>

    <div class="sidebar-resizer" :class="{'resizing': resizingSidebar}" role="separator" aria-orientation="vertical" aria-label="Resize sidebar" @pointerdown="startSidebarResize($event)"></div>

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
        <div class="small text-secondary">Milestones</div>
        <template x-if="milestoneSummary()">
          <div class="small text-body-secondary mb-2" x-text="milestoneSummary()"></div>
        </template>
        <template x-if="milestoneItems().length === 0">
          <div class="text-secondary">None</div>
        </template>
        <div class="list-group list-group-flush mt-2" x-show="milestoneItems().length > 0">
          <template x-for="milestone in milestoneItems()" :key="milestoneRef(milestone)">
            <div class="list-group-item bg-transparent px-0 py-2">
              <div class="d-flex align-items-start justify-content-between gap-2">
                <div class="text-break">
                  <span class="fw-semibold" x-text="milestoneTitle(milestone)"></span>
                  <span class="small text-secondary" x-text="' ' + milestoneRef(milestone)"></span>
                </div>
                <span class="badge" :class="milestoneBadge(milestoneStatus(milestone))" x-text="milestoneStatus(milestone)"></span>
              </div>
              <div class="small text-secondary mt-1" x-show="milestoneNotes(milestone)" x-text="milestoneNotes(milestone)"></div>
            </div>
          </template>
        </div>
      </div>
      <div class="mb-3">
        <div class="small text-secondary">Todos</div>
        <template x-if="todoItems().length === 0">
          <div class="text-secondary">None</div>
        </template>
        <div class="list-group list-group-flush mt-2" x-show="todoItems().length > 0">
          <template x-for="todo in todoItems()" :key="todo.ID || todo.id">
            <div class="list-group-item bg-transparent px-0 py-2">
              <div class="d-flex align-items-start gap-2">
                <i class="bi mt-1" :class="todoIcon(todoStatus(todo))"></i>
                <div class="text-break">
                  <div x-text="todo.Content || todo.content"></div>
                  <div class="small text-secondary" x-text="todoStatus(todo)"></div>
                </div>
              </div>
            </div>
          </template>
        </div>
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
            <div class="list-group-item list-group-item-action d-flex align-items-center gap-2" :class="{'active': chatID(chat) === state.active_chat_id}" @click="switchChat(chatID(chat))">
              <button type="button" class="btn btn-sm p-0 border-0 bg-transparent flex-grow-1 text-start" :class="{'text-white': chatID(chat) === state.active_chat_id}" @click.stop="switchChat(chatID(chat))">
                <div class="d-flex align-items-center gap-2">
                  <i class="bi bi-chat-left-text"></i>
                  <span class="text-truncate" x-text="chat.Title || chat.title || 'Chat'"></span>
                  <i class="chat-status-icon bi" :class="[chatStatusIcon(chat), chatStatusClass(chat)]" :title="chatStatusLabel(chat)"></i>
                </div>
              </button>
              <button type="button" class="btn btn-sm" :class="chatID(chat) === state.active_chat_id ? 'btn-outline-light' : 'btn-outline-danger'" title="Delete chat" @click.stop="deleteChat(chatID(chat))">
                <i class="bi bi-trash"></i>
              </button>
            </div>
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
      <div class="mt-3">
        <div class="d-flex align-items-center justify-content-between">
          <div class="small text-secondary">Git</div>
          <button class="btn btn-sm btn-outline-secondary" title="Refresh git status" @click="refreshWorkspace()"><i class="bi bi-arrow-clockwise"></i></button>
        </div>
        <template x-if="!gitStatus().available">
          <div class="text-secondary mt-1">No git repository</div>
        </template>
        <template x-if="gitStatus().available">
          <div class="mt-1">
            <div class="d-flex align-items-center gap-2 flex-wrap">
              <span class="fw-semibold" x-text="gitStatus().branch || '-'"></span>
              <span class="small text-secondary" x-show="gitStatus().upstream" x-text="gitStatus().upstream"></span>
              <span class="badge text-bg-secondary" x-show="gitStatus().summary" x-text="gitStatus().summary"></span>
            </div>
            <div class="small mt-1">
              <span class="diff-add" x-text="'+' + (gitStatus().added || 0)"></span>
              <span class="text-warning ms-2" x-text="'~' + (gitStatus().modified || 0)"></span>
              <span class="diff-del ms-2" x-text="'-' + (gitStatus().deleted || 0)"></span>
              <span class="text-info ms-2" x-text="'?' + (gitStatus().untracked || 0)"></span>
            </div>
            <div class="list-group list-group-flush mt-2" x-show="gitFiles().length > 0">
              <template x-for="file in gitFiles()" :key="file.path || file.Path">
                <div class="list-group-item bg-transparent px-0 py-1 git-file" :class="gitFileClass(file)">
                  <div class="d-flex align-items-start justify-content-between gap-2">
                    <span class="text-break"><span class="badge me-1" :class="gitCodeBadge(file)" x-text="gitFileCode(file)"></span><span x-text="file.path || file.Path"></span></span>
                    <span class="small text-nowrap" x-show="gitAdditions(file) || gitDeletions(file)">
                      <span class="diff-add" x-text="'+' + gitAdditions(file)"></span>
                      <span class="diff-del ms-1" x-text="'-' + gitDeletions(file)"></span>
                    </span>
                  </div>
                </div>
              </template>
            </div>
          </div>
        </template>
      </div>
    </aside>

    <form class="composer p-3 bg-body" @submit.prevent="send()">
      <div class="composer-menu list-group shadow-sm mb-2" x-show="completion.items.length > 0" style="display: none;">
        <template x-for="(item, idx) in completion.items" :key="item.insert_text + idx">
          <button type="button" class="list-group-item list-group-item-action d-flex align-items-start justify-content-between gap-3" :class="{'active': idx === completion.selected}" @mousedown.prevent="acceptCompletion(idx)">
            <span class="text-start">
              <span class="fw-semibold" x-text="item.label"></span>
              <span class="d-block small opacity-75" x-text="item.description || item.kind || ''"></span>
            </span>
            <span class="badge text-bg-secondary" x-text="completion.kind"></span>
          </button>
        </template>
      </div>
      <div class="input-group">
        <textarea class="form-control composer-input" rows="1" x-ref="composerInput" x-model="draft" placeholder="Ask koder or type / for commands" @input="onComposerInput()" @click="updateCompletions()" @keyup="onComposerKeyup($event)" @keydown="onComposerKeydown($event)"></textarea>
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

  <div class="modal-backdrop-lite" x-show="showSessions" x-transition @keydown.escape.window="closeSessionDialog()" style="display: none;">
    <section class="session-dialog bg-body border rounded shadow">
      <header class="d-flex align-items-center justify-content-between gap-3 p-3 border-bottom">
        <div>
          <div class="fw-semibold"><i class="bi bi-collection me-1"></i> Sessions</div>
          <div class="small text-secondary" x-text="sessionState.workdir || state.workdir || ''"></div>
        </div>
        <button type="button" class="btn btn-sm btn-outline-secondary" @click="closeSessionDialog()"><i class="bi bi-x-lg"></i></button>
      </header>
      <div class="p-3 border-bottom">
        <div class="input-group">
          <input class="form-control" x-model="newSessionTitle" placeholder="New Session" @keydown.enter.prevent="createSession()">
          <button class="btn btn-primary" type="button" @click="createSession()"><i class="bi bi-plus-lg"></i></button>
        </div>
      </div>
      <div class="session-list list-group list-group-flush">
        <template x-if="sessionLoading">
          <div class="list-group-item text-secondary"><span class="spinner-border spinner-border-sm me-2"></span>Loading sessions</div>
        </template>
        <template x-if="!sessionLoading && sessionRows().length === 0">
          <div class="list-group-item text-secondary">No sessions in this workspace</div>
        </template>
        <template x-for="session in sessionRows()" :key="sessionID(session)">
          <button type="button" class="list-group-item list-group-item-action d-flex align-items-start justify-content-between gap-3" :class="{'active': sessionID(session) === activeSessionID()}" @click="switchSession(sessionID(session))">
            <span class="text-start">
              <span class="fw-semibold" x-text="sessionTitle(session)"></span>
              <span class="d-block small opacity-75">
                <span x-text="sessionModel(session)"></span>
                <template x-if="session.LastMessage || session.last_message"><span x-text="' · ' + (session.LastMessage || session.last_message)"></span></template>
              </span>
            </span>
            <span class="badge text-bg-secondary" x-text="'#' + sessionID(session)"></span>
          </button>
        </template>
      </div>
    </section>
  </div>

  <div class="modal-backdrop-lite" x-show="showProviders" x-transition @keydown.escape.window="closeProviderDialog()" style="display: none;">
    <section class="provider-dialog bg-body border rounded shadow">
      <header class="d-flex align-items-center justify-content-between gap-3 p-3 border-bottom">
        <div>
          <div class="fw-semibold"><i class="bi bi-plug me-1"></i> Providers</div>
          <div class="small text-secondary" x-text="providerState.default_provider ? providerState.default_provider + ' / ' + (providerState.default_model || '-') : 'No default provider'"></div>
        </div>
        <button type="button" class="btn btn-sm btn-outline-secondary" @click="closeProviderDialog()"><i class="bi bi-x-lg"></i></button>
      </header>
      <div class="provider-body">
        <aside class="provider-list border-end">
          <div class="p-3 border-bottom d-flex align-items-center justify-content-between gap-2">
            <span class="small text-secondary">Configured</span>
            <button type="button" class="btn btn-sm btn-outline-primary" @click="addProvider()"><i class="bi bi-plus-lg"></i></button>
          </div>
          <div class="list-group list-group-flush">
            <template x-if="providerRows().length === 0">
              <div class="list-group-item text-secondary">None</div>
            </template>
            <template x-for="item in providerRows()" :key="item.id">
              <div class="list-group-item d-flex align-items-start gap-2" :class="{'active': providerDraft?.original_provider_id === item.id || providerDraft?.provider_id === item.id}">
                <button type="button" class="btn btn-sm p-0 border-0 bg-transparent flex-grow-1 text-start" :class="{'text-white': providerDraft?.original_provider_id === item.id || providerDraft?.provider_id === item.id}" @click="editProvider(item.id)">
                  <span class="fw-semibold" x-text="item.name || item.id"></span>
                  <span class="d-block small opacity-75">
                    <span x-text="item.default_model || '-'"></span>
                    <span class="badge text-bg-primary ms-1" x-show="item.default">default</span>
                  </span>
                </button>
                <button type="button" class="btn btn-sm" :class="(providerDraft?.original_provider_id === item.id || providerDraft?.provider_id === item.id) ? 'btn-outline-light' : 'btn-outline-danger'" title="Delete provider" @click.stop="deleteProvider(item.id)">
                  <i class="bi bi-trash"></i>
                </button>
              </div>
            </template>
          </div>
        </aside>
        <form class="provider-form p-3" @submit.prevent="saveProvider()">
          <template x-if="providerDraft">
            <div class="row g-3">
              <div class="col-md-6">
                <label class="form-label small text-secondary">Template</label>
                <select class="form-select" x-model="providerDraft.template_id" @change="providerTemplateChanged()">
                  <template x-for="template in providerTemplates()" :key="template.id">
                    <option :value="template.id" x-text="template.title"></option>
                  </template>
                </select>
              </div>
              <div class="col-md-6">
                <label class="form-label small text-secondary">Provider ID</label>
                <input class="form-control" x-model="providerDraft.provider_id" autocomplete="off">
              </div>
              <div class="col-md-6">
                <label class="form-label small text-secondary">Name</label>
                <input class="form-control" x-model="providerDraft.name" autocomplete="off">
              </div>
              <div class="col-md-6">
                <label class="form-label small text-secondary">Model</label>
                <input class="form-control" x-model="providerDraft.model" autocomplete="off">
              </div>
              <div class="col-12">
                <label class="form-label small text-secondary">Base URL</label>
                <input class="form-control" x-model="providerDraft.base_url" autocomplete="off">
              </div>
              <div class="col-12">
                <label class="form-label small text-secondary">API key</label>
                <input class="form-control" type="password" x-model="providerDraft.api_key" autocomplete="off">
              </div>
              <div class="col-12">
                <label class="form-label small text-secondary">Headers JSON</label>
                <textarea class="form-control font-monospace" rows="4" x-model="providerHeadersText" spellcheck="false"></textarea>
              </div>
              <div class="col-12" x-show="providerStatus">
                <div class="alert mb-0" :class="providerStatusKind === 'success' ? 'alert-success' : (providerStatusKind === 'danger' ? 'alert-danger' : 'alert-secondary')" x-text="providerStatus"></div>
              </div>
            </div>
          </template>
          <div class="text-secondary" x-show="!providerDraft">Select or add a provider</div>
          <footer class="d-flex justify-content-end gap-2 mt-3 pt-3 border-top">
            <button type="button" class="btn btn-outline-secondary" @click="testProvider()" :disabled="!providerDraft || providerTesting">
              <span class="spinner-border spinner-border-sm me-1" x-show="providerTesting"></span>Test
            </button>
            <button type="submit" class="btn btn-primary" :disabled="!providerDraft || providerSaving">
              <span class="spinner-border spinner-border-sm me-1" x-show="providerSaving"></span>Save
            </button>
            <button type="button" class="btn btn-outline-secondary" @click="closeProviderDialog()">Cancel</button>
          </footer>
        </form>
      </div>
    </section>
  </div>

  <div class="toast-stack" x-show="toast" x-transition style="display: none;">
    <div class="alert alert-danger shadow mb-0" role="status" x-text="toast"></div>
  </div>

  <script>
    window.KODER_ASSET_HASH = "__KODER_ASSET_HASH__";
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
    function toolTitleText(tool) {
      const kind = String((tool && tool.tool) || '');
      const data = toolData(tool);
      const args = toolArgs(tool);
      const path = firstValue(data, ['path', 'Path']) || firstValue(args, ['path']);
      switch (kind) {
        case 'read': return path ? 'Read ' + path : 'Read';
        case 'write': return path ? 'Write ' + path : 'Write file';
        case 'edit': return path ? 'Edit ' + path : 'Edit file';
        case 'apply_patch': return 'Apply patch';
        case 'bash': return 'Run command';
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
        default: return kind || 'Tool';
      }
    }
    function toolPreviewText(tool) {
      const args = toolArgs(tool);
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
          if (kind === 'view_image') return 'bi-image';
          return 'bi-wrench-adjustable';
        },
        toolTitle(tool) { return toolTitleText(tool); },
        toolPreview(tool) { return toolPreviewText(tool); },
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
  </script>
</body>
</html>`
