# PI-09 execution plan

Status: **B1 locally complete and published** at
`28b97bf41587a3e0a1158d7ec389ae60eb076831`, with exact-head CI green per the
supplied baseline. B2 is replanned. B2 delivery, independent T and the milestone
**Assignments have one authoritative service** remain pending. This replan has
no remote CI, hardware, deployment or production-migration acceptance.
Original R request `pi09-r-assignment-semantics-contract-20261031`; baseline
`90301bfe2b5badd684229c3d5d140fa975842d91`.

## Scope and decisions

The [assignment policy](pi-09-assignment-policy.md) is normative for future
internal delivery; the [budget](pi-09-budget.md) controls each admission. The
[roadmap](../teddycloud-next-pi-roadmap.md) and [routing](../task-model-routing.md)
select R/T `gpt-6-astra`, A/B `gpt-6.1-sol`, high reasoning. Routing is a requested
role, not proof of the actual executing model or independent review.

R changes exactly these repository files:

- `docs/architecture/pi/pi-09-assignment-policy.md`
- `docs/architecture/pi/pi-09-plan.md`
- `docs/architecture/pi/pi-09-budget.md`
- `docs/architecture/pi/README.md`

No runtime, schema, workflow, existing contract or issue changes. This request's
explicit no-issue instruction overrides the roadmap's default sprint issue flow.
No push/publication or delivery implementation is included in R.

| R acceptance criterion | Decision / future evidence |
| --- | --- |
| Authoritative terms and no heuristic classification | D1/D2; A1 classification permutation and protection tests. |
| Precedence and ambiguity | Explicit version tuple only, no catalog supersession/fallback retargeting; A1/A2. |
| Pure URI admission | D4 closed HTTPS metadata profile, no I/O/authority; B1. |
| History, replay and concurrency | D3/D5 append-only events, derived intervals, canonical replay, CAS and ownership; A1/A2/B2. |
| Compatibility and migration | D6, inherited PI-06/07/08 boundaries and D7 deferrals; B2. |
| Ordered bounded delivery | A1 -> A2a -> A2b.1 -> A2b.2 -> A2b.3 -> B1 -> B2.1 -> B2.2 -> B2.3 -> B2.4 -> independent T below. |
| Budget and exact 5% reserve | Integrated independent-window estimates in budget; no parent double count. |
| Honest status | README links only R completion; delivery tests below are specifications, not results. |

R resolves the prototype mismatch deliberately: retain PI-04-style immutable
interval history unchanged and add a separately named decision ledger/projector.
A new assignment transition can end an earlier effective projection without
editing a historical interval. Original-target baseline is review evidence and
never an automatic default. Custom protection, physical classification, media
availability and assignment are independent dimensions.

## Delivery ownership and sequencing

All paths below are relative to `next/backend`, except explicit documentation
paths. Brace lists are exact filenames; matching tests are named explicitly.
They are future ownership expectations, not this R's write permission. Each
checkpoint requires fresh admission and a reviewed file manifest before starting;
a needed file outside its manifest causes a checkpoint/replan before writing.
A2 owns persistence/activation; B1 owns URI code; B2 owns recovery/query completion;
T owns final consolidation/audit. Do not parallelize their dependent mutations.
Future evidence may update the four R documents and a separately authorized
`docs/architecture/pi/pi-09-review.md`; no other historical document/workflow
change is implicit. Checkpoint evidence must identify exact commit, tests and
remaining work; issue publication still requires separate authorization.

### A1: pure decisions, classification and service boundary

Depends on accepted R and inherited PI-06 identity, PI-07 storage and PI-08 catalog
contracts. Own new `internal/domain/assignment/{classification,decisions,projection,
encoding,limits}.go` and `{classification,decisions,projection,encoding,limits}_test.go`;
new `internal/application/assignment/{service,commands,queries,ports,errors}.go`
and `{service,commands,queries}_test.go`. Retain existing assignment prototype
files unchanged. No adapter/schema/Tag Registry/catalog changes.

Implement D1-D3, bounded evidence, explicit original-target review, all command
kinds, independent original/custom/protection states, canonical replay, desired
queries and one callback-scoped cross-domain port. URI-bearing new commands are
unsupported until B1; absent URI remains valid. No temporary weaker parser or
alternate writer. Use a fake scoped port only for tests; A1 is not durable delivery.

