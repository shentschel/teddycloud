# PI-08/A1.3a catalog application service checkpoint

Status: accepted bounded checkpoint; PI-08/A remains incomplete.
Issue: [PI-08/A #26](https://github.com/shentschel/teddycloud/issues/26).

## Delivered scope

Commit `0242b1d576edaffba6b8dd03703e719a2718241c` changes only
`next/backend/internal/application/catalog`: commands, queries, service,
transaction ports, typed errors and service tests.

The application boundary exposes explicit observation, review, replay and order
operations over one catalog transaction callback. It forwards expected
revisions, does not auto-retry conflicts or choose a first result, preserves
protected-custom/confidence semantics, checks cancellation before commit and
uses sanitized typed failures. Query results distinguish absence, conflict and
incomplete/catalog-only media state without probing storage.

## Verification

Next CI run [38466866456](https://github.com/shentschel/teddycloud/actions/runs/38466866456)
passed all four jobs for the exact commit: deterministic workspace/application
tests, persistence/domain race evidence, reproducibility, browser smoke and
advisory checks.

The local process starter remains unavailable with
`Failed to create unified exec process: No such file or directory (os error 2)`;
no local clean-worktree or local-test claim is made.

## Remaining boundary

The durable transaction adapter, managed Content write guard, catalog-only
version persistence, migrations and recovery belong to A2. Import adapters,
public APIs and the PI-08/A milestone remain open. Rollback is the focused
application-layer source revert.
