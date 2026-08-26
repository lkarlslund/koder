# ADR 0002: Store memory in an independent replaceable database

## Status

Accepted on 2026-08-22.

## Context

Koder's existing `internal/store` persists sessions, chats, messages, milestones, tasks,
and related application state. Durable memory has different operational needs:

- its derived search and graph indexes must be independently rebuildable;
- imports and future embedding indexes can create different write and compaction patterns;
- operators may need to back up, restore, disable, or migrate memory independently;
- a memory failure must not make ordinary chats and sessions unavailable;
- the long-term storage engine is not yet known and must remain replaceable.

Using a namespace in Koder's main database would share its lock, recovery, compaction,
schema, and failure boundary. Depending directly on a graph or vector database would make
an early implementation choice part of the domain API.

## Decision

The first memory backend uses CockroachDB Pebble, which Koder already depends on, but
opens a distinct database at:

```text
<koder-state-dir>/memory-pebble-v1/
```

The directory is not a namespace inside Koder's existing `store-pebble-v7` database. It
has its own:

- Pebble handle and process lock;
- write-ahead log and compaction lifecycle;
- backend, schema, and derived-index metadata;
- open, health, checkpoint, restore, migration, and close operations;
- corruption and recovery boundary.

Memory domain and service packages depend on a narrow, domain-facing store interface.
The interface expresses transactions, canonical record operations, filtered pagination,
typed traversal, search/index operations, health, checkpoints, and migrations. It does not
expose Pebble keys, batches, iterators, options, or errors.

Pebble key encodings and secondary indexes remain private to the Pebble implementation.
Canonical domain objects, the versioned HTTP/tool contracts, and the `.kmemory`
interchange format—not Pebble records—are the compatibility boundary.

Derived indexes identify their schema and generation. They can be rebuilt from canonical
records and switched atomically. A future backend must pass the same store contract tests
before replacing Pebble.

## Availability and lifecycle

Memory is optional by default at process startup:

- If the memory store opens successfully, Koder starts its memory service and
  advertises the actions allowed by runtime policy.
- If it cannot open, ordinary session/chat storage and turn execution remain available.
  The memory tool is withheld or returns a structured unavailable error, the explorer
  shows the cause, and authorized health/debug surfaces report it.
- A deployment may explicitly configure memory as required, in which case failure to
  open it fails startup rather than silently weakening that deployment.

Koder owns exactly one memory-store lifecycle per process. Shutdown closes memory
independently of the main store and reports close errors without skipping the remaining
process cleanup.

## Alternatives considered

### Store memory in the main Koder database

Rejected because it couples unrelated locks, recovery, compaction, schema migrations,
backups, and failure handling. Namespace separation is not operational isolation.

### Adopt a graph database immediately

Rejected for the first implementation. The required graph operations are typed adjacency
lookups and bounded traversal, which can be represented by canonical records and derived
indexes. A general graph database adds deployment and compatibility commitments before
real workloads establish a need.

### Adopt a vector database immediately

Rejected because embeddings are optional derived retrieval data. Exact, lexical, and
graph retrieval must work without an embedding provider or vector service.

### Reuse the generic application-store interface

Rejected because the existing boundary models generic application collections, while the
memory service needs atomic graph invariants, revisions, traversal, index generations,
and backend-neutral migration operations. Adapting memory to generic records would
leak these invariants across the service layer.

## Consequences

- Koder initially operates two Pebble databases when memory is enabled.
- Configuration, startup, shutdown, health, backup, restore, and diagnostics must identify
  the memory store separately.
- Disk usage and compaction can be observed and tuned without conflating chat data.
- Main-store availability is not reduced by an optional memory failure.
- Canonical records and indexes need explicit transaction and generation semantics.
- Replacing Pebble later requires a new backend and migration, but not changes to domain
  identities, tools, APIs, or package files.
