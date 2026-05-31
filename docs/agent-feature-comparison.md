# Coding Agent Feature Comparison

Feature matrix across major coding agents and koder's current implementation.

Legend: ✅ = native support, ⚠️ = partial/limited, ❌ = missing, — = not applicable

## Core Features

| Feature  | Koder | Aider | Cline | OpenCode | Gemini CLI | Goose | OpenHands | Codex | Hermes | Continue | Tabby | PR-Agent |
|----------------------------------|:-----:|:-----:|:-----:|:--------:|:----------:|:-----:|:---------:|:-----:|:------:|:--------:|:-----:|:--------:|
| **Terminal UI**  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ✅  |
| **Browser / Web UI**  | ✅  | ❌  | ❌  | ⚠️  | ❌  | ❌  | ✅  | ⚠️  | ⚠️  | ❌  | ❌  | ❌  |
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
| **PR review (GitHub/GitLab)**  | ❌  | ❌  | ❌  | ❌  | ✅ Action  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ✅  |
| **CI / GitHub Action**  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ✅  |
| **Headless / CI mode**  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ✅  |

## Planning & Orchestration

| Feature  | Koder | Aider | Cline | OpenCode | Gemini CLI | Goose | OpenHands | Codex | Hermes | Continue | Tabby | PR-Agent |
|----------------------------------|:-----:|:-----:|:-----:|:--------:|:----------:|:-----:|:---------:|:-----:|:------:|:--------:|:-----:|:--------:|
| **Milestone / todo planning**  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  |
| **Background sub-agents**  | ✅  | ❌  | ❌  | ✅  | ❌  | ❌  | ✅  | ✅  | ✅  | ❌  | ❌  | ❌  |
| **Multi-agent teams**  | ❌  | ❌  | ✅  | ⚠️  | ❌  | ❌  | ✅  | ❌  | ✅  | ❌  | ❌  | ❌  |
| **Scheduled / cron tasks**  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |
| **Skills system**  | ✅  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  |
| **Project rules (AGENTS.md)**  | ✅  | ❌  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ❌  |
| **Queue / steer while running**  | ✅  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  |

## Extensibility & Platforms

| Feature  | Koder | Aider | Cline | OpenCode | Gemini CLI | Goose | OpenHands | Codex | Hermes | Continue | Tabby | PR-Agent |
|----------------------------------|:-----:|:-----:|:-----:|:--------:|:----------:|:-----:|:---------:|:-----:|:------:|:--------:|:-----:|:--------:|
| **Messaging (Slack/Telegram…)**  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |
| **Voice input / TTS**  | ❌  | ✅  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |
| **Browser / desktop control**  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ⚠️  | ❌  | ⚠️  | ❌  | ❌  | ❌  |
| **SDK / programmatic API**  | ❌  | ❌  | ✅  | ❌  | ⚠️  | ✅  | ✅  | ✅  | ❌  | ❌  | ✅  | ❌  |
| **Plugin system**  | ❌  | ❌  | ✅  | ✅  | ❌  | ✅  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  |
| **Self-hosted / on-prem**  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ✅  | ❌  | ✅  | ✅  | ✅  | ✅  |
| **Learning loop / memory**  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ❌  | ✅  | ❌  | ❌  | ❌  |

## Key Observations

1. **Koder's unique strengths**: milestone/todo planning, queue/steer while running, shell sandboxing, background sub-agents, permission profiles, and the browser UI. No other agent combines all of these.

2. **Biggest gaps vs. competition**:
   - **Repo map / codebase index** — Only Aider and Tabby have this. This is the single biggest gap for large codebase support.
   - **Auto-commit** — Aider, Cline, OpenCode, and OpenHands all auto-commit with AI-generated messages. Koder has no structured git commit tool.
   - **Post-edit diagnostics** — Hermes and Cline automatically feed lint/compiler errors back after every edit. Koder has the infrastructure (`codediag.CheckEdit`) but it's not wired into the edit loop automatically.
   - **Headless/CI mode** — Almost every competitor supports non-interactive, scripted execution. Koder is browser-only.
   - **PR review / CI integration** — Continue, PR-Agent, and Gemini CLI offer automated PR review as a core feature.

3. **Features koder doesn't need to copy**: messaging platform integrations, voice, browser control, and learning loops are high-effort, niche features that don't align with koder's browser-based, local-first positioning.