# Knowledge

Koder Knowledge stores curated, reusable material separately from chat history. The
architectural domain and storage decisions are recorded in
[ADR 0001](decisions/0001-knowledge-domain.md) and
[ADR 0002](decisions/0002-knowledge-storage-boundary.md).

This document defines the vocabulary used by domain records, tools, APIs, packages, and
user interfaces. Wire values use lowercase `snake_case` and must not be given different
meanings in individual transports.

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
