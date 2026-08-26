# Using Koder Memory

Koder Memory is the durable library Koder can reuse across chats. It is intentionally
separate from conversation history: a transcript records what happened, while Memory
holds the conclusions, procedures, preferences, and warnings that remain useful later.

Open **Memory** from Koder's navigation, or visit `/memory` on your Koder server.
The explorer has three areas:

- **Browse** finds and filters memory chunks;
- **Graph** shows the selected chunk or entry and its relationships;
- **Inspector** shows content, evidence, warnings, revision history, and available actions.

On a narrow screen, these areas become tabs. Selecting an item moves to its inspector.
The accessible table button above the graph provides the same objects and actions without
requiring the canvas.

## Chunks, entries, and relationships

A **chunk** is a maintained collection such as “Linux storage tools”, “About me”, or
“.NET practices”. An **entry** is one reusable fact, procedure, preference, decision, or
warning inside a chunk. A **relationship** explains how two objects connect—for example,
an entry can be part of a chunk, support another entry, supersede an older instruction,
or contradict another claim.

Prefer one clear subject per entry. Put supporting detail in its Markdown body, not in a
collection of near-duplicate entries. Choose the narrowest correct scope:

- **Global** for generally applicable reference material;
- **Personal** for the user's preferences or circumstances;
- **Project** for one repository or project;
- **Session** for one durable Koder session;
- **Environment** for a particular host, device, or service.

Scope says where information applies; visibility says who may read it. A specific scope
does not automatically make a claim more trustworthy.

## Find and use memory

Type ordinary words in **Search memory**. You can narrow results by chunk type, scope,
lifecycle, or tag. Search returns current active memory by default; choose an archived
lifecycle only when looking for retired material. Results are bounded, so refine the query
instead of expecting the whole store in one response.

Select a chunk or entry to draw its neighborhood. Use the incoming and outgoing expansion
buttons to load more relationships. The graph is a bounded view, not the entire database;
**Partial graph** means the server stopped at its safety limit and you should expand a
specific object. Local graph actions such as hide, isolate, pin, and undo change only your
saved view, not canonical memory.

The inspector can send the selected object to the chat from which the explorer was opened.
This sends a stable reference, and the chat retrieves the current authorized content; it
does not paste a stale screenshot of the inspector.

Chats with the Memory tool can do the same work conversationally. Useful requests are:

```text
Check Memory for the partitioning workaround we learned on this workstation.
Save the successful sfdisk procedure as environment-specific memory with its evidence.
What in Memory contradicts this instruction, and which version is newer?
Remember that I prefer Danish calendar names. This is an explicit personal preference.
```

Koder should search Memory before repeating web research when an existing durable lesson
may apply. Search results marked stale, disputed, scope-mismatched, or unverified are leads,
not permission to state them as current fact.

## Create and edit

Use the add action in **Browse** to create a chunk. Give it a descriptive title, a short
purpose, useful tags, and the correct scope and visibility. With a chunk selected, use
**New entry** in the inspector. A good entry includes:

- a title that names the conclusion;
- a one-sentence summary suitable for search results;
- a self-contained Markdown body;
- applicability such as locale, platform, or software version when relevant;
- uncertainty, risk, review date, and inspectable evidence where required.

Use **Edit** when the same underlying claim remains valid and its wording or applicability
needs correction. Writes are revision-checked. If somebody changed the object after you
opened it, Koder reports a conflict rather than overwriting their revision; reload and
reapply the intended edit.

Use a relationship when the distinction itself matters. Use **Supersede** when a new entry
materially replaces an old one. Keep simultaneous conflicting claims and connect them with
a contradiction relationship, including the different scope or evidence, instead of
silently deleting one side.

## Provenance, verification, and history

The inspector shows where a claim came from, its verification state, risk labels,
applicability, and when it becomes due for review. Evidence can point to a user statement,
chat turn, tool result, file, web source, imported package, or direct observation. Evidence
is inspectable and immutable; changing evidence creates a replacement rather than rewriting
what an older revision relied on.

Verification means that the stated claim was actually checked against its evidence policy:

- **Unverified** was recorded but not adequately checked;
- **Partially verified** has material parts still unchecked;
- **Verified** met its stated evidence policy at verification time;
- **Disputed** has credible unresolved conflicting evidence.

