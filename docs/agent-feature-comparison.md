# Coding Agent Feature Comparison

Feature comparison of Koder and the agent projects tracked under `research/`, based
on repository source and documentation as of 2026-08-05.

This compares product capabilities, not marketing breadth. A feature is marked as
native only when the checked repository contains a first-class implementation or
documents one. Companion products, optional execution backends, experimental
features, and host-provided behavior are marked partial.

Legend: **Yes** = first-class support, **Partial** = limited, experimental,
companion, backend-dependent, or indirect support, **No** = no evidence of native
support, **N/A** = not applicable to the product's scope, **Host** = inherited from
OpenCode or Codex by Oh My OpenAgent.

## Research Snapshot

| Product | Revision reviewed | Snapshot date | Refresh result |
|---|---:|---:|---|
| Koder | `e8be4631` | 2026-08-05 | Current working repository |
| OpenHands | `bf2e37dc` | 2026-08-04 | Fast-forwarded |
| Aider | `5dc9490` | 2026-05-22 | Already current |
| Claude Code snapshot | `5a774a2` | 2026-03-31 | Upstream disabled (HTTP 403) |
| Cline | `d626cfb0` | 2026-08-05 | Fast-forwarded |
| Codex | `f2d82553` | 2026-08-05 | Fast-forwarded; `codex-cli` is a duplicate clone |
| Continue | `5522c6f4` | 2026-07-20 | Fast-forwarded; repository is read-only/final 2.0.0 |
| Gemini CLI | `ac42fb0` | 2026-08-03 | Fast-forwarded |
| Goose | `96a5a99` | 2026-08-05 | Fast-forwarded |
| Hermes Agent | `1be70d63` | 2026-08-05 | Fast-forwarded |
| Oh My OpenAgent | `302c5eae` | 2026-08-05 | Fast-forwarded; repository folder retains old name |
| OpenCode | `4a57013c` (`origin/dev`) | 2026-08-05 | Fetched only; local branch is divergent and has untracked files |
| PR-Agent | `4a26c38` | 2026-08-01 | Fast-forwarded |
| Tabby | `21b2904` | 2026-06-30 | Fast-forwarded |

The Claude Code checkout is an unofficial snapshot and cannot be treated as a
complete or authoritative representation of the current proprietary product.
OpenCode was inspected at the fetched `origin/dev` revision without changing its
dirty local worktree.

## Surfaces and Deployment

| Product | Interactive CLI / TUI | Browser UI | Desktop app | IDE / ACP surface | Headless / API | Multi-provider | Local models | Self-hosted |
|---|---|---|---|---|---|---|---|---|
| **Koder** | No | **Yes** | No | No | **Yes** (`koder exec`; internal RPC) | **Yes** | **Yes** | **Yes** |
| Aider | **Yes** | **Yes** | No | No | **Yes** | **Yes** | **Yes** | **Yes** |
| Claude Code snapshot | **Yes** | Partial (hosted product) | Partial (host integration) | **Yes** (IDE bridge) | **Yes** | No | No | No |
| Cline | **Yes** | **Yes** (Kanban companion) | No | **Yes** (VS Code, JetBrains, ACP) | **Yes** (CLI/SDK) | **Yes** | **Yes** | **Yes** |
| Codex | **Yes** | **Yes** (Codex Web) | **Yes** | **Yes** (IDE) | **Yes** (`exec`, app server) | Partial (custom/OSS providers) | **Yes** (OSS mode) | **Yes** (CLI) |
| Continue | **Yes** | No | No | **Yes** (VS Code, JetBrains) | **Yes** (`cn -p`, server) | **Yes** | **Yes** | **Yes** |
| Gemini CLI | **Yes** | No | No | **Yes** (IDE companion, native ACP) | **Yes** | Partial (Gemini agent) | Partial (Gemma routing only) | **Yes** |
| Goose | **Yes** | No | **Yes** | **Yes** (ACP server/client) | **Yes** | **Yes** | **Yes** | **Yes** |
| Hermes Agent | **Yes** | Partial (API/external frontends) | **Yes** | **Yes** (native ACP) | **Yes** (API server) | **Yes** | **Yes** | **Yes** |
| Oh My OpenAgent | **Host** | **Host** | **Host** | **Host** | **Host** | **Host** | **Host** | **Host** |
| OpenCode | **Yes** | **Yes** | **Yes** | **Yes** (native ACP) | **Yes** (server/SDK) | **Yes** | **Yes** | **Yes** |
| OpenHands Agent Canvas | Partial (launcher/backends) | **Yes** | Partial (Electron) | **Yes** (hosts ACP agents) | **Yes** (agent server) | **Yes** | **Yes** | **Yes** |
| PR-Agent | **Yes** | No | No | No | **Yes** (CLI/webhooks/actions) | **Yes** (LiteLLM) | **Yes** | **Yes** |
| Tabby | N/A (server administration) | **Yes** (admin/code browser) | No | **Yes** (VS Code, JetBrains, Vim) | **Yes** (server API) | **Yes** | **Yes** | **Yes** |

