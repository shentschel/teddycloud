# PI-08/T independent audit checkpoint

Status: **D2j checkpoint recorded; F02, T and the milestone are not accepted**.
Latest inspected implementation: `fbc8f9996e2cd8d9842048002f67dbf5a2cda2c5`;
exact-head CI is green. Earlier sections below are historical checkpoints.

The independent audit at
[`a9909431`](https://github.com/shentschel/teddycloud/commit/a99094313a6d1dce53000406b9856768c7dc130c)
added the required policy-permutation, parallel-array, migration and bounded
fuzz evidence without changing production code. Policy permutations, importer
envelope fuzzing, decision-sequence fuzzing and the real v4 migration audit pass.

The initially failing `TestCatalogMilestoneQueryable` was isolated in
[`ab09e3e9`](https://github.com/shentschel/teddycloud/commit/ab09e3e99ace0a1ad8d98841f4d1972445e47bc2).
Its shared recovery fixture intentionally contains both a NUL-bearing title and
an empty non-NULL model number. Catalog queries validate the complete snapshot,
so this malformed nonmatching row correctly fails closed. The test now proves
that each malformed field is independently rejected, repairs only its local
fixture, and then verifies two distinct catalog-only versions, ambiguity,
restart, restore and unchanged history. No production query defect or production
change was found.

Parent review reran `TestCatalogMilestoneQueryable` ten times. The worker also
passed the full backend, vet, architecture and workspace checks. Exact-head
[Next CI 39667376764](https://github.com/shentschel/teddycloud/actions/runs/39667376764)
completed all four jobs successfully, including SQLite repetition, race and
qualified-filesystem evidence.

At that audit checkpoint, two substantive policy gaps were open:

- [PI08-T-F01 #27](https://github.com/shentschel/teddycloud/issues/27): guarded
  `RegisterContent` and `RegisterCatalogVersion` authoring is absent; SQL-seeded
  fixtures cannot satisfy the milestone.
- [PI08-T-F02 #28](https://github.com/shentschel/teddycloud/issues/28): immutable
  version supersession with retained support and cycle/cross-content rejection
  is absent.

Both stable IDs were searched before publication and are linked from the shared
backlog. They blocked PI-08/T and milestone acceptance; accepted R/A/B evidence
is unchanged.

## F02 refinement checkpoint, 2026-10-24

Status: **F02 refined/implementation-ready**. F02 implementation, independent
acceptance, PI-08/T and the milestone remain open.

The parent reports F01-D independently accepted at
`abe0c956e8f38c20aaae966faafe4115feb409e4`, with exact-head
[Next CI 40056183655](https://github.com/shentschel/teddycloud/actions/runs/40056183655)
fully green. Local inspection confirms the registration implementation and
migration 8 at that base; this documentation refinement does not repeat or
replace the parent's F01 acceptance. The earlier absent-registration finding
above is retained as historical audit evidence.

The [F02 supersession contract](pi-08-f02-supersession-contract.md) fixes command,
authority/revision fences, linear immutable chains, canonical bytes/bounds,
retained support, query/reconstruction, unchanged assignments, migration 9,
accounting/recovery and ordered implementation/test gates. Existing order-command
`Supersedes` remains separate. No production code or GitHub issue was changed by
request `pi08-t-f02-refinement-20261024`; #28 remains the existing delivery issue.
Its proposed replacement body is included in the contract for parent review.
F02 and final independent T acceptance still block the milestone; no hardware,
deployment or production migration evidence is claimed.

## F02 D2j independent acceptance checkpoint, 2026-10-30

**Verdict: blocked, not full acceptance.** Reviewed the implementation from the
F01 base `abe0c956e8f38c20aaae966faafe4115feb409e4` through the exact clean head
`fbc8f9996e2cd8d9842048002f67dbf5a2cda2c5`, including A/B/C, D1 `6e26b154`,
D2a `20c5e26d`, D2e accounting `e67bf119`, observation/FK follow-up `50b4aa51`,
event/cache follow-up `442aecb1`, and the final workflow. D2b supplied verification;
D2c/D2e/D2i runtime work is assessed against the actual final CI below. The failed,
nonintegrated D2d fixture is not counted as passing evidence. D2f/D2g conclusions
were checked against current source, not accepted from their summaries.

Paths below are relative to `next/backend/internal`, except the workflow and
contract. The original four Medium-gap list was recovered read-only from the
local D2 checkpoint because the published refinement documents did not contain
it. All dispositions below use current source and freshly executed tests.

### Findings by severity

**High/security/data loss:** none confirmed in this inspected scope. This does
not turn incomplete contract compliance into acceptance.

**Medium D2j-M1 — combined admission occurs after writes in old writers.**
The contract's Migration 9/accounting section explicitly requires prospective
totals before enabling writes, followed by actual-total validation before commit.
`adapters/sqlite/catalog_transaction.go:1156-1207` enables writes, updates the
aggregate/subject and inserts review/provenance/state rows before the combined
check; `:1320-1351` does the same for order. Registration similarly enables
writes at `catalog_registration.go:244` and first admits the new combined total
at `:326`. Domain Apply/encoding checks only their own state; the earlier graph
read checks the old persisted total. Neither includes all pending cross-writer
charges. F02 itself correctly calls `registrationAccountingWith` with
`supersessionAdded` before `query_only=OFF` (`catalog_supersession.go:539-542`).

The passing `TestSupersessionSQLiteCombinedWriterBytes`,
`CombinedWriterObservations`, `CombinedWriterEvents` and
`CombinedRegisterContentBytes` prove the exact result, typed over-limit rejection,
complete rollback and zero-charge replay. Their after-callback measurement seam
(`catalog_supersession_test.go:30-49`) does not assert absence of attempted writes
before budget rejection. The D2h reordering fixes error classification but does
not implement pre-write admission. No committed over-limit state or lost rows
was observed. This is a concrete admission-order/resource-control contract gap,
not a demonstrated durable data-loss defect.

Required follow-up: compute complete prospective charges for registration,
review and order before enabling writes, retain the final persisted check, and
add a write-stage observation proving over-limit rejection before the first
mutation. Check adoption/import's applicable global provenance admission in that
same bounded follow-up (`catalog_adoption.go:193-276`,
`catalog_import.go:122-152`), without inventing per-Content events for import.
Production/test changes require a separately authorized scope; none were made.
Do not silently replace pre-write admission with successful rollback semantics.

**Medium D2j-M2 — original delivery-gate wording and delivered CI tier differ.**
The contract says to repeat SQLite `TestSupersession` tests ten times. The final
workflow explicitly excludes `CombinedWriterObservations|Events` from that
lane, and four accounting fixtures from race. Their assertions are mandatory in
an uncached ordinary one-shot; no fixture is silently omitted from all execution.
This is the intentional D2e/D2i runtime tier, not a failing CI run or a demonstrated
race. Nevertheless green CI does not prove the original unqualified repeat-all
sentence. Before full acceptance, record an explicit contract decision accepting
this exact tier (and its residual repetition/race limitation), or supply the
original repetition/race evidence under unchanged operation deadlines. This
checkpoint does neither silently. The workflow itself was not changed.

### Original D2 Medium gaps

| Original gap | Current evidence and disposition |
| --- | --- |
| Persisted corrupt cycle/branch/query behavior | Closed. `TestSupersessionSQLitePersistedGraphShapes` rewrites canonical command/result, hashes and relation evidence consistently for cycle/branch/merge, verifies unrelated FKs/accounting, then requires unavailable/zero results from reconstruction, readback, history and all ordinary query kinds. No repair writes. |
| Combined exact/+1 across writers, full leak checks | Boundary-result evidence closed for reachable pairs: six byte pairs, four observation pairs and five event pairs, with whole database/schema snapshots and replay. Four support-growing writer pairs are formally dominated as proved in the contract. Pre-write admission remains separately open as M1; the overall accounting gate is not accepted. |
| History cursor after actual restore | Closed. `TestSupersessionHistoryCursorVerifiedRestore` retains the original service/owner, proves a cursor usable, authors later state, restores a verified earlier database to a new path, rejects the old cursor even with the original revisions restored, and reproduces fresh canonical history without writes. |
| Independent 10s fuzz and catalog domain/application race | Closed at this exact head. Fresh 10s/parallel=2 fuzz and uncached focused domain/application race both pass; commands/results below. |

### A/B/C/D and test-family gate dispositions

| Gate | Evidence and disposition at the inspected head |
| --- | --- |
| A: grant, canonical bytes, immutable graph, bounds | Domain `TestSupersessionGoldenVectors`, `Bounds`, `RetainedBounds`, `CanonicalMutationAndOrder`, `Decoding`, `StagingBounds` pass. Fixed literals, independent framing, owned copies, all four fences and complete graph replay inspected. The domain has no storage/assignment write path. |
| B: application and lifecycle | Application `TestSupersessionApplicationAuthority`, `ReplayReadback`, `MalformedResponseRollback`, `Lifecycle`, `IndeterminateDominatesCancellation` and history contract/bounds pass. `SupersessionService.run` validates responses inside the existing callback; no nested transaction or automatic retry. |
| C: schema, old-writer integration and accounting | Migration 9 is active; the diff only appends to migrations 1..8. Schema dispatch, registration compatibility, rollback/dirty/checksum/future-schema tests pass. Shared event continuity includes F02; registration growth is counted once. Query/collision/replay evidence passes. M1 blocks complete C acceptance. |
| D authority and transition negatives | Domain transitions/authority/successor-support plus SQLite `Fences`, `AuthorityAtomicity`, `EvidenceAndReviewer`, `LaterReviewAndAuthority` pass: same-Content immutable endpoints, isolated target, supported successor, revoked/detached grants and stale fences. Historical revocation remains provenance, not deletion. |
| D replay and all-kind keys | SQLite `Replay`, `KeyCollisions`, `BatchKeys`, `LaterReviewAndAuthority`, integration and durable-response recovery pass. Full canonical command comparison precedes current revision/terminal fences; exact replay returns original result with unchanged rows/charges. |
| D atomicity, cancellation and uncertainty | `RollbackStages` injects before/after aggregate, provenance, edge, evidence and support writes; `PartialRowsAndProvenance` covers new source and second evidence/support insertion. Application lifecycle and SQLite authority/driver-fault cases cover cancellation and uncertainty. `DurableResponseRecovery` distinguishes before/after-commit and cleanup uncertainty with exact authorized readback/retry. |
| D concurrency | `SQLiteConcurrency` and `SQLiteCrossWriterConcurrency` pass for identical/different F02 and F01/review/order competitors. Owner lease, aggregate CAS, callback-once, failure propagation and final authorization inspected; CI retains these tests under race and repetition. |
| D queries, reconstruction and cursor bounds | `HistoryQuery`, `HistoryCursor`, `HistoryBoundsAndAbsent`, `HistoryOversizeEntry`, `HistoryMissingCorruption`, persisted graph tests and verified restore pass. Old pair/ID lookup, ambiguity, whole-chain filtering, retained source revocation, page bounds and unavailable corruption are preserved. |
| D migration and recovery | `SchemaUpgrade`, `SchemaRollbackAndDirty`, `SchemaFreshAndCorrupt`, `LegacyQueryCompatibility`, helper and integration tests pass. Real SIGKILL before commit and after durable commit/lost response; two fresh owners compare complete rows/accounting. v8 restore disables F02 while F01 continues; backup/original rows remain unchanged. No physical power-loss claim. |
| D media/assignment compatibility | `MediaAssignmentCompatibility` passes real PI-07 attachment/receipt replay, changed owner/audio/hash/order/rebinding rejection, actual old-ID media resolution, reopen and verified restore. Assignment evidence covers immutable domain values and historical/current/future intervals; no persistent assignment repository or hardware claim. |
| D command-authored milestone | `TestCatalogMilestoneQueryable` invokes the F02 integration fixture. Two independent Contents, protected custom facts, dissent/shared-pair ambiguity, two explicit corrections, order independence, old/current/history queries, reopen and actual restore are command-authored. SQL mutation is confined to corruption/migration fixtures, not milestone authoring. Evidence passes; milestone acceptance remains blocked by the overall gates. |
| D independent acceptance and CI | Exact implementation head reviewed; all four CI jobs green. M1 and M2 remain open; F02/T/milestone are not accepted and issue #28 is unchanged. |

### Transaction-cache review

`catalog_repository.go:432-574` retains one copied manifest and one successful
FK check per leased repository, keyed by Content where applicable and by
connection-local `total_changes()` plus `schema_version`. DML, trigger effects
and DDL invalidate the stamp. A separate owner's writes cannot alter an already
established SQLite transaction snapshot. Each callback creates a new repository;
lease revocation and generation checks reject escaped ports after exit/restore.

`catalog_supersession.go:775-820` retains at most two graph reconstructions,
including exact source/reviewer/namespace handles and AllowUnbound in the key;
binding slices are cloned. Reconstructed state owns its slices and returned
history/version/result accessors copy them. Only successful validation is cached.
Authority is checked outside reuse on the command path and again before commit;
no cached state itself grants authority. The separate context read cache is
created inside a single mutex-protected read-only Query/QuerySupersession call;
it cannot span a writer call or callback.

`catalog_transaction.go:410-590,720-766` shares successful exact SQL predicates
only within one unchanged reconstruction; keys frame query and typed arguments,
including nil versus empty BLOB. Every canonical envelope is still decoded and
replayed. Supplied manifests/FK results are shared only among those read-only
cross-reference checks. Statements are callback-local and closed at exit.

`TestSupersessionSQLiteSnapshotMutation` passes manifest DML, schema DDL and
deferred FK-corruption cases within the same transaction, requiring fresh failure
and full rollback. `ForeignKeyRecheck` covers later corruption after a prior
successful reconstruction. Both remain in CI's tenfold and race lanes. No cache
invalidation/authorization bypass was found for supported production operations;
arbitrary hostile SQLite pragma/schema manipulation is not an authorization API.

### Exact-head CI and local validation

Live GitHub run metadata confirms run `40989601324`, attempt 1, push head
`fbc8f9996e2cd8d9842048002f67dbf5a2cda2c5`, conclusion success. The checkout SHA
also appears in both inspected job logs. Four jobs succeeded: deterministic
build/smoke, persistence/domain race, reproducibility, and advisory. The advisory
job is nonblocking by workflow design, but actually succeeded here.

Persistence job `123848529399` proves the mandatory four-fixture one-shot
(142.374s), the SQLite tenfold lane excluding only observation/event fixtures
(627.589s), and the race lane excluding only the four accounting fixtures
(721.556s), plus qualified contentfs repetition and Darwin boundary compilation.
Deterministic job `123848529442` proves `make -C next check`, full backend tests
(SQLite 216.999s), vet, build and smoke. No remote rerun was requested. These
results belong to the implementation SHA, not to the subsequent documentation
commit; that new commit has not been pushed or tested remotely.

Local commands used `golang:1.27.1-bookworm` with the repository mounted read-only
at `/src`, cwd `/src/next`, volumes `teddycloud-pi08-go-mod:/go/pkg/mod`,
`teddycloud-pi08-go-build:/root/.cache/go-build` and
`teddycloud-pi08-testtmp:/testtmp`, and
`TMPDIR=/testtmp TEDDYCLOUD_CONTENTFS_TEST_ROOT=/testtmp`:

```sh
go test -count=1 -v -run 'TestSupersession|TestCatalogMilestoneQueryable' ./backend/internal/domain/catalog ./backend/internal/application/catalog ./backend/internal/adapters/sqlite
go test -run '^$' -fuzz '^FuzzCatalogSupersessionState$' -fuzztime=10s -parallel=2 ./backend/internal/domain/catalog
go test -count=1 -race -run '^TestSupersession' ./backend/internal/domain/catalog ./backend/internal/application/catalog
```

All passed: 60 top-level focused tests, no top-level skips; domain/application/
SQLite package durations 0.762/0.020/204.292s. The event fixture authored 1,023
events below the other bounds; all five exact/+1 writer pairs passed. Fuzz ran
109,007 executions in 11.035s; race package durations were 17.284/1.102s.
Local full-backend/make/race-SQLite suites were not duplicated; their exact-head
CI evidence is identified above.

Documentation closeout runs `python3 scripts/check_architecture_docs.py`
(including relative links), `python3 next/tools/check_workspace_boundaries.py`,
`git diff --check`, `git diff --cached --check`, and exact three-file scope review.
Only this audit, the F02 contract and PI README are changed. The support proof is
an accepted unreachable-boundary disposition; remaining decisions are M1's scoped
implementation follow-up and M2's explicit delivery-tier disposition. Existing
accepted F01/R/A/B checkpoints are not reopened. No issue, status, workflow,
production or test file is changed; no push, deployment or production migration.
