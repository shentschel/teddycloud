# PI-06 execution plan

Status: R accepted; A1 identity/service, decision-codec and count-gate checkpoints implemented; encoded aggregate, A2/B/T pending.
R milestone: accepted Tag Registry design only.
Delivery milestone: Tags are managed transactionally; not established by R.

## Inputs and decisions

Follow the [roadmap](../teddycloud-next-pi-roadmap.md),
[routing](../task-model-routing.md), [PI-04 plan](pi-04-plan.md),
[PI-04 review](pi-04-review.md), [PI-05 plan](pi-05-plan.md),
[PI-05 review](pi-05-review.md), and ADRs
[0001](../adr/0001-control-plane-and-gateway-sequencing.md),
[0002](../adr/0002-technology-and-repository-topology.md),
[0003](../adr/0003-persistence-and-projection-recovery.md).

The [Tag Registry contract](pi-06-tag-registry-contract.md) defines strict
existing codecs, immutable physical identity, independent auth/claim/ownership
evidence, revision conflicts, storage-neutral ports and one shared lifecycle
owner. The contract's failure matrix is the acceptance checklist for A/B/T.

PI04 codecs/provenance and PI05 migrations, transactions, contention and restore
are completed foundations. Extend them only where Tags need an integration seam;
do not repeat driver selection, rebuild the lifecycle or change their semantics.
No new classification heuristic or identity derived from AudioID is permitted.

## Sequence and scope control

`R acceptance -> A1 -> A2 -> B1 -> B2 -> T -> delivery milestone review`.
A1/A2 are checkpoints inside one PI-06/A issue, and B1/B2 inside one PI-06/B
issue. They are independently reviewable, not additional duplicate issues.
Each checkpoint needs fresh admission under the [budget](pi-06-budget.md).

The parent owns issue discovery/reuse, baseline tests, live quota admission,
README integration, independent validation and publication. The accepted A/B/T slices are published as issues #19–#21.
R changes documentation only; publication is owned by the parent, not the
subagent. No runtime or production change belongs to R.

## [PI-06/A](https://github.com/shentschel/teddycloud/issues/19) — Transactional Tag identity registry

Recommended model: `gpt-5.6-sol`; reasoning: high.
Objective: register and read one stable Tag per normalized physical UID.
Depends on accepted PI06/R, completed PI04/05 and fresh checkpoint admission.

### A1: domain and application checkpoint

Files, relative to `next/backend`:

- Add `internal/domain/tag/registry.go`, `registry_test.go` for immutable Tag,
  revision and metadata value types; reuse `uid.go` and `identity.TagID`.
- Add `internal/application/tagregistry/ports.go`, `errors.go`, `service.go`,
  `service_test.go`; a bounded in-memory test double exercises command semantics.
- Reuse `internal/domain/evidence/evidence.go` without changing accepted rules.

Acceptance: explicit UID/rUID field presence, uppercase normalization, byte
reversal, valid zero bytes, malformed/mismatch rejection and immutable identity
have table tests. Missing metadata remains unknown. Exact registration replay
does not advance revision; ID/UID collisions never merge. Domain/application
imports pass existing architecture tests. Verify the contract's fixed worst-case
bounds: at most 16384 cumulative decision-support references per Tag and an
8 MiB complete encoded aggregate, including escaping and history. Test exact
limits, one-over inputs and overflow-safe rejection before aggregate copies
or joined-row materialization. Individually valid fields cannot bypass the
aggregate limit. Validate the fixed `dec_` plus 26-character decision-ID codec.

Runnable checkpoint after implementation:

```sh
cd next
go test ./backend/internal/domain/tag ./backend/internal/application/tagregistry ./backend/internal/architecture
```

Current identity/service subset: [checkpoint](pi-06-a1-identity-checkpoint.md).
The [decision-ID codec](pi-06-a1-decision-checkpoint.md) is implemented separately.
The [history-count gate](pi-06-a1-counts-checkpoint.md) checks real slices without
copies, including cumulative support; retained representation, complete encoded
size/worst-case and adapter integration remain open. These subsets alone do not
complete A1 or issue #19. The [encoding refinement](pi-06-a1-encoding-refinement.md)
settles the internal shape, timestamps and exact byte accounting as design only;
a serialized-input reader has no current consumer and is not A1 scope.
The [counter/writer checkpoint](pi-06-a1-encoder-checkpoint.md) now supplies actual
field/byte parity and exact-boundary fixtures. The allocation-only follow-up
adds zero-allocation byte preflight; explicit enum mappings now pin the three
domain value maps. A private semantic validator checks cross-references and
projections; a validated retained value clones and compares bounded canonical
fields. Identity-only Tag and registry integration remain open.

### A2: durable identity checkpoint

Files: add `internal/adapters/sqlite/tag_repository.go`, `tag_repository_test.go`,
`tag_transaction.go`, `tag_transaction_test.go`; extend `schema.go` additively
and `lifecycle_owner.go` with the distinct tag transaction method. If transaction
plumbing is extracted from `content_repository.go`, preserve catalog behavior
and cover both ports in the existing adapter tests.

Acceptance: fresh/reopen registration and point lookup by TagID/UID/rUID agree;
database UID uniqueness is enforced, including concurrent writes. Read/write
calls use the existing lifecycle gate and selected handle. Callback failure
rolls back; errors preserve application taxonomy and hide storage details.
The initial identity-only migration need not introduce FKs. It must preserve
existing Content records and immutable migration checksums.

Verification: A1 command plus
`go test ./backend/internal/adapters/sqlite` from `next`; require the specifically
named A tests in the contract to exist and execute before accepting the slice.
Update this review with actual evidence after delivery, not during R.

