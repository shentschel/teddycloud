# PI-05 persistence execution plan

Status: **R complete; A/B/T not started**

PI-05 proves that a versioned local database can be upgraded and restored
without leaking storage details into domain code. The milestone and Gate B are
not accepted by this refinement alone.

## Ownership and scope

The core process is the only authoritative database owner. A new
`internal/adapters/sqlite` package owns connection setup, schema migrations,
checkpointing and the database backup primitive. Application use cases define
transaction and repository ports; the adapter implements them with domain
values. SQL rows, transactions, nullable driver types, table names and driver
errors must not cross into application or domain packages.

One serialized writer is allowed. Readers are bounded. Plugins, workers and the
legacy service must use core contracts and may not open the database directly.
The legacy writer remains authoritative for each fact until its later,
explicit ownership transfer.

PI-05 owns the driver decision, connection invariants, schema ledger,
forward-only migration runner, drift detection, a transaction/repository
prototype and a database-only backup/restore smoke test. It defers:

- immutable content-file lifecycle to PI-07;
- assignment and legacy projections to PI-09/PI-36;
- outbox and Event Hub semantics to PI-11;
- snapshot import and reconciliation to PI-34;
- installer and operational rollback to PI-37;
- production power-loss and hardware evidence to later acceptance work.

## Connection and migration invariants

- The database is a local file opened only by the core process.
- WAL, `synchronous=FULL` and foreign keys are enabled and read back at startup.
- Foreign-key enforcement is applied to every connection.
- Busy waiting and application retries are bounded by context and a documented
  budget; exhaustion returns a stable application error.
- Writes are serialized; the reader pool is explicitly bounded.
- A schema ledger records version, migration identity/checksum and dirty state.
- Unknown newer schemas, checksum drift and incompatible pragmas fail closed.
- Migrations are ordered, forward-only and transactional where SQLite permits.
- Upgrade creates and verifies a pre-upgrade backup before incompatible change.
- Rollback means restoring that verified backup while access is fenced; it is
  not a destructive down migration.
- Reopening an already migrated database is idempotent.

## Driver experiment required by A

A must compare at least one pure-Go SQLite driver with the established CGO
alternative before pinning a dependency. Record the exact SQLite version and
build options, licensing, supported architectures, reproducible-build impact,
backup API, WAL/checkpoint controls, busy semantics, per-connection foreign-key
behavior, race-test behavior and vulnerability scan. Selection requires
executable evidence; R does not select a driver.

## Failure matrix

| Case | Required outcome | Evidence in A/B/T |
|---|---|---|
| Fresh database | Baseline schema installed once | A integration test |
| Supported upgrade | Ordered migration commits atomically | A integration test |
| Dirty/partial migration | Startup refuses or safely resumes by documented rule | A failure test |
| Newer schema | Startup refuses without mutation | A failure test |
| Checksum drift | Startup refuses with actionable error | A failure test |
| Lock/busy exhaustion | Bounded retry, stable error, no partial write | B test |
| Concurrent second writer | Serialization or deterministic rejection | B test |
| Restart after interrupted write | Last committed state remains valid | B test |
| Backup failure | Upgrade aborts before schema mutation | B smoke test |
| Restore | Closed/fenced restore verifies and reopens | B smoke test |
| Corrupt database | Fail closed; original file retained | T failure test |
| Missing external blob | Repository reports missing content without DB corruption | T boundary test |
| Readers during write | Bounded readers see valid transaction states | B concurrency test |

## Issue-ready delivery slices

### [PI-05/A](https://github.com/shentschel/teddycloud/issues/14) — SQLite driver decision and migration framework

- Compare drivers and pin the selected dependency with rationale.
- Implement connection verification, schema ledger and migration runner under
  `next/backend/internal/adapters/sqlite/**`.
- Extend architecture checks if needed and add fresh/open/upgrade/drift tests.
- Do not migrate production data or expose SQL outside the adapter.

Acceptance: the A rows in the failure matrix pass, dependency/security evidence
is recorded, and the standard Next checks remain green.

### [PI-05/B](https://github.com/shentschel/teddycloud/issues/15) — Transaction ports, repositories and backup/restore smoke

- Define minimal application transaction/repository ports.
- Implement one representative repository without persistence leakage.
- Prove serialized writes, bounded contention and transaction rollback.
- Implement and test verified database-only backup/restore.

Acceptance: the B rows in the failure matrix pass and a restored database
reopens at the expected schema version with the expected committed values.

### [PI-05/T](https://github.com/shentschel/teddycloud/issues/16) — Persistence leakage and failure hardening

- Audit dependency direction, error mapping and direct database access.
- Add corruption, missing-content and migration edge-case tests.
- Wire focused tests into CI and record unresolved operational evidence.

Acceptance: no SQL/driver types leak into domain/application contracts, all T
rows pass, and the PI milestone has reproducible local and remote evidence.

All three slices route to **gpt-5.6-sol** with high reasoning. Each slice needs a
fresh quota admission and independent integration review.

## PI acceptance

Run at minimum:

```text
python3 scripts/check_architecture_docs.py
make -C next clean check artifact artifact-check reproducibility-check
git diff --check
```

Also require focused persistence tests, the repository security/advisory scan,
and successful remote CI. No production deployment, live database migration,
destructive cleanup or mainline merge is authorized by this plan.
