# PI-05/T persistence and milestone audit

Status: **Local acceptance complete; remote evidence required**

## Outcome and ownership

The core has one adapter-owned database lifecycle. Application ports contain
domain values; SQL connections, rows, transactions and driver types stay inside
the SQLite adapter. Executable import guards now cover domain, application and
direct database ownership.

Generic storage failures—including cleanup and closed-handle failures—map to
application-owned repository errors. Real missing-table Save/Find regressions
reject driver types, database/sql sentinels, physical table names and SQL
diagnostics. Context cancellation, bounded contention and callback errors retain
their meanings. The duplicated lifecycle repository-error wrapper was removed.

Startup first checks existing databases through a read-only connection, including
SQLite integrity_check. Unknown populated databases without the ledger and
damaged data pages fail before writable pragmas or migration. Corrupt main-file
bytes remain unchanged. A read-only SQLite WAL connection can create auxiliary
coordination sidecars; this is not a guarantee that opening any WAL-mode file
creates no sidecars. Existing sidecars are never deleted by this preflight.

## Combined milestone proof

TestMilestoneVersionedContentUpgradeAndVerifiedRollback starts with committed
Content at schema V1, upgrades to V2 only after a verified pre-upgrade snapshot,
commits newer values, and restores the snapshot through the fenced owner into a
new active path. The selected handle reports V1 and its earlier Content values.
The preserved original still reports V2 and its newer values. The test passed
25 independent repetitions. This proves the planned **database-only versioned
upgrade and rollback smoke**, not production rollback, a legacy schema import,
hardware power-loss tolerance or media/credential restore.

## Failure-matrix disposition

| Planned row | Evidence | Disposition |
| --- | --- | --- |
| Fresh and reopen | Fresh/empty/same-version tests | Passed locally |
| Supported upgrade | Pre-upgrade snapshot and combined milestone | Passed locally |
| Dirty/partial migration | Dirty refusal, retained recovery snapshot | Passed locally |
| Newer/drift/gap | Ledger validation before snapshot/mutation | Passed locally |
| Busy and second writer | Contention, non-replayed callback, serialized access | Passed locally |
| Interrupted write | Unix helper termination and committed-state readback | Passed locally; not power loss |
| Backup failure | Values, schema and full ledger unchanged | Passed locally |
| Restore | Exclusive gate, exact snapshot schema, retained original | Passed locally |
| Corrupt database | Invalid header and damaged content-page bytes retained | Passed locally |
| Bounded readers | Context-bounded serialized pool/owner access | Passed locally; not parallel readers |
| Missing external blob | No file reference/port exists in ContentFacts | Unproven; owned by PI-07/T-01 |

## Scope reconciliation

The original PI-05 plan both deferred immutable file lifecycle to PI-07 and
prematurely placed missing-external-blob evidence in its T matrix. The roadmap's
PI-05 milestone is a versioned database that can upgrade and roll back; PI-07
owns safe/resumable TAF storage. The row stays explicitly unproven and is assigned
to [PI-07/T-01](https://github.com/shentschel/teddycloud/issues/17), with acceptance
and model routing. It is not replaced by a missing-record test or silently
counted as passed. This clarification preserves the database milestone and
does not certify absent external-content handling or coordinated media restore.

## Verification and remaining debt

Independent full backend tests/vet, 25 repetitions of the entire SQLite suite,
formatting, architecture-document and diff checks, and govulncheck passed.
Next CI now includes focused regression/repetition and Ubuntu race checks in
addition to its deterministic build, browser smoke, artifact, reproducibility
and advisory jobs. Remote success is required before final acceptance; local
race testing is unavailable because the host has no C compiler.

[F-PERSIST-01](https://github.com/shentschel/teddycloud/issues/18) tracks explicit
foreign_key_check validation before backup support is accepted for FK-bearing
schemas. The current representative Content schema has no FK relations;
integrity_check alone is not claimed as referential-consistency proof.

Windows subprocess restart, hardware/power loss, cross-process ownership,
persisted production configuration switching, real migration, external media and
credentials remain open at their roadmap owners. No deployment, destructive
cleanup, history rewrite or mainline merge occurred.