Non-goals/security: auth execution, classification, assignments, blobs, deletion,
legacy import and HTTP. Only synthetic identifiers in fixtures; none in errors.
Migration/rollback: additive development schema, verified pre-upgrade snapshot
and existing fenced new-path restore. No down migration or production change.

## [PI-06/B](https://github.com/shentschel/teddycloud/issues/20) — Independent metadata and lifecycle safety

Recommended model: `gpt-5.6-sol`; reasoning: high.
Objective: persist bounded evidence and explicit reviewed state transitions
atomically, then prove Tag operations respect database recovery boundaries.
Depends on A2; B2 additionally requires acceptance evidence from
[F-PERSIST-01 (#18)](https://github.com/shentschel/teddycloud/issues/18) before
FK-bearing snapshot acceptance. Reuse that issue, do not absorb its work here.

### B1: metadata command checkpoint

Files: add `internal/domain/tag/metadata.go`, `metadata_test.go` and
`internal/application/tagregistry/metadata.go`, `metadata_test.go`; extend
`tag_repository.go` and append schema migrations. Add
`internal/adapters/sqlite/tag_metadata_test.go`.

Acceptance: all four independent keys, unknown/true/false/conflict states,
observation identity replay, contrary evidence and explicit support selection
match the contract. Confidence/time/import order never picks a winner.
Same-command replay is a no-op; stale or partial replay conflicts. Reject
cross-Tag support, unsupported accepted facts and quota/length overflow.
Inject failure between evidence and revision writes: neither persists.
Concurrent updates using the same expected revision cannot lose evidence.
The immutable resolution record retains superseded evidence. Auth metadata
never mutates identity, claim, ownership, assignment or content availability.

Verification: focused domain/application tests and
`go test ./backend/internal/adapters/sqlite -run 'TestTag(Metadata|Concurrent)'`
from `next`, after confirming those targets execute. Record FK snapshot
acceptance as pending until #18 and B2 pass; B1 is not recovery acceptance.

### B2: shared lifecycle and recovery checkpoint

Files: add `internal/adapters/sqlite/tag_lifecycle_test.go`,
`tag_recovery_test.go`, `tag_failure_test.go`; extend lifecycle/transaction
adapter files only as required for the shared fence and sanitized tag errors.

Acceptance: prove the B2 matrix rows in the contract: bounded busy/cancellation,
single callback execution, uncertain-outcome readback, corruption refusal,
process-restart rollback, Tag/Content restore contention and no old-handle use.
Upgrade a Content-only fixture to the Tag schema, retain its Content rows,
then snapshot/restore committed Tag evidence at the exact schema version.
Restoring a pre-Tag snapshot must leave Tag calls unavailable without silently
upgrading it. With #18 evidence available, reject FK-invalid snapshots before
handle selection. Retain recoverable originals on failure.

Verification: run the complete SQLite and architecture packages, then repeat
`TestTag` tests 10 times. Remote Linux race evidence is required at delivery;
missing local toolchain support is recorded honestly. Never substitute these
tests for missing external-blob evidence owned by
[PI-07/T-01 (#17)](https://github.com/shentschel/teddycloud/issues/17).

Non-goals/security: PI12 secrets/authentication, PI07 blob lifecycle, PI09
assignments/classification, PI11 event delivery and production import. Migration
and rollback use PI05 infrastructure, with #18 as a dependency rather than a
second implementation. Update the review with failure evidence and limitations.

## [PI-06/T](https://github.com/shentschel/teddycloud/issues/21) — Audit identifiers, transitions and ownership

Recommended model: `gpt-6-astra`; reasoning: high.
Objective: independently challenge A/B invariants and simplify only demonstrated
duplication without weakening provenance, conflict or recovery behavior.
Depends on A/B, resolved FK-snapshot gate and separate T quota admission.

Files: extend `internal/domain/tag/audit_test.go`, add
`internal/domain/tag/registry_audit_test.go` and
`internal/adapters/sqlite/tag_audit_test.go`; adjust
`internal/architecture/boundary_test.go` only for uncovered leakage. Record
results in `docs/architecture/pi/pi-06-review.md` and an optional T audit.

Acceptance: extend the existing `FuzzUIDByteOrderRoundTrip` coverage to
registration equality and generated state-command sequences. Assert codec
idempotence, permutation-independent evidence conflicts, unrelated-key
invariance, immutable identities and no same-revision lost update. Exercise
zero/max values, revision overflow, oversized evidence, stale resolution,
mixed Content/Tag lifecycle operations and sanitized error chains. No secret,
Original/Custom heuristic, AudioID identity, second writer or SQL port leakage.
Use failures to justify narrow fixes; do not create another state machine.

Verification after implementation, from `next`:

```sh
go test ./backend/...
go vet ./backend/...
go test ./backend/internal/adapters/sqlite -run TestTag -count=10
go test ./backend/internal/domain/tag -run '^$' -fuzz '^FuzzUIDByteOrderRoundTrip$' -fuzztime=10s
go test -race ./backend/internal/adapters/sqlite ./backend/internal/architecture
```

Run additional new fuzz targets separately with bounded time. Parent delivery
validation includes the existing Next checks and required remote CI. A regex
matching zero tests is not evidence. Deferred findings reuse existing issue IDs
where applicable; new findings require parent issue handling, not hidden scope.
Rollback/security remain the A/B contract; no production data is touched.

## Acceptance distinction

R acceptance requires these four consistent documents, successful architecture
documentation checks and parent review of assumptions, boundaries and budgets.
It accepts design only. A/B/T tests above are planned, not run or passed in R.
The transactional Tag milestone requires implemented behavior, the full matrix,
independent T evidence, required CI and no unresolved high-severity/data-loss
finding. Gate B, media/secret restore and deployment remain later milestones.
