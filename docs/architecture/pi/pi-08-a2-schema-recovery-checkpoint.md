# PI-08/A2.6a schema-recovery checkpoint

Status: accepted bounded v4-to-v6 upgrade evidence; PI-08/A2 and the PI milestone remain incomplete.

## Accepted implementation

- Test evidence: `16c902f52066b824309cdfbc341419d6b29de9f8`
- Production code changed: none

A real v4 database fixture now exercises retained Content, Tag, version,
binding and receipt data through the ordered upgrade to v6. The test compares
all retained rows and bytes, migration identities and checksums, clean-state
markers, foreign keys, schema objects, indexes and triggers. Repeated v6 opens
are idempotent.

The additive catalog tables remain empty after upgrade and catalog queries
report the retained rows as unmanaged. No Review or Order state, authority or
protected-custom decision is fabricated. Dirty, checksum-drifted and corrupted
fixtures fail closed without changing the original bytes or publishing an
upgrade backup.

This checkpoint covers schema upgrade only. Snapshot restore and helper-process
recovery remain separate acceptance work.

## Verification

Digest-pinned Go 1.27.1 Docker checks passed the focused recovery test, ten
repetitions, the relevant SQLite/catalog/architecture suite and its race-enabled
run. Next CI run 38869925807 passed all four required jobs: persistence/race,
deterministic build/smoke, two-work-directory reproducibility and advisories.

The known intermittent cancellation-test behavior did not occur here and remains
unresolved.

## Deferred

Verified current/pre-PI-08 snapshot restore, rollback-failure injection,
helper-process recovery and the remaining A2 acceptance matrix are still open.
