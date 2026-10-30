# PI-08/T independent audit checkpoint

Status: **F02, PI-08/T and the versioned-catalog-query milestone accepted as
software checkpoints by independent D2m review (2026-10-30)**.
Reviewed basis: `8a4764a53541c36cb121fdde904db5b902a60eb7`, including M1
implementation/tests at `fae704ab5365c20da4af590f27429266833cdaa1` and the
normative D2l CI-tier disposition. **No open High, Medium, security or data-loss
finding in this acceptance scope.** M1 and M2 are independently closed; see
[D2m evidence and limits](#f02-d2m-final-independent-acceptance-2026-10-30).
Hardware, deployment, production migration and physical power-loss remain
explicitly unverified. Earlier D2j/D2l acceptance restrictions below are historical;
accepted F01/R/A/B evidence and the bounded D2l residual risk remain intact.

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

Historical status at that checkpoint:

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

Historical record: all findings, source line references, gate dispositions and
fresh-test claims in this section refer to `fbc8f999`, before M1 remediation
and the D2l contract decision. The current status is given above and in D2m below.

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
an accepted unreachable-boundary disposition; at that checkpoint, remaining
decisions were M1's scoped implementation follow-up and M2's explicit delivery-tier
disposition. Existing accepted F01/R/A/B checkpoints are not reopened. No issue, status, workflow,
production or test file is changed; no push, deployment or production migration.

## F02 D2l CI-tier contract disposition, 2026-10-30

Historical acceptance status: D2m below completes the independent review reserved
here. The normative CI allocation and its accepted residual risk remain binding.

Request: `pi08-t-f02-d2l-ci-tier-contract-disposition-20261030`.
Basis: `fae704ab5365c20da4af590f27429266833cdaa1`.
**D2j-M2: closed.** The explicit user decision accepts the exact existing
[D2l normative CI tier](pi-08-f02-supersession-contract.md#d2l-accepted-ci-tier-disposition-2026-10-30)
as a bounded risk-based allocation. The mandatory uncached non-race one-shot
runs all four command-authored accounting fixtures (Bytes, Observations, Events,
RegisterContentBytes). Only observation/event fixtures are excluded from SQLite
`-count=10`; only those four fixtures are excluded from the uncached race lane.
Both byte fixtures still repeat ten times. All other SQLite tests retain tenfold
and race coverage, with unchanged selectors, assertions and operation deadlines.
The contract records the exact commands, package set and unchanged other gates.

The accepted residual risk is lower repetition evidence for observation/event
boundaries and absent race instrumentation of the four complete large-fixture
paths. Intermittent or path-specific races may escape; ordinary owner/concurrency/
cache race tests and focused evidence do not prove those large paths race-free.
No test, accounting limit, normative 10s/5s deadline or functional gate is waived.
Expanding the exclusions requires a new explicit disposition.

**D2j-M1: remediation implemented; exact-head CI green.** The request supplies
pre-mutation evidence and implementation at `fae704ab5365c20da4af590f27429266833cdaa1`
with successful [Next CI 41070315658](https://github.com/shentschel/teddycloud/actions/runs/41070315658).
This updates the historical open-implementation status; this docs task neither
repeats the implementation review nor independently closes M1 acceptance.

**Remaining blocker:** separate independent final F02/PI-08/T and milestone
acceptance under existing #28. M2 is no longer a blocker. The D2j gate tables and
line-number findings above describe the old inspected head and must not be read
as current M1/M2 status. Accepted F01/R/A/B evidence remains intact.

This closeout uses architecture documentation/relative-link validation,
workspace-boundary validation, unstaged/staged diff checks and exact three-file
scope review. `go test -count=1 ./backend/internal/architecture` also passes in
a read-only Go 1.27.1 container (2.647s package time). No full backend, SQLite
matrix, fuzz or CI rerun is needed for this disposition. Optimize context/polling
and avoid duplicate full validation; observe the contract's limit of at most
2–3 complete jobs per 5h window and budget reserve. Only this
audit, the F02 contract and PI README are committed. No push or GitHub issue edit;
CI 41070315658 belongs to the basis implementation, not this new docs commit.

## F02 D2m final independent acceptance, 2026-10-30

Request: `pi08-t-f02-d2m-final-independent-acceptance-20261030`.
Basis: `8a4764a53541c36cb121fdde904db5b902a60eb7` (clean at review start).
**Verdict: F02, PI-08/T and the versioned-catalog-query milestone are accepted
as software checkpoints.** The only outstanding D2j findings, M1 and M2, are
closed independently below. This bounded review inspected `6f9f181e..fae704ab`
and the D2l documentation change; it does not repeat D2j's accepted gates.

### Findings by severity and disposition

- **High / security / data loss:** no open finding in the reviewed delta and
  its consistency with the accepted D2j evidence.
- **Medium D2j-M1: closed.** Prospective combined admission precedes write
  enablement and the first mutation; actual persisted accounting remains required.
- **Medium D2j-M2: closed.** The exact delivered CI allocation matches the
  explicitly accepted D2l contract, including its residual risk. No exclusions
  are broadened.
- **Low:** no additional finding in this bounded review.

### M1 independent implementation and evidence review

Paths here are relative to `next/backend/internal/adapters/sqlite/`.
`catalog_registration.go`'s `registrationAccountingProspective` sums the existing
F01 SQL charges, existing F02 usage and the complete pending delta. It selects
pending order logical usage instead of adding it to the old logical usage;
physical order snapshots and v2 manifest rows remain additive. Registration's
order advance counts once, through the registration event. The shared helper
still supports schema 8 and adds F02 persisted usage only for schema 9.

| Writer / concern | Independently verified disposition |
| --- | --- |
| `CommitRegistration` | Before `query_only=OFF`, admits canonical command/result, initial review and order snapshots, all registration source/support index charges, new normalized observations, the complete pending v2 version manifest, one event and selected links. The precomputed manifest includes the newly registered version and is the same set written afterward. Both registration APIs retain the final `registrationAccounting` check before replay reconstruction. |
| `CommitReview` | Validates the successor against domain Apply, then decodes it without a repository or write projector. Charges the new snapshot and this command's exact canonical command/result slices, one event, distinct selected support and absent observations. All applicable sources/coordinates are included before enabling writes; final persisted accounting precedes readback. |
| `CommitOrder` | Charges the entire new physical snapshot and each applicable v2 manifest row, one event and absent provenance. Pending canonical order history supplies the complete deduplicated logical observation/support counters; prior logical usage is replaced, not charged twice. Final persisted accounting remains before readback. |
| Dedup and encoding | Sources use `(source_id, registration_revision)`, batches their ID, records `(batch_id, record_index)`, observations their ID, both within the command and against the leased snapshot. Record reuse requires exact locator/snapshot equality, preventing a smaller conflicting input from understating charges. Ordinary text charges preserve SQLite `length(TEXT)` Unicode-character semantics; canonical BLOB lengths remain byte lengths. Registration syntax rejects duplicate observation/selection IDs; review support and order evidence retain their canonical dedup semantics. |
| Adoption / import | Adoption admits an absent source, one new batch and both record snapshots globally before writes. It initializes bounded revision-1 review/order state without decisions, observations or support, and retains the final per-Content/global check. Import is byte-for-byte unchanged: it pre-admits source/batch and artifact/report/record bytes, checks persisted global usage afterward and creates no Content event. Neither path is treated as a support-growing writer. |
| Replay / final check | Exact replay branches precede prospective growth and write enablement. The driver observer positively detects accepted writes, then proves replay attempts no write and preserves the complete snapshot. Deliberate trigger-induced growth after successful admission is rejected by final persisted accounting with full rollback for all eight writer cases. |

`TestSupersessionSQLitePrewriteObserver` covers eight writer cases: both
registrations, RecordObservations, AcceptSupport, DecideOrder, adoption, import
and supersession. `PrewriteProvenance` checks reachable exact/+1 global budgets
with typed errors, zero write attempts on rejection and complete row/schema
comparisons; its unrelated global occupancy is explicit SQL test data, not
command-authored milestone evidence. `PrewriteReuse` checks full-budget reuse,
duplicate observations/support/sequence evidence, Unicode and conflicting record
snapshots. `PrewritePersistedRecheck` proves final checks remain effective.
The four existing combined-boundary fixtures now assert zero write enablements
and zero DML attempts on over-limit rejection as well as typed errors, complete
rollback and replay. Their existing reachable-boundary assertions are retained.

No concrete counterevidence reopens D2j's accepted authority, canonical identity,
cache/FK, graph/query, concurrency, migration/recovery, media/assignment,
command-authored milestone or support-count dominance dispositions. Closing M1
completes D2j's C/accounting gate; closing M2 completes its delivery-tier gate.
Existing F01/R/A/B acceptance remains unchanged.

### M2 and exact-head CI

All five commands in the [normative D2l tier](pi-08-f02-supersession-contract.md#d2l-accepted-ci-tier-disposition-2026-10-30)
were compared verbatim with `.github/workflows/next-ci.yml`. All four accounting
fixtures remain mandatory uncached non-race one-shots. Only observations/events
leave SQLite tenfold repetition; only the four named fixtures leave race. Both
byte fixtures and the new Prewrite tests remain in tenfold and race coverage.
The 10s/5s operation deadlines, package/job timeouts and other gates are unchanged.
The expressly accepted risk remains intermittent failures in the two unrepeated
full-boundary paths and races unique to the four uninstrumented large paths.
Ordinary race/concurrency coverage does not eliminate that limitation. Further
exclusions require a new explicit disposition.

Live GitHub run metadata was read at **2026-10-30 19:59:11 UTC**. Both runs were
already completed successfully, attempt 1; no queued result is treated as green
and no repeat status polling was needed.

| Run | Exact head and verified result |
| --- | --- |
| [41070315658](https://github.com/shentschel/teddycloud/actions/runs/41070315658) | `fae704ab5365c20da4af590f27429266833cdaa1`; **TeddyCloud Next CI**, success, run metadata updated 14:54:19 UTC. All four jobs succeeded, including the otherwise nonblocking advisory job. |
| [41119529097](https://github.com/shentschel/teddycloud/actions/runs/41119529097) | `8a4764a53541c36cb121fdde904db5b902a60eb7`; **Check architecture documentation**, success, run metadata updated 19:58:23 UTC. The check job and its six validation/checkout steps succeeded. This is documentation CI, not a second Next CI run. |

Both inspected Next job logs contain the exact implementation checkout SHA.
Persistence job `124112369060` records the mandatory four-fixture one-shot
(146.546s), SQLite tenfold lane (1075.455s), SQLite race lane (812.468s), qualified
contentfs tenfold run (55.538s) and successful Darwin compilation. Deterministic
job `124112369067` records `make -C next check`, vet and full backend tests
(SQLite 242.982s), browser smoke and artifact checks. Reproducibility also passed.
The diff from the M1 head to the D2l head changes only these three documents;
production, tests and the Next workflow are identical. CI evidence therefore
covers the reviewed implementation and D2l documentation separately. It is not
claimed as remote CI for this subsequent, unpushed D2m documentation commit.

### Focused local validation and closeout

Fresh validation used Go 1.27.1 in the same read-only `/src` container setup and
qualified `/testtmp` volume described in D2j above, from `/src/next`:

```sh
go test -count=1 -json -run '^TestSupersessionSQLite(Prewrite(Observer|Provenance|PersistedRecheck|Reuse)|Combined(WriterBytes|WriterObservations|WriterEvents|RegisterContentBytes))$' ./backend/internal/adapters/sqlite
go test -count=1 ./backend/internal/architecture
```

All eight selected top-level SQLite tests passed, with no failures or skips;
package duration **281.109s**. Architecture tests passed in **1.236s**.

| SQLite test suffix | Duration |
| --- | --- |
| `PrewriteObserver` | 0.88s |
| `PrewriteProvenance` | 50.13s |
| `PrewritePersistedRecheck` | 0.92s |
| `PrewriteReuse` | 3.59s |
| `CombinedWriterBytes` | 9.15s |
| `CombinedWriterObservations` | 120.84s |
| `CombinedWriterEvents` | 94.57s |
| `CombinedRegisterContentBytes` | 1.01s |

No full backend, race matrix, fuzz or previously accepted D2j gate was rerun.
Architecture documentation/relative links, internal links added by D2m,
workspace boundaries, unstaged/staged whitespace checks and the exact three-file
write scope pass. Only this audit, the F02 contract and PI README are committed;
production, tests, workflow, issue #28 and status files are unchanged. No push.

Acceptance is limited to the reviewed software checkpoint. Hardware behavior,
deployment, production migration and physical power-loss remain explicitly
unverified. Existing process-crash/restore evidence does not establish physical
power-loss safety. D2l's exact residual repetition/race risk remains accepted.
