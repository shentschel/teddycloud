# PI-08 execution plan

Status: final R decision set; R acceptance is conditional on the publication
checks below. A/B/T and the milestone are not delivered by this document.
Milestone: **versioned catalog records are queryable**.
Issue: [PI-08/R #25](https://github.com/shentschel/teddycloud/issues/25).
Baseline: `96b8cdcbf0bf0986afe3a23fecfbff4a508f2cef` on
`docs/pi-00-r-charter`.

## Inputs, selected decisions and scope

Read the [R1 evidence ledger](pi-08-r1-catalog-inventory.md),
[roadmap](../teddycloud-next-pi-roadmap.md),
[routing](../task-model-routing.md), [execution index](README.md),
PI-04 [plan](pi-04-plan.md)/[review](pi-04-review.md),
PI-05 [plan](pi-05-plan.md)/[review](pi-05-review.md),
PI-06 [plan](pi-06-plan.md)/[review](pi-06-review.md)/
[contract](pi-06-tag-registry-contract.md), and PI-07
[plan](pi-07-plan.md)/[review](pi-07-review.md)/
[contract](pi-07-taf-storage-contract.md).
The latest PI-06/07 review acceptances are prerequisites already satisfied;
do not repeat their completed work because earlier status paragraphs are stale.

The [catalog policy](pi-08-catalog-policy.md) is normative for internal delivery.
It selects core-bound source registrations and immutable batches; field-level
support with retained safe snapshots; explicit review for all acceptance;
catalog-only versions; separate later order/correction decisions; unresolved
unproven arrays; and protected custom identities with explicit compatibility
ambiguity. Confidence never grants authority. No first-hit or implicit overwrite.

R changes only this plan, [budget](pi-08-budget.md), policy and the README's
links/status. No code, schema, provider access, production migration, public V3
freeze, issue creation, legacy mutation or deployment is part of this request.
R1 remains an unchanged historical evidence-only checkpoint.

## R acceptance crosswalk

| R1 question / issue criterion | Final decision and review assertion |
| --- | --- |
| Source classes and issuer binding | Policy D1: allowlisted core issuer/adapter revisions; publisher claims, acquisition and artifact identity remain separate; payloads cannot mint authority. |
| Granularity, support and bounds | D2/resource table: immutable snapshots plus field/relation support, unsupported-field reports, retained dissent and exact byte/count/deadline bounds. |
| Commands, precedence, protected corrections | D3/commands: explicit review, expected revision, canonical replay, no auto-enrichment; guarded persistence prevents Save bypass. |
| Conflict, ambiguity and stale evidence | D3/D5: query all bounded candidates, explicit incomplete/unknown/conflict, stale revision, no arrival/score/AudioID winner. |
| Catalog-only, order and width | D4/D5: independent version reads without media; uint32 durable admission, wider candidates retained; separate effective-order history preserves PI-07 commands. |
| V1/custom/MyTonies/Set | D6/deferrals: unproven arrays unresolved, no positional or offset guess, no custom-first global trust, no heuristic Set membership. |
| Migration and rollback | Additive migration after v4, baseline provenance without fabricated review, managed Save fence, verified backup and exact-schema restore, no media deletion. |
| Bounded delivery and review | A1/A2/B1/B2/T below; named tests and prerequisites, independent T, fresh quota admission. |

Review must challenge spoofed source labels, contradictory accepted support,
same-revision races, custom/public identity collisions, delayed evidence,
catalog-only media attachment and exact old receipt replay. These decisions
resolve R's internal policy questions; deferred public/provider contracts cannot
be implemented by inference.

## Ordered delivery checkpoints

Sequence: accepted R -> A1 -> A2 -> B1 -> B2 -> independent T -> milestone.
A/B use `gpt-6.1-sol` with high reasoning; T uses `gpt-6-astra` with high
reasoning. R's routing recommendation is Astra; this final refinement is
explicitly requested in the current session. No separately executed independent
model review is claimed. Actual model/effort and uncertainty belong in the
private ledger. Each checkpoint needs fresh admission under the budget.
Names below are future tests/files, not claims they already exist or passed.

Paths below are relative to `next/backend`. Changes outside each named scope
require a documented replan. Each checkpoint also updates its PI-08 delivery
review/evidence after implementation; no such review is created by this R request.
Publish/reuse A/B/T issues only when delivery is separately authorized, searching
stable task IDs first. Checkpoints stay inside their parent sprint budget.

### PI-08/A1: guarded catalog domain and application service

Prerequisites: accepted R, PI-04 domain and PI-06 evidence semantics.
Add `internal/domain/catalog/{policy,provenance,decisions,limits}.go` with matching
tests, and `internal/application/catalog/{service,commands,queries}.go` with
matching tests. Extend existing `ports.go`/`errors.go` additively with a distinct
catalog transaction seam. Reuse immutable ContentVersion and generic evidence;
do not alter Tag semantics or PI-07 canonical encoding.

Deliver typed source/capability boundaries, pending observations, explicit review,
expected revisions, exact command replay, field conflicts, catalog-only query
models, effective order and supersession. Fix internal encoding/ID spellings in
the implementation checkpoint with golden vectors before persistence; they must
not become public V3. Verify real encoded limits before copies.

Required tests: `TestCatalogIssuerBinding`,
`TestCatalogAcceptanceTransitions`, `TestCatalogCommandReplay`,
`TestCatalogOrderDecisions`, `TestCatalogLimits` and
`TestCatalogProtectedCustom`. Test spoofed authority, changed same-ID claims,
cross-subject support, active source revocation, current authorization on replay,
confidence independence, contradictory support and every boundary/one-over case.
All observation permutations must retain the same candidates/conflicts.

Rollback: additive source revert only. Security: internal capability handles,
synthetic inputs, no external caller/authentication claim or direct storage.

### PI-08/A2: durable version queries and guarded provenance

Prerequisite: A1 accepted with encoding/bounds evidence.
Add `internal/adapters/sqlite/{catalog_repository,catalog_transaction}.go` and
matching tests; add `catalog_schema_test.go`, `catalog_query_test.go` and
`catalog_recovery_test.go`. Extend `schema.go` by an explicitly numbered
additive migration after v4 and `lifecycle_owner.go` by the shared scoped port.
Change `content_repository.go` only for the managed-subject Save guard.
Read existing version rows without the media-only join; do not modify applied
migrations or PI-07 canonical command bytes.

Required tests: `TestCatalogOnlyVersionQuery`,
`TestCatalogManagedSaveGuard`, `TestCatalogBaselineAdoption`,
`TestCatalogSchemaRecovery`, `TestCatalogCorruptRelations`,
`TestCatalogRevisionRace` and `TestCatalogQueryBounds`.
Prove independent opaque identities sharing fingerprints, no-match/unique/
ambiguous and incomplete results, stable bounded pagination, stale cursors,
atomic projection/support/revision writes, exact replay and raw unsupported-field
retention. Existing rows stay explicitly unreviewed until guarded adoption.

Recovery must upgrade a real v4 fixture while preserving Content/Tag/version/
binding/receipt values and migration checksums; restore current and pre-PI-08
snapshots using the accepted FK verifier. The latter disables catalog commands
without implicit migration. Inject broken support, missing bindings versus
dangling receipts, backup failure and rollback failure; keep originals.

Rollback/security: verified pre-upgrade snapshot and fenced new-path exact-schema
restore; no down migration, media deletion, external fetch or second owner.

### PI-08/B1: offline import and compatibility dispositions

Prerequisite: accepted A2, trusted source registrations and staging budget.
Add `internal/adapters/catalogimport/{offline,report}.go` and matching tests;
add `internal/application/catalog/import.go`/`import_test.go`.
Use generated sanitized fixtures in
`internal/adapters/catalogimport/testdata/`.
Extend `internal/domain/migration/migration_test.go` only to preserve the
existing unequal/unproven-array prototype guarantees.

Implement one bounded offline JSON record envelope carrying explicit pairs and
raw legacy-array candidates. This is an internal adapter contract, not a complete
V1/V2/MyTonies importer. Parse before transaction; commit batch/report atomically.
Reject a batch containing prohibited sensitive fields before retaining any
artifact bytes or digest. Other invalid records receive safe dispositions within
a retained valid envelope. Import never accepts facts or derives identities.

Required tests: `TestCatalogImportDispositions`,
`TestCatalogImportArrayAmbiguity`, `TestCatalogImportNoAuthority`,
`TestCatalogImportUnsupportedFields`, `TestCatalogImportBounds`.
Cover crossed/equal-unproven/unequal arrays, explicit multiple pairs, uint64
out-of-storage-width candidates, unknown fields/enums, duplicate keys, malicious
URI/image references without I/O, source spoofing, custom collisions, exact batch
replay and bounded rejection before persistence. No first-hit or partial success
label hiding rejected/unsupported records.

Rollback: revert adapter and retain previously committed evidence; database
recovery follows A2. No original file replacement or legacy endpoint takeover.

### PI-08/B2: media compatibility, uncertainty and lifecycle

Prerequisites: A2/B1 accepted; accepted PI-07 software milestone including #17.
Add `internal/adapters/sqlite/catalog_media_test.go`,
`catalog_lifecycle_test.go`, `catalog_failure_test.go` and
`catalog_restart_test.go`. Add application catalog recovery tests in
`internal/application/catalog/recovery_test.go`.
Narrow fixes may touch the named catalog adapter/service files and
`internal/adapters/sqlite/blob_repository.go` only if an integration regression
requires them; never change receipt encoding/equality or media verification.

Required tests: `TestCatalogMediaAttachment`,
`TestCatalogReceiptAfterCorrection`, `TestCatalogLifecycleFence`,
`TestCatalogUncertainCommit`, `TestCatalogRestart`,
`TestCatalogSanitizedErrors`.
Attach verified generated media to a catalog-only version with the same ID,
reject changed tuples, distinguish unbound/missing/corrupt/unavailable, and replay
an original PI-07 command after later catalog/order corrections. Verify unchanged
receipt bytes, version facts and domain Assignment references; persistent
assignment recovery is not claimed before PI-09.
Use real helper termination/reopen for an uncommitted catalog write; prove lost
response readback, single callback execution, bounded busy/cancel behavior,
context check before commit, revoked sessions and restore cursor invalidation.
No repeated implementation of accepted PI-07 missing-media or FK verification.

### PI-08/T: independent ambiguity and migration audit

Prerequisite: accepted A/B, separate Astra admission and independent reviewer.
Add `internal/domain/catalog/policy_audit_test.go`,
`internal/adapters/catalogimport/audit_test.go`,
`internal/adapters/sqlite/catalog_audit_test.go`; extend
`internal/architecture/boundary_test.go` only for uncovered boundary leakage.
Scoped fixes remain within A/B targets; record new findings under stable IDs.

Required tests: `TestCatalogPolicyPermutations`,
`TestCatalogParallelArrayRegression`, `TestCatalogMigrationAudit`,
`TestCatalogMilestoneQueryable`, plus bounded
`FuzzCatalogImportEnvelope` and `FuzzCatalogDecisionSequence`.
Challenge all policy decisions and failure outcomes independently, especially
source capability spoofing, Save bypass, confidence promotion, customs dedupe,
candidate truncation presented as unique/latest, receipt mutation and rollback
data loss. Remove parallel-array assumptions from the new authoritative path;
legacy C behavior stays unchanged until its compatibility PI.

No unresolved High/security/data-loss finding permits acceptance. Record exact
commit, tests, remote CI and deferred IDs; a green suite alone is not an audit.

## Verification and milestone evidence

R from repository root:

```sh
python3 scripts/check_architecture_docs.py
git diff --check
git diff --stat
```

Review all three new files as well as the README diff; unstaged/untracked files
are not covered by ordinary git diff. When local process creation is unavailable,
use GitHub tree/compare/readback for the exact four-file scope and the existing
documentation workflow to execute the exact Python command. Do not claim a
local clean checkout or local checker pass. Remote head must match the published
commit and the successful workflow must belong to that SHA.

For A/B/T from `next`, after the proposed packages exist:

```sh
go test ./backend/internal/domain/catalog ./backend/internal/application/catalog ./backend/internal/adapters/catalogimport ./backend/internal/adapters/sqlite ./backend/internal/architecture
go test ./backend/...
go vet ./backend/...
go test ./backend/internal/adapters/sqlite -run TestCatalog -count=10
go test -race ./backend/internal/domain/catalog ./backend/internal/application/catalog ./backend/internal/adapters/catalogimport ./backend/internal/adapters/sqlite ./backend/internal/application/contentstore
go test ./backend/internal/adapters/catalogimport -run '^$' -fuzz '^FuzzCatalogImportEnvelope$' -fuzztime=10s -parallel=2
go test ./backend/internal/domain/catalog -run '^$' -fuzz '^FuzzCatalogDecisionSequence$' -fuzztime=10s -parallel=2
```

A1 runs only its existing domain/application/architecture packages; A2 adds
SQLite; B1 adds catalogimport. A regex executing zero tests is not evidence.
Delivery also requires `make -C next check` from root, architecture docs and
the existing Next CI jobs; qualified filesystem tests retain PI-07 requirements.
These are future delivery tests, not R results.

The milestone test must register two independently identified Contents (one
protected custom), retain competing provenance, accept explicit versions,
query an unbound version after restart, return ambiguity for shared fingerprints,
retain unsupported fields, demonstrate a revision-fenced correction and unknown/
conflicting order, attach media without changing version identity, preserve old
receipts/assignments, and repeat catalog queries after verified restore.
A/B/T, migration/recovery evidence, independent audit and green CI are all
required before claiming the milestone.

## Publication and acceptance record

Final R review finds the bounded decisions consistent with the accepted
PI-04--PI-07 contracts; later public/provider/production questions are explicitly
owned in policy PI08-D01--D05. This is a review of the decision set, not an
independent implementation audit or delivered runtime capability.

The R gate is satisfied only when the four-file diff and whitespace review,
architecture checker and remote head/Actions verification all pass. The
commit-linked evidence comment on #25 records the observed result and final
R disposition. Until that evidence exists this is a reviewable decision set,
not unconditional acceptance. Even after R acceptance, A1 requires a new
execution admission; this request does not start delivery or close later gates.
