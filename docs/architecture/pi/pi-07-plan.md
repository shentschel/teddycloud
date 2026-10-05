# PI-07 TAF lifecycle execution plan

Status: R, A and PI-07/B are fully accepted after parent review, including the
real helper-process kill/reopen recovery matrix at code commit
`a62ea20cd420bd5219b8be71fe6c83eddcc91f6d`, with Next CI 37327204076 green.
PI07-T-F01, the retained-verification/import-admission coupling, was confirmed
High and fixed at `76cab7a33eaf715cd0209653c9be2236ced63b94`; parent review is
complete and [Next CI 37375380712](https://github.com/shentschel/teddycloud/actions/runs/37375380712)
passed all four jobs. This finding alone is closed. T and the PI milestone
remain open for the rest of the independent audit and issue #17 evidence.
Further delivery requires fresh admission; hardware power-loss evidence remains open.
Refinement baseline: `aef53a69ab10f1480c1dade9797ce4dd9ce645bd`.

Follow the [roadmap](../teddycloud-next-pi-roadmap.md),
[routing](../task-model-routing.md), [PI-05 review](pi-05-review.md),
[PI-06 review](pi-06-review.md), and
[ADR-0003](../adr/0003-persistence-and-projection-recovery.md).
The [storage contract](pi-07-taf-storage-contract.md),
[budget](pi-07-budget.md) and [review](pi-07-review.md) define this checkpoint.

Milestone: safe, retryable immutable TAF storage with explicit missing-media
recovery. R documentation alone does not deliver it. Sequence: R acceptance,
A, B, independent T, reviewed evidence. No production migration or deployment.

## Proposed delivery slices

Paths below are relative to `next/backend`; new names are proposed, not existing
implementation. Each slice requires fresh admission and bounded checkpoints.

| ID | Model / dependencies | Files and acceptance |
| --- | --- | --- |
| PI-07/R | Astra/high; accepted PI-05/06 | These four documents and README. Five gates resolved and independently accepted. |
| [PI-07/A](https://github.com/shentschel/teddycloud/issues/22) | 6.1 Sol/high; accepted R | Add `internal/domain/content/blob.go`, `blob_test.go`; `internal/application/contentstore/ports.go`, `errors.go`; `internal/adapters/contentfs/store.go`, `store_test.go`. Digest/path codecs, bounded streaming, validation and durable no-overwrite publication, duplicate verification, failpoints before/after flush and publication. No catalog inference. |
| [PI-07/B](https://github.com/shentschel/teddycloud/issues/23) | 6.1 Sol/high; A | Add `internal/application/contentstore/service.go`, `service_test.go`; `internal/adapters/sqlite/blob_repository.go`, `blob_repository_test.go`; extend `schema.go`, `lifecycle_owner.go` additively. Add `internal/adapters/contentfs/range_test.go`, `recovery_test.go`. Transactional import results/references, replay/conflict, lost-response recovery, bounded reads and orphan inventory; preserve existing Content/Tag transactions and restore fencing. |
| [PI-07/T](https://github.com/shentschel/teddycloud/issues/24) | Astra/high; A/B | Add `internal/adapters/contentfs/audit_test.go`, `missing_blob_test.go`; application recovery tests; extend `internal/architecture/boundary_test.go` only for actual new boundaries. Audit traversal, symlink races, crash/disk-full/cancellation, old handles, sanitized errors, missing blob and re-import. Reuse PI-07/T-01 #17 within T, not another budget bucket. |

Development rollback uses source revert and the existing verified pre-upgrade
snapshot/new-path restore. Never modify old migration checksums or delete blobs
to compensate for DB rollback. Keep generated fixtures only; no live paths,
credentials, provider downloads or physical card identifiers.

## Concrete checkpoint order

Keep the proposed stable issue IDs A/B/T. These checkpoints belong to their
parent sprint and are not additional quota buckets; re-admit each independently.

1. A1 adds `internal/domain/content/{taf,import_command}.go` and corresponding
   tests, plus test-only `internal/testutil/taffixture/builder.go`. Require the
   contract's exact envelope profile, malformed/one-over cases and hand-encoded
   golden command vectors. Generated fixtures prove envelope validity, not audio
   playback. Domain/content owns BlobID, envelope facts and command encoding;
   existing catalog owns ContentVersion and order evidence.
2. A2 adds `internal/adapters/contentfs/{store_linux,capability}.go` and tests
   alongside the table's store files. Require qualified Linux descriptor I/O,
   no-replace, flush and confinement tests. Add fail-closed non-Linux stubs to
   preserve existing cross-builds. No DB or live deployment in A.
3. B1 adds migration 4 and revocable owner-scoped content operation/session in
   the named SQLite files. Require atomic DB-only version/binding/receipt writes,
   replay/conflict, corrupt-row refusal, upgrade and exact pre-v4/current restore.
   No transaction spans file I/O and no session re-enters the lifecycle gate.
4. B2a verified ranges and B2b bounded inventory/quarantine adapter primitives
   are accepted in the [review](pi-07-review.md). B2b code is
   `91f839f3817746500327f24dc943aaad7d07e80a`; Next CI run 37220679818 passed all
   four jobs. The bounded B2c application/lifecycle integration and gated database
   reference reconciliation are accepted at
   `c55637a2672c9c85ea490a5d1b69e05788023dca`; parent review and Next CI run
   37222899075 passed all four jobs. This includes gate fencing, restore/mutation
   cursor invalidation, retained DB references after quarantine, typed missing
   availability and deterministic publish-before-commit retry/reuse evidence.
   Content/Tag regressions remain green. The remaining real helper-process
   SIGKILL/reopen matrix is accepted at
   `a62ea20cd420bd5219b8be71fe6c83eddcc91f6d`; Next CI 37327204076 passed all
   four jobs. Parent review confirmed real files/DB reopen, bounded inventory,
   retained DB facts, typed availability and exact retry/reuse with one
   binding/receipt. `after-rename` and `before-source-sync` name the same real
   intermediate position; `after-source-sync` and `before-destination-sync`
   likewise name the same real position. Seven named scenarios do not imply
   seven physically distinct syscall positions. See the
   [B recovery acceptance](pi-07-review.md#b-helper-process-recovery-and-full-sprint-acceptance).
   PI-07/B is fully accepted. Issue #23 is closed/completed;
   T, the PI milestone and hardware power-loss evidence remain open.
5. T independently audits every named contract recovery row and resource bound,
   including actual file removal/re-import for existing #17. High-severity defects
   block acceptance; record stable findings rather than relaxing assertions.
   PI07-T-F01 is already reproduced, fixed and parent-accepted with green CI;
   do not repeat that completed finding as a new task. Resume the remaining
   contract-to-code/test audit and #17 evidence before accepting T.

The contract's executable acceptance map supplies exact test names and outcomes.
An implementation may split focused tests within these packages; no HTTP API,
production assignment, conversion, GC or coordinated media backup is included.

## Verification targets

R commands from repository root:

```sh
python3 scripts/check_architecture_docs.py
git diff --check
```

A/B/T commands from `next`, after the proposed packages exist:

```sh
go test ./backend/internal/domain/content ./backend/internal/application/contentstore ./backend/internal/adapters/contentfs ./backend/internal/adapters/sqlite ./backend/internal/architecture
go test ./backend/...
go vet ./backend/...
go test ./backend/internal/adapters/contentfs ./backend/internal/adapters/sqlite -count=10
go test -race ./backend/internal/adapters/contentfs ./backend/internal/adapters/sqlite
go test ./backend/internal/domain/content -run '^$' -fuzz '^FuzzTAFEnvelopeHeader$' -fuzztime=10s -parallel=2
```

Require named tests for every contract recovery row and actual Linux race CI.
Unexecuted commands, injected failures and process-kill tests do not certify
hardware power-loss durability. Select a qualified Linux `TMPDIR` after the
capability probe; unavailable qualification is a blocked requirement, never a
skipped green test. Existing Next CI and docs CI must pass on the published head.
These commands are future acceptance targets, not R execution evidence.

A1 focused check before A2 packages exist, from `next`:

```sh
go test ./backend/internal/domain/content -run 'TestTAFEnvelope|TestImportCommandGolden' -count=1
```

B/T also run all named recovery tests and application/adapter races:

```sh
go test ./backend/internal/application/contentstore ./backend/internal/adapters/contentfs ./backend/internal/adapters/sqlite -run 'TestBlob|TestImport|TestContentStore|TestMissingBlob' -count=1
go test -race ./backend/internal/application/contentstore ./backend/internal/adapters/contentfs ./backend/internal/adapters/sqlite
```
