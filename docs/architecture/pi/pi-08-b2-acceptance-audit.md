# PI-08/B2 acceptance audit

Status: **software checkpoint accepted**.

This audit cross-checks every PI-08/B2 requirement against the three bounded
delivery slices, parent review and exact-head CI evidence.

| Required evidence | Disposition |
| --- | --- |
| `TestCatalogMediaAttachment` | Accepted: verified generated media attaches only to the identical unbound catalog tuple; changed content, audio, hash or order facts fail closed. |
| `TestCatalogReceiptAfterCorrection` | Accepted: later catalog corrections do not change the original PI-07 receipt bytes, version facts or Assignment reference. |
| `TestCatalogLifecycleFence` | Accepted: bounded busy/cancel behavior, shared restore/close ownership, revoked sessions and stale cursors are proven. |
| `TestCatalogUncertainCommit` | Accepted: commit-before/after/lost-response and cleanup failures return a typed indeterminate outcome only when a commit may be durable; authorized readback and exact retry recover safely. |
| `TestCatalogRestart` | Accepted: real helper termination before expiry, reopen and exact row/revision/receipt/version comparisons prove rollback and durable lost-response outcomes. |
| `TestCatalogSanitizedErrors` | Accepted: storage paths, SQL/schema details and sensitive values never cross the adapter boundary; stable typed failures and cancellation remain distinguishable. |

The application recovery tests additionally prove that an uncertain result is
never converted into success, automatically retried or masked by cancellation.
Fresh authorized command readback distinguishes the two externally identical
outcomes. Same-command retry is idempotent, changed-command retry conflicts,
and authorization revocation prevents recovery access.

Commits [`8a95c5b9`](https://github.com/shentschel/teddycloud/commit/8a95c5b944b8da8177c2b95519d500e49898dffd),
[`016d46c9`](https://github.com/shentschel/teddycloud/commit/016d46c95e762e91c05bdc8cd660bdf4a7146171),
[`e7983389`](https://github.com/shentschel/teddycloud/commit/e79833894b8d244673b0ea2018a7d7c6322188cb)
and parent-review correction
[`2834cf3f`](https://github.com/shentschel/teddycloud/commit/2834cf3fdffae439c36daf2fb5b16675bd671466)
contain the accepted implementation and evidence. The correction limits
cleanup uncertainty to transactions for which commit was attempted; read-only
and rolled-back operations remain unavailable rather than indeterminate.

Repeated local Go 1.27.1 verification passed the named adapter and application
tests, relevant suites, full backend tests, vet, architecture and workspace
checks. [Next CI 39568780739](https://github.com/shentschel/teddycloud/actions/runs/39568780739)
completed all four exact-head jobs successfully, including SQLite repetition,
persistence/domain race evidence, qualified Linux blob-filesystem tests,
non-Linux fail-closed compilation, deterministic build/smoke, advisory checks
and two-directory reproducibility.

No unresolved B2 High, security or data-loss finding is known. Independent T
and the PI-08 versioned-catalog-query milestone remain undelivered. Persistent
Assignment recovery remains explicitly deferred to PI-09, and physical
power-loss evidence remains unproven.
