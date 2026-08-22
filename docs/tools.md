# Tool Architecture

Koder exposes a compact, runtime-specific tool surface to models. Related
operations use a stable resource name plus an `action` argument. Operations with
materially different domains remain separate resources even when one client
implements all of them.

The normal Koder chat surface is 29 built-in names when the browser is available.
The stock ceiling is 44 when every browser and connected-phone resource is
counted. A particular request receives only the subset allowed by its workflow
role, interaction mode, session settings, and live runtime capabilities.

## Shape

Use plural resource nouns for durable collections and singular nouns for one
runtime resource:

| Resource | Actions |
|---|---|
| `milestones` | `list`, `create`, `update`, `set_dependency`, `archive`, `restore`, `delete` |
| `tasks` | `list`, `create`, `update`, `next`, `archive`, `restore`, `delete` |
| `chats` | `list`, `start`, `send`, `cancel`, `archive`, `restore`, `rename`, `cleanup` |
| `exec_session` | `list`, `status`, `wait`, `send_input`, `resize`, `terminate`, `cleanup` |
| `browser_tabs` | `list`, `create`, `claim`, `select`, `close` |
| `browser_navigation` | `goto`, `back`, `forward`, `reload` |
| `browser_page` | `snapshot`, `find`, `wait` |
| `browser_interact` | `click`, `fill`, `type`, `press`, `select`, `check`, `uncheck`, `hover`, `drag`, `scroll`, `upload` |
| `browser_capture` | `screenshot`, `image`, `pdf` |
| `browser_network` | `list`, `get_request`, `get_response_body` |
| `browser_downloads` | `list`, `get` |
| `present` | `content`, `media`, `file` |
| `phone_photos` | `search`, `thumbnails`, `view`, `transfer` |

Phone capabilities are grouped by domain rather than put into one argument bag:
`phone_device`, `phone_location`, `phone_contacts`, `phone_calendar`,
`phone_messages`, `phone_calls`, `phone_notifications`, `phone_clock`,
`phone_clipboard`, `phone_apps`, `phone_media`, `phone_share`, `phone_open`, and
`phone_photos`. This keeps read-only lookup separate from communication,
navigation, state changes, and user-facing actions.

Some operations intentionally remain first-class tools:

- `exec_command` starts a command; `exec_session` manages one after it outlives
  the startup wait.
- `file_read`, `file_write`, and `file_edit` have distinct schemas, access modes,
  results, and model-selection semantics.
- `file_glob`, `file_grep`, and `code_search` represent meaningfully different
  search strategies.
- `web_fetch` and `web_search` distinguish retrieval from discovery.
- `view_image` puts an image into model context, while `present` is explicitly
  user-facing.
- `chat_status` describes the current chat and is independent of chat lifecycle
  state.

## Exposure

Definition filtering happens before every model request:

1. Legacy aliases are never advertised.
2. Workflow-role and interaction-mode policy remove forbidden resources or
   individual actions.
3. Session tool settings can disable a whole resource. Historical per-operation
   settings still remove the corresponding action during migration.
4. Runtime-backed resources appear only when their service exists. Browser
   actions require the browser service; phone actions require a connected device
   that advertised the exact server-known capability.
5. Destination-backed resources require an active persisted chat. `present` is
   therefore available to web chats and phone-companion conversations, but not
   to stateless single-call requests that have nowhere durable to render or replay
   the result. Its `media` and `file` actions additionally require the attachment
   and offered-file services, respectively; clients render the resulting shared
   presentation according to their own runtime capabilities.

An action resource builds its action enum dynamically from the operations that
survive those checks. This preserves narrow execution-chat policy without
creating separate model-facing names.

Codex receives the same canonical Koder planning, chat, presentation, and phone
resources as dynamic tools. Native Codex filesystem, shell, search, and MCP tools
remain native rather than being duplicated by Koder.

## Compatibility and Safety

Old operation handlers remain registered with `ToolSpec.Legacy`. They are hidden
from new model schemas but can still execute stored calls and old transcripts.
Canonical resource calls delegate to those handlers, preserving argument
normalization, ownership checks, access enforcement, stored-result types, and UI
rendering.

When old history is sent back to a provider, Koder translates it to the canonical
resource/action identity in memory. Stored data is not rewritten. For example,
`browser_tab_close` replays as `browser_tabs` with `action=close`, and old `bash`
calls replay as `exec_command`.

For diagnostics, a call has both a resource name and an action-aware identity such
as `browser_tabs.close`. Lifecycle records include `tool`, `action`, and
`tool_identity`. Filesystem and network checks still run in the delegated action
handler, so consolidation does not broaden access.

## Adding or Changing Tools

Before adding a callable name:

1. Decide whether it is an action on an existing resource.
2. Keep it separate if its domain, risk boundary, result medium, or selection
   semantics differ materially.
3. Give each action a precise schema and server-owned guidance.
4. Add role, interaction, runtime-capability, and disabled-state tests.
5. Add interoperability tests proving canonical dispatch and legacy replay when
   replacing an existing name.
6. Add web and voice render adapters for media or structured results.
7. Update prompts, bundled skills, and this document; models must never be taught
   a hidden legacy name.

Do not remove a legacy handler until persisted transcripts no longer need it or a
storage migration has converted every call and result safely.
