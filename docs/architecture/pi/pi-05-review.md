# PI-05 review

Status: **R/A/B/T complete; database-only milestone accepted**

The PI milestone—versioned database upgrade and rollback by verified restore—is
accepted for the database-only prototype. PI-05 contributes persistence evidence to Gate B; it does not
accept that later phase gate.

## R outcome

R established a single persistence owner, application-defined transaction
ports, strict adapter boundaries, connection and migration invariants, a
driver-selection experiment, a failure matrix and issue-ready A/B/T slices.
Production migration, schema implementation and dependency selection remain
outside R.

| Slice | State | Evidence |
|---|---|---|
| R | Complete | Plan, budget and review; documentation checks |
| A | Complete | [Driver decision](pi-05-a-sqlite-driver.md), commit 3421501, [Next CI](https://github.com/shentschel/teddycloud/actions/runs/35750113413), [docs CI](https://github.com/shentschel/teddycloud/actions/runs/35750113522) |
| B | Complete | Repository, contention, restart, upgrade fencing and [restore owner](pi-05-b-restore-checkpoint.md); green delivery CI |
| T | Complete | [Failure checkpoint](pi-05-t-failure-checkpoint.md) and [milestone audit](pi-05-t-audit.md) |

The delegated Sol refinement reached its budget checkpoint without producing
files and was stopped. The parent recovered the bounded R deliverable; private
agent and usage details remain in the ignored automation state.

## A outcome

A selected and pinned modernc.org/sqlite v1.59.0 after a Pure-Go versus CGO
comparison. The adapter now owns one serialized connection, verifies WAL,
synchronous=FULL, foreign keys and busy timeout, and applies contiguous
forward-only migrations through a checksummed dirty-state ledger. Tests cover
fresh creation, clean reopen, upgrade, failed/dirty migration, newer schema,
checksum drift and invalid version gaps.

Local backend tests, vet, CGO-free tests and cross-builds, documentation checks
and govulncheck passed. Both remote workflows passed; Next CI also proved the
deterministic artifact and two-work-directory reproducibility checks. The local
race test remains unavailable because the host has no C compiler, but remote CI
is green and no race-specific concurrency was added in A.

## B transaction/repository checkpoint

The application now defines storage-neutral Content repository and transaction
ports. The SQLite adapter supplies an immutable catalog-content migration and
proves commit/round-trip, operation-error rollback, serialized writes and that a
second operation cannot observe an uncommitted write through the current
single-connection boundary. Full backend tests, vet, architecture-document
checks and diff checks pass locally.

The [contention checkpoint](pi-05-b-contention-checkpoint.md) proves bounded
busy exhaustion, cancellation, callback non-replay and connection-setting
restoration after rollback.

The [restart and bounded-access checkpoint](pi-05-b-restart-checkpoint.md)
proves committed-value and migration-ledger preservation after terminating a
helper with an uncommitted write, plus deadline-bounded pool wait and recovery.
The single connection deliberately serializes read and write transactions;
this is not a parallel reader-pool implementation.

The [upgrade checkpoint](pi-05-b-upgrade-checkpoint.md) validates the migration
ledger before creating a mandatory verified pre-upgrade snapshot. Backup failure
leaves values, schema SQL and the full ledger unchanged; migration failure
retains snapshot metadata for recovery.

The [restore owner checkpoint](pi-05-b-restore-checkpoint.md) drains current
transactions, fences later callbacks, closes the old handle, and selects only a
verified new-path restore at the exact snapshot schema. The original database
remains recoverable; preflight failures retain the current handle and later
failures leave the owner closed. Independent 25x lifecycle tests, the full backend
suite/vet, formatting, docs, diff and advisory checks pass. Remote delivery CI passed. T and the milestone were subsequently verified by
the final audit below.

The database-only smoke covers a WAL-mode snapshot, digest, integrity and
migration-ledger verification, plus restore to a new path with committed-value
readback. Existing destinations and tampered backups are rejected. See the
[checkpoint](pi-05-b-backup-checkpoint.md).

## Open decisions

- Finalize repository shapes only with B tests.
- Calibrate busy timeout and retry counts from measured contention tests.
- Audit generic repository errors and corrupted/missing-content boundaries in T.

Production migration, coordinated media/credential restore and hardware
durability evidence remain outside this checkpoint.

## T local outcome

Storage-origin errors no longer expose driver/SQL details through application
ports. Read-only startup integrity/ownership checks retain corrupt and unknown
originals. Import guards enforce the application boundary, and CI adds focused
SQLite repetition/race evidence. The combined Content V1-to-V2 upgrade and exact
pre-upgrade restore passed 25 independent repetitions; the entire SQLite suite
also passed 25 times. Full backend tests/vet, docs, formatting/diff and the
advisory scan are green locally.

The [audit](pi-05-t-audit.md) explicitly reconciles the premature missing-blob row
with PI-07/T-01 (#17), not as passed evidence. Foreign-key snapshot validation is
tracked in F-PERSIST-01 (#18). Final milestone acceptance is supported by successful remote CI for the
published T delivery. Gate B, production migration, hardware,
media and secret restore are not accepted by this database-only milestone.

## Final published evidence and next step

Implementation [b73d98f](https://github.com/shentschel/teddycloud/commit/b73d98f)
passed [Next CI](https://github.com/shentschel/teddycloud/actions/runs/36288455791) and [docs CI](https://github.com/shentschel/teddycloud/actions/runs/36288455756). The focused SQLite regression and race job
passed alongside deterministic build/browser smoke/artifact, two-work-directory
reproducibility and advisory checks. All in-scope PI-05 failure rows have evidence.
The missing external-blob row remains unproven under PI-07/T-01, not counted as
passed. F-PERSIST-01 remains open before FK-bearing schema backup acceptance.

R/A/B/T are complete; the next ordered job is PI-06/R (Tag Registry state-machine
refinement) with its routed Astra model and a fresh independent quota admission.
No production deployment, destructive cleanup or mainline merge occurred.