Required tests: `TestAssignmentClassificationPermutations`,
`TestAssignmentOriginalBaselineNoFallback`, `TestAssignmentTimelineProjection`,
`TestAssignmentCanonicalReplay`, `TestAssignmentAuthority`,
`TestAssignmentProspectiveLimits`, `TestAssignmentServiceDependencies`.
Cover equal metadata with distinct identities, spoofed actor, same Tag support,
contrary accepted claims/resolution/reopened conflict, same target/new command,
future and as-known queries, retirement, expected-revision overflow, old prototype
regressions, canonical golden vectors and simultaneous byte/count exact/+1 limits.
Prove unreachable isolated bounds explicitly rather than labeling a byte-limit
failure a successful count-boundary test.

Rollback: source revert; no persistent or public API changes. A1 acceptance alone
must never enable a production assignment path.

### A2: durable assignment transactions and activation

Depends on accepted A1 encodings/limits. Own new
`internal/adapters/sqlite/{assignment_schema,assignment_repository,assignment_transaction}.go`
and `{assignment_schema,assignment_repository,assignment_transaction}_test.go`,
plus `assignment_integration_test.go`. Own extensions to existing
`internal/adapters/sqlite/{schema,lifecycle_owner}.go` and
`{database,lifecycle_owner}_test.go` only for new-schema/owner integration.
A2b additionally owns only schema-compatibility gates in
`internal/adapters/sqlite/{catalog_transaction,catalog_registration,catalog_supersession,catalog_repository}.go`,
and exact-schema expectations in
`internal/adapters/sqlite/{tag_schema,catalog_audit,catalog_adoption,catalog_recovery,catalog_registration_integration,catalog_state_schema,catalog_supersession_schema}_test.go`.
These files currently contain v9-specific behavior/expectations; blanket widening
would be unsafe. No existing migration checksum, receipt codec or catalog/Tag
semantic change is admitted.

A2a owns only `assignment_schema.go` and `assignment_schema_test.go`. Accepted
commit `88d27472b927baf90cdd6d69488f74cdd9ee80cd` stages migration **10 /
0010-assignment-ledger**, without appending it to `SchemaMigrations`; the active
schema remains v9. A1 is complete at this baseline. A2a is a schema foundation,
not activated assignment delivery.

The required smaller replan, request `pi09-a2b-bounded-replan-20261104`, replaces
one-job A2b with **PI09-A2b.1 -> PI09-A2b.2 -> PI09-A2b.3** below. These stable
IDs are sequential acceptance checkpoints, not parallel jobs. A2b remains the
parent scope, not an additional implementation or CI budget. This replan writes
only this plan and `docs/architecture/pi/pi-09-budget.md`; existing README links
already reach both, so no README edit or new checkpoint document is needed.

Schema activation, LifecycleOwner wiring, exact v9/v10 compatibility gates,
synthetic-v11 rejection and public assignment port activation belong together in
PI09-A2b.3. No earlier checkpoint can advertise usable v10, production assignment
authority or durable delivery. All existing migration checksums, codecs, catalog
registration/supersession/accounting checks and the full A2 criteria remain binding.
Implement one-owner reference reads and dependency revision fences in the same
transaction, atomic ledger/projection/command storage and direct-writer guards.
Persist history from the first assignment; B does not backfill missing A history.

Required tests: `TestAssignmentSchemaUpgrade`, `TestAssignmentRestrictiveReferences`,
`TestAssignmentSameRevisionRace`, `TestAssignmentSingleOwnerFence`,
`TestAssignmentCommitAtomicity`, `TestAssignmentNoCatalogRetarget`,
`TestAssignmentCatalogOnlyTarget`, `TestAssignmentStorageBypassRejected`.
Use real SQLite, v9/current-schema fixtures, two same-revision commands, foreign
Content/version tuples, concurrent catalog changes, superseded targets and
unbound versions. Verify managed ownership support, no generic Save bypass, no
nested callbacks and no alteration to Tag/catalog/media receipts. A2 needs basic
restart and pre-upgrade restore smoke evidence; B2 owns the complete failure matrix.

