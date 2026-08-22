# Knowledge

Koder Knowledge stores curated, reusable material separately from chat history. The
architectural domain and storage decisions are recorded in
[ADR 0001](decisions/0001-knowledge-domain.md) and
[ADR 0002](decisions/0002-knowledge-storage-boundary.md).
Backup, restore, and maintenance procedures live in
[Knowledge operations](knowledge-operations.md).

This document defines the vocabulary used by domain records, tools, APIs, packages, and
user interfaces. Wire values use lowercase `snake_case` and must not be given different
meanings in individual transports.

## Package ownership and import direction

Knowledge follows inward-only dependencies. Domain and service code must remain usable
without a browser, chat backend, tool registry, or Pebble database.

```text
cmd/koder and internal/app                 process wiring and lifecycle
              │
      ┌───────┼────────────────┐
      ▼       ▼                ▼
knowledgeapi  tools/knowledgetool  background curator
      └───────┼────────────────┘
              ▼
     knowledge/service                    policy and use cases
              │
      ┌───────┴────────┐
      ▼                ▼
knowledge/store   knowledge classifier    consumer-owned boundaries
      │                │
      ▼                ▼
store/pebble      local/remote adapters   replaceable implementations
store/memory
      └───────┬────────┘
              ▼
      internal/knowledge                  canonical domain values
```

Planned package responsibilities:

| Package | Owns | May import |
| --- | --- | --- |
| `internal/knowledge` | Canonical types, enums, validation, normalization, classification contracts | Standard library and general-purpose text/ID packages |
| `internal/knowledge/store` | Domain-facing transaction, query, cursor, health, checkpoint, and backend-neutral canonical migration interfaces | `internal/knowledge` |
| `internal/knowledge/store/memory` | Deterministic in-memory implementation for contracts and service tests | Domain and store contracts |
| `internal/knowledge/store/pebble` | Private keys, records, indexes, batches, generations, and Pebble lifecycle | Domain/store contracts and Pebble |
| `internal/knowledge/kpackage` | Deterministic, backend-neutral per-chunk `.kknowledge` serialization, integrity inventory, and signature envelopes | Canonical Knowledge domain only |
| `internal/knowledge/migrationarchive` | Deterministic whole-store canonical snapshot envelope for offline backend replacement | Canonical Knowledge and store contracts |
| `internal/knowledge/service` | Authorization, policy, normalization, classification, transactions, search, audit decisions, import/export orchestration | Domain and store/classifier contracts |
| `internal/tools/knowledgetool` | Model-facing multi-action schema and adaptation to service operations | Knowledge service and generic tool contracts |
| `internal/knowledgeapi` | Authenticated HTTP/event DTOs, limits, ETags, and error envelopes | Knowledge service and Web/API infrastructure |
| `internal/app` and `cmd/koder` | Configuration, construction, startup degradation, lifecycle, and dependency injection | Public constructors/interfaces from the layers above |
| `internal/webui/assets` | Knowledge explorer, graph renderer, and API client | HTTP/event contracts only; no Go package dependency |

The names may be shortened if Go package usage reads more naturally, but ownership and
dependency direction are fixed.

### Import rules

- `internal/knowledge` does not import store backends, the main Koder store, web/API code,
  tool packages, chat/agent packages, turn drivers, `internal/app`, or `cmd/koder`.
- Store interfaces do not expose Pebble types, keys, batches, iterators, options, or
  errors. Only the Pebble implementation imports `github.com/cockroachdb/pebble`.
- The service owns policy and transactions. HTTP handlers and model tools must not bypass
  it to read or write a backend directly.
- Koder-native and Codex turn drivers do not import knowledge storage. They receive the
  same runtime-filtered tool contract through existing tool integration.
- Browser code does not know the database shape. It consumes bounded, versioned HTTP and
  event DTOs and treats node/edge patches as projections.
- Configuration describes backend selection and paths but does not construct backends.
  Process wiring resolves configuration and owns open/close order.
- The background curator proposes service commands; it cannot write canonical records or
  indexes directly.
- Package import/export uses the service boundary for validation, classification,
  authorization, staging, and activation.

### Contract testing

Store behavior is specified once as a reusable contract suite and run unchanged against
memory and Pebble implementations. Service tests use the memory store and fake
classifier/clock/actor sources. Pebble-specific tests cover encoding, atomicity, restart,
locking, migration, and failure behavior without leaking those concerns upward.

API, tool, import, and browser tests share the canonical JSON fixtures under
`protocol/knowledge/v1/testdata`. Transport adapters may add envelopes, but canonical
objects and enum meanings remain owned by `internal/knowledge`.

