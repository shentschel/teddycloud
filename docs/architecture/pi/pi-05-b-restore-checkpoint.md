# PI-05/B closed and fenced restore lifecycle checkpoint

Status: independently verified implementation; remote publication check pending;
PI-05/T remains open.

`LifecycleOwner` is the single-process core owner of one SQLite handle. It
implements the application catalog transaction port without exposing
`Database`, `database/sql`, driver values or storage errors. Its context-aware
gate serializes transactions, version reads, close and restore, gives a waiting
lifecycle action priority over later callbacks, and drains the active callback
before closing its handle.

Restore verifies the snapshot and requires an unused, non-current destination
with no stale `-wal` or `-shm` sidecars before the old handle closes. A preflight
failure leaves the current handle usable. After close it restores only to the
new path and reopens exactly `migrations[:snapshot.SchemaVersion]`; it does not
immediately upgrade an older snapshot. Selection happens only after that handle
and version verify. A later restore or open failure leaves the owner closed and
fail-closed, while the original database remains recoverable. Persistent-WAL
file control keeps the old database sidecars rather than renaming or deleting
them.

This is a database-only prototype. It neither restores external content or
credentials nor persists a production configuration switch. The gate assumes
one owner in one process and makes no cross-process locking claim. There is no
production operation, rename-over-live or deletion in this checkpoint.

Focused tests prove transaction drain and restore priority, deadline rejection
without callback entry, successful switch and subsequent use, preservation of
newer committed state at the original path, exact older-schema reopen,
preflight rejection of same/existing destinations, stale sidecars and tampered
snapshots, and fail-closed behavior after a post-close restore failure. They
also check that lifecycle transaction failures expose stable boundary errors,
not SQLite driver errors or SQL diagnostics.

Independent verification passed 25 repetitions of the lifecycle tests, the full
backend suite and vet, architecture docs, formatting/diff checks and govulncheck.
Integration additionally preserves verified upgrade snapshot metadata through
successful owner opens and sanitized failed-upgrade errors. The owner reports
that metadata without exposing the underlying database. CI must pass before this
delivery is marked complete.
