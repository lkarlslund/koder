# Third-Party Notices

Koder is licensed under the MIT license. This file records third-party
dependencies and bundled assets used by Koder so redistributions can preserve
the applicable license notices.

## Go Dependencies

The Go dependency license report was generated for the app packages with:

```sh
go run github.com/google/go-licenses@latest report \
  --ignore github.com/lkarlslund/koder ./cmd/... ./internal/...
```

The build graph currently contains permissive licenses only:

- Apache-2.0
- BSD-2-Clause
- BSD-3-Clause
- ISC
- MIT

Notable direct dependencies:

| Dependency | License |
| --- | --- |
| `github.com/coder/websocket` | ISC |
| `github.com/creack/pty` | MIT |
| `github.com/fsnotify/fsnotify` | BSD-3-Clause |
| `github.com/modelcontextprotocol/go-sdk` | Apache-2.0 |
| `github.com/pelletier/go-toml/v2` | MIT |
| `github.com/sammcj/mermaid-check` | Apache-2.0 |
| `github.com/santhosh-tekuri/jsonschema/v6` | Apache-2.0 |
| `github.com/sergi/go-diff` | MIT |
| `github.com/spf13/cobra` | Apache-2.0 |
| `github.com/cockroachdb/pebble` | BSD-3-Clause |
| `golang.org/x/text` | BSD-3-Clause |

## Vendored Browser Assets

The web UI serves static third-party browser assets from
`internal/webui/assets/vendor`. Their versions and licenses are tracked in
`internal/webui/assets/vendor/README.md`.

| Asset | Version | License |
| --- | --- | --- |
| Alpine.js | 3.14.8 | MIT |
| Bootstrap CSS | 5.3.3 | MIT |
| Bootstrap Icons | 1.11.3 | MIT |
| DOMPurify | 3.4.0 | MPL-2.0 or Apache-2.0 |
| highlight.js CDN assets | 11.11.1 | BSD-3-Clause |
| KaTeX | 0.17.0 | MIT |
| marked | 18.0.3 | MIT |
| Mermaid | 11.10.1 | MIT |

## Research Directory

The `research/` directory contains snapshots of other projects for comparison
and should not be treated as Koder product code. If it is redistributed, each
project's own license and notices must be preserved separately.
