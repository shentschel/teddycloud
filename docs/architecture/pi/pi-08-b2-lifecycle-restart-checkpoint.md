# PI-08/B2 lifecycle and restart checkpoint

Status: **bounded software checkpoint accepted**.

Commit [`016d46c9`](https://github.com/shentschel/teddycloud/commit/016d46c95e762e91c05bdc8cd660bdf4a7146171)
adds the lifecycle-fence and real helper-process restart evidence required by
PI-08/B2 without changing production code.

`TestCatalogLifecycleFence` proves bounded cancellation while a catalog
callback is active, zero execution for queued work that is cancelled, shared
restore/close ownership, rejection of escaped repositories and stale content
sessions, and restore cursor invalidation. Queued catalog, tag and content work
either uses the new owner generation after restore or fails closed after close.

`TestCatalogRestart` kills a real helper process before an uncommitted catalog
Order write can finish, reopens the database and compares exact rows,
revisions, version facts and receipts. A second lost-response phase proves that
the durable commit is retained after process death and that the callback ran
exactly once. Bounded deadlines distinguish termination from expiry.

Parent review covered both new test files and confirmed that no production,
receipt-encoding, media-verification or previously accepted PI-07 behavior was
changed. Local Go 1.27.1 verification passed the named tests ten times, the
relevant package suite, full backend tests and vet. Exact-head CI initially
exposed an unrelated newly published Vite advisory. Commit
[`a7816fc4`](https://github.com/shentschel/teddycloud/commit/a7816fc40cd3b9f9e10ac8603c467ef38b3aef3f)
updates only the direct Vite pin and generated lock data to 8.3.3. The audit
then reported no known vulnerability.

[Next CI 39532388820](https://github.com/shentschel/teddycloud/actions/runs/39532388820)
completed all four jobs successfully for the exact final head, including
SQLite repetition, persistence/domain race evidence, qualified Linux
blob-filesystem tests, non-Linux fail-closed compilation, deterministic build
and smoke checks, dependency advisories and two-directory reproducibility.

This accepts only the B2 lifecycle/restart slice. Uncertain-commit,
sanitized-error and application recovery evidence remain open, as do
independent T and the PI-08 milestone. Physical power-loss evidence remains
explicitly unproven.
