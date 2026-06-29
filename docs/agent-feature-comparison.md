# Coding Agent Feature Comparison

Feature matrix across major coding agents and koder's current implementation.

Legend: ✅ = native support, ⚠️ = partial/limited, ❌ = missing, — = not applicable

## Core Features

| Feature  | Koder | Aider | Cline | OpenCode | Gemini CLI | Goose | OpenHands | Codex | Hermes | Continue | Tabby | PR-Agent |
|----------------------------------|:-----:|:-----:|:-----:|:--------:|:----------:|:-----:|:---------:|:-----:|:------:|:--------:|:-----:|:--------:|
| **Terminal UI**  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ✅  |
| **Browser / Web UI**  | ✅  | ❌  | ⚠️  | ⚠️  | ❌  | ❌  | ✅  | ⚠️  | ⚠️  | ❌  | ❌  | ❌  |
| **Desktop App**  | ❌  | ❌  | ❌  | ⚠️ BETA  | ❌  | ✅  | ✅  | ✅  | ❌  | ❌  | ❌  | ❌  |
| **VS Code Extension**  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ✅  | ❌  |
| **JetBrains Plugin**  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  |
| **Multi-provider / BYO model**  | ✅  | ✅  | ✅  | ✅  | ⚠️ Gemini  | ✅  | ✅  | ⚠️ OA | ✅  | ✅  | ✅  | ✅  |
| **Local model support**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ✅  | ✅  | ✅  | ✅  |
| **MCP integration**  | ✅  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ❌  |
| **Persistent sessions**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  |
| **Context compaction**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  |
| **Permission / approval system** | ✅  | ⚠️  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ❌  |
| **Shell sandboxing**  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅ Docker | ⚠️  | ✅  | ❌  | ❌  | ❌  |

## Code Understanding & Editing

| Feature  | Koder | Aider | Cline | OpenCode | Gemini CLI | Goose | OpenHands | Codex | Hermes | Continue | Tabby | PR-Agent |
|----------------------------------|:-----:|:-----:|:-----:|:--------:|:----------:|:-----:|:---------:|:-----:|:------:|:--------:|:-----:|:--------:|
| **Read / glob / grep**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  |
| **LSP code search**  | ✅  | ❌  | ❌  | ✅  | ❌  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  |
| **Repo map / codebase index**  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ⚠️  | ❌  |
| **Targeted file edits**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  |
| **Post-edit diagnostics**  | ⚠️  | ❌  | ✅  | ⚠️  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |
| **Auto-fix lint/compiler errors**| ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |
| **Multi-file coordinated edits** | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  |
| **Image / multimodal input**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ❌  |
| **Web fetch / search**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ❌  |

## Git & Workflow Integration

| Feature  | Koder | Aider | Cline | OpenCode | Gemini CLI | Goose | OpenHands | Codex | Hermes | Continue | Tabby | PR-Agent |
|----------------------------------|:-----:|:-----:|:-----:|:--------:|:----------:|:-----:|:---------:|:-----:|:------:|:--------:|:-----:|:--------:|
| **Git status tracking**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  |
| **Auto-commit with messages**  | ❌  | ✅  | ✅  | ✅  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  |
| **Git diff / undo AI changes**  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  |
| **Checkpoint / restore workspace** | ❌ | ✅ | ✅ | ✅ | ✅ | ⚠️ | ✅ | ✅ | ⚠️ | ⚠️ | ❌ | ❌ |
| **PR review (GitHub/GitLab)**  | ❌  | ❌  | ❌  | ❌  | ✅ Action  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ✅  |
| **CI / GitHub Action**  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ✅  |
| **Headless / CI mode**  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ✅  |

## Planning & Orchestration