## Runtime and Control

| Product | Persistent sessions | Compaction | Approvals | OS sandbox | Queue / steer | MCP | ACP | Skills / rules | Vision | Web / browser tools |
|---|---|---|---|---|---|---|---|---|---|---|
| **Koder** | **Yes** | **Yes** | **Yes** | **Yes** (bubblewrap) | **Yes** (queue, steer, abort-and-send) | **Yes** (HTTP, stdio) | No | **Yes** | **Yes** | Partial (fetch/search, no browser control) |
| Aider | **Yes** | **Yes** | Partial (command confirmation) | No | No | No | No | Partial (conventions) | **Yes** | Partial (URLs/search) |
| Claude Code snapshot | **Yes** | **Yes** | **Yes** | Partial (permission/sandbox policy) | **Yes** | **Yes** | No native evidence | **Yes** | **Yes** | **Yes** |
| Cline | **Yes** | **Yes** | **Yes** | No | Partial (interrupt/follow-up) | **Yes** | **Yes** | **Yes** | **Yes** | **Yes** |
| Codex | **Yes** | **Yes** | **Yes** | **Yes** | **Yes** | **Yes** | No native evidence | **Yes** (skills/plugins) | **Yes** | **Yes** |
| Continue | **Yes** | **Yes** | **Yes** | No | No | **Yes** | No evidence | **Yes** | **Yes** | **Yes** |
| Gemini CLI | **Yes** | **Yes** | **Yes** | **Yes** (Seatbelt/containers/runsc/LXC) | Partial (steering/follow-up) | **Yes** | **Yes** | **Yes** | **Yes** | **Yes** |
| Goose | **Yes** | **Yes** | **Yes** | Partial (optional container/cloud backends) | **Yes** (including ACP steer) | **Yes** | **Yes** (server and client) | **Yes** | **Yes** | **Yes** |
| Hermes Agent | **Yes** | **Yes** | **Yes** | Partial (Docker/SSH/cloud backends) | Partial (interrupt/follow-up) | **Yes** | **Yes** | **Yes** | **Yes** | **Yes** |
| Oh My OpenAgent | **Host** | **Host** | **Host** | **Host** | Partial (host plus continuation loop) | **Yes** (host and embedded) | **Host** | **Yes** | **Host** | **Yes** |
| OpenCode | **Yes** | **Yes** | **Yes** | No (permissions are not OS isolation) | **Yes** | **Yes** | **Yes** | **Yes** | **Yes** | **Yes** |
| OpenHands Agent Canvas | **Yes** | Partial (agent-dependent) | Partial (agent-dependent) | **Yes** (Docker option) | Partial (agent-dependent) | Partial (agent-dependent) | **Yes** (ACP host) | Partial (agent-dependent) | Partial (agent-dependent) | **Yes** |
| PR-Agent | N/A | Partial (PR compression) | N/A | N/A | N/A | No | No | Partial (skills/AGENTS.md) | No | N/A |
| Tabby | Partial (chat history) | No evidence | N/A | N/A | N/A | No | No | Partial (repository context) | No | N/A |

## Code and Git Workflow