Rollback: before activation source revert; afterwards verified pre-upgrade backup
and fenced new-path exact-schema restore. Preserve post-upgrade DB/media and report
lost newer history. No down migration, production rollout or dual writer.

#### A2b checkpoint rules

Each manifest below is exhaustive, relative to `next/backend/internal/adapters/sqlite/`.
No implicit glob, application/domain change, workflow edit or unrelated integration
fix is allowed. Shared files have explicit sequential ownership below. Evidence
updates to the two replan documents require inclusion in the next dispatch's exact
manifest and estimate; they are not a blanket four-document write grant. An
out-of-manifest need stops that slice for replan before edits.

PI09-A2b.1/.2 use real SQLite and explicitly staged migration fixtures only, via
A2a's `assignmentSchemaMigrations()` test helper. Their production components
must be private, with no exported constructor, LifecycleOwner method, runtime
call site or production implementation wired to the public Transactor. Only
`_test.go` harnesses may instantiate them. A private component may consume an
already-owned SQL transaction; it must not open a nested transaction, invoke Tag
or catalog transactors, offer unmanaged Save, or masquerade staged v10 as v9 to
pass a catalog gate. Dependency reads must use that same transaction. Fixtures
must establish real managed ownership/support, not treat A2a's synthetic blobs as
canonical application receipts. Default `SchemaMigrations()` stays at v9 and
normal runtime opens cannot create assignment tables. Both checkpoints verify
these boundaries by tests and call-site/export review. Fixture persistence is
component evidence only; live authority and delivery remain unaccepted.

#### PI09-A2b.1: inert repository and exact reference reconstruction

Depends on accepted A1/A2a and this replan. This is the **first safely dispatchable
slice**, subject to fresh budget admission; no later slice is dispatched with it.
Exact write manifest (two new files):

- `assignment_repository.go`
- `assignment_repository_test.go`

Deliver bounded canonical snapshot/command readback and exact Tag/Content/version
reference reconstruction on an injected transaction. Validate retained head,
projection, event, evidence, support, command indices and usage; reject mismatches
and foreign tuples. Keep writes and transaction ownership out of this checkpoint.
A complete repository read contract is independently useful to the next slice;
no partial writer is exposed.

Acceptance: `TestAssignmentRepositoryRoundTrip`,
`TestAssignmentRepositoryCorruption`, `TestAssignmentRepositoryInert`, plus the
required A2 tests assigned in the coverage table. Use canonical A1-generated
material seeded in the test harness, including first-event history, global command
lookup, unbound/catalog-only versions, superseded exact targets, foreign tuples
and genuine managed ownership support. Corrupt each retained structure and reject
it; distinguish absent data from corrupt data. Inert test and source review prove
v9 default schema and absence of production reachability.

Exact focused commands from `next` (all five named tests must be discovered):

```sh
go test -list '^TestAssignment(RepositoryRoundTrip|RepositoryCorruption|RepositoryInert|NoCatalogRetarget|CatalogOnlyTarget)$' ./backend/internal/adapters/sqlite
go test -count=1 -run '^TestAssignment(RepositoryRoundTrip|RepositoryCorruption|RepositoryInert|NoCatalogRetarget|CatalogOnlyTarget)$' ./backend/internal/adapters/sqlite
go test -count=10 -run '^TestAssignment(RepositoryRoundTrip|RepositoryCorruption|RepositoryInert|NoCatalogRetarget|CatalogOnlyTarget)$' ./backend/internal/adapters/sqlite
go test -race -count=1 -run '^TestAssignment(RepositoryRoundTrip|RepositoryCorruption|RepositoryInert|NoCatalogRetarget|CatalogOnlyTarget)$' ./backend/internal/adapters/sqlite
```

Then run the common A/B verification gates below, including the unchanged A2a
schema tests. Rollback: revert these two inert files; no runtime database was
upgraded. Non-claims: no atomic writer, owner fence, schema activation, production
port or durable delivery. Handoff records the accepted commit, exact fixtures,
checks/durations and remaining PI09-A2b.2/.3 obligations.

#### PI09-A2b.2: inert atomic transaction components

Depends on accepted PI09-A2b.1. Exact write manifest:

- new `assignment_transaction.go`
- new `assignment_transaction_test.go`
- extend `assignment_repository.go` only for private guarded append/CAS and
  atomic installation of the validated ledger, projection, command index and usage