| Feature  | Koder | Aider | Cline | OpenCode | Gemini CLI | Goose | OpenHands | Codex | Hermes | Continue | Tabby | PR-Agent |
|----------------------------------|:-----:|:-----:|:-----:|:--------:|:----------:|:-----:|:---------:|:-----:|:------:|:--------:|:-----:|:--------:|
| **Milestone / task planning**  | ✅  | ❌  | ⚠️  | ⚠️  | ⚠️  | ⚠️  | ❌  | ⚠️  | ❌  | ❌  | ❌  | ❌  |
| **Session todo / task tracker** | ✅ | ❌ | ✅ | ✅ | ✅ | ⚠️ | ❌ | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Plan-only / read-only mode** | ⚠️ | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ⚠️ | ✅ | ❌ | ❌ |
| **Background sub-agents**  | ✅  | ❌  | ✅  | ✅  | ⚠️  | ✅  | ✅  | ✅  | ✅  | ❌  | ❌  | ❌  |
| **Multi-agent teams**  | ❌  | ❌  | ✅  | ⚠️  | ⚠️  | ⚠️  | ✅  | ✅  | ✅  | ❌  | ❌  | ❌  |
| **Scheduled / cron tasks**  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |
| **Skills system**  | ✅  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  |
| **Project rules (AGENTS.md)**  | ✅  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ❌  |
| **Queue / steer while running**  | ✅  | ❌  | ❌  | ✅  | ⚠️  | ✅  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  |

## Extensibility & Platforms

| Feature  | Koder | Aider | Cline | OpenCode | Gemini CLI | Goose | OpenHands | Codex | Hermes | Continue | Tabby | PR-Agent |
|----------------------------------|:-----:|:-----:|:-----:|:--------:|:----------:|:-----:|:---------:|:-----:|:------:|:--------:|:-----:|:--------:|
| **Messaging (Slack/Telegram…)**  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |
| **Voice input / TTS**  | ⚠️  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |
| **Browser / desktop control**  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ⚠️  | ❌  | ⚠️  | ❌  | ❌  | ❌  |
| **SDK / programmatic API**  | ❌  | ❌  | ✅  | ✅  | ⚠️  | ✅  | ✅  | ✅  | ❌  | ❌  | ✅  | ❌  |
| **Plugin system**  | ❌  | ❌  | ✅  | ✅  | ❌  | ✅  | ❌  | ✅  | ❌  | ✅  | ❌  | ❌  |
| **Self-hosted / on-prem**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ✅  | ✅  | ✅  | ✅  |
| **Learning loop / memory**  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |

## Key Observations

1. **Koder's strengths**: persistent milestone/task planning, browser-native session management, shell sandboxing, background execution chats, permission profiles, local-first model/provider configuration, and live steer/queue control. The steer feature is no longer unique: Codex, OpenCode, and Goose now expose steer-style control, and Gemini CLI has model steering.

2. **Biggest gaps vs. competition**:
   - **Repo map / codebase index** — Only Aider and Tabby have this. This is the single biggest gap for large codebase support.
   - **Auto-commit** — Aider, Cline, OpenCode, and OpenHands all auto-commit with AI-generated messages. Koder has no structured git commit tool.
   - **Checkpoint / restore workspace** — Aider, Cline, OpenCode, Gemini CLI, OpenHands, and Codex have a stronger story for returning the workspace to a previous agent state.
   - **Plan-only / read-only mode** — Cline, OpenCode, Gemini CLI, Goose, OpenHands, Codex, and Continue expose an explicit planning mode before edits or commands. Koder has permissions and orchestration, but not a first-class read-only planning workflow.
   - **Post-edit diagnostics** — Hermes and Cline automatically feed lint/compiler errors back after every edit. Koder has the infrastructure (`codediag.CheckEdit`) but it's not wired into the edit loop automatically.
   - **Headless/CI mode** — Almost every competitor supports non-interactive, scripted execution. Koder is browser-only.
   - **PR review / CI integration** — Continue, PR-Agent, and Gemini CLI offer automated PR review as a core feature.

3. **Recent research updates**:
   - **Codex** now has todo/plan events, sub-agent tooling, plugins/skills, and steer/interrupt tests.
   - **OpenCode** exposes queue vs. steer delivery, todos with cancelled status, background subagents, generated SDK types, LSP events, and plan agents.
   - **Goose** exposes ACP steer handling and has documented subagents/subrecipes.
   - **Gemini CLI** has todos, a task tracker, plan mode, model steering, checkpoints, and documented subagent support.
   - **Cline** now has a web-based Kanban/task-board direction, checkpoints, diagnostics, Plan/Act, and multi-agent team features.

4. **Features koder doesn't need to copy blindly**: messaging platform integrations, general voice input, browser control, and learning loops are high-effort, niche features. Koder should only adopt them where they support its browser-based, local-first agent workflow.