## Identity, revisions, time, and cursors

### Stable identities

Canonical knowledge objects use Koder's existing UUIDv7 identifiers. JSON and tool
transports encode them as lowercase hyphenated strings, for example:

```text
01a01688-fc5d-7f7d-8bb8-de244977f8a1
```

Chunk, entry, link, evidence, revision, package-import, saved-view, and asynchronous-job
identities occupy distinct typed fields even though they share the same wire format.
Callers must not infer an object's type from an ID or use a display title as identity.

IDs are assigned once at creation and never change during rename, archive, restore,
supersession, package export/import, or index rebuild. An imported object retains its ID
only when the import policy establishes that it is the same object; “keep both” allocates
a new ID and records the source identity as provenance.

UUIDv7's timestamp component is useful for locality and deterministic tie-breaking, but
it is not a substitute for an object's explicit timestamps or authorization checks.

### Revisions and optimistic writes

Every mutable canonical object has:

- `revision`, an unsigned integer starting at `1` and increasing by exactly one for each
  committed mutation;
- `revision_id`, a stable UUIDv7 identifying the immutable revision record;
- `updated_at` and the actor/reason associated with that revision.

A transaction that mutates several objects advances each changed object's own revision.
No-op requests do not create revisions. Derived index updates, last-used counters, and
layout/view state do not silently revise canonical knowledge content.

API and tool writes provide the revision they observed. A mismatched revision returns a
structured `conflict` containing the object's current revision and safe identity metadata;
it never overwrites newer content. HTTP resources expose a strong ETag derived from object
ID and revision and accept `If-Match` as the transport equivalent.

Permanent deletion does not leave an addressable object at its former revision. If an
installation retains a minimal audit tombstone, it is inaccessible through ordinary
knowledge reads and contains no deleted content.

### Timestamps and validity

Go domain objects use `time.Time`. Durable and wire representations use RFC 3339 with
nanosecond precision, normalized to UTC with a trailing `Z`:

```text
2026-08-22T14:37:05.123456789Z
```

Inputs containing an explicit numeric offset are accepted and normalized to UTC. Inputs
without an offset are rejected rather than interpreted in the server's local timezone.
Persisted values do not contain Go's monotonic-clock component.

`created_at` is immutable. `updated_at` is the commit time of the current canonical
revision. Domain events produced by the same transaction carry that commit time and the
resulting revision.

Optional temporal fields have distinct meanings:

- `observed_at`: when evidence or an observation occurred;
- `valid_from` and `valid_until`: the interval in which the claim applies;
- `last_verified_at`: when the stated verification was performed;
- `review_after`: when retrieval must additionally mark the claim stale;
- `last_used_at`: a derived retrieval/use statistic that does not verify or revise content.

Intervals are start-inclusive and end-exclusive: `valid_from <= t < valid_until`. A
missing bound is open. `valid_until` must be later than `valid_from` when both exist.

### Stable pagination cursors

List and search cursors are opaque, URL-safe, versioned values. Clients store or return a
cursor but never parse, construct, compare, or persist it as an object identity.

A cursor binds:

- cursor format version and owning index;
- index generation;
- normalized filter/query fingerprint;
- sort field and direction;
- the last composite sort value and stable object ID.

Pagination is exclusive: the next page begins strictly after the cursor item in the
requested order. Every order ends with object ID as a deterministic tie-breaker, so equal
titles or timestamps neither duplicate nor skip records.

Changing filters, sort order, or direction while reusing a cursor returns `invalid_cursor`.
Using a cursor after its derived index generation has been retired returns `stale_cursor`
and instructs the caller to restart pagination. Authorization and lifecycle filters are
reapplied on every page; cursor contents never grant access.

Cursors may encode a private backend position internally, but the domain/API contract
must not expose raw Pebble keys. Cursor encoding can change by adding a new format version
while existing versions remain supported for their documented lifetime.

## Scope

Scope answers **where does this knowledge apply?** It does not grant access. An entry can
be visible to an actor yet excluded from a result because its scope does not match the
current task.

Scopes form a specificity order:

| Kind | Meaning | Example |
| --- | --- | --- |
| `global` | Generally applicable within its locale/domain constraints | How an HTTP status code is interpreted |
| `personal` | Applies to the identified person's preferences or circumstances | The user prefers Danish for calendar names |
| `project` | Applies to one project identity, normally anchored by its configured root | This repository uses `scripts/lint` |
| `session` | Applies to one durable Koder session and its work | The selected deployment target for a milestone |
| `environment` | Applies to a machine, device, container, service, or named environment | This Arch host has `sfdisk` but not `fdisk` |