| Product | Read/edit/shell | LSP / symbols | Repo map / index | Post-edit diagnostics | Repair/test loop | Diff review | Workspace rollback | Auto-commit | Worktrees | PR / CI workflow |
|---|---|---|---|---|---|---|---|---|---|---|
| **Koder** | **Yes** | **Yes** | No | **Yes** (LSP/syntax, Go, shell, Markdown/Mermaid) | Partial (diagnostics feed the next model step) | **Yes** (tree/status/diff UI) | No | No | No | No |
| Aider | **Yes** | No | **Yes** (repository map) | **Yes** (automatic lint/test) | **Yes** | **Yes** | **Yes** (`/undo` via git) | **Yes** | No | Partial (commit-oriented) |
| Claude Code snapshot | **Yes** | Partial (IDE bridge) | Partial | **Yes** | **Yes** | **Yes** | Partial | No | Partial | **Yes** |
| Cline | **Yes** | Partial (IDE diagnostics) | Partial (codebase indexing) | **Yes** | **Yes** | **Yes** | **Yes** (shadow-git checkpoints) | Partial (Kanban companion) | **Yes** (Kanban companion) | **Yes** |
| Codex | **Yes** | No native symbol tool | No | Partial (command/test driven) | **Yes** | **Yes** | Partial (thread rollback does not restore files) | No | **Yes** | **Yes** (review/action/cloud) |
| Continue | **Yes** | **Yes** (IDE context) | **Yes** (codebase index) | Partial (IDE diagnostics) | Partial | **Yes** | Partial (IDE/session undo) | No | No | **Yes** |
| Gemini CLI | **Yes** | Partial (IDE context) | No | Partial (command/test driven) | **Yes** | **Yes** | **Yes** (workspace checkpoint/restore) | No | **Yes** | **Yes** (GitHub action) |
| Goose | **Yes** | Partial (extensions) | No | Partial (tool/extension driven) | **Yes** | **Yes** | No native workspace checkpoint | No | Partial (recipes/extensions) | Partial |
| Hermes Agent | **Yes** | **Yes** | Partial (memory/search, not a repo map) | **Yes** | **Yes** | **Yes** | **Yes** (automatic git-object checkpoints) | No | **Yes** | Partial |
| Oh My OpenAgent | **Yes** | **Yes** | Partial (AST-grep, no repo map) | **Yes** (LSP) | **Yes** | **Host** | **Host** | Partial (shipping workflows) | **Yes** | **Yes** (workflows) |
| OpenCode | **Yes** | **Yes** | No | **Yes** (LSP) | **Yes** | **Yes** | **Yes** (snapshots/revert) | No | **Yes** | Partial (GitHub agent) |
| OpenHands Agent Canvas | Partial (agent-dependent) | Partial (agent-dependent) | Partial (agent-dependent) | Partial (agent-dependent) | Partial (agent-dependent) | **Yes** | Partial (runtime/backend-dependent) | Partial (agent-dependent) | Partial (runtime-dependent) | **Yes** (integrations/automations) |
| PR-Agent | Partial (PR patch only) | No | Partial (PR/repository context) | N/A | N/A | **Yes** | N/A | No | No | **Yes** (core purpose) |
| Tabby | Partial (completion/chat; Pochi is separate) | Partial (IDE) | **Yes** (AST/code index) | Partial (IDE) | No | Partial | No | No | No | No |

Koder's post-edit loop is stronger than the previous matrix stated. File edit and
write tools automatically run applicable diagnostics and append the result to the
tool response. That includes LSP/syntax diagnostics and dedicated Go, shell,
Markdown, and server-side Mermaid validation. The remaining gap is a deterministic
test runner or repair controller; today the model decides how to react to the
diagnostic feedback.

Workspace rollback and auto-commit are deliberately separate rows. Codex thread
rollback changes conversation history but explicitly does not restore filesystem
state. OpenCode and Gemini have workspace snapshots, while Aider uses git commits
as its undo mechanism. Cline's automatic commits and per-card worktrees belong to
its Kanban companion rather than every IDE task.

## Planning and Orchestration

| Product | Plan-only mode | Task tracker | Dependencies / milestones | Human board | Subagents | Teams / orchestration | Concurrency control | Schedule / webhook | Persistent learning memory |
|---|---|---|---|---|---|---|---|---|---|
| **Koder** | Partial (read-only permission profile) | **Yes** | **Yes** (milestones, statuses, dependencies) | **Yes** (drag/drop swim lanes) | **Yes** (execution chats) | Partial (orchestrator/worker topology) | **Yes** (per-session preference) | No | No |
| Aider | No | No | No | No | No | No | N/A | No | No |
| Claude Code snapshot | **Yes** | **Yes** | Partial | No | **Yes** | Partial (teams) | Partial | Partial (hosted automations) | Partial (project memory) |
| Cline | **Yes** (Plan/Act) | **Yes** | **Yes** (Kanban chains) | **Yes** (Kanban companion) | Partial (research agents) | **Yes** (teams) | **Yes** | **Yes** (cron agents) | Partial (rules/memory bank) |
| Codex | **Yes** | **Yes** (plans) | No task dependencies | No | **Yes** | **Yes** (teams) | **Yes** | Partial (hosted automations) | Partial (project instructions) |
| Continue | **Yes** | No | No | No | No | No | N/A | No | Partial (rules/context) |
| Gemini CLI | **Yes** | **Yes** | **Yes** (dependency-aware tracker) | No | **Yes** | Partial (experimental agents) | **Yes** | No | Partial (`GEMINI.md`/saved context) |
| Goose | **Yes** | Partial (Todo extension) | No | No | **Yes** | Partial (conductor/subrecipes) | Partial | **Yes** (scheduled recipes) | Partial (session/memory extensions) |
| Hermes Agent | **Yes** | **Yes** | **Yes** (durable SQLite tasks) | **Yes** (Kanban dashboard) | **Yes** | **Yes** (named worker profiles) | **Yes** | **Yes** (cron) | **Yes** |
| Oh My OpenAgent | **Yes** (Prometheus) | **Yes** | **Yes** (persistent dependencies) | No | **Yes** (background agents) | **Yes** (teams up to configured limits) | **Yes** | No evidence | Partial (persistent task/project state) |
| OpenCode | **Yes** | **Yes** | Partial (todos, no rich dependency graph) | No | **Yes** | Partial (background agents) | **Yes** | No | Partial (rules/session state) |
| OpenHands Agent Canvas | Partial (agent-dependent) | Partial (agent-dependent) | Partial (automation workflows) | No task swim-lane board | **Yes** (multiple ACP agents) | **Yes** (control plane) | Partial (runtime-dependent) | **Yes** (scheduled/webhook automation) | Partial (agent-dependent) |
| PR-Agent | N/A | N/A | N/A | No | No | No | N/A | **Yes** (webhooks/actions) | No |
| Tabby | N/A | No | No | No | No | No | N/A | No | No |

