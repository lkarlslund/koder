# Roadmap

## Current milestone

Ship a focused local-first browser coding agent with:

- A single local daemon serving the in-browser application
- Session-scoped project roots, chats, milestones, todos, approvals, and compaction
- OpenAI-compatible local or remote model providers configured from the browser UI
- Structured workspace tools for search, reads, edits, shell execution, web/image access, skills, and MCP
- Browser-first session, provider, permission, and workspace controls

## Next milestones

1. Simplify app/session/chat ownership so live in-memory state is the only mutation surface.
2. Tighten browser RPC and state deltas around explicit app APIs.
3. Improve multi-chat orchestration, archiving, and completion notifications.
4. Expand provider/model diagnostics and compaction visibility in the browser UI.
5. Keep command-line surface focused on launching, resuming, debugging, and non-interactive exec flows.