A scope consists of a kind plus its selector, except `global`, whose selector is empty.
Selectors use stable Koder identities rather than display names. Records may add
applicability predicates such as operating system, architecture, software version, locale,
or validity dates.

Search prefers the most specific matching scope but does not assume that a specific claim
is more correct. Contradictory matching scopes are returned with a warning. A session
entry can override a global recommendation for that session without rewriting the global
entry.

Examples:

```json
{"kind":"global","selector":""}
{"kind":"project","selector":"project:01a02..."}
{"kind":"environment","selector":"host:workstation"}
```

Knowledge useful only inside one unfinished chat normally belongs in the transcript, not
in a new chat-scoped knowledge record. A durable outcome can instead use session, project,
or environment scope and cite the chat as evidence.

## Visibility

Visibility answers **who may discover or read this knowledge?** Authorization is applied
before search scoring, counts, graph traversal, and error details so hidden knowledge does
not leak through metadata.

| Value | Meaning | Example |
| --- | --- | --- |
| `private` | Visible only to its owning user and explicitly authorized local services | The built-in `personal/me` chunk |
| `installation` | Visible to authorized chats and users of this Koder installation | Locally verified developer tooling guidance |
| `shared` | Visible only to an explicit principal/group list | A team project convention |
| `public` | Eligible for deliberate public distribution | A signed general reference package |

`public` does not make an object anonymously available from Koder. Serving, exporting, or
publishing remains a separate authorized action. Imported chunks never gain broader
visibility than the importer explicitly grants.

The initial single-user implementation primarily uses `private` and `installation` while
preserving the vocabulary needed for future multi-user deployments.

Canonical store transactions are an internal persistence boundary and do not make access
decisions. Consumer-facing reads go through the Knowledge service, which authorizes the
owning chunk for direct chunk, entry, relationship, list, search, traversal, history, and
usage operations. Evidence is read through an authorized citing entry rather than by a
bare evidence ID, because one immutable evidence record can support chunks with different
visibility policies.

## Lifecycle

Lifecycle controls normal visibility and mutation. Permanent deletion is an operation,
not a retained lifecycle value.

### Chunks

| Value | Meaning |
| --- | --- |
| `draft` | Being assembled or reviewed; excluded from normal retrieval |
| `active` | Available to authorized retrieval and graph browsing |
| `archived` | Retained and inspectable but excluded unless explicitly requested |

### Entries

| Value | Meaning |
| --- | --- |
| `draft` | Not yet accepted for normal retrieval |
| `active` | Current and retrievable |
| `superseded` | Replaced by an identified entry or revision; visible in history and explicit searches |
| `archived` | Retained but excluded from normal retrieval |

### Links

Links are `active` or `archived`. Unlinking archives the relationship and records the
revision reason. A later retention action may permanently erase it.

`deleted` is never returned as a durable object state. After confirmation and dependency
checks, permanent deletion erases the object and its derived indexes. Counts therefore do
not include deleted objects.

## Risk

Risk is a set of classifications, because privacy sensitivity and harmful-advice risk can
coexist. An empty set means ordinary knowledge.

| Value | Meaning | Required handling |
| --- | --- | --- |
| `personal_sensitive` | Could reveal private personal attributes or behavior | Private by default; no implicit export or diagnostic logging |
| `medical` | Could influence health or treatment decisions | Locale, authoritative evidence, uncertainty, and review date |
| `legal` | Could influence legal rights, duties, or proceedings | Jurisdiction, authoritative evidence, uncertainty, and review date |
| `financial` | Could influence material financial decisions | Market/jurisdiction context, fresh evidence, uncertainty, and review date |
| `physical_safety` | Incorrect use could cause injury or property damage | Preconditions, warnings, verified procedure, and review date |
| `security_sensitive` | Could weaken or expose systems even without containing a secret | Narrow visibility/scope and explicit threat/applicability context |
| `prohibited_secret` | Credential, token, private key, recovery code, or equivalent secret | Reject before canonical storage and derived indexing |

`prohibited_secret` is a rejection classification and must never appear on a successfully
stored record. Risk classification raises requirements; it does not prove correctness.

Example: an entry about the interaction between a user's medication and an OTC product is
both `personal_sensitive` and `medical`.

## Verification

Verification describes the current support for a claim, independently from who originally
stated it.