Koder's distinctive planning feature is not merely a model-local todo list. Tasks
and milestones are durable session data, visible to both the orchestrator and the
user, and editable through both tools and a browser board. Hermes now has the
closest comparable implementation, but extends it into a dispatcher with worker
heartbeats, retries, comments, named profiles, and selectable worktree/scratch/
directory workspaces. Cline's companion board is the closest Git-centric workflow,
with dependency chains, per-card worktrees, and automatic commits.

## Extensibility and Operator Experience

| Product | Plugins / hooks | Public SDK / API | Custom request shaping | File/artifact viewer | Rich Markdown / diagrams | Voice / TTS | Debug / traces | Browser automation |
|---|---|---|---|---|---|---|---|---|
| **Koder** | Partial (skills and MCP; no hook API) | Partial (headless CLI/internal RPC) | **Yes** (per-model JSON) | **Yes** | **Yes** (Markdown, Mermaid, math, images) | **Yes** (TTS output) | **Yes** (provider/debug traces) | No |
| Aider | Partial (scripting/conventions) | Partial (CLI) | **Yes** | Partial (terminal/browser chat) | Partial | **Yes** (voice input) | Partial (verbose logs) | No |
| Claude Code snapshot | **Yes** | **Yes** (agent SDK) | Partial | Partial | Partial | Partial | **Yes** | **Yes** |
| Cline | **Yes** | **Yes** | **Yes** | **Yes** (IDE) | **Yes** | No | **Yes** | **Yes** |
| Codex | **Yes** | **Yes** (app server/protocol) | **Yes** | Partial (client-dependent) | **Yes** | No | **Yes** (OTel/logs) | **Yes** |
| Continue | **Yes** | Partial (CLI/server) | **Yes** | **Yes** (IDE) | **Yes** | No | Partial | Partial (MCP) |
| Gemini CLI | **Yes** (extensions/hooks) | **Yes** | **Yes** | Partial (terminal/IDE) | **Yes** | No | **Yes** | **Yes** |
| Goose | **Yes** (extensions) | **Yes** (ACP/API) | **Yes** | **Yes** (desktop) | **Yes** | Partial (extensions) | **Yes** | **Yes** |
| Hermes Agent | **Yes** | **Yes** (OpenAI-compatible API) | **Yes** | **Yes** | **Yes** | **Yes** | **Yes** | **Yes** |
| Oh My OpenAgent | **Yes** | **Host** | **Host** | **Host** | **Host** | **Host** | **Yes** (hooks/notifications) | **Yes** |
| OpenCode | **Yes** | **Yes** (SDK/server) | **Yes** | **Yes** | **Yes** | No | **Yes** | **Yes** |
| OpenHands Agent Canvas | **Yes** (integrations/ACP agents) | **Yes** (REST agent server) | **Yes** | **Yes** | **Yes** | Partial (agent-dependent) | **Yes** | **Yes** |
| PR-Agent | Partial (skills/configuration) | **Yes** (CLI/webhooks) | **Yes** (LiteLLM/config) | N/A | **Yes** (PR comments) | No | **Yes** | N/A |
| Tabby | Partial (model/back-end integrations) | **Yes** | **Yes** | **Yes** (web/IDE) | Partial | No | **Yes** | No |

## What Koder Already Does Well

1. **Browser-native parallel work.** Multiple sessions and chats run concurrently
   in one process without a global active-session assumption. Users can inspect
   tools, processes, Git changes, files, rendered artifacts, and agent state in
   separate browser tabs.
