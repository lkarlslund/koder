# Memory performance and enforced bounds

Koder bounds result size and graph work independently from the total durable corpus. The
current production envelope has been measured through 100,000 active entries in the Pebble
backend; it is not inferred from a small fixture.

## Reproducing the measurement

Run the checked-in scale benchmark from the repository root:

```sh
go test ./internal/memory/service -run '^$' \
  -bench '^BenchmarkMemorySearchScale' -benchtime=3x -count=1
```

The fixture creates a fresh Pebble store for each scale, puts every entry in one global
reference chunk, gives one entry a unique search term, and links that root to 20 entries.
Setup is excluded from the measured interval. `lexical` measures one-hit retrieval;
`graph_expansion` performs the same retrieval plus the default bounded one-hop expansion.

These results were recorded on 2026-08-23 with Go on Linux/amd64 and an AMD Ryzen AI Max+
395. Each row is the mean reported by a three-iteration benchmark run:

| Active entries | Lexical | Lexical allocation | With graph expansion | Expansion allocation |
| ---: | ---: | ---: | ---: | ---: |
| 1,000 | 4.78 ms | 6.91 MB | 5.10 ms | 7.12 MB |
| 10,000 | 54.27 ms | 79.38 MB | 54.34 ms | 79.60 MB |
| 100,000 | 504.93 ms | 841.50 MB | 512.62 ms | 841.74 MB |

The first measurement exposed quadratic corpus paging: 10,000 entries took about 2.7
seconds and allocated about 4.3 GB per query. Pebble now offers a single-snapshot filtered
entry scan to corpus consumers, making the measured curve approximately linear. The
100,000-entry result is the verified envelope for this release, not a promise that
arbitrarily larger corpora have acceptable latency or memory behavior. Allocation at that
scale also means concurrent broad searches should remain uncommon until corpus decoding is
made more compact.

## Runtime bounds

The total store is not destructively capped. Instead, every response and graph operation
has a hard server-side bound:

| Operation | Default | Hard maximum |
| --- | ---: | ---: |
| Lexical search results | 25 entries | 100 entries |
| Search graph roots | 5 | 25 |
| Search graph connections | 25 | 200 |
| Entries added by search expansion | 20 | 100 |
| General graph traversal depth | 2 | 8 |
| General graph traversal nodes | 100 | 1,000 |
| General graph traversal edges | 200 | 2,000 |
| General graph traversal time | 2 seconds | 10 seconds |

Clients receive truncation metadata when a bounded graph operation has more work. They
must expand or page deliberately rather than requesting the entire durable graph. Search
authorization and scope filtering happen before ranking, so these counts cannot be used to
infer hidden records.

Re-run and update this document when entry encoding, Pebble iteration, authorization,
ranking, graph expansion, or output limits change materially. Treat a clearly superlinear
curve, a 100,000-entry regression beyond roughly one second on comparable hardware, or a
material increase above the recorded allocation as a release investigation rather than an
automatic flaky-test threshold.

## Explorer rendering at the graph limits

The browser boundary test uses the real explorer application, Graphology store, Sigma
renderer, virtualized accessible table, and Chromium canvas. It injects an API-shaped
snapshot at the enforced maximum of 1,000 nodes and 2,000 directed relationships through
the application's normal graph-selection path. It then selects 35 dispersed nodes,
discards five warm-up selections, and records 30 interaction paints. Run it with:

```sh
go test ./internal/webui \
  -run '^TestMemoryBrowserPerformanceAtSnapshotLimits$' -count=3 -v
```

These results were recorded on the same 2026-08-23 Linux/amd64 machine in a 1440 by 1000
headless Chromium viewport. Chromium used its SwiftShader WebGL implementation, which is a
repeatable software-rendering baseline rather than a claim about a particular phone or
desktop GPU. The table reports the median of three independent browser runs; each
interaction percentile is calculated within one run and then the median is reported:

| Measurement at 1,000 nodes / 2,000 edges | Observed time |
| --- | ---: |
| API snapshot handoff to completed first paint | 56.7 ms |
| Initial Sigma refresh work | 13.7 ms |
| Selection interaction, p50 | 93.4 ms |
| Selection interaction, p95 | 103.0 ms |
| Selection interaction, maximum | 108.3 ms |
| Sigma refresh work during selection, p50 | 5.3 ms |
| Sigma refresh work during selection, p95 | 7.6 ms |
| Sigma refresh work during selection, maximum | 8.0 ms |

Snapshot-to-paint deliberately excludes network transfer and server traversal; those are
bounded separately and the backend search/traversal cost is measured above. The interaction
measurement includes waiting across browser animation frames, while the Sigma refresh
measurement isolates the synchronous renderer work inside those frames. This distinction
keeps frame scheduling in the user-visible number without misdiagnosing idle frame wait as
graph processing.

The automated test asserts the exact maximum object counts, the bounded/truncated UI state,
and that every interaction causes a successful paint. Its timing ceilings are intentionally
wide (15 seconds to first paint and one second at interaction p95) so ordinary CI hardware
variation does not make performance verification flaky. Use the recorded values, browser
traces, and renderer metrics to investigate meaningful regressions instead of tightening
those safety ceilings to one workstation's current speed.
