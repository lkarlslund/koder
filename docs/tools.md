# Tool Architecture

Koder exposes a compact, runtime-specific tool surface to models. Related
operations use a stable resource name plus an `action` argument. Operations with
materially different domains remain separate resources even when one client
implements all of them.

The normal Koder chat surface is 31 built-in names when the browser is available.
The stock ceiling is 46 when every browser and connected-phone resource is
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
| `browser_task` | Complete one bounded outcome using lightweight automation with managed-browser fallback |
| `browser_navigation` | `goto`, `back`, `forward`, `reload` |
| `browser_page` | `snapshot`, `find`, `wait` |
| `browser_interact` | `click`, `fill`, `type`, `press`, `select`, `check`, `uncheck`, `hover`, `drag`, `scroll`, `upload` |
| `browser_capture` | `screenshot`, `image`, `pdf` |
| `browser_network` | `list`, `get_request`, `get_response_body` |
| `browser_downloads` | `list`, `get` |
| `present` | `content`, `media`, `file` |
| `cron` | `create`, `list`, `delete` |
| `phone_photos` | `search`, `thumbnails`, `view`, `transfer` |

Phone capabilities are grouped by domain rather than put into one argument bag:
`phone_device`, `phone_location`, `phone_contacts`, `phone_calendar`,
`phone_messages`, `phone_calls`, `phone_notifications`, `phone_clock`,
`phone_clipboard`, `phone_apps`, `phone_media`, `phone_share`, `phone_open`,
`phone_photos`, `phone_logs` (the Koder app's own diagnostic log and logcat), and
`phone_alerts` (`schedule`, `list`, `cancel` notifications the phone shows at a
set time, armed with Android's alarm manager so they fire even when Koder is
closed). This keeps read-only lookup separate from communication, navigation,
state changes, and user-facing actions. Like every phone tool, each action is
Off, Ask, or On in the app's phone tool settings.

`cron` schedules a prompt in the calling chat, once or on a recurring 5-field
cron schedule in the server's local time (intervals such as `5m` also work).
Jobs live in the store, so they survive restarts. When one comes due the server
queues its prompt in the chat as a `cron` notification, and it runs once any
turn in progress has finished. Each job runs at a fixed offset derived from its
ID, so jobs set for the same minute do not reach the provider together: a
recurring job up to 10% of its period late (at most 15 minutes), a one-shot job
on :00 or :30 up to 90 seconds early. Recurring jobs expire after 7 days, with
a final run. A one-shot job that came due while Koder was not running is not
run; the chat is told it was missed. A recurring job skips the runs it missed.
Jobs of a deleted or archived chat are dropped when they come due. A chat sees
and deletes only its own jobs, at most 20 of them.

`web_search` runs through the providers in `internal/websearch`: Exa, Brave,
Tavily, Parallel, Perplexity, Serper, You.com, Firecrawl, Linkup, Mojeek, Jina,
Ollama, SearXNG and DuckDuckGo. Each adapter maps the same query (text, limit,
allowed and blocked domains, recency) onto its provider's own parameters, and
the results are then filtered by domain and date, deduplicated and cut to the
limit, so every provider answers in one shape. Settings > Search orders the
providers and holds their API keys (stored in `config.toml` under `[search]`,
never sent back to the browser) or base URLs. The tool asks the first enabled
provider and falls back to the next when one fails. DuckDuckGo needs no setup
and is on by default; SearXNG needs only a base URL; the rest need an API key.

Some operations intentionally remain first-class tools:

- `exec_command` starts a command; `exec_session` manages one after it outlives
  the startup wait.
- `file_read`, `file_write`, and `file_edit` have distinct schemas, access modes,
  results, and model-selection semantics.
- `file_glob`, `file_grep`, and `code_search` represent meaningfully different
  search strategies.
- `web_fetch` and `web_search` distinguish retrieval from discovery.
- `view_image` puts an image into model context, `view_pdf` renders one requested
  PDF page into bounded image context, while `present` is explicitly
  user-facing.
- `browser_task` owns a complete bounded workflow and a verified file result;
  it is not an action on a page resource. Koder selects its browser backend.

`view_pdf` is advertised only when Poppler's `pdfinfo` and `pdftoppm` commands
are installed. On Debian-derived systems they are provided by `poppler-utils`.

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

Codex and Claude Code receive the same canonical Koder planning, chat,
presentation, and phone resources: Codex as dynamic tools, Claude Code as an MCP
server named `koder` that Koder hosts over the CLI's own stdio. Their native
filesystem, shell, search, and MCP tools remain native rather than being
duplicated by Koder.

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

## Browser tab previews

The browser preview is a read-only viewport capture of an already owned tab.
It does not select or activate that tab, refresh every browser target, or wait
for the agent's navigation/interaction lock. Concurrent previews of one tab
return a busy response instead of queuing behind each other.

Preview captures have a three-second backend deadline. The UI uses a separate
HTTP request with a four-second deadline, rather than the sequential WebSocket
RPC queue. Closing or switching previews aborts the old request; late replies
cannot replace the new preview. Refresh failures clear the previous image and timestamp and show an explicit error; a stale capture is never presented as the refresh result.
Normal agent screenshot operations still synchronize with tab interactions.

## Exec inventory and resource usage

`exec_session action=list` lists running Koder-managed commands. Use
`scope=session` to include sibling chats. Entries include their description,
owner chat, host PID, working directory, UTC start time, elapsed wall time, consumed
CPU time, lifetime-average CPU percentage, process count, and summed resident
memory. `action=status` includes the same timing and resource fields.

On Linux, live resources are sampled from /proc for the command's process tree,
including waited-for child CPU time. A CPU average of 100% means one core;
multi-threaded work can exceed 100%. This is a lifetime average, not a current
utilization sample. Memory sums RSS and can double-count shared pages. Sampling
is best effort; unsupported or inaccessible metrics are reported as unavailable.
Completed commands retain final OS-reported CPU time when available.

Exec inventories are available on demand through `exec_session list`, including
`scope=session` for sibling jobs. They are not automatically injected into model
requests. An empty output stream does not mean a job has stopped.

Starting an identical command in the same working directory while it is already
running in this session is rejected, including simultaneous starts from sibling
chats. The error identifies the existing exec session and owner. Inspect and
reuse the existing job, or stop it before replacing it. For an intentional
concurrent copy, pass `allow_duplicate=true` to `exec_command`. This compares
command text and working directory, not semantic equivalence; changed commands
and separate working directories are not automatically blocked.

Only the owning chat can directly inspect or terminate an exec session.
Coordinate with its owner when a session-wide listing shows sibling work.
Keep jobs attached to their exec session: detached/reparented processes, remote
processes, and native external-agent jobs are outside this process-tree inventory.
This does not impose CPU quotas or automatically kill expensive work.

## Exec Notifications

An exec session that is still running when `exec_command` returns reports to
its chat on its own. The model does not need to wait for it.

- **Exit:** when the session ends, Koder queues one notification in the chat
  with the process id, state (with the reason when Koder stopped it, such as a
  timeout), exit code, runtime and the output not yet shown. Koder skips it
  when a tool result has already shown the exit, when a tool call that may show
  it is still in flight, or when the session was terminated on request. A
  later `wait` or `status` result says the exit was already reported, so its
  empty output does not read as the command printing nothing.
- **Lines (`notify=lines`):** each batch of new stdout lines is a notification
  too. Output printed before `exec_command` returns is in its result instead.
  - Lines arriving within 200 ms share one notification. Each line is cut at
    500 bytes and each batch at 3000.
  - A token bucket allows a burst of 10 notifications, refilled one per 2 s.
    Batches over the limit are dropped and counted, and the next notification
    says how many.
  - If output stays over the limit for 30 s, Koder stops the session.
  - The session stops after `timeout_ms`, 5 minutes by default and 30 at most.
    The exit notification then counts the events delivered.
  - Stderr is not an event source; a watch command merges it with `2>&1`.

Notifications are queued as steers with source `exec` or `monitor`: they start a
turn when the chat is idle, and join the running turn at its next step
otherwise. A child chat's report to its parent uses the same path with source
`child_report`. These messages reach the model framed as
`Koder notification, not written by the user (source: …)` around a
`<notification>` block. At a turn boundary, each source becomes its own message,
so a notification is never merged into the user's own steer.

A child chat that goes idle while one of its exec sessions may still report an
exit does not report idle to its parent yet. The exit report starts a new turn
in the child, and the parent hears about the child when that turn ends. If the
session ends without a report, for example because it was terminated, the idle
report goes out then.

