# PI-08/A1.2a guarded catalog decisions checkpoint

Status: accepted bounded checkpoint; PI-08/A remains incomplete.
Issue: [PI-08/A #26](https://github.com/shentschel/teddycloud/issues/26).

## Delivered scope

Commit `2921dc8f69f06e8bd004dacfa2cadbbaa4eca214` adds the
catalog-domain review state machine in
`next/backend/internal/domain/catalog/decisions.go`, its tests, and a minimal
source-revocation hook in `policy.go`.

The checkpoint provides:

- positive aggregate revisions with checked overflow;
- expected-revision fencing for mutations;
- immutable observation identity and changed-payload conflicts;
- explicit support acceptance and conflict resolution;
- conflict fields without an implicit scalar winner;
- canonical exact-command replay without revision increments;
- current-capability checks for replay and monotonic source revocation;
- deterministic support/conflict ordering and bounded retained state;
- protected-custom identity preservation.

Confidence, timestamps, AudioID values and source order never grant acceptance,
overwrite authority or freshness priority.

## Verification

Next CI run [38462406351](https://github.com/shentschel/teddycloud/actions/runs/38462406351)
passed all four jobs for the exact commit: deterministic workspace gates and
catalog tests, persistence/domain race evidence, reproducibility, browser smoke
and advisory checks.

The local process starter remains unavailable with
`Failed to create unified exec process: No such file or directory (os error 2)`;
therefore no local clean-worktree or local-test claim is made.

## Remaining boundary

Effective-order decisions are deferred to A1.2b. Application commands, queries
and transaction ports remain A1.3. Persistence, import adapters and the PI-08/A
milestone remain open. Rollback is the focused additive source revert.
