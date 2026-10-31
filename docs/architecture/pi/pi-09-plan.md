# PI-09 execution plan

Status: **R complete as a source-backed contract**; A/B/T and the milestone
**Assignments have one authoritative service** remain pending. No independent
review, remote CI, hardware, deployment or production-migration acceptance is
implied. Request `pi09-r-assignment-semantics-contract-20261031`; baseline
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
| Ordered bounded delivery | A1 -> A2a -> A2b -> B1 -> B2 -> independent T below. |
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

Split A2 into separately admitted sequential checkpoints: **A2a** owns only
`assignment_schema.go` and `assignment_schema_test.go`, with the migration staged
and tested explicitly, never appended to SchemaMigrations. **A2b** owns the rest
of the exact A2 manifest plus integration fixes to those two A2a files. A2a is a
schema-foundation checkpoint, not activated assignment delivery. A2b integrates
owner/repository writes and all enumerated v9/v10 gates together. The schema and
restrictive-reference tests are A2a gates; all remaining A2 tests below are A2b
gates. Both share A2's sprint ceiling, without counting parent work twice.

Before implementation bind candidate migration 10 (if still free), identifier,
exact schema gates and additive table/FK/index/immutable-trigger manifest. Activate
schema and port together in A2b, not a staged A2a table advertised as
usable delivery. Test supported v9/v10 catalog behavior plus rejection of a
synthetic v11 ledger, retaining current registration/supersession/accounting checks. A larger activation touch set requires a smaller replan first.
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

Depends on accepted B1. Own new
`internal/adapters/sqlite/{assignment_history,assignment_recovery,assignment_restart,assignment_failure,assignment_recovery_process}_test.go`
and `internal/application/assignment/{history,recovery}_test.go`;
extend A1's `queries.go`, A2's `assignment_repository.go` and
`assignment_transaction.go` only for history/recovery corrections.
No rewrite of accepted PI-05/07 recovery machinery or changed timeouts.

Required tests: `TestAssignmentHistoryPagination`, `TestAssignmentTombstoneRetention`,
`TestAssignmentLostResponse`, `TestAssignmentCommitUncertainty`,
`TestAssignmentRestoreFence`, `TestAssignmentHelperProcessRecovery`,
`TestAssignmentCorruptLedger`, `TestAssignmentPreSchemaRestore`.
Prove real helper termination before commit and lost response after commit, exact
readback/replay after restart, revoked sessions, cancellation and bounded contention,
rollback/backup failures, stale cursors after dependency writes/restart/restore,
head/projection mismatch, missing evidence and restrictive references. Distinguish
not-found/no aggregate/unassigned/retired/corrupt/schema-unavailable. Query history
at retained revisions; retain retired references and original receipts. Pre-schema
restore disables commands without read-triggered upgrades. No replay-induced
catalog, media or classification mutation.

Rollback: A2 database recovery, retained newer DB for reconciliation. Software
crash evidence is not physical power-loss or production migration acceptance.

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
