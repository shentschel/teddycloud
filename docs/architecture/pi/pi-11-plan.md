# PI-11 execution plan

Status: complete R refinement for parent review; A/B/T are prospective, neither
admitted nor delivered. Milestone **Live tag/playback events are reliable** is
pending. Request `pi11-r-event-delivery-20261118`, exact base
`ac08326eaeaa7dee5c2414864977982ede0ff51d`.
The [contract](pi-11-event-delivery-contract.md) owns D1-D9 and F1;
[budget](pi-11-budget.md) owns integrated admission. The
[roadmap](../teddycloud-next-pi-roadmap.md) remains authoritative and unchanged.

## R boundary, evidence reads and routing

Requested model/reasoning: `gpt-6-astra`, high; this records requested routing,
not independently verified host model identity. Later jobs use the then-approved
[model routing](../task-model-routing.md) and a fresh whole-job estimate. No model
switch or presumed reset expands capacity.

Exact R write allowlist (also the complete local commit manifest):

```text
docs/architecture/pi/pi-11-event-delivery-contract.md
docs/architecture/pi/pi-11-plan.md
docs/architecture/pi/pi-11-budget.md
docs/architecture/pi/README.md
```

The first three files are new; the index adds PI-11 links/status only. No runtime,
schema, workflow, issue/status/ledger, deployment, hardware, public API/V3 freeze,
auth implementation, production migration or push. No review file is authorized
in this R despite the index's generic per-PI template. Parent acceptance is not
invented through a local status file or issue update.

Reads are limited to concrete dependencies: contract E1 source locators for
legacy provenance and PI-05..10 owner invariants; existing mutation/transaction,
schema dispatch, lifecycle and Library source paths listed in A below to make
its integration manifest real; PI-10 plan/budget for check conventions; roadmap
admission; architecture/workspace checkers, their workflow and Python quality
tests for actual verification coverage. Path discovery is not permission for
unrelated source exploration. No private usage history/status was read or copied.

## Dependency order and admission

One complete A, then one complete B, then one complete T. No isolated schema,
codec, activation or cleanup micro-sprints. A includes every DB producer hook,
retention, replay, observation state and common lifecycle integration; B includes
the complete SSE/reconciliation path and isolation; T proves the composed failure
and resource matrix. Basic cancellation/backpressure is required in A/B, not
postponed until T. T supplies adversarial proof and measurements.

Before each job: record accepted predecessor SHA, compare manifest to that head,
qualify runner/storage capabilities, and admit both independent quota estimates
through review/CI/accounting. A requires accepted R and the actual inherited
PI-05..10 head; B requires accepted A; T requires accepted B. Historical pending
labels in owner contracts do not reopen accepted predecessors.

All A/B/T paths below are relative to `next/backend/`; lists are exhaustive.
N means new/prospective at R base; E means existing. Files created by A are
existing for B/T. Each list is a future scope proposal requiring separate job
admission, never today's write permission. No implicit schema, generated output,
module/dependency, workflow, legacy C/JS, command-main, public API, SDK, UI or doc
allowance. Missing necessary files require a reported scope finding and revised
admission before edits. No wildcard “and affected files” permission.

## A: integrated internal Event Hub and durable producer path

Owner: Event application/SQLite implementer; Tag, Content, Catalog, Assignment
and Lifecycle owners review their hooks. Deliver D1-D4 and D6-D8 core behavior,
including read/barrier primitives needed by B. Internal constructors compose a
hub with the existing LifecycleOwner; no listener or CLI activation.

Dependency-ordered N manifest:

```text
internal/application/eventhub/types.go
internal/application/eventhub/envelope.go
internal/application/eventhub/errors.go
internal/application/eventhub/ports.go
internal/application/eventhub/capability.go
internal/application/eventhub/observation.go
internal/application/eventhub/service.go
internal/application/eventhub/subscription.go
internal/application/eventhub/diagnostics.go
internal/application/eventhub/envelope_test.go
internal/application/eventhub/authority_test.go
internal/application/eventhub/observation_test.go
internal/application/eventhub/service_test.go
internal/application/eventhub/subscription_test.go
internal/adapters/sqlite/event_schema.go
internal/adapters/sqlite/event_journal.go
internal/adapters/sqlite/event_producers.go
internal/adapters/sqlite/event_observation.go
internal/adapters/sqlite/event_cursor.go
internal/adapters/sqlite/event_retention.go
internal/adapters/sqlite/event_lifecycle.go
internal/adapters/sqlite/event_schema_test.go
internal/adapters/sqlite/event_owner_test.go
internal/adapters/sqlite/event_atomicity_test.go
internal/adapters/sqlite/event_observation_test.go
internal/adapters/sqlite/event_journal_test.go
internal/adapters/sqlite/event_cursor_test.go
internal/adapters/sqlite/event_retention_test.go
internal/adapters/sqlite/event_recovery_test.go
internal/adapters/sqlite/event_recovery_process_test.go
internal/adapters/sqlite/event_lifecycle_test.go
```