| Value | Meaning |
| --- | --- |
| `unverified` | Recorded but not checked against adequate evidence |
| `partially_verified` | Some material claims or applicability conditions were checked |
| `verified` | The stated claim and applicability were checked against the required evidence policy |
| `disputed` | Credible evidence conflicts and the disagreement is unresolved |

Verification has an actor, timestamp, method, evidence IDs, and optional `review_after`.
Passing a review date does not rewrite the status; retrieval additionally marks the entry
`stale`. A verified but stale entry is not presented as currently verified without that
warning.

Verification is not confidence. Confidence may express a bounded assessment, while
verification records what was actually checked. Model fluency or repetition across model
outputs is not verification.

## Evidence

Evidence type identifies what can be inspected again:

| Type | Required identity | Example |
| --- | --- | --- |
| `user_statement` | user plus chat/turn or explicit-edit identity | “Always use the speaker unless Bluetooth is connected” |
| `chat_turn` | session, chat, and timeline item | The turn where a workaround succeeded |
| `tool_result` | chat, call ID, tool/action, and result hash | Output showing `sfdisk --version` |
| `file` | canonical file identity/path and content hash | Repository testing policy |
| `web` | URL, title, access time, and content hash or bounded excerpt | Current official command documentation |
| `package` | package identity, publisher, version, and file hash | Imported `.kknowledge` reference chunk |
| `observation` | actor, time, method, and observed-value hash | A successful operation on a named environment |

Evidence also carries a source-quality classification:

| Quality | Meaning |
| --- | --- |
| `primary` | Original specification, record, measurement, or direct observation |
| `authoritative` | Official or professionally authoritative source for the domain |
| `secondary` | Reputable analysis derived from primary sources |
| `anecdotal` | Informal report useful as a lead but insufficient for strong verification |
| `generated` | Model-generated or transformed material requiring independent support |

Evidence is immutable. Correcting its metadata creates a replacement evidence record and
a new owner revision. Evidence content is referenced by identity and bounded metadata;
Koder does not copy an entire web page or transcript into every knowledge entry.

## Portable packages

The normative `.kknowledge` v1 manifest schema, byte-level format rules, security
boundary, and canonical unpacked example live under
[`protocol/knowledge/package/v1`](../protocol/knowledge/package/v1/README.md). Package
metadata and signatures describe provenance; they never grant runtime authority or widen
the importer's visibility and scope policy.

Deployments may label known signing identities with a narrow trusted-publisher registry.
Keys are Base64-encoded Ed25519 public keys indexed by the key ID used in the package:

```toml
[[knowledge.trusted_publishers]]
id = "publisher:example"
name = "Example publisher"

[knowledge.trusted_publishers.keys]
"example:2026" = "BASE64_ED25519_PUBLIC_KEY"
```

The import preview distinguishes a verified registered publisher from unsigned,
unknown-key, and publisher/key-mismatch packages. “Trusted” means only that the package
signature matches this local identity binding. It does not bypass classification,
authorization, conflict review, import limits, or any tool/runtime policy.

Exporting a personal chunk is a separate, explicit action. The service rejects personal
package exports unless the caller opts in for that request, and the explorer explains
that the downloaded file leaves Koder's private store before asking for confirmation.
The Knowledge tool exposes the same opt-in only for a user-requested export in the
current conversation; an agent must not silently turn routine lookup into data export.

## Origin of personal knowledge

Personal entries additionally record how they arose:

| Value | Meaning |
| --- | --- |
| `explicit` | The user directly stated, confirmed, or edited it |
| `observed` | An authorized tool produced the observation |
| `inferred` | Koder inferred it and the user has not confirmed it |

Sensitive inferred attributes are not persisted automatically. Origin does not replace
evidence or verification: an explicit preference can be well evidenced by the user's
statement while still being time-limited or later superseded.

Every entry in the built-in `personal/me` chunk retains that exact personal scope and
must declare one of these origins. An inferred entry carrying personal-sensitive,
medical, legal, financial, physical-safety, or security-sensitive risk can exist only as
a reviewable draft until it is replaced or explicitly confirmed; canonical validation
enforces this even when a pluggable classifier returns `allow`.

The explorer presents `personal/me` as a dedicated editor: its scope is locked, origin
is required in the main form rather than hidden as advanced metadata, and observed or
inferred choices explain their evidence and review consequences.

## Background curation contracts

Curation starts from a sealed user/assistant turn boundary, identified by session, chat,
user-item, and assistant-item IDs plus the assistant seal time. Cheap provider-independent
signals schedule inspection; they do not authorize a knowledge write:

| Signal | Meaning |
| --- | --- |
| `failed_then_succeeded` | A failed approach was followed by a successful fallback |
| `researched_then_succeeded` | Research was followed by a successful outcome |
| `user_correction` | The user corrected a claim or instruction |
| `repeated_workaround` | A workaround appears durable enough to compare across turns |
| `contradicting_evidence` | New evidence may contradict existing knowledge |
| `explicit_personal_preference` | The user directly stated a durable preference |

The queue record contains only turn/item references, signal kinds, confidence used for
scheduling, lifecycle, safe counts, and a machine error class. It does not copy transcript
text or provider error details. Its lifecycle is `queued` -> `processing` -> one of
`no_candidates`, `candidates_ready`, or `failed`. Candidate extraction and subsequent
policy checks remain separate stages; signal confidence is never permission to persist.

The curation queue depends on a small `Extractor` interface, not on a model or provider.
Submission is idempotent by the complete turn identity, and the queue store atomically
claims a record so concurrent workers cannot inspect it twice. Extractors return only a
safe candidate count to the queue; candidate payload storage remains behind the extractor
boundary. Failures retain a bounded machine code, never the provider error text.

Optional model drafting sits behind that extractor interface. The adapter receives only
the requested, bounded turn items after local secret findings are replaced with
`[REDACTED]`. Every request includes a strict JSON Schema, and Koder independently decodes
with unknown-field rejection, validates action/target shape, UUIDs, source references,
scope, personal origin, uncertainty, risk, timestamps, Markdown, and size limits, then
classifies the draft again. The candidate sink is called only after the entire response
passes; malformed, fabricated, or prohibited output produces no partial candidate writes.

Before storage, normalized entry content is fingerprinted and compared with authorized
active and superseded entries in the target chunk. Exact no-ops and duplicates within the
same model batch are suppressed; a mutation draft must also name an existing active or
superseded target in that chunk. Action, prose reason, and model confidence cannot make
identical content appear new. The sink reports the post-deduplication count back to the
queue, so a fully duplicated batch becomes `no_candidates`.

Automatic application is deliberately narrower than drafting. Only `allow`-classified
candidates with no risk labels and a create/update action can enter the low-risk applier.
The service atomically writes immutable completed-turn evidence and the verified entry
revision, pins updates to the revision observed during deduplication, and includes the
curation record ID in the revision reason. Any validation, policy, conflict, or persistence
failure rolls both writes back. Supersession, contradiction, review-classified content,
and risk-labelled content remain outside this automatic path.

Candidate routing is a server-owned decision applied after model validation. A candidate
is stored as `pending_review` when classification requests review, any risk label is
present, personal knowledge is inferred, or the action is supersession/contradiction.
Each safe reason is retained for the curator UI. Only low-risk create/update drafts are
marked `automatic`; model output cannot set or override the route, and the low-risk adapter
rejects anything not explicitly routed there.

## Operational health

The authenticated `GET /api/knowledge/v1/status` response reports the backend lifecycle,
schema version and state, canonical index generation and state, rebuild progress, semantic
index status when configured, and the mutation checkpoint used by live explorer clients.
An open validated schema reports `current`; canonical indexes report `ready`, `rebuilding`,
`stale`, `error`, or `unavailable` from runtime state rather than from UI inference.

Backends may also implement the sanitized operational-details contract. Pebble reports
physical/live/reclaimable storage bytes, memory and WAL bytes, table count, compaction
state/count/debt, read amplification, write amplification, and maximum level score. These
are runtime observations, so consumers should display them as a current snapshot rather
than durable Knowledge. The response never includes the database path, keys, record text,
queries, or raw backend errors. If metric collection fails, `storage_state` becomes `error`
while the rest of the operational status remains available.

## Combined examples

### Environment-specific procedure

> Use `sfdisk` for scripted partition-table changes on workstation A; `fdisk` is not
> installed.

- scope: `environment`, selector `host:workstation-a`
- visibility: `private`
- lifecycle: `active`
- risk: `physical_safety`
- verification: `verified`
- evidence: successful `tool_result`, direct `observation`, and authoritative manual
- applicability: Linux distribution/version and tool versions
- review: required after a material OS/tool change

### General reference knowledge

> The project's focused Go tests should run from the real repository root.

- scope: `project`
- visibility: `installation`
- lifecycle: `active`
- risk: none
- verification: `verified`
- evidence: repository `file` with content hash

### Personal preference

> Prefer Bluetooth audio when it is connected.

- scope: `personal`
- visibility: `private`
- lifecycle: `active`
- risk: `personal_sensitive`
- verification: `verified`
- evidence: `user_statement`
- origin: `explicit`
