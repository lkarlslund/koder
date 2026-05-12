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
    .app-shell { height: 100vh; display: grid; grid-template-columns: minmax(0, 1fr) 6px var(--sidebar-width, 340px); grid-template-rows: auto minmax(0, 1fr) auto; }
    .topbar { grid-column: 1 / 4; }
    .transcript { min-height: 0; overflow: auto; }
    .sidebar-resizer { min-height: 0; cursor: col-resize; background: var(--bs-border-color); opacity: .55; touch-action: none; }
    .sidebar-resizer:hover, .sidebar-resizer.resizing { background: var(--bs-primary); opacity: 1; }
    .sidebar { min-height: 0; overflow: auto; border-left: 1px solid var(--bs-border-color); }
    .composer { grid-column: 1 / 4; border-top: 1px solid var(--bs-border-color); }
    .composer-input { max-height: 20vh; overflow-y: hidden; resize: none; }
    .composer-menu { max-height: 32vh; overflow: auto; }
    .message { border-radius: .75rem; }
    .message.user { background: var(--bs-primary-bg-subtle); }
    .message.assistant { background: var(--bs-tertiary-bg); }
    .tool { border-left: 3px solid var(--bs-info); }
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
                <i class="bi bi-chat-left-text"></i> <span x-text="chat.Title || chat.title || 'Chat'"></span>
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
          if (msg.type === 'theme') { this.theme = msg.payload.theme || 'auto'; writePreference('theme', this.theme); this.applyTheme(); }
        },
        rpc(method, params) {
          const id = this.nextID++;
          this.ws.send(JSON.stringify({id, method, params}));
          return new Promise((resolve, reject) => this.pending[id] = {resolve, reject}).catch(err => { this.error = err.message; this.showToast(err.message); throw err; });
        },
        applyState(s) {
          this.state = s || {}; this.applyTheme(); this.error = this.state.error || '';
          if (!this.restoreSelectedChat()) this.writeSelectedChat();
          this.$nextTick(() => { const el = document.querySelector('.transcript'); if (el) el.scrollTop = el.scrollHeight; });
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
          this.rpc('switch_chat', {chat_id: id}).then(s => this.applyState(s));
          return true;
        },
        timeline() { return this.state.snapshot?.Timeline || this.state.snapshot?.timeline || []; },
        approvals() { return this.state.snapshot?.Approvals || this.state.snapshot?.approvals || []; },
        pendingText() { const p = this.state.snapshot?.PendingAssistant || this.state.snapshot?.pending_assistant || {}; return [p.Reasoning || p.reasoning, p.Text || p.text].filter(Boolean).join('\n'); },
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
        formatArgs(args) { return args ? JSON.stringify(args, null, 2) : ''; },
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
        switchChat(id) { if (id) this.rpc('switch_chat', {chat_id: id}).then(s => { this.applyState(s); this.writeSelectedChat(); }); },
        newChat() { this.rpc('new_chat', {title: 'Chat'}).then(s => { this.applyState(s); this.writeSelectedChat(); }); },
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