Dependency-ordered E manifest:

```text
internal/adapters/sqlite/schema.go
internal/adapters/sqlite/database.go
internal/adapters/sqlite/lifecycle_owner.go
internal/adapters/sqlite/content_repository.go
internal/adapters/sqlite/tag_transaction.go
internal/adapters/sqlite/tag_repository.go
internal/adapters/sqlite/blob_repository.go
internal/adapters/sqlite/catalog_transaction.go
internal/adapters/sqlite/catalog_adoption.go
internal/adapters/sqlite/catalog_import.go
internal/adapters/sqlite/catalog_registration.go
internal/adapters/sqlite/catalog_supersession.go
internal/adapters/sqlite/assignment_transaction.go
internal/adapters/sqlite/assignment_repository.go
internal/adapters/sqlite/library_index.go
internal/application/contentstore/ports.go
internal/application/contentstore/service.go
internal/application/contentstore/recovery_test.go
internal/adapters/sqlite/database_test.go
internal/adapters/sqlite/tag_schema_test.go
internal/adapters/sqlite/blob_schema_test.go
internal/adapters/sqlite/catalog_schema_test.go
internal/adapters/sqlite/catalog_state_schema_test.go
internal/adapters/sqlite/catalog_adoption_test.go
internal/adapters/sqlite/catalog_recovery_test.go
internal/adapters/sqlite/catalog_registration_test.go
internal/adapters/sqlite/catalog_registration_integration_test.go
internal/adapters/sqlite/catalog_supersession_schema_test.go
internal/adapters/sqlite/assignment_schema_test.go
internal/adapters/sqlite/assignment_repository_test.go
internal/adapters/sqlite/assignment_transaction_test.go
internal/adapters/sqlite/assignment_integration_test.go
internal/adapters/sqlite/assignment_recovery_test.go
internal/adapters/sqlite/library_index_test.go
internal/adapters/sqlite/upgrade_backup_test.go
internal/adapters/sqlite/lifecycle_owner_test.go
internal/architecture/boundary_test.go
```

The E list permits only these concrete changes:

| Files / responsibility | Integration invariant |
| --- | --- |
| `schema.go`, new `event_schema.go` and schema tests | Append forward migration (candidate 11), checksums/constraints, verified backup/upgrade support, no old-migration edit or data backfill pretending to be history. |
| `database.go`, `content_repository.go`, `tag_transaction.go`, `catalog_transaction.go`, `assignment_transaction.go`, `lifecycle_owner.go` | Carry a transaction-scoped event collector/context, append before the same commit, publish hints afterward, preserve uncertainty/rollback/callback-once/deadlines. Private helpers never escape callbacks. Direct unmanaged Save must participate too; no alternate bypass. |
| `tag_repository.go`, `blob_repository.go`, `catalog_adoption.go`, `catalog_import.go`, `catalog_registration.go`, `catalog_supersession.go`, `catalog_transaction.go`, `assignment_repository.go` | Build redacted owner facts only at accepted write/replay boundaries. Catalog review/order live in `catalog_transaction.go`; do not invent `catalog_review_write.go`/`catalog_order_write.go`. Source-wide inserts/imports produce coarse invalidation. No new source administration/revocation command. |
| `assignment_repository.go`, `catalog_transaction.go`, `library_index.go` | Replace hardcoded current schema assumptions with explicitly tested support for the appended schema; retain exact dirty/checksum/complete reconstruction guards. Library indexing remains private and volatile. |
| Content Store `ports.go`, `service.go`, `recovery_test.go`, blob/lifecycle adapter | Typed storage invalidation intent before authorized filesystem mutation, including re-import repair and post-operation completion invalidation; no filesystem I/O in SQL, no new contentfs implementation or destructive cleanup. Existing constructors/fakes receive explicit bounded capability behavior. |
| Listed existing tests and `boundary_test.go` | Update only expectations affected by the new schema/ports; retain old-schema, backup, restore, owner and architecture assertions. New cross-owner tests belong in the N event test files. No blanket golden/assertion rewrite. Current-head v10 expectations and synthetic future-v11 fixtures in adoption, registration, catalog recovery and assignment transaction/integration/recovery tests must distinguish pinned old-schema tests from latest-schema tests; move only the latter to v11/future-v12. |