- extend `assignment_repository_test.go` only for those write/readback regressions

Complete the private callback-scoped port mechanics over one supplied transaction,
with authenticated actor input from the trusted harness, dependency revision fences,
first-event history, global CommandID uniqueness, revocation and cancellation.
The harness owns the transaction lifecycle; production LifecycleOwner dispatch and
public Transactor implementation remain absent. Do not create a temporary second
owner or an exported Save to make tests convenient.

Acceptance: `TestAssignmentTransactionScope` and
`TestAssignmentTransactionInert`, plus the required A2 tests in the coverage table.
Use two real SQLite connections for same-revision commands; exactly one succeeds.
Test concurrent Tag/catalog revision changes, global command collision across
Tags, replay versus conflicting reuse, atomic failure injection before commit,
callback errors/cancellation, retained-port reuse and nested-callback rejection.
Prove ledger/projection/indices/usage roll back together and original
Tag/catalog/media receipts remain unchanged. No timeout weakening or retrying the
callback. Run focused uncached, tenfold and race tests, previous component/schema
regressions and the common A/B gates.

Rollback: revert this checkpoint while retaining the accepted read-only component;
no default-schema upgrade or live write path exists. Non-claims: component CAS
and rollback tests do not establish LifecycleOwner restore fencing, production
assignment authority, usable v10 or durable delivery.

#### PI09-A2b.3: coordinated activation and recovery smoke

Depends on accepted PI09-A2b.2 and fresh admission of the entire following manifest.
This is the smallest indivisible activation boundary; if it cannot fit, stop and
replan inert prerequisites, never split schema, gates and authority into releases.
Exact write manifest:

- `schema.go`, `lifecycle_owner.go`, `database_test.go`, `lifecycle_owner_test.go`
- `catalog_transaction.go`, `catalog_registration.go`, `catalog_supersession.go`,
  `catalog_repository.go` (only exact schema-compatibility gates)
- `tag_schema_test.go`, `catalog_audit_test.go`, `catalog_adoption_test.go`,
  `catalog_recovery_test.go`, `catalog_registration_integration_test.go`,
  `catalog_state_schema_test.go`, `catalog_supersession_schema_test.go`
  (only exact-schema expectations and compatibility evidence)
- new `assignment_integration_test.go`
- `assignment_transaction.go`, `assignment_transaction_test.go`
  (owner integration, production scope/restore fences and their regressions)
- `assignment_repository.go`, `assignment_repository_test.go`
  (only owner-integration corrections and their regressions)
- `assignment_schema.go`, `assignment_schema_test.go`
  (only required integration corrections and staged-to-active fixture expectations;
  preserve accepted manifest and migration identity, never silently rewrite SQL)

Append migration 10 to `SchemaMigrations`, implement/wire the public assignment
Transactor through the single LifecycleOwner and activate its callback-scoped
port in this one checkpoint. Coordinate all exact v9/v10 gates and synthetic-v11
rejection; no broad `>=9` admission. Keep supported pre-v9 behavior unchanged.
v9 catalog operations remain supported, assignment commands on v9 fail closed,
v10 supports the accepted catalog behavior and assignment path, and synthetic v11
is rejected without writes. Verify registration, supersession, accounting and
receipt preservation across the matrix. Adapt A2a's helper to select v9 explicitly
before adding staged v10, so activation neither appends migration 10 twice nor
removes the historical v9 fixture. Retain frozen prior checksums and all schema
constraints/manifest tests.

Acceptance: required owner/bypass tests from the coverage table plus
`TestAssignmentActivationCompatibility`, `TestAssignmentRestartSmoke` and
`TestAssignmentPreUpgradeRestoreSmoke` in `assignment_integration_test.go`.
Exercise the real application service through LifecycleOwner, a single connection
and transaction for reference reads, dependency fences and atomic commit. Prove
no nested Tag/catalog transactor, generic Save bypass or alternate writer; ports
are revoked after callback, close and restore. Rerun every earlier A2 test through
its retained fixture and run activation integration scenarios for exact targets,
race, atomicity and replay through the real owner. Those integration regressions
are final composition checks, not duplicate acceptance ownership or budgets.

