# PI-08/B1 acceptance audit

Status: **software checkpoint accepted**.

This audit cross-checks the five B1 test families required by
[`pi-08-plan.md`](pi-08-plan.md) against the parser checkpoint, atomic SQLite
import, repeated local verification and exact-head CI evidence.

| Required evidence | Disposition |
| --- | --- |
| `TestCatalogImportDispositions` | Accepted: the complete safe artifact, every record snapshot and every explicit disposition are retained atomically without accepting facts or allocating identities. |
| `TestCatalogImportArrayAmbiguity` | Accepted: crossed, equal-unproven and unequal legacy arrays remain explicit; no positional pairing or first-hit winner is inferred. |
| `TestCatalogImportNoAuthority` | Accepted: only a current core source registration and capability admit import; source spoofing, revocation and changed batch/source/report replay fail closed. |
| `TestCatalogImportUnsupportedFields` | Accepted: safe unknown fields receive dispositions while prohibited sensitive fields reject the whole batch before retaining bytes, digest or rows. |
| `TestCatalogImportBounds` | Accepted: exact batch, record, staging-byte and staging-batch limits are checked before writes; replay is not double-charged. |

Commit [`9d73464a`](https://github.com/shentschel/teddycloud/commit/9d73464a448c6cb14356979c1e3bbffbb6165e6f)
binds the already accepted offline parser to trusted source authority and an
additive immutable SQLite import schema. Artifact, report and snapshots commit
in one transaction. Injected failures at each write stage, cancellation,
authorization loss and callback failure leave no partial batch; exact replay
survives restart while changed same-ID input is rejected.

Commit [`4b0fd82d`](https://github.com/shentschel/teddycloud/commit/4b0fd82d625bbd0c889ef99392c916dfd85729c2)
closes the final migration-fixture gaps and verifies the exact JSON report size
before the encoder allocates its complete output. The measured size is checked
against the encoder result and retained-byte limit.

Pinned Go 1.27.1 Docker verification passed the relevant suite, all catalog
tests ten times, the relevant race suite, full backend tests, vet, architecture
documentation and whitespace checks. [Next CI 39011982434](https://github.com/shentschel/teddycloud/actions/runs/39011982434)
completed all four jobs successfully for the exact final commit. The parent
independently reviewed the final diff and CI jobs.

No unresolved B1 High, security or data-loss finding is known. B2, independent
T and the PI-08 versioned-catalog-query milestone remain undelivered. Physical
hardware/power-loss evidence remains explicitly unproven and outside B1.