A passed review date adds a stale warning even when the stored verification label is
verified. Repetition by an AI or frequent retrieval is not verification.

Open **History** in the inspector to see immutable revisions, who or what made them, and the
reason. Load older revisions on demand. History is the first place to inspect an unexpected
change; do not create a duplicate entry merely to preserve old wording.

## Archive, restore, and delete

Archive memory that is no longer normally useful but may be needed for audit, recovery,
or context. Archived objects disappear from ordinary search, remain available through the
archived filter, retain their history, and can be restored.

Deletion is deliberately a second step:

1. Archive the chunk or entry.
2. Inspect the dependency and impact summary.
3. Choose **Delete** and confirm the named object and consequences.

Delete means erase, not another hidden state. Koder blocks unsafe deletions and explains
active entries, relationships, or evidence that must be handled first. The browser deletes
only an empty, dependency-free archived chunk; resolve the listed objects and retry. An
authorized tool workflow can request a separately confirmed atomic cascade, but should do
so only after the user deliberately chooses that wider erasure. Once deletion succeeds,
ordinary Memory APIs cannot recover the object; restore a prior store backup if the
erasure was a mistake.

Relationships use the same idea: unlinking archives the relationship so its revision
history remains inspectable, and restoring makes it active again.

## Import and export packages

A `.kmemory` package moves one chunk and its referenced records between installations.
To import, choose the package button in **Browse**, select the file, and follow three
separate stages:

1. **Preview** validates format, limits, hashes, dependencies, signature identity,
   classification findings, and conflicts without writing anything.
2. **Stage package** records the reviewed import plan. Choose `merge`, `replace`, or
   `keep both` deliberately when conflicts exist, and acknowledge sensitive findings when
   requested.
3. **Activate import** atomically makes that exact stage visible. A failed activation
   exposes no partial package.

A valid signature proves that the bytes match a configured publisher key. It does not make
the content correct, grant it tools, widen visibility, or bypass local review policy.

Select a chunk and use **Export** to create a portable package. Personal chunks require a
separate explicit acknowledgement because the file leaves Koder's private store. Store and
send packages according to their content sensitivity.

Packages merge content. They are not a complete installation backup and do not replace the
backup or migration procedures below.

## Personal memory and privacy

The built-in **About me** chunk is private, personal-scoped memory. Its editor requires
an origin:

- **Explicit** means the user directly stated or confirmed it;
- **Observed** means an authorized tool produced the observation;
- **Inferred** means Koder derived it and the user has not confirmed it.

Koder does not treat casual conversation as permission to build a profile. Sensitive
inferences remain reviewable drafts and cannot silently become active personal memory.
Credentials, tokens, private keys, recovery codes, and equivalent secrets are rejected
before storage and indexing. Do not use Memory as a password manager.

Authorization is applied before search scoring, counts, and graph traversal. An actor that
cannot read a private entry also cannot infer it from result counts, graph degree, errors,
or diagnostics. Exporting personal memory is never implied by viewing or searching it.

## Backup and recovery

Memory lives in its own Pebble store, separate from sessions and chats. A normal
chat/session backup is therefore not a Memory backup. Before upgrades or important bulk
edits, create an offline validated Memory checkpoint. If an edit or deletion must be
undone, restore that checkpoint; successful restore retains the replaced database as a
rollback directory.

Use a backend-neutral migration archive when moving all Memory into a new data directory
or future storage backend. It contains canonical content and revision histories, while the
target rebuilds derived indexes. Import requires an empty target and is atomic.

Exact commands, lock handling, personal-data acknowledgements, rollback behavior, and
migration steps are in [Memory operations](memory-operations.md). Do not copy live
Pebble files or rename its directories manually.

If the explorer says Memory is unavailable, ordinary sessions and chats should still
work. Check the authenticated Memory status or the sanitized `/debug/memory` endpoint,
then follow the integrity, rebuild, backup, or restore procedure appropriate to the reported
state. Operational details are documented in [Memory operations](memory-operations.md)
and [the debug API](debug-api.md).

## Further reference

- [Memory domain and policy reference](memory.md)
- [Memory operations](memory-operations.md)
- [Performance limits](memory-performance.md)
- [Portable package format](../protocol/memory/package/v1/README.md)