For catalog media binding, reuse the already-authoritative blob/import or catalog
registration path; emit the owning event once. No fictitious separate media
service. Catalog source revocation has no new administration endpoint here: the
reserved reason is contract-tested with trusted owner fixtures and remains
unsupported for external input until that owner's later command exists.

Mandatory A family placement (all names are prospective):

| File(s) | Contract F1 families / evidence |
| --- | --- |
| `envelope_test.go`, `authority_test.go` | `TestEventEnvelope`, `FuzzEventEnvelope`, `TestEventProducerAuthority`; golden/negative/fuzz and redaction at admission. |
| `observation_test.go`, SQLite `event_observation_test.go` | `TestEventObservationContinuity`; last-input duplicate semantics, two boxes and unknown identity. |
| `service_test.go`, `subscription_test.go` | Basic bounded subscription, cancellation, slow consumer and fairness; fake dependencies only supplement real-adapter tests. |
| `event_owner_test.go`, `event_atomicity_test.go` | `TestEventOwnerCoverage`, `TestEventCommitAtomicity`, `TestEventIdempotency` across every actual writer and exact retry; state/journal all-or-none, no out-of-scope owner mutation. |
| `event_journal_test.go`, `event_schema_test.go` | Ordered committed suffix, corruption/upgrade/capacity accounting and strict canonical persistence. |
| `event_cursor_test.go`, `event_retention_test.go` | `TestEventCursor`, `TestEventRetention`; exact/+1 reachable bounds, F/F-1 and mixed-stream pressure. |
| `event_recovery_test.go`, `event_recovery_process_test.go`, `event_lifecycle_test.go` | `TestEventRecovery`, `TestEventRestoreEpoch`; journal/owner reopen, current/pre-event schema restore, commit uncertainty, basic cleanup and rollback. |

A acceptance requires complete producer coverage, durable replay and restart,
not merely a working in-memory bus. Event profile/snapshot/control scope cannot
silently admit external writers. No state owner service is replaced. Validate
inherited Tag/Catalog/Assignment command outcomes, Content receipts and PI-10
pagination/cursors at the new schema. Reject activation if any owner can commit
without its required journal record. Read-only and exact-replay paths stay silent,
except an independently explicit storage invalidation intent for media repair.

## B: complete internal SSE, cursor and reconciliation adapter

Owner: transport/application implementer with Lifecycle owner review. A is a
prerequisite; no public route or SDK/web changes. Complete D5/D7 plus transport
parts of D6/D8. Use Go standard-library HTTP handlers and test servers, without
module additions or changing `cmd/teddycloud-next/main.go` (currently health JSON).
The exported internal constructor receives the authorized scope provider; it
does not create an unauthenticated production listener.

Ordered N manifest:

```text
internal/application/eventhub/reconcile.go
internal/application/eventhub/reconcile_test.go
internal/adapters/sqlite/event_snapshot.go
internal/adapters/sqlite/event_snapshot_test.go
internal/adapters/sse/handler.go
internal/adapters/sse/framing.go
internal/adapters/sse/handler_test.go
internal/adapters/sse/framing_test.go
internal/adapters/sse/reconcile_test.go
internal/adapters/sse/isolation_test.go
internal/adapters/sse/lifecycle_test.go
```

Ordered E manifest (created by A, except architecture test):

```text
internal/application/eventhub/ports.go
internal/application/eventhub/capability.go
internal/application/eventhub/service.go
internal/application/eventhub/subscription.go
internal/application/eventhub/service_test.go
internal/application/eventhub/subscription_test.go
internal/adapters/sqlite/event_cursor.go
internal/adapters/sqlite/event_cursor_test.go
internal/architecture/boundary_test.go
```

`reconcile.go` coordinates bounded owner reads and H/K watermarks through ports;
`event_snapshot.go` supplies existing scoped owner readers and observation state,
not a duplicated aggregate implementation or implicit all-data dump. Constructors
keep dependency direction application-to-ports/adapters-to-application. A's
journal reader/barrier primitives suffice; a new SQL/migration requirement is a
scope finding before edits. No new external snapshot endpoint is implied.

