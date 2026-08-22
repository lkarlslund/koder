# `.kknowledge` package format v1

`.kknowledge` is Koder's portable, data-only Knowledge package. Version 1 is a ZIP
container with this root layout:

```text
manifest.json
entries/<entry UUID>.md
edges.jsonl
sources.jsonl
assets/<optional payload files>
```

The normative manifest schema is [`manifest.schema.json`](manifest.schema.json). The
unpacked [`examples/linux-partition-tools`](examples/linux-partition-tools) directory is
the canonical v1 example. A packed example is deliberately not committed: deterministic
ZIP production, including timestamps and entry order, belongs to the exporter contract.

## Safety boundary

A package contains knowledge data, never authority or executable behavior. Importing one
must not install code or skills, alter prompts, invoke tools, execute markup, or widen the
importing user's permissions. Package-provided scope and visibility are suggestions; the
import service applies the importing user's policy and may narrow or reject them.

Importers treat every byte as untrusted and enforce resource limits before parsing. ZIP
entries must be regular files with relative UTF-8 names. Absolute paths, `..` components,
backslashes, links, devices, duplicate normalized paths, encrypted entries, nested
archives, and undeclared files are invalid. HTML in Markdown is rendered only after the
same sanitization used for locally authored Knowledge.

## Canonical bytes

- Text files are UTF-8 without a byte-order mark and use LF line endings.
- `manifest.json` is two-space-indented JSON with object keys in lexical order and one
  final newline.
- JSON Lines records are compact JSON with lexical object-key order; every record,
  including the last, ends in LF. An empty JSON Lines file is zero bytes.
- Entry paths are `entries/<lowercase UUIDv7>.md`. Asset paths begin with `assets/`.
- `files` contains every payload file except `manifest.json`, sorted bytewise by `path`.
- `sha256` is the lowercase hexadecimal SHA-256 of the exact uncompressed file bytes.
- ZIP entries are ordered `manifest.json`, then payload paths in bytewise order. Exporters
  encode the package `created_at` instant in the ZIP's UTC DOS timestamp, rounded down to
  its two-second resolution, and do not add platform extras.

These rules make the logical package deterministic. KG-1102 defines and tests the exact
ZIP encoding used by Koder's exporter.

## Entry Markdown

An entry is Markdown with restricted JSON front matter. The first line is `---`, followed
by one JSON object, followed by another `---` line. The remaining bytes are the entry
body. JSON is used instead of general YAML so aliases, tags, custom types, and implicit
scalar coercion do not enter the parser.

The front matter contains the canonical `Entry` fields except `body`. Imported revisions
are source provenance: activation creates an authorized local revision rather than
trusting a package actor as a local actor. The front matter's `chunk_id` must equal
`manifest.chunk.id`, its `id` must match the filename, and every evidence reference must
resolve to `sources.jsonl`.

## Relationships and evidence

Each non-empty line in `edges.jsonl` is one canonical `Link` JSON object. Link endpoints
must resolve to the manifest chunk, an entry in this package, or an explicitly declared
dependency. Each non-empty line in `sources.jsonl` is one canonical immutable `Evidence`
object. IDs are unique across their own object kinds. Records are sorted by ID.

## Manifest and signatures

`format` is exactly `koder.knowledge.package`, and `schema_version` is `1`. `package.id`
identifies one package lineage; `package.version` changes when its content changes.
`chunk` describes the portable chunk, while `content` declares record locations and
counts. `files` is the complete integrity inventory.

The optional Ed25519 `signature` does not grant trust. It authenticates bytes from a
publisher key; policy still decides whether that publisher and content may be imported.
The signature input is the canonical `manifest.json` object with the `signature` member
omitted. Because that object contains all payload hashes, the signature covers the whole
package. KG-1103 implements signing and verification.

## Compatibility

Readers reject an unknown `schema_version`, an unsupported `min_koder_version`, unknown
required features, or unknown manifest members. Optional future behavior is advertised in
`features.optional`; unsupported optional features may be ignored only when their files
and semantics remain safe to preserve. Required and optional feature names are unique and
sorted.
