# Knowledge v1 canonical fixtures

These JSON files are the shared canonical-record fixtures for the first Knowledge schema.
They are intentionally outside a Go package so persistence, HTTP/tool adapters, package
imports, and browser tests can consume exactly the same examples.

Rules:

- Files are canonical pretty-printed JSON with two-space indentation and a final newline.
- Go tests unmarshal, validate, and reproduce each file byte for byte.
- Consumers may add transport envelopes around these objects but must not reinterpret
  enum strings, IDs, revisions, or timestamps.
- A schema change updates the versioned directory or includes an explicit compatible
  migration; tests must not silently rewrite fixtures at runtime.