Mandatory tests: `TestEventSSE` in framing/handler tests; `TestEventReconciliation`
in application/SQLite/SSE reconcile tests; `TestEventIsolation` and
`TestEventRevocation` in isolation tests, with real two-box journals and separate
tenant owners; deadline/partial-frame/basic cleanup in lifecycle tests. Exercise
the actual HTTP handler with a cancellable slow writer and a loopback test server,
not just string formatting. No raw-fetch web bypass or generated SDK update.
Test invalid/expired cursors before headers, lost terminal reset, replay of a
partially written event and H/K race through real owner writes. Keep A tests green.

B acceptance is a composed software adapter with a documented consumer contract.
It does not complete a real browser reconnect UX, production route or hardware
feed. No legacy SSE translation from an unidentified broadcast is accepted.

## T: composed lifecycle, slow-consumer and resource acceptance

Owner: independent integration reviewer/test implementer. Accepted A and B are
prerequisites. T is one composed acceptance job; no duplicate umbrella follow-up
job is needed. The full F1 matrix must hold with real SQLite, actual SSE handler
and trusted synthetic observation ingress. Physical device behavior remains
explicitly outside this software milestone.

Ordered N manifest:

```text
internal/application/eventhub/lifecycle_test.go
internal/application/eventhub/fairness_test.go
internal/adapters/sqlite/event_audit_test.go
internal/adapters/sqlite/event_milestone_test.go
internal/adapters/sqlite/event_benchmark_test.go
internal/adapters/sse/slow_consumer_test.go
internal/adapters/sse/milestone_test.go
internal/adapters/sse/benchmark_test.go
```

Ordered E manifest:

```text
internal/application/eventhub/subscription_test.go
internal/adapters/sqlite/event_recovery_process_test.go
internal/adapters/sqlite/event_lifecycle_test.go
internal/adapters/sqlite/event_retention_test.go
internal/adapters/sse/isolation_test.go
internal/adapters/sse/lifecycle_test.go
internal/adapters/sse/reconcile_test.go
```

No runtime fix is implicitly authorized by this test manifest. A defect requiring
runtime/schema/workflow changes produces a concrete finding, exact proposed paths
and fresh integrated admission before repair; never weaken assertions to finish T.
The allowed T evidence can still establish reproduction and affected guarantees.

`TestEventLifecycle` covers cancel/close/restore/revoke versus publication and
stalled socket races; successful shutdown must leave zero owned tasks/timers,
registrations and queue charges, with explicit failure on timeout.
`TestEventSlowConsumer` and `TestEventFairness` fill exact/+1 stream/global limits,
flood one box and continue another, check round-robin turn bounds and healthy
producer completion independently of blocked writes. No sleep-only timing oracle.
`TestEventRedaction`/`TestEventRollback` live in event audit tests. Both milestone
test files exercise `TestEventMilestone` at their respective package boundaries.
Use repetition and race detection with deterministic synchronization/fake clocks;
real helper processes prove crash/reopen only, not power-loss durability.

Measure cold startup/validation, append throughput, replay, commit-to-enqueue/write
p50/p95/p99, queue peaks, allocations and task counts at empty, boundary and mixed
box loads. Record OS/filesystem/Go version, fixture counts/bytes, warm/cold status,
commands and duration. Benchmarks report distributions and bounded resource
behavior; they do not set a universal latency SLA. Preserve PI-10/F02 deadlines,
mandatory one-shots and accepted expensive-tier exclusions. Do not raise timeouts
or alter CI to hide a failed resource or cleanup invariant.

## Verification, completeness and rollback gates

R checklist: every D1-D9 has owner, invariant, failure and F1 evidence; all F1
families have a manifest home; current links resolve; future file names remain
plain code, not links pretending they exist. Check N/E claims against the base.
A/B/T proposals cover typed core, persistent schema, all current producer paths,
recovery/lifecycle, SSE and scoped snapshot composition, with no implicit files.