2. **Shared human/agent planning.** The milestone and task board is durable,
   interactive, and controlled through the same data model used by agent tools.
3. **Controlled orchestration.** An orchestrator can steer execution chats while a
   per-session concurrency cap prevents uncontrolled fan-out.
4. **Local-model depth.** Provider discovery, model health/capability detection,
   per-model request JSON, long-context compaction, cache diagnostics, and TTS are
   unusually complete for a self-hosted browser agent.
5. **In-loop validation.** LSP code search and automatic post-edit diagnostics,
   including Markdown and Mermaid validation without a Node server dependency,
   already exceed many general-purpose agents.
6. **Inspectable execution.** Tool calls, command input/output/exit status,
   permission decisions, live processes, and provider traces remain visible rather
   than being hidden behind a single progress indicator.

## Highest-Value Gaps

1. **Workspace checkpoints and rollback.** Add filesystem restoration independent
   of chat rollback. This is a more important safety primitive than auto-commit and
   is now mature in Cline, Gemini CLI, Hermes, and OpenCode.
2. **Worktree-isolated execution chats.** Optional per-task or per-worker worktrees
   would let parallel agents edit safely and make review/merge boundaries explicit.
3. **ACP support.** Serving Koder chats as ACP agents and optionally hosting ACP
   agents would connect Koder's browser control plane to the ecosystem used by
   Gemini CLI, Goose, Hermes, OpenCode, Cline, and OpenHands.
4. **Repository map/index.** Aider and Tabby still have the clearest large-codebase
   context advantage. Koder's LSP tools help precise lookup but do not provide a
   compact architectural map for prompt construction.
5. **Richer durable worker semantics.** Task blocked reasons, comments, attempts,
   retry policy, worker heartbeat/lease, and attached artifacts would strengthen
   long-running orchestration without requiring peer-agent teams.
6. **Hooks and a stable API/SDK.** Lifecycle hooks around tool calls, turns,
   compaction, and task transitions would make integrations possible without
   modifying Koder core.
7. **Scheduled and webhook-triggered runs.** OpenHands, Cline, Goose, Hermes, and
   PR-Agent demonstrate useful automation patterns once execution isolation and
   recovery are reliable.
8. **PR/CI integration.** A focused review/action mode would make headless Koder
   useful outside an open browser session.
9. **Browser automation.** This is increasingly common, but should follow stronger
   checkpointing and isolation because it expands the permission surface.
10. **Optional persistent memory.** Project learnings could survive compaction and
    sessions, but should be inspectable, scoped, and user-editable rather than an
    opaque automatic memory store.

## Conclusions

- **Hermes Agent** now has the broadest overlap with Koder's durable task and worker
  direction, while adding checkpoints, worktrees, retries, schedules, memory, and
  multiple execution backends.
- **OpenHands Agent Canvas** is the closest browser control-plane comparison. Its
  advantage is ACP aggregation and automation; Koder's advantage is a tighter,
  locally inspectable chat/tool/task implementation.
- **Cline** has the most complete IDE-to-Kanban development workflow, especially
  checkpoints, diagnostics, worktrees, and automated commits.
- **OpenCode plus Oh My OpenAgent** has the strongest combination of extensibility,
  LSP tooling, subagents, team orchestration, and host portability.
- **Aider** remains the strongest focused edit/lint/test/commit loop and repository
  map, despite having little orchestration infrastructure.
- **Gemini CLI, Codex, and Goose** set the reference point for protocol integration,
  sandboxing or approvals, and steerable headless agent runtimes.
- **PR-Agent and Tabby** are specialists rather than direct Koder replacements:
  PR-Agent owns PR automation, while Tabby owns self-hosted completion and indexed
  IDE context.

Koder should not copy breadth indiscriminately. Its clearest path is to deepen the
browser-based, local-first orchestrator: recoverable workspaces, isolated workers,
ACP interoperability, and durable task execution. Auto-commit, messaging bots, and
general personal-assistant features are secondary once those foundations exist.

## Primary Evidence Paths

- Koder: [`README.md`](../README.md), [`cmd/koder/exec.go`](../cmd/koder/exec.go),
  [`internal/chat/tool_call.go`](../internal/chat/tool_call.go),
  [`internal/codediag`](../internal/codediag), and
  [`internal/tools/codesearchtool`](../internal/tools/codesearchtool).
- Competitors: each repository's root README plus its checked-in `docs/`, command
  source, protocol definitions, tool implementations, and tests at the revisions
  listed above. Source code was preferred where documentation and behavior differed.
