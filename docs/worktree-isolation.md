# Isolated child chats

Koder normally shares a checkout between chats. For independent parallel edits,
start a child with `chats`, `action: "start"`, and `isolation: "worktree"`.
The web chat creator also offers **Separate worktree and branch**.

```json
{
  "action": "start",
  "profile": "execution",
  "objective": "Implement the parser changes and report the branch for integration",
  "isolation": "worktree"
}
```

Each isolated child gets a checkout under the configured Koder state directory's
`worktrees/<chat-id>` and a branch named `koder/<chat-id>`. It starts at the
parent checkout's exact HEAD, including when the parent itself is isolated.
A session rooted in a repository subdirectory keeps that relative working
directory in the new checkout. Parent uncommitted and untracked files are not
copied. Non-Git directories and repositories without a commit return an error.

The parent branch is never switched. Koder tools, Codex, Claude Code, prompts,
file suggestions, and the file browser use the selected chat's checkout.
Children started without isolation share their parent's checkout, including
when that parent is isolated. This is checkout isolation, not a security
boundary between mutually untrusted agents.

## Ownership and restart

Chat records persist the path, working directory, source directory, branch,
base commit, and shared Git directory. An atomic, versioned ownership record
alongside the checkout is written before Git starts creating it. This makes
interrupted creations discoverable even if no chat was persisted.

Creation finishes before the child is allowed to run. In-process creation and
cleanup use leases and a session lock. Restoring a chat holds the same lock
through the metadata update, so cleanup cannot race with restoration.

A service restart or cancellation retains the checkout and its files. Chat
forks and shared descendants retain their checkout association. Background
execution uses the same directory after resuming. Missing checkouts never
fall back to the source folder.

## Completion and integration

Completion and archive retain worktrees. Automatic child reports include the
path, branch, and base commit; `chats action=list` includes them too.

The parent reviews and integrates work explicitly:

1. Have the child commit its intended changes on the reported branch.
2. Stop concurrent writers in the parent checkout.
3. Review the diff and run the project's applicable verification.
4. Merge or cherry-pick the intended commits using the normal Git workflow.
5. Archive all chats sharing the child checkout, then clean it up.

Koder does not automatically commit, merge, resolve conflicts, or discard
files. A merge conflict leaves both branches and their files available for
normal Git resolution. Cleanup's ancestry check recognizes merges and
fast-forwards. A cherry-pick or squash does not make the original commits
ancestors of the parent; cleanup deliberately retains that checkout until
the original branch history is reconciled or an explicit manual Git workflow
handles it. It does not infer equivalence from similar patches.

## Inspecting and cleaning up

Use the **Manage worktrees** button next to the Git sidebar heading, or:

```json
{"action":"worktrees","worktree_action":"list"}
```

To remove one checkout:

```json
{"action":"worktrees","worktree_action":"cleanup","chat_id":"<owning-chat-id>"}
```

Management lists all records belonging to the current session, including
interrupted and failed creations. It reports local files, unmerged commits,
and diagnostic errors.

Cleanup refuses to remove a checkout if:

- Creation or attachment of a chat is still in progress.
- A chat using it is visible, busy, queued, or waiting for approval.
- A tracked background exec session still uses it.
- Another loaded session has a visible or busy chat using that directory.
- Another retained managed worktree depends on it as its integration target.
- There are tracked changes, untracked files, or ignored local files.
- Its HEAD is not an ancestor of the parent checkout's current HEAD.
- Its repository or branch no longer matches the ownership record.

Checks fail closed. Nested isolated children must be integrated and cleaned up
before their parent's checkout. Git removal does not use `--force`. A failed
removal keeps its record and error so cleanup can be retried after resolving
the blocker. Cleanup is idempotent and retains the branch.

## Restore, deletion, and recovery

Restoring an archived chat whose checkout was deliberately cleaned up recreates
the checkout from its retained branch, at the same location. Shared descendants
use that same restored checkout.

Unexpectedly missing or moved checkouts produce an error rather than a silent
recreation, because their uncommitted files may need recovery. Use the recorded
paths and normal `git worktree list` / `git worktree repair` workflow to repair
a moved checkout. Branches preserve committed work; Git cannot recover missing
uncommitted files merely by recreating a checkout.

Chat deletion requires cleanup of its owned worktree first. Session deletion
also requires cleanup of retained or incomplete worktrees, so deletion cannot
erase the only management handle for unfinished work. An interrupted creation
that never registered a checkout can be cleaned up explicitly; any branch that
already exists is retained.

There is no age-based deletion of retained work. Small ownership records and
retained branches remain after checkout cleanup. They preserve restore and
diagnostic information; they do not retain the checkout's copied files.

## Research references

The implementation was compared with the local research snapshots:

- Claude Code: optional per-agent worktrees, retained changes, and explicit
  integration in `tools/AgentTool/AgentTool.tsx` and `utils/worktree.ts`.
- Codex: the updated `codex-rs/worktree` crate's ownership metadata,
  source-relative working directories, and refusal to delete ignored files.
- OpenCode: explicit ready/failed lifecycle states and managed-worktree APIs.
- oh-my-opencode: team worktree creation and orphan discovery. Its forceful
  removal behavior was not adopted.

The Codex research checkout was updated. Claude Code's remote returned HTTP
403; the OpenCode and oh-my-opencode checkouts had local changes and were
inspected without modifying them.