Restart must retain the first assignment, complete history and exact command
readback/replay. Verify a pre-upgrade backup, preserve post-upgrade DB/media,
restore into a fenced new path at exact v9, reject stale handles and assignment
commands without read-triggered upgrade, and report lost newer history. B2 still
owns the full failure/helper-process matrix. Run focused uncached/repeated/race
assignment tests and the common A/B gates, including all existing catalog/Tag/media
regressions with accepted PI-08 CI-tier exceptions intact.

Rollback before activation: source revert. After any v10 write: fence commands,
retain newer DB/media for reconciliation and use the verified pre-upgrade backup
and exact-schema new-path restore; no source-only downgrade against v10, down
migration or dual writer. Non-claims: this software activation is not B1/B2/T,
whole-system legacy writer transfer, deployment, hardware, physical power-loss
or production-migration acceptance. Durable software delivery may only be claimed
after this entire checkpoint's tests and review pass, never at an intermediate
file commit or from the inert fixtures.

#### A2 required-test ownership

Each original named A2 criterion has exactly one acceptance owner. A2a's completed
gates remain mandatory regressions, not new work. Re-execution in a later slice
preserves coverage and is charged only to that later slice's verification cost.

| Original required test | Acceptance owner | Required evidence / file |
| --- | --- | --- |
| `TestAssignmentSchemaUpgrade` | A2a (complete) | `assignment_schema_test.go`; v9/staged-v10, upgrade/checksums; adapt fixture only at final activation. |
| `TestAssignmentRestrictiveReferences` | A2a (complete) | `assignment_schema_test.go`; immutable/restrictive references retained. |
| `TestAssignmentNoCatalogRetarget` | PI09-A2b.1 | `assignment_repository_test.go`; supersession never changes retained explicit target. |
| `TestAssignmentCatalogOnlyTarget` | PI09-A2b.1 | `assignment_repository_test.go`; managed exact ownership/support, unbound versions allowed, foreign Content/version rejected. |
| `TestAssignmentSameRevisionRace` | PI09-A2b.2 | `assignment_transaction_test.go`; real SQLite race, one winner, no partial loser state. |
| `TestAssignmentCommitAtomicity` | PI09-A2b.2 | `assignment_transaction_test.go`; complete ledger/projection/command/usage atomicity and unchanged Tag/catalog/media receipts. |
| `TestAssignmentSingleOwnerFence` | PI09-A2b.3 | `assignment_integration_test.go`; real owner, same-transaction dependency reads/revision fences, concurrent catalog change, restore/close revocation, no nesting. |
| `TestAssignmentStorageBypassRejected` | PI09-A2b.3 | `assignment_integration_test.go`; no unmanaged Save, direct writer or unscoped public port. |

The additional compatibility/restart/pre-upgrade-restore tests above bind the
previously unnamed A2 obligations to PI09-A2b.3. They do not replace any original
criterion. A2a's manifest, constraints, blob-envelope and migration-failure tests
also remain mandatory. All new test names are specifications until implemented;
this documentation replan claims no delivery test result.

### B1: pure source-URI admission and provenance integration

Depends on accepted A2. Own new `internal/domain/assignment/source_uri.go` and
`source_uri_test.go`, new `internal/application/assignment/source_uri_test.go`;
extend A1's `commands.go`, `service.go`, `commands_test.go`, `service_test.go`,
`encoding.go`, `encoding_test.go` and A2's `assignment_integration_test.go` only
for URI profile integration. Encoding paths above retain their A1 directories.
URI slot/profile absence is reserved in A1 encoding; B1 must not reinterpret old
commands or alter the schema merely to fill that slot.

Required tests: `TestAssignmentSourceURIProfile`, `TestAssignmentURIHasNoAuthority`,
`TestAssignmentURICanonicalReplay`, `TestAssignmentURIErrorRedaction`, and bounded
`FuzzAssignmentSourceURI`. Cover every D4 accepted/rejected vector, exact byte
limit, malformed escapes/authority, equivalent encodings, query credentials,
Unicode, IP forms, file paths and malicious schemes. Assert parser determinism,
no network/file capabilities and preservation of old URI-absent replay. Retained
locator never changes target/classification/availability.

