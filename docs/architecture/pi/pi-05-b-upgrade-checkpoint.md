# PI-05/B verified pre-upgrade backup checkpoint

Status: local adapter evidence complete; PI-05/B remains open.

Opening an existing supported schema with pending migrations now requires an
explicit `Config.UpgradeBackupPath`. The SQLite adapter validates the complete
migration ledger first, creates a database-only snapshot at that unused path,
verifies its digest, integrity and ledger, and only then writes a dirty ledger
entry or changes schema. Fresh databases and same-version reopens do not create
or require a backup.

Backup creation fails closed when the destination is missing, already exists or
cannot be written. Tests preserve and compare committed values, schema SQL and
every ledger field across those failures. Dirty, newer and checksum-drifted
ledgers are rejected before a snapshot is created. A successful upgrade exposes
the `BackupFile` through `Database.UpgradeBackup`; if a later migration fails,
`UpgradeError.Backup` retains the same verified pre-upgrade metadata. The source
may retain its dirty ledger entry under the existing forward-only policy.

The explicit one-shot destination avoids hidden backup naming and unbounded
accumulation. Retention and deletion remain caller responsibilities. This
checkpoint relies on the documented core single-owner lifecycle while `Open`
runs; it does not claim cross-process fencing.

Restore lifecycle is not implemented here. Future restore selects a new,
verified database path only after the old handle is drained and closed, while
retaining the old database file; no rename over a live database is required.

Local evidence:

```text
go test ./backend/internal/adapters/sqlite
go test ./backend/...
go vet ./backend/...
git diff --check
```

Independent integration verification also passed the focused upgrade tests 25
times, full backend tests/vet, formatting, architecture-document and diff checks,
and govulncheck. Remote CI is required before this delivery is marked complete.

Production deployment, destructive cleanup and history rewriting remain outside
this checkpoint.
