# ADR 0001: Model durable memory as chunks, entries, links, and evidence

## Status

Accepted on 2026-08-22.

## Context

Koder already preserves chat transcripts, tool results, sessions, project state, and
skills. Those records explain what happened, but they do not provide a reliable way to
reuse a durable lesson in a later chat. Searching old conversation text also cannot
reliably distinguish a verified procedure from an abandoned attempt, a current fact from
a stale one, or a user statement from a model inference.

Koder needs durable memory that can be searched, corrected, linked, inspected, and
transported without treating all prior conversation text as trusted model context.

## Decision

Koder has a first-class **Memory** domain, separate from chat history and skills. Its
user-facing objects are:

- A **memory chunk** is a coherent, portable collection with shared metadata and
  policy. Examples include “About me”, “OTC medicine in Denmark”, and “Modern .NET
  development”. A chunk is a container, not an arbitrary text-search segment.
- A **memory entry** is one reusable fact, procedure, concept, warning, preference,
  decision, or reference in a chunk. Entries carry applicability, lifecycle, temporal,
  verification, and provenance metadata.
- A **memory link** is a typed, directed relationship between entries or chunks. Links
  express relationships such as `part_of`, `requires`, `alternative_to`, `supersedes`,
  and `contradicts` without copying the related content.
- **Evidence** is an immutable reference supporting an entry revision or link. Evidence
  can identify a user statement, chat turn, tool result, file and content hash, web source,
  imported package, or verified observation.

Mutable chunks, entries, and links have revision history. Corrections normally create a
new revision or explicitly supersede an earlier claim. Evidence remains attached to the
revision it supported.

Retrieval may derive internal fragments and indexes from entries. These derived records
are rebuildable implementation details and are not called memory chunks in user-facing
APIs or interfaces.

## Boundaries

### Chat history

Chat history is the episodic record of a conversation. Memory is curated, reusable
material with explicit applicability and provenance. A memory entry may cite a chat
turn, but Koder does not silently promote every transcript message into memory.

### Skills

Memory describes what is known. Skills describe executable workflows and tool-use
instructions. They may refer to one another, but importing memory cannot install code,
modify prompts, enable tools, or change execution policy.

### Model context and retrieval indexes

Memory is not injected wholesale into every prompt. A chat searches for a small,
authorized set of relevant entries and fetches their details on demand. Lexical indexes,
embeddings, ranking statistics, and graph projections are derived from canonical domain
records and are never the source of truth.

## Non-goals

This decision does not:

- choose a physical database, key layout, graph database, or vector database;
- define the complete retrieval or ranking algorithm;
- make model-generated claims authoritative without evidence and policy checks;
- replace chat transcripts, project files, tasks, milestones, or skills;
- turn imported text into executable instructions;
- require embeddings or an external service;
- define a general-purpose graph query language;
- automatically share private or personal memory between users or deployments.

## Consequences

- Memory services and tools use the domain vocabulary consistently.
- User interfaces can expose provenance, revisions, contradictions, and lifecycle instead
  of showing an opaque similarity result.
- Retrieval and storage implementations can change without changing the user-visible
  meaning of chunks and entries.
- Writers must provide enough scope and evidence for a durable claim; ordinary turn
  narration remains in the transcript.
- Personal, inferred, stale, contradictory, and high-risk memory can receive explicit
  policy instead of relying on prompt wording alone.