Rollback: stop new URI-bearing admissions while retaining B1 codec read/replay
support. Do not deploy an older writer that cannot read committed B1 evidence;
use A2 recovery if binary downgrade is necessary.

### B2: complete history queries, uncertainty and recovery

Request `pi09-b2-bounded-replan-20261106` partitions B2 into four sequential
acceptance checkpoints. The prerequisite is accepted B1 at
`28b97bf41587a3e0a1158d7ec389ae60eb076831`. Each next checkpoint depends on its
predecessor's accepted exact commit. History must establish trustworthy readback
before command outcomes can be resolved. In-process outcome and restart checks
provide the oracle for fenced restore. Process termination uses those accepted
paths and fixtures to prove the precommit and postcommit boundaries end to end.
Pagination and integrity, lookup and replay, and restore and its fences each
stay together because separating either half would leave its public behavior
unverified. B2 remains the delivery owner for all four checkpoints.

This replan writes exactly `docs/architecture/pi/pi-09-plan.md` and
`docs/architecture/pi/pi-09-budget.md`. The following manifests are exhaustive
for future delivery dispatches and relative to `next/backend/`. Test files are
new. Existing source paths may change only for their stated B2 corrections.
No implicit docs, workflow, schema, lifecycle-owner or other cross-owner edits.
If a required correction exceeds a manifest, stop and replan at that boundary.

#### PI09-B2.1: retained history and integrity

Depends on B1. Exact write manifest:

- `internal/application/assignment/queries.go`
- `internal/application/assignment/history_test.go`
- `internal/adapters/sqlite/assignment_repository.go` (history and reconstruction)
- `internal/adapters/sqlite/assignment_history_test.go`

Verify `GetEffectiveAssignment`, `GetAssignment` and `ListAssignmentHistory`
through the service and real SQLite. Prove retained-revision and as-known
answers, revision-ordered bounded pages and full aggregate validation before
pagination. Enforce the D5 page, encoded response and cursor limits together.
Check binding to query/time, knowledge/head revision, dependency snapshot and
owner generation. Reject stale cursors after relevant dependency writes,
restart and restore through the existing A2 lifecycle. Check continuation and
explicit rejection of invalid page sizes and future knowledge revisions.

Prove retention of tombstones, historical targets, restrictive references and
original receipts. Distinguish Tag not found, known Tag with no aggregate,
unassigned, retired, corrupt/unavailable and schema unavailable. Preserve
typed D5 outcomes for unmanaged or oversized state, stale cursors, limits,
busy and canceled/deadline. A head/projection mismatch or missing persisted
evidence, even outside the requested page, fails closed. Preserve exact targets
and revision-labelled catalog annotations without fallback or media verification.

#### PI09-B2.2: command outcome, retained replay and restart

Depends on PI09-B2.1. Exact write manifest:

- `internal/adapters/sqlite/assignment_transaction.go` (command outcome and scope)
- `internal/adapters/sqlite/assignment_repository.go` (command lookup/readback)
- `internal/adapters/sqlite/assignment_failure_test.go`
- `internal/adapters/sqlite/assignment_restart_test.go`
- `internal/application/assignment/service.go` (post-finalization outcome reporting)
- `internal/application/assignment/recovery_test.go`

Use the real owner/SQLite path with bounded in-process faults. Prove precommit
rejection or cancellation has no partial ledger, projection, command or usage
effect. Exercise contention, caller deadlines, rollback failure and commit or
cleanup uncertainty. Distinguish confirmed failure from an indeterminate
outcome. Retain the owner fence while commit is still possible and require
authorized command lookup to resolve uncertainty. Verify callback/close
revocation and fail-closed behavior for corrupt command evidence.

Prove a lost response after commit is resolved by authorized
`LookupAssignmentCommand` and exact same-ID replay, including after a real
close and reopen. Cover absent, identity conflict and current authorization,
original URI-absent and B1 receipts, and exact retained readback after restart.
Compare assignment, Tag, catalog, media and classification state before and
after replay. Replay may neither add assignment state nor mutate dependencies.
Keep the original command ID and result. This checkpoint verifies in-process
outcomes and restart. Process death and the full restore matrix follow.

#### PI09-B2.3: fenced restore and backup failures

Depends on PI09-B2.2. Exact write manifest:

