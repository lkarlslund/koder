# Knowledge graph browser dependencies

These are the exact upstream packages selected for Koder's first knowledge
explorer. Production assets are built from these releases and served locally;
the explorer must never load a library from a CDN. Update this file and verify
the package license, npm integrity, tarball SHA-256, browser bundle, and tests
together when changing a version.

| Package | Version | License | npm integrity (SHA-512) | Tarball SHA-256 |
| --- | --- | --- | --- | --- |
| [`graphology`](https://www.npmjs.com/package/graphology) | `0.26.0` | MIT | `sha512-8SSImzgUUYC89Z042s+0r/vMibY7GX/Emz4LDO5e7jYXhuoWfHISPFJYjpRLUSJGq6UQ6xlenvX1p/hJdfXuXg==` | `3c0cc63556e32b9d00ea953dce29790a5e49fc448f19247e25540156caef6b98` |
| [`sigma`](https://www.npmjs.com/package/sigma) | `3.0.3` | MIT | `sha512-5H0zFlx6/NTQpqBg4Rm569ZOpnBOXMaS25UQThIWMU3XyzI5AhmorK/gnl87BvJBLhQd0tW4C0LIp3enWzMoNw==` | `ff467677a066c06ef81a2b66cde421a26cd629ef5321e4c08742b69c8d32a9bb` |
| [`graphology-layout-forceatlas2`](https://www.npmjs.com/package/graphology-layout-forceatlas2) | `0.10.1` | MIT | `sha512-ogzBeF1FvWzjkikrIFwxhlZXvD2+wlY54lqhsrWprcdPjopM2J9HoMweUmIgwaTvY4bUYVimpSsOdvDv1gPRFQ==` | `62ea434a9a478807390062fbd7ded2647794d04a71bd3708cff85b9a4ea53c7e` |
| [`graphology-layout-noverlap`](https://www.npmjs.com/package/graphology-layout-noverlap) | `0.4.2` | MIT | `sha512-13WwZSx96zim6l1dfZONcqLh3oqyRcjIBsqz2c2iJ3ohgs3605IDWjldH41Gnhh462xGB1j6VGmuGhZ2FKISXA==` | `cc150019635dbc330cb07ee520f96132230a045c94b8eab816aff99d0d3d1fc2` |

Sigma 3 is selected instead of the separately published Sigma 4 alpha line so
the first explorer is built on a stable renderer API. Graphology is the owned
in-browser graph model; Sigma is only the WebGL view. ForceAtlas2 and Noverlap
are kept as separate layout dependencies so layout can later move behind the
planned worker boundary without coupling stored knowledge to a renderer.

The SHA-256 values above are hashes of the npm `.tgz` payloads named
`<package>-<version>.tgz`, downloaded from the registry on 2026-08-22. The npm
integrity values are the registry's package metadata and provide an independent
content check.

## Vendored browser assets

| Asset | Source package path | SHA-256 |
| --- | --- | --- |
| `graphology/graphology.umd.min.js` | `graphology/dist/graphology.umd.min.js` | `dc337efa23903f61e064c8e7e7f93a429e6855dccfc2458802b4ed30c621c087` |
| `graphology/LICENSE.txt` | `graphology/LICENSE.txt` | `9d396b4882c329077f32861c0d6822dcee48f2d0ff6196d8459af70844196275` |
| `sigma/sigma.min.js` | `sigma/dist/sigma.min.js` | `58e30383ab428f832068d9d16a5215c65ba12430d438ed091c5703f398de9e16` |
| `sigma/LICENSE.txt` | `sigma/LICENSE.txt` | `2d6beb4435a5186414986ef25facdea68619d0f3e1043b9a795f5c6233914013` |

The owning Go test checks these hashes from the embedded filesystem, serves the
assets through Koder's local asset handler, and rejects network-loading
primitives in the JavaScript payloads.
