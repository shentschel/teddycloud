# PI-05/B contention checkpoint

Status: **Bounded contention checkpoint; PI-05/B remains open**

The Content repository maps SQLite BUSY/LOCKED exhaustion to the application-owned
`catalog.ErrRepositoryContention`. Save retries only its single idempotent SQL
statement within the configured busy budget; it never replays the user operation.
A short polling interval observes context cancellation without waiting for the
entire driver timeout. The single-connection writer boundary is unchanged.

Independent review found that automatic rollback could prevent transaction-local
cleanup from restoring the original busy timeout. The transaction now holds its
connection until post-rollback cleanup restores the setting. A regression test
verifies the original timeout after cancellation. Contention and cancellation
classification do not expose the underlying SQLite error through their unwrap
chain. General adapter-error hardening remains PI-05/T work.

Evidence: two real handles on a temporary WAL database; bounded lock exhaustion,
exactly one callback invocation, rollback, successful subsequent save, context
cancellation and connection-setting restoration. Full backend tests and vet,
25 repetitions of the contention tests, architecture-document and diff checks
passed before publication. [Next CI](https://github.com/shentschel/teddycloud/actions/runs/36267119143)
and [documentation CI](https://github.com/shentschel/teddycloud/actions/runs/36267119145)
passed for commit `7a5524f`; the local govulncheck scan also reported no vulnerabilities. SQL LOCKED shares the primary-code mapper but
has no separate real-lock fixture. Local race evidence remains unavailable
without a C compiler; no production or hardware durability claim is made.

Still open: bounded reader behavior, interrupted-write restart, verified
pre-upgrade backup failure fencing and coordinated closed/fenced restore.
