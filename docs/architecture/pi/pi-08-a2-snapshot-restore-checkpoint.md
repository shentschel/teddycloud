# PI-08/A2.6b snapshot-restore checkpoint

Status: accepted bounded snapshot-restore evidence; PI-08/A2 and the PI milestone remain incomplete.

## Accepted implementation

- Test evidence: `9288ac8888091eaab339f9d8e6e6238ad4f73fbb`
- Production code changed: none

The accepted SQLite backup/restore path now has catalog-specific evidence for a
current v6 snapshot and an exact pre-PI-08 v4 snapshot. The v6 case preserves
Review and Order state bytes, normalized relations, revisions, prior command
replay, legacy Content/Tag/version/binding/receipt rows and foreign-key
integrity. Restore drains active callbacks, invalidates escaped repositories and
old cursors, closes the old database handle and selects only the verified target.

The v4 case restores the exact old schema without implicit migration. Catalog
transactions remain unsupported, no authority is fabricated, retained receipts
remain readable and generated external media is not deleted.

Failure injection covers digest and relational corruption, missing bindings,
dangling receipts, inventory invalidation, competing destination replacement
and post-preflight source mutation. Failed restores retain the original,
publish no unverified target and remove staging files.

## Verification

Digest-pinned Go 1.27.1 Docker checks passed the focused restore tests, ten
repetitions, the relevant SQLite/catalog/architecture suite and its race-enabled
run. Next CI run 38904951483 passed all four required jobs: persistence/race,
deterministic build/smoke, two-work-directory reproducibility and advisories.

## Open findings

A catalog query after an Order write can still fail and requires a separate
bounded diagnosis/fix. The previously observed intermittent cancellation-test
failure also remains open. Neither is claimed resolved by this checkpoint.

## Deferred

Helper-process termination/reopen evidence and the remaining A2 acceptance
matrix are still open.
