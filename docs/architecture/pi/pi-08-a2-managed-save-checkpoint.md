# PI-08/A2.2 managed Save guard checkpoint

Status: accepted bounded checkpoint; PI-08/A and the milestone remain incomplete.

Implementation: `cce8fcecdf8c1e0cf48e272537d152c3663dd313`

## Delivered

The unmanaged `ContentRepository.Save` path now checks the selected schema
ledger and `tc_catalog_managed` inside the same SQLite transaction. Unmanaged
content remains writable. Ordinary managed and protected-custom identities fail
closed with the stable application authorization error before an insert or
update is attempted.

The guard and write share one transaction snapshot. A stale pre-adoption reader
cannot be promoted into an overwrite after another connection commits adoption;
SQLite contention is returned without replaying the callback. Cancellation,
rollback, restore fencing and storage-error sanitization remain intact. An exact
v4 restore keeps pre-PI-08 unmanaged operations available without implicitly
creating v5 objects.

Changed code scope:
- `next/backend/internal/adapters/sqlite/content_repository.go`
- `next/backend/internal/adapters/sqlite/catalog_managed_save_test.go`

## Verification

Next CI [38522637614](https://github.com/shentschel/teddycloud/actions/runs/38522637614)
passed all four jobs for the implementation SHA, including deterministic
workspace checks, focused SQLite repetitions, persistence/domain race evidence,
qualified filesystem checks, reproducibility, browser smoke and advisory checks.
The focused workflow observed one transient failure in the pre-existing
`TestImportConfiguredDeadlineThroughCommit` repetition before its successful
bounded rerun; this checkpoint does not claim to fix or explain that unrelated
test instability.

The local process launcher remains unavailable, so no local clean-worktree or
local test claim is made.

## Deferred

A2 still requires the catalog repository/transaction adapter, durable
review/order persistence, catalog-only queries and cursors, explicit baseline
adoption, revision-race evidence and remaining recovery/query bounds. No schema
change, public API, import adapter, production migration or milestone acceptance
is included.
