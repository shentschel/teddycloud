# PI-08/A2.1 durable catalog schema checkpoint

Status: accepted bounded checkpoint; PI-08/A and the milestone remain incomplete.

Implementation:
- `a4335cd167e7d8b4b9f9144078bf4ff47028ec19`
- test-fixture correction: `979743f3490a7f7e4d878a9613a01a6ccf964342`

## Delivered

Migration v5 (`0005-catalog-provenance`) adds the normalized SQLite
foundation for core-bound source registrations, immutable artifact records,
managed catalog subjects, typed observations, guarded review decisions and
support relations. Applied v1-v4 migration statements and checksums remain
unchanged.

The schema is additive: upgrading a real v4 fixture preserves existing
Content, Tag, ContentVersion, blob binding and import receipt values. Existing
rows are not implicitly adopted or reviewed. A catalog-only ContentVersion is
valid without a fabricated blob binding or import receipt. Strict constraints,
foreign keys and retention triggers reject malformed or dangling relations,
and verified current/pre-v5 snapshots restore to their exact schema version.

Changed code scope:
- `next/backend/internal/adapters/sqlite/schema.go`
- `next/backend/internal/adapters/sqlite/catalog_schema_test.go`
- minimal compatibility updates in `blob_schema_test.go`,
  `tag_schema_test.go` and `lifecycle_owner.go`

## Verification

Next CI [38490839395](https://github.com/shentschel/teddycloud/actions/runs/38490839395)
passed all four jobs for the final code SHA, including deterministic workspace
checks, focused SQLite repetitions, persistence/domain race evidence,
reproducibility, browser smoke and advisory checks. The local process launcher
remains unavailable, so no local clean-worktree or local test claim is made.

## Deferred

A2 still requires the catalog repository/transaction adapter, managed Save
guard, catalog-only query/cursor implementation, explicit baseline adoption,
order persistence, revision-race evidence and the remaining recovery/query
bounds. No public API, import adapter, legacy production mutation, deployment
or milestone acceptance is included.
