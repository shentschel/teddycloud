# PI-08/A2.3 read-only catalog query checkpoint

Status: accepted bounded checkpoint; PI-08/A and the milestone remain incomplete.

Implementation: `51450ed5732417a4cc03568e30c3c57374c8ac24`

Repair and acceptance head: `da992229fa3a3f585ee6f4fae6867c7abe53b729`

## Delivered

The SQLite adapter now supplies a read-only catalog repository and transaction
boundary for Content, ContentVersion and bounded per-Content version queries.
Stored ContentFacts remain visible as the baseline while managed review state,
accepted projections, conflicts and their support are materialized separately.

Catalog-only versions do not fabricate blob bindings or immutable receipts.
Results are deterministically ordered and bounded. Pagination is revision-bound;
same-connection and external ABA writes, legacy catalog changes and restore
generation changes invalidate stale cursors. Corrupt revisions, support
relations and claim shapes fail closed. The transaction rejects mutation and
does not broaden legacy writer ownership.

Changed code scope:
- `next/backend/internal/adapters/sqlite/catalog_repository.go`
- `next/backend/internal/adapters/sqlite/catalog_transaction.go`
- `next/backend/internal/adapters/sqlite/catalog_query_test.go`

## Verification

Next CI [38567282477](https://github.com/shentschel/teddycloud/actions/runs/38567282477)
passed all four jobs for the acceptance head, including gofmt, go vet, backend
tests, focused SQLite repetitions, persistence/domain race evidence, qualified
filesystem checks, reproducibility, browser smoke and advisory checks.

The local process launcher remains unavailable, so no local clean-worktree or
local test claim is made.

## Deferred

Global fingerprint/audio-ID find queries, review/order rehydration, catalog
mutation and canonical command replay remain explicit `ErrUnsupported`
follow-up work. Per-Content scans are capped at 1,024 versions. No schema,
migration or public API change is included.
