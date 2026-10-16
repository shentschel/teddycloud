# PI-08/A2 acceptance audit

Status: **software checkpoint accepted**.

This audit cross-checks the seven A2 tests required by
[`pi-08-plan.md`](pi-08-plan.md) against implementation, repeated execution and
remote CI evidence.

| Required evidence | Disposition |
| --- | --- |
| `TestCatalogOnlyVersionQuery` | Accepted: catalog-only rows, independent identities, bounded no-match/unique/ambiguous reads. |
| `TestCatalogManagedSaveGuard` | Accepted: managed subjects reject ordinary Save, including rollback/cancellation paths. |
| `TestCatalogBaselineAdoption` | Accepted at `ff27c6bf`/`6d5c758b`: explicit capability, digest and revision fence; no implicit review or legacy-row mutation. |
| `TestCatalogSchemaRecovery` | Accepted: real v4-to-v6 upgrade and exact current/pre-PI-08 restore. |
| `TestCatalogCorruptRelations` | Accepted at `236d5171`: broken support, missing bindings and dangling receipts fail closed while originals remain byte/row intact. |
| `TestCatalogRevisionRace` | Accepted: independent SQLite owners yield one winner and one fenced loser. |
| `TestCatalogQueryBounds` | Accepted: stable bounded pagination and stale-cursor rejection. |

Further A2 evidence covers exact unsupported-field retention, Review/Order
atomicity and replay, query-after-Order correctness, helper termination before
commit and after durable commit, lost-response lookup/replay, FK verification,
and snapshot preservation.

The parent independently read the three final commits and verified both final
workflow runs. [Next CI 38955557089](https://github.com/shentschel/teddycloud/actions/runs/38955557089)
and [Next CI 38957341835](https://github.com/shentschel/teddycloud/actions/runs/38957341835)
completed all four jobs successfully.

No unresolved A2 High, security or data-loss finding is known. The sporadic
cancellation-test investigation remains tracked technical debt because repeated
and remote acceptance runs passed; it is not evidence of accepted cancellation
failure. Physical power-loss/hardware evidence remains explicitly unproven and
outside this software checkpoint. B1, B2, independent T and the PI-08 milestone
remain undelivered.
