# PI-08/A2.4 global catalog find checkpoint

Status: accepted bounded checkpoint; PI-08/A and the milestone remain incomplete.

Implementation: `f227739df1f4085bfccc6208521b8740f3191650`

## Delivered

The read-only SQLite catalog adapter now implements global
`FindVersionsByPair` and `FindVersionsByAudioID` queries. Fingerprints remain
search attributes rather than identities: independent opaque Content and
ContentVersion IDs sharing a fingerprint are preserved as explicit ambiguous
candidates without selecting a winner.

Global results are deterministic, revision-bound and limited by entries,
encoded bytes, cursor size and a 1,024-version scan ceiling. Cursor MACs bind
the query attributes, owner filter, revision and catalog snapshot; local and
external ABA writes plus restore-generation changes invalidate continuations.
Catalog-only matches retain `CatalogOnly` media state and do not create blob
bindings, receipts or managed review records. Corrupt rows and relations fail
closed.

Changed code scope:
- `next/backend/internal/adapters/sqlite/catalog_repository.go`
- `next/backend/internal/adapters/sqlite/catalog_query_test.go`

## Verification

Next CI [38569123849](https://github.com/shentschel/teddycloud/actions/runs/38569123849)
attempt 2 passed all four jobs, including gofmt, go vet, backend tests,
tenfold SQLite repetition, persistence/domain race evidence, qualified
filesystem checks, cross-compile, reproducibility, browser smoke and advisory
checks.

Attempt 1 exposed failures in the pre-existing contention cancellation and
configured-deadline import repetitions. The unchanged second attempt passed;
this checkpoint does not claim to fix or explain that unrelated instability.

The local process launcher remains unavailable, so no local clean-worktree or
local test claim is made.

## Deferred

Review/order rehydration, catalog mutation and canonical replay remain follow-up
work. Global scans reject catalogs over 1,024 versions. Bounded partial pages
remain `Incomplete`; a terminal partial page may have no `Next` and therefore
does not assert uniqueness. No schema, migration or public API change is
included.
