# PI-05/T persistence failure checkpoint

Status: **scoped T implementation locally complete; final PI-05 milestone decision remains with the parent audit**

## Closed in this checkpoint

- Generic SQLite, `database/sql`, cleanup and closed-handle failures crossing
  the application Content repository/transaction ports map to
  `catalog.ErrRepositoryUnavailable`. Cancellation, deadlines, bounded
  contention and callback/domain error identity remain observable.
- Missing-table Save and Find tests reject driver types, `database/sql`
  sentinels, physical table names and SQL diagnostics. The independent parent
  probe for the raw `Database` application port now passes.
- Startup performs a read-only preflight before connection pragmas or schema
  work. Corrupt files retain their original bytes; populated databases without
  the TeddyCloud migration ledger are rejected before writable pragmas or schema
  mutation; missing, zero-length and valid empty files remain fresh-compatible.
- Dirty, drifted and gapped ledgers fail before upgrade backup or migration.
- Executable import guards keep application packages independent of SQL,
  modernc SQLite and adapters, and keep direct `database/sql`/modernc ownership
  in the SQLite adapter. Command composition may import the adapter but not
  those database packages.
- Next CI retains its deterministic build/smoke, artifact, reproducibility and
  advisory jobs and adds pinned-Go focused SQLite regression, repetition and
  race evidence on Ubuntu.

## Evidence

Independent full backend tests/vet, the whole SQLite suite 25 times, docs,
formatting/diff and govulncheck also passed. The scoped checks are:

```text
go test ./backend/internal/adapters/sqlite ./backend/internal/architecture
go test -count=10 ./backend/internal/adapters/sqlite
go test ./backend/...
go vet ./backend/...
python3 scripts/check_architecture_docs.py
git diff --check
```

The parent audit owns the combined Content V1-to-V2 upgrade and exact
pre-upgrade restore proof, final plan/review/README reconciliation, remote CI
links and final milestone decision.

## Explicitly open evidence

- The failure-matrix row for a missing external blob is not testable through
  the current ContentFacts repository: it has no blob reference or filesystem
  port. The parent-created PI-07/T-01 follow-up owns the future ContentStore
  evidence and its issue URL will be recorded in the final audit. This row is
  classified as future PI-07 work, not passed or silently removed.
- Hardware power-loss, Windows subprocess restart and production cutover
  evidence remain outside this local checkpoint.
- Local race execution remains unavailable on the current host because it has
  no C compiler; the explicit Ubuntu CI race step is the required evidence.
- Backup verification uses SQLite `integrity_check` but not
  `foreign_key_check`; foreign-key consistency validation remains scoped
  debt rather than an implied guarantee.

The parent integration adds read-only integrity_check and a real damaged-content
page fixture, handles SQLite WAL auxiliary-file semantics explicitly, rejects
SQLite-like foreign table names without an ownership bypass, and proves the
combined upgrade/restore milestone. See [final audit](pi-05-t-audit.md).
