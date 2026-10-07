# Memory

Koder keeps notes the model carries between chats as markdown files.

## Where memories live

- **Global memory** — `memory/` beside the config file (usually
  `~/.config/koder/memory/`): the user, their machine, and preferences that
  apply in every project.
- **Project memory** — `memory/projects/<project>-<hash>/` in the state
  directory: one project's private notes. It is kept outside the project so it
  is never committed; team-wide conventions belong in `AGENTS.md` instead.

Each memory is one file, `<name>.md`, with the description in front matter:

```markdown
---
description: User wants terse answers without closing summaries
---
They read the diff themselves. Applies to every reply.
```

Files can be edited by hand. A file without front matter is read as content,
with its first line as the description.

## How the model uses memory

Every chat's instructions include the memory index: each memory's name and
description, per scope, and how full the scope is. The index is read once per
chat and then kept, so memories written during a chat do not change its
instructions and the provider's prompt cache stays valid.

The `memory` tool reads and changes memories:

| action | does |
|---|---|
| `list` | shows the current index |
| `get` | reads a memory in full |
| `create` | adds a memory (scope, name, description, content) |
| `update` | replaces a memory's description, content or both |
| `delete` | removes a memory |

`get`, `update` and `delete` find a name in either scope when none is given.

## Limits

A scope holds at most 100 memories; creating more fails until memories are
updated or deleted. Descriptions are one line under 200 characters, content
under 8 KB. Writes that look like credentials (private keys, tokens,
`password: …`) are refused.

Settings → Memory lists, edits, creates and deletes memories for the current
session's project and globally.
