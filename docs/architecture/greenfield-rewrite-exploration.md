# Greenfield TeddyCloud rewrite exploration

Status: exploration  
Scope: architecture, existing plugins, Tonie lookup and migration strategy

## Decision summary

A complete big-bang rewrite is not recommended. The preferred approach is a
new control plane for metadata, assignments, library management and the Web UI,
while the existing C daemon initially remains the proven box protocol gateway.

The target should be a modular monolith with two isolated workers, not a fleet
of microservices:

```text
Toniebox
   | TLS / protobuf / RTNL
   v
Protocol gateway
   |
   v
TeddyCloud core ---- SQLite + TAF/file storage
   |       |
   |       +---- typed event hub ---- React UI
   |
   +------------ media and connector workers
```

## Domain model

Physical identity and content identity must be separate:

- `Tag`: UID, rUID, authentication state, claimed state and ownership.
- `Content`: stable content identity, optional verified model and metadata.
- `ContentVersion`: exact audio ID, hash, size, track count and release data.
- `Set`: product/article grouping.
- `SetMember`: ordered relation from a set to independent contents.
- `Assignment`: relation from a physical tag to a content version and source.

Rules:

1. rUID is the canonical physical tag identity.
2. `content_id` is the canonical logical content identity.
3. Audio ID and hash identify a content version, not a physical tag.
4. A model number is metadata and may be absent.
5. Title and set heuristics only produce review candidates.
6. Ambiguous matches are explicit and never resolved by taking the first hit.

This also addresses the identity problem discussed in
[teddycloud#430](https://github.com/toniebox-reverse-engineering/teddycloud/issues/430).

## Target modules

1. Protocol gateway for TB1/TB2 HTTPS, client certificates, RTNL and legacy TLS.
2. Tag registry for identity, authentication and ownership.
3. Content store for immutable TAF blobs, hashes, range streaming and sources.
4. Catalog service for metadata, versions, sets and provenance.
5. Assignment service for Original Tonies and Custom Cards.
6. Library/query service.
7. Typed event hub with SSE and optional WebSocket adapters.
8. Management API, authentication, settings, overlays and audit trail.

SQLite should store relational state and migrations. TAF files, covers and
certificates remain in the filesystem; only paths, metadata and digests belong
in the database.

## Existing enhancement migration

| Existing component | Target capability |
| --- | --- |
| Plugin Common | Generated API client and shared domain types |
| Tonie Manager | Main library UI |
| Current Tonie | Current-tag/now-playing panel driven by typed events |
| Unknown Tonie | Catalog enrichment and review workflow |
| YouTube2Tonie | Least-privileged media worker and UI module |
| NFC/MyTonies Sync | Least-privileged connector worker |
| Meta installer | Versioned release package with migrations and rollback |

## Technology recommendation

- Go for the new backend and eventual protocol gateway.
- React and TypeScript for the Web UI.
- OpenAPI and a generated TypeScript client for management APIs.
- SQLite for state; filesystem/object storage for large files.
- A local authenticated API or Unix socket for workers.

Rust provides stronger memory-safety guarantees but materially increases the
delivery cost unless maintainers already use it confidently. Python and Node.js
are suitable for isolated media/connectors, but not the initial box-facing
gateway.

## Migration plan

1. Capture sanitized golden fixtures for current APIs and box traffic.
2. Import the existing catalog, tags and content in read-only shadow mode.
3. Make the new database/API/UI authoritative for management operations.
4. Preserve the old API through a compatibility facade during plugin migration.
5. Move assignments, events, content streaming and cache incrementally.
6. Replace the hardware-facing gateway last and only after real-device tests.
7. Keep import/export, snapshots and a documented rollback path throughout.

## Required verification

- Contract tests for all APIs used by existing plugins.
- Sanitized protocol fixtures for TLS, protobuf, range requests and RTNL.
- A device matrix covering supported box generations.
- Property tests for UID/rUID and audio-ID/hash matching.
- Reconciliation of old and new tag indexes, assignments and TAF counts.
- Power-loss, interrupted-download and migration-rollback tests.
- Browser tests for all migrated management workflows.

## Risk and effort

The protocol and hardware compatibility layer is the principal risk, not the UI
or database.

- New control plane while retaining the old gateway: approximately 6-10
  full-time weeks.
- Protocol gateway replacement: approximately another 3-6 months.
- Complete feature parity: approximately 6-12 person-months.

A five-hour work package can produce an ADR, schema, repository scaffold or one
narrow proof of concept, but not a usable rewrite.

If code from the existing GPL-2.0-or-later project is reused, the resulting
distribution must be planned around that license. A differently licensed
implementation would require a genuine clean-room approach and legal review.

