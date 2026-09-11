# Current TeddyCloud dependency matrix

Status: PI-00 preparation snapshot

This matrix is an architecture input, not a promise of feature parity. Validate
it against fixtures and real installations during PI-02.

## Runtime components

| Current component | Responsibilities and interfaces | TeddyCloud Next target | Migration order | Main risk |
| --- | --- | --- | ---: | --- |
| C server/router | Web/API routing, content routes, reverse proxy | Legacy gateway, then Go gateway | Last | Undocumented coupling |
| TB1 cloud handlers | `/v1/time`, OTA, claim, content, freshness, log, reset | Protocol gateway | Last | Device/TLS compatibility |
| TB2 cloud handlers | `/v3/freshness-check`, OTA, setup, chapter, content metadata | Protocol gateway | Last | Incomplete hardware evidence |
| RTNL handler | Binary TLS/protobuf playback and tag stream | Protocol gateway plus event hub | Last | Framing/reconnect behavior |
| Tag/content API | `/api/getTagIndex`, `/api/getTagInfo`, `/content/json/*` | Tag, assignment and library services | Early with facade | Existing client compatibility |
| Catalog API | `toniesJson*`, `toniesCustomJson*` | Catalog and review services | Early | Ambiguous identity/version model |
| File/TAF API | upload, encode, file index, content download | Content store and media worker | Middle | Range, path and partial-write safety |
| Settings/overlays | Settings CRUD and content overlays | Core settings service | Middle | Migration and secret ownership |
| SSE endpoint | Untyped live events | Typed event hub with SSE adapter | Early | Event compatibility |
| Plugin loader | Folder discovery, manifest metadata, iframe page | Manifest V2 and extension registry | Early | Same-origin trust boundary |
| React Web UI submodule | Administration, Tonies and library UI | New React shell and public SDK | Middle | Internal components becoming accidental API |

## Enhancement repositories

| Repository | Current dependency | Target ownership | Required compatibility |
| --- | --- | --- | --- |
| `teddycloud-plugin-common` | Tag APIs, SSE, assignment/readback logic | Generated SDK and shared domain types | UID/rUID and assignment semantics |
| `teddycloud-tonie-manager` | Tag index/info and browser-side grouping | Standard Tonies UI plus core queries | Preferred-card and Custom Card behavior |
| `teddycloud-current-tonie` | SSE and tag info | Current-tag UI extension | Tag placed/removed and playback events |
| `teddycloud-unknown-tonie` | Catalog search/custom metadata APIs | Catalog review workflow | Candidate ranking and provenance |
| `teddycloud-youtube2tonie` | FastAPI, media tools, TAF creation and assignment | Isolated media worker plus UI extension | Jobs, playlists, metadata and assignment |
| `teddycloud-nfc-sync` | NFC files, MyTonies, catalog/tag APIs, TAF download | Isolated connector worker | Idempotency, rate limits and credentials |
| `teddycloud-enhancements` | Installer and deployment aggregation | Versioned release/installer | Upgrade, backup and rollback |

## External and bundled dependencies

| Dependency | Current use | Decision required |
| --- | --- | --- |
| CycloneTCP/CycloneSSL | HTTP/TLS and legacy box compatibility | Retain in legacy gateway; replace only after fixtures |
| cJSON | JSON parsing in C core | Removed from new core when gateway retires |
| protobuf-c/protocol schemas | Box protocol messages | Preserve wire compatibility and fixture coverage |
| Opus/Ogg/TAF encoder | Audio processing | Isolate in content/media boundary |
| Filesystem catalog/content | Metadata, TAFs, cache and certificates | Define SQLite ownership while retaining file blobs |
| Boxine cloud/MyTonies | Authentication, content and enrichment | Connector/proxy policy and rate limits |
| Public Tonies catalogs | Metadata matching | Provenance, versioning and ambiguity policy |
| ffmpeg/tonietoolbox/YouTube | Media conversion | Least-privileged media worker |

## Critical migration seams

1. Existing box traffic must remain on the legacy gateway until hardware tests
   pass.
2. Management clients require a compatibility facade while APIs migrate.
3. Tags, Custom Cards and TAF paths need reconciliation before any source of
   truth changes.
4. Browser plugins must migrate from iframe/DOM customization to semantic slots.
5. External enrichment may propose metadata but must not silently override
   verified physical or content identities.