- `internal/adapters/sqlite/assignment_transaction.go` (assignment-side restore fence)
- `internal/adapters/sqlite/assignment_recovery_test.go`

Use the existing PI-05/07 verified backup and closed, fenced new-path restore.
Test backup and restore failures, including integrity/FK/ledger rejection,
and prove a failure never exposes a partial or unverified candidate to reads or
writes. Prove old sessions and cursors are revoked on restore and the current-schema
restore retains exact lookup/replay, history, references and receipts. Exercise
the exact pre-assignment v9 restore with the original newer DB and external media
preserved. Commands fail closed and reads create no assignment tables or schema
upgrade. Keep supported v9 catalog behavior and report the newer history omitted
from the restored snapshot. Restore and revocation are one acceptance boundary.

#### PI09-B2.4: helper-process recovery and B2 composition

Depends on PI09-B2.3. Exact write manifest:

- `internal/adapters/sqlite/assignment_recovery_process_test.go`

Use a real helper process on the normal owner/service path with bounded
synchronization and verified termination. Stop it before commit and prove after
reopen that no partial state or command result exists. Separately terminate it
after commit before response delivery, then reopen and prove exact retained
history, result and authorized same-ID replay without mutation. Verify process
exit and the reached commit boundary in both scenarios. In-process faults and
normal restart are regressions here and cannot substitute for process death.
Rerun the entire B2 evidence set and the common gates at the composed head.
If the process test exposes a runtime gap, return it to the owning checkpoint
for a reviewed correction before accepting B2.

#### B2 required-test ownership and common gates

| Original required test | Acceptance owner | Test file |
| --- | --- | --- |
| `TestAssignmentHistoryPagination` | PI09-B2.1 | `assignment_history_test.go`; application coverage in `history_test.go`. |
| `TestAssignmentTombstoneRetention` | PI09-B2.1 | `assignment_history_test.go` |
| `TestAssignmentCorruptLedger` | PI09-B2.1 | `assignment_history_test.go` |
| `TestAssignmentLostResponse` | PI09-B2.2 | `assignment_failure_test.go`; restart/readback in `assignment_restart_test.go`. |
| `TestAssignmentCommitUncertainty` | PI09-B2.2 | `assignment_failure_test.go`; typed application outcomes in `recovery_test.go`. |
| `TestAssignmentRestoreFence` | PI09-B2.3 | `assignment_recovery_test.go` |
| `TestAssignmentPreSchemaRestore` | PI09-B2.3 | `assignment_recovery_test.go` |
| `TestAssignmentHelperProcessRecovery` | PI09-B2.4 | `assignment_recovery_process_test.go` |

Every slice runs focused uncached, repeated and race assignment checks and the
common A/B gates below. Include accepted assignment tests to date, A2
schema/owner/restart/restore and restrictive-reference regressions, B1 URI and
old-receipt compatibility, and existing Tag, catalog/accounting and media
regressions. Preserve accepted PI-08 tier exceptions and mandatory one-shots.
Keep the 10-second operation and 5-second transaction limits, including
contention and earlier caller deadlines. Preserve existing test deadlines,
assertions, schema and PI-05/07 recovery machinery. Run with the pinned toolchain
and qualified storage fixture. Record actual selected tests and evidence of
execution.

Each checkpoint hands off the dependency SHA, exact commit and changed files,
scenario evidence, commands/results/durations, runner limits, separate local
and exact-head CI status, both budget deltas and remaining owner work. Final
software B2 acceptance requires the composed evidence, parent review and
required exact-head CI. Earlier checkpoints claim only their verified behavior.
T remains independently admitted after B2 acceptance.

Rollback at every delivery checkpoint uses the A2 recovery boundary after any
assignment-schema write. Fence commands, keep the newer DB and media for
reconciliation, and use a verified backup and closed, fenced new-path
exact-schema restore. Report lost newer history. No source-only downgrade
against the active assignment schema, down migration, dual writer or destructive
cleanup. Process-crash evidence covers software recovery. This replan authorizes
no implementation, deployment, physical power-loss or production-migration
acceptance.

### T: independent classification consolidation and service audit