Future A/B/T require focused new tests plus affected inherited owner suites,
Go architecture checks and repository-required CI on their exact published heads.
For event families run uncached once, deterministic concurrency repetition
`-count=10`, race `-race -count=1`, and envelope fuzz `-fuzz=FuzzEventEnvelope
-fuzztime=10s`. Runner and exact package/regex commands must be fixed at job
admission; no unmeasured full expensive SQLite repeat is granted here. No workflow
change is authorized by these manifests. Existing CI alone may not execute every
new focused family; record those local commands and parent exact-head evidence,
or obtain an explicit workflow scope revision before changing coverage.

R rollback: revert these four docs. A/B rollback: disable event capability, retain
forward schema and owner data, revoke streams, require new epoch after any
journaling discontinuity; see contract D8. Never roll back source ledgers, delete
media or open new schema with an incompatible binary. T is test-only rollback.

R local checks from repository root (all Python uses no bytecode writes):

```sh
PYTHONDONTWRITEBYTECODE=1 python3 scripts/check_architecture_docs.py
PYTHONDONTWRITEBYTECODE=1 python3 scripts/check_compatibility_fixtures.py
PYTHONDONTWRITEBYTECODE=1 python3 next/tools/check_workspace_boundaries.py
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s next/tests -p 'test_*.py' -v
PYTHONDONTWRITEBYTECODE=1 python3 next/tools/check_architecture_docs.py
git diff --check
```

The first two are the two existing checkers invoked by architecture-doc CI.
The last Python path, requested by earlier PI verification conventions, is absent
at this base; report exit 2 honestly, without creating a wrapper. Check links and
heading/line fragments in the four changed files separately: the architecture
checker only verifies relative file targets. Discovery is scoped to `next/tests`,
the existing workspace quality gates.
The root API suite includes live-server writes and backup changes, so it is not
run for this four-doc refinement; no server is started or production data touched.
No Go runtime change means no new runtime test execution claim.

Before commit inspect the entire diff, exact four-path set, empty pre-existing
staging and base SHA; run staged diff check. Commit locally, no push. Verify
parent equals base, exact committed file set and clean worktree. Parent review,
publication, required exact-head CI, final/delayed accounting remain pending.

### Local R evidence

Measured on 2026-11-18 from the exact base plus these four docs. Durations are
shell `TIMEFORMAT='WALL_SECONDS=%3R'; time <command>` wall times, not estimates.
They prove documentation/fixture/workspace consistency, not future delivery.

| Exact command (repository root) | Result | Wall time |
| --- | --- | ---: |
| `PYTHONDONTWRITEBYTECODE=1 python3 scripts/check_architecture_docs.py` | Pass: 113 documents | 2.464 s |
| `PYTHONDONTWRITEBYTECODE=1 python3 scripts/check_compatibility_fixtures.py` | Pass: 4 JSON catalogs, 12 cases, 2 route manifests, offline | 0.250 s |
| `PYTHONDONTWRITEBYTECODE=1 python3 next/tools/check_workspace_boundaries.py` | Pass | 0.168 s |
| `PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s next/tests -p 'test_*.py' -v` | Pass: 6 tests | 0.399 s |
| `PYTHONDONTWRITEBYTECODE=1 python3 next/tools/check_architecture_docs.py` | Exit 2: requested historical path absent; no wrapper added | 0.030 s |
| `git diff --check` | Pass (tracked diff; final staged check also required) | 2.252 s |
| `PYTHONDONTWRITEBYTECODE=1 python3 -` with session-inline link/scope/manifest verifier | Pass: exact base/four paths/empty staging; 143 local links, 6 fragments; A 68, B 20, T 15 paths; 21 named families | 2.961 s |

The inline verifier reads only the four docs, their local link targets, manifest
path existence and Git metadata. It validates heading/line fragments, N/E path
status in dependency order, duplicate paths, D1-D9 presence and all named F1
families mapped in the plan. Its measured time uses Python `perf_counter`.
Manual semantic review checks the owner/invariant/failure/evidence content; the
verifier does not prove semantics or implementation feasibility automatically.
Final post-edit architecture/link/scope/staged checks and post-commit cleanliness
are reported in the handoff; no future test family or remote CI is claimed run.

Resolved refinement findings: missing historical checker path explicitly recorded;
legacy removal/box identity not promoted to capabilities; source revocation stays
reserved rather than inventing an administration command; hardcoded schema/future
schema fixtures included in A's exact manifest; defensive encoding ceilings not
misrepresented as reachable canonical exact/+1 pairs; storage completion
invalidation closes the requery-during-filesystem-change race. Parent semantic
review and integrated R acceptance are still required.
