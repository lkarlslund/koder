# Chat backends and interaction modes

Koder's durable product hierarchy remains:

```text
app -> session -> chat -> tools
```

An external agent is not a new object beside that hierarchy. It is one way a
normal chat can execute a turn. Voice is similarly not a special kind of
session or agent. It is one way the user interacts with a normal chat.

## Independent chat dimensions

Every newly created chat stores these independent choices:

- **Backend**: who executes each turn (`koder`, `codex`, or a future driver).
- **Workflow role**: what the chat is responsible for (`orchestrator`,
  `planning`, `execution`, `general`, or `standalone`).
- **Interaction mode**: how input and output are adapted (`text` or `voice`).
- **Model and permission profile**: backend configuration and Koder policy.
- **Optional scope**: one milestone or one task for a focused execution chat.
- **Tool states**: per-chat switches for optional tools.

This permits Koder + voice and Codex + voice without implementing a separate
voice agent. It also permits a Codex orchestrator and a Koder execution chat.
The role controls which Koder tools are offered regardless of backend.

Legacy records with the old composite `voice` role are normalized to an
orchestrator workflow role plus voice interaction mode when loaded.

## Turn-driver boundary

The normal chat actor still owns identity, the input queue, cancellation,
runtime state, history, persistence, subscriptions, and rendering. Native
Koder turns can use the existing approval machinery; externally sandboxed
agent backends do not expose per-command approvals.
At the start of a turn it resolves the chat's backend to a turn driver:

```text
browser / Android / chat_send
              |
              v
       persistent chat actor
       /                   \
Koder native driver     Codex app-server driver
       \                   /
        canonical Koder timeline
              |
       Web UI / voice rendering
```

`NativeTurnDriver` adapts the existing Koder provider/tool loop. The Codex
driver supervises one `codex app-server --stdio` process per hydrated Codex
chat. Adding another backend requires a driver and backend discovery option;
it does not require a new session type, transcript format, or client protocol.

The driver emits the same domain events used by native turns. Consequently
both backends share queueing, busy/idle state, cancellation, `chat_status`,
voice busy cues, browser updates, and Android presentation adapters.

## Codex equivalence

A Codex-backed chat is persisted and displayed as an ordinary Koder chat. On
first hydration Koder starts a durable Codex thread in the session project
root, then stores the chat-to-thread binding. Later hydration resumes that
thread. A stopped app-server is restarted on demand and an interrupted turn
fails visibly instead of leaving the chat busy forever.

Koder owns chat lifecycle state. It mirrors a rename into a currently running
Codex thread, stops the process on archive, and removes the process, private
home, and local binding on delete. It deliberately does not archive the
underlying Codex rollout, so restoring a lazily stopped Koder chat can resume
the same thread without starting a process merely to update metadata.

Codex agent messages, reasoning, native command/file/MCP/web tool calls, Koder
dynamic tool calls, results, and errors are converted to canonical Koder
timeline items. This is what makes history, generic presentations,
`show_media`, and voice rendering work without Codex-specific UI code.

Codex retains its native coding tools. Koder supplies complementary dynamic
tools such as chats, sessions, milestones, tasks, presentations, and phone
photos. The server publishes the complete addition catalog and configured
permission profiles to both chat creators. Users can disable additions per
chat, and Koder rechecks role, interaction, and permission policy when a call
executes.

## External-agent isolation

Koder treats an external agent process as untrusted execution even when that
agent may operate autonomously. Each Codex chat therefore has:

- its own app-server process and private `CODEX_HOME`;
- the session project root as its working directory;
- the session's filesystem, `/tmp`, mount, and network access settings enforced
  around the entire process with bubblewrap;
- Codex `approvalPolicy=never` and `externalSandbox`, making bubblewrap the hard
  boundary instead of relying on per-command approval prompts;
- a policy fingerprint that restarts the process before the next turn whenever
  effective access settings change.

Processes start lazily, stop after an idle period, and are stopped immediately
when a chat is archived or deleted, Koder shuts down, or Codex configuration is
changed. Deleting a chat also removes its private Codex home. Koder uses a
short-lived discovery process for the model catalog; it is not shared with a
chat.

Existing Codex chats created before per-chat isolation keep their durable
thread. On first hydration Koder copies only that thread's rollout from the
configured account-level Codex home into the chat's private home. It does not
copy other Codex conversations.

Network access is one boundary today: disabling it removes networking from the
whole app-server, including model transport. Supporting a remote Codex backend
while denying network only to its child commands requires a separate
backend-transport proxy; Koder does not silently weaken a network-disabled
session in the meantime.

Koder never creates branches or applies a special branching policy. A user or
orchestrator may ask a Codex chat to create or use a branch; that remains work
performed by Codex inside its configured workspace and sandbox.

## Voice layering

Voice interaction contributes generic behavior instructions—brief spoken
sentences, no accidental Markdown, and deliberate presentation tool use—to
whichever turn driver the chat selected. Android and the browser continue to
use the same `voice.v1` transport, STT/TTS pipeline, history paging, barge-in,
busy state, phone tools, and generic MIME presentations.

Koder may run many voice chats concurrently. Each connected device or browser
tab may lease only one active voice chat at a time. The constraint belongs to
the client lease, not the backend process or session.

## Creation and orchestration

The browser `+` creator and Android conversation creator submit the same
`domain.ChatCreateSpec`. `chat_start` uses the same dimensions and defaults its
backend to the calling chat, so either an ordinary or Codex orchestrator can
create a peer on either backend. `chat_list` returns backend and interaction
metadata so an orchestrator can choose an appropriate existing chat.

Backend discovery is live. Opening a creator probes Codex app-server for its
current model catalog; a disabled, missing, or unhealthy backend remains
visible with its error but cannot be selected.
