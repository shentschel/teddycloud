# PI-05 review

Status: **R/A complete; B transaction/repository checkpoint complete; B/T not complete**

The PI milestone—versioned database upgrade and rollback by verified restore—is
not yet accepted. PI-05 contributes persistence evidence to Gate B; it does not
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
| B | Partial checkpoint | Content repository and [database backup smoke](pi-05-b-backup-checkpoint.md); remaining failure evidence missing |
| T | Not started | Hardening and final milestone audit missing |

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

B remains open: restart evidence, explicit bounded
readers and coordinated backup/restore of a live database path have not yet been
implemented or accepted.

The database-only smoke covers a WAL-mode snapshot, digest, integrity and
migration-ledger verification, plus restore to a new path with committed-value
readback. Existing destinations and tampered backups are rejected. See the
[checkpoint](pi-05-b-backup-checkpoint.md).

## Open decisions

- Finalize repository shapes only with B tests.
- Calibrate busy timeout and retry counts from measured contention tests.
- Integrate a verified pre-upgrade backup before incompatible migration.

Production migration, coordinated media/credential restore and hardware
durability evidence remain outside this checkpoint.
