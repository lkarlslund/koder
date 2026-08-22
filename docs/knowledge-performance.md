# Knowledge performance and enforced bounds

Koder bounds result size and graph work independently from the total durable corpus. The
current production envelope has been measured through 100,000 active entries in the Pebble
backend; it is not inferred from a small fixture.

## Reproducing the measurement

Run the checked-in scale benchmark from the repository root:

```sh
go test ./internal/knowledge/service -run '^$' \
  -bench '^BenchmarkKnowledgeSearchScale' -benchtime=3x -count=1
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