Depends on accepted A/B and independently admitted Astra review. Own new
`internal/domain/assignment/policy_audit_test.go`,
`internal/application/assignment/audit_test.go`,
`internal/adapters/sqlite/assignment_audit_test.go`; extend
`internal/architecture/boundary_test.go` and
`internal/domain/migration/{migration.go,migration_test.go,audit_test.go,fixtures_test.go}`.
Migration fixture classification becomes an explicit adapter to the single domain
classification rule, preserving its raw data and previous unresolved outcomes;
do not turn this fixture into a production importer. Narrow findings may return
to exact A/B-owned files with a recorded finding/owner, not a broad T write grant.

Required tests: `TestAssignmentClassificationSingleAuthority`,
`TestAssignmentNoHeuristicAuthority`, `TestAssignmentMilestone`,
`TestAssignmentMigrationCompatibility`, `TestAssignmentAuditNoBypass` and bounded
`FuzzAssignmentDecisionSequence`. Independently challenge URI trust escalation,
custom/public collisions, ambiguous catalog lookup, time versus revision, future
retirement, cross-subject evidence, lost responses and restored stale handles.
Check all writes in the Next authoritative assignment path delegate to the one
service. Legacy C paths remain outside this milestone until their migration PI;
there is no claim of system-wide writer transfer.

The milestone scenario registers two Tags and independent public/custom Contents,
assigns an explicit catalog-only version, reassigns, reviews conflicting physical
classification without retargeting, records an original baseline without fallback,
validates a URI without I/O, supersedes the catalog version without changing the
assignment, unassigns/retires, restarts and queries intact history. Exact replay
and race rejection must remain true across those steps. No open High/security/
data-loss findings permit milestone acceptance; any other residual findings need
explicit reviewer disposition. Record exact head and matching CI separately.
Rollback follows the owning checkpoint; never erase evidence to make tests pass.

## Verification and R completion evidence

R verification from repository root:

```sh
python3 scripts/check_architecture_docs.py
PYTHONDONTWRITEBYTECODE=1 python3 next/tools/check_workspace_boundaries.py
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s next/tests -p 'test_*.py' -v
(cd next && go test -count=1 ./backend/internal/architecture ./backend/internal/domain/assignment ./backend/internal/domain/migration)
git diff --check
```

The architecture-doc checker includes relative file-link checks; no separate
network link checker is required. Check new local heading fragments separately
if introduced. No external pages are needed to establish these repository facts.
Review all new/untracked documents, stage exactly four allowed paths, check
`git diff --cached --check` and exact `git diff --cached --name-only`, then commit
on a dedicated branch. Verify parent SHA, four-file commit diff and clean status.
Record command results/durations and any environment blockers in the handoff;
a failed runner is not a passing test. R completion means these contract artifacts
are complete; an unexecuted required check leaves verification conditional.

Future A/B/T run focused tests first, then full backend tests, vet and existing
workspace/Next CI gates. Use the repository-pinned Go toolchain and qualified
filesystem for media regressions. Repeat/race assignment tests plus 10-second
bounded fuzzing for B1/T; no regex selecting zero tests counts. Preserve accepted
PI-08 accounting-tier exceptions, normative deadlines and mandatory one-shots;
do not broaden repeated expensive unrelated suites to claim extra coverage.
R does not need a full runtime delivery suite, remote CI or hardware execution.

Local R checks on 2026-10-31: architecture docs including file links passed
(107 documents, 2.425 s); workspace boundary passed (0.123 s); all six Python
quality-gate tests passed (0.320 s). Architecture, existing assignment and migration
Go packages passed uncached (8.525 s wall time), using Windows Go with the
workspace-selected `go1.27.1`. WSL had no native Go/usable Docker daemon; the
existing Windows executable was the successful local fallback. No remote CI was
run. Final link/scope/diff checks and commit identity are reported in the handoff.

## Parent decisions and preserved deferrals

No runtime choice remains necessary to complete this R. Parent acceptance and
fresh integrated quota admission are required before each delivery checkpoint;
A2 must confirm its actual migration number and file manifest at then-current head.
The conservative HTTPS-only locator profile, ledger temporal policy and protected
retention limits are selected R decisions; expanding them requires refinement,
not an implementer's guess. D7 lists later owners; real migration, hardware,
auth/network fetches and production rollback stay explicitly unaccepted.
