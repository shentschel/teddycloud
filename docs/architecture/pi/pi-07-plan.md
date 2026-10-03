# PI-07 TAF lifecycle execution plan

Status: R checkpoint only; design acceptance and A/B/T admission pending.
Baseline: `428b2ee4957d75f05b11b370df044d625a78abdb`.

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
| PI-07/R | Astra/high; accepted PI-05/06 | These four documents and README. Resolve remaining contract gates before accepting design. |
| PI-07/A | 6.1 Sol/high; accepted R | Add `internal/domain/content/blob.go`, `blob_test.go`; `internal/application/contentstore/ports.go`, `errors.go`; `internal/adapters/contentfs/store.go`, `store_test.go`. Digest/path codecs, bounded streaming, validation and durable no-overwrite publication, duplicate verification, failpoints before/after flush and publication. No catalog inference. |
| PI-07/B | 6.1 Sol/high; A | Add `internal/application/contentstore/service.go`, `service_test.go`; `internal/adapters/sqlite/blob_repository.go`, `blob_repository_test.go`; extend `schema.go`, `lifecycle_owner.go` additively. Add `internal/adapters/contentfs/range_test.go`, `recovery_test.go`. Transactional import results/references, replay/conflict, lost-response recovery, bounded reads and orphan inventory; preserve existing Content/Tag transactions and restore fencing. |
| PI-07/T | Astra/high; A/B | Add `internal/adapters/contentfs/audit_test.go`, `missing_blob_test.go`; application recovery tests; extend `internal/architecture/boundary_test.go` only for actual new boundaries. Audit traversal, symlink races, crash/disk-full/cancellation, old handles, sanitized errors, missing blob and re-import. Reuse PI-07/T-01 #17 within T, not another budget bucket. |

Development rollback uses source revert and the existing verified pre-upgrade
snapshot/new-path restore. Never modify old migration checksums or delete blobs
to compensate for DB rollback. Keep generated fixtures only; no live paths,
credentials, provider downloads or physical card identifiers.

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
```

Require named tests for every contract recovery row and actual Linux race CI.
Unexecuted commands, injected failures and process-kill tests do not certify
hardware power-loss durability. Exact test names and remaining contract gates
must be settled before implementation dispatch.
