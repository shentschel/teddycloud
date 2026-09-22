# PI-05/B database backup and restore checkpoint

Status: local smoke test passed; PI-05/B remains open.

The SQLite adapter now creates a database-only snapshot through the selected
driver's online backup API. It verifies SQLite integrity, the migration ledger
and a SHA-256 digest before publishing a new backup path. Restore checks the
digest and schema, uses the driver's restore API in a temporary database, then
publishes a new path without replacing an existing file.

The smoke test writes Content while the source database is open in WAL mode,
creates a snapshot, changes the Content afterward, closes the source and
restores the earlier value. It reopens both databases and checks schema version
and committed values. A tampered snapshot is rejected and an existing restore
destination remains untouched.

This is a database-only primitive. It does not back up external TAF files or
credentials and does not switch a live database path. Upgrade fencing,
pre-upgrade backup failure handling, bounded contention, restart behavior and
reader-pool evidence remain in PI-05/B.

Local evidence: focused backup/restore tests, `go test ./backend/...`,
`go vet ./backend/...`, architecture-document checks and diff checks passed.
Remote CI and artifact reproducibility evidence are pending publication.
