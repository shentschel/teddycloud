# PI08-T-F01 registration refinement checkpoint

Status: **incomplete refinement; not implementation-ready**. No production
implementation or PI-08/T acceptance is claimed. This checkpoint records the
confirmed constraints and the remaining decisions needed for
[PI08-T-F01 #27](https://github.com/shentschel/teddycloud/issues/27).
[PI08-T-F02 #28](https://github.com/shentschel/teddycloud/issues/28), immutable
version supersession, remains a separate task.

## Evidence and inherited acceptance

The [plan](pi-08-plan.md) and [policy](pi-08-catalog-policy.md) require guarded
registration. The [A2](pi-08-a2-acceptance-audit.md),
[B1](pi-08-b1-acceptance-audit.md), [B2](pi-08-b2-acceptance-audit.md) and
[T checkpoint](pi-08-t-audit-checkpoint.md) remain authoritative for their
existing evidence. F01 closes the missing authoring path; it does not reopen
accepted evidence or turn SQL-created fixtures into registration proof.

Inspection of the existing APIs establishes these integration constraints:

- `application/catalog/Service` binds one source, review capability and order
  namespace. `run` already supplies bounded transactions, current authorization,
  callback-once checks and sanitized errors. Additive registration methods must
  use this lifecycle seam rather than ordinary `ContentRepository.Save`.
- `CatalogRevision` is positive. New-content absence cannot be represented by
  pretending revision zero is a valid revision. Registration needs an explicit
  expected-absent condition; successful creation yields aggregate revision 1.
- Existing review state starts at revision 1 with no accepted decisions;
  `Apply` advances its own revision. Aggregate and review revisions are distinct.
  Calling today's RecordObservations/AcceptSupport in separate transactions is
  not atomic initial registration and does not satisfy revision-1 creation.
- A source with `OfflinePublic` or `OfflineCustom` acquisition cannot authorize
  `OwnershipClaimKey`. Version registration therefore cannot treat a staged
  public pair as authority to assign Content ownership.
- Existing order state contains an immutable version manifest. Adding a version
  must preserve accepted order history while incorporating the new version as
  unordered. Reconstructing a fresh order state would erase accepted decisions.
- Migrations 1..7 exist. Migration 5 decision rows require positive expected
  revisions and result = expected + 1; migration 6 constrains state encoding 1
  and immutable snapshots. A new registration cannot be squeezed into these
  formats by editing already applied migrations or fabricating normal decisions.

Source paths above are relative to `next/backend/internal`. Relevant domain
files are `catalog/{content,policy,provenance,decisions,order,adoption}.go`;
application files are `catalog/{ports,commands,service,adoption}.go`; persistence
constraints are in `adapters/sqlite/{schema,catalog_adoption}.go`.

## Confirmed command and identity requirements

Both commands require a separately allocated opaque `CatalogCommandID` and
ContentID; version registration also requires an independent ContentVersionID.
No identity derives from title, model, article, AudioID, hash, record position or
blob. IDs must be allocated before invocation and retained across retries.

`RegisterContent` creates an absent managed Content with a nonempty supported
title and an explicit source-record association. Optional model/article fields
need their own selected support. Existing unmanaged identities require the
accepted baseline-adoption flow; registration cannot silently adopt them.

`RegisterCatalogVersion` requires an existing managed Content and its current
expected aggregate revision, explicit supported ownership and one inseparable
AudioID/SHA1 claim. Durable AudioID is nonzero uint32; wider candidates retain
their unsupported-width disposition. Creation inserts an unbound immutable
version and advances the aggregate once. Missing/conflicting support cannot
create a version. No media I/O or placeholder receipt is permitted.

Registration must retain complete canonical commands and original results.
Canonical comparison includes operation kind, encoding/policy revision,
command/subject IDs, expected condition, core-bound principal/source revision,
every selected support reference, admitted evidence, reason and immutable facts.
Normalize sets deterministically before encoding; reject duplicate support IDs.
Use the existing big-endian uint64 and length-framed byte conventions, with
counting before allocation and golden vectors. Exact field order, new format
tags and result layout are still blocking decisions below; existing encodings
must remain byte-for-byte unchanged.

Current authorization precedes lookup. Exact committed replay precedes stale
revision rejection and returns the original result without another write or
budget charge. Changed command data with the same key conflicts. A different
command claiming an existing identity does not become an implicit replay.
Distinct version IDs may share a fingerprint, including across Content IDs;
queries must retain ambiguity. Reusing one version ID with changed ownership,
fingerprint or original order conflicts. No registration chooses a latest
version from arrival time, source labels or ID ordering.

## Authority, transaction and recovery requirements

Capabilities come from trusted core composition. Imported labels, confidence,
review flags and detached persisted source objects cannot grant authority.
Rebinding after restart must match the entire source registration and reviewer
binding; matching human-readable names is insufficient. Revoked sources deny
new operations, replay and command readback. Preserve their historical evidence.
Check both current capability state and persisted source revocation before
writes, and current authority again before commit through the owner boundary.

The selected source-record association must refer to retained safe bytes and
their exact batch, locator, parser revision and source registration. Reject
cross-subject observations and changed same-ID artifacts. Protected custom
provenance is monotonic and cannot be removed by registration or refresh.
Offline ownership cannot be inferred; the missing multi-source/core-operator
support design is explicitly unresolved below.

Use one callback-scoped catalog repository, the shared owner connection and
restore fence. No nested transaction, callback retry or independent SQL owner.
Validate bounds before materialization. Insert identity, managed projection,
support, registration result and query-visible state atomically. Every write
stage needs rollback injection. Leaked repositories are revoked on callback
exit. Deadlines remain 10 seconds per operation and 5 per transaction, bounded
by any earlier caller deadline; check cancellation immediately before commit.

Preserve B2 uncertainty rules: pre-commit cancellation/confirmed rollback leaves
no partial registration; attempted commit with uncertain outcome returns
indeterminate. Cleanup failure alone without attempted commit does not imply
durability. Do not mask uncertainty with cancellation or retry automatically.
Authorized readback with the complete original command distinguishes committed,
absent, conflicting and unavailable outcomes. Absence permits an explicit exact
retry only after the earlier operation is no longer able to commit. Never
compensate uncertainty with deletion or return raw SQL/path/private values.

## Compatibility and required persistence decisions

Use an additive migration after 7 with restrictive foreign keys and immutable
registration evidence. Preserve all existing migration checksums, ContentVersion
tuples, PI-07 receipt/canonical bytes, bindings and Assignment references.
Registration must not write blob storage or make an availability claim. Later
PI-07 attachment must match the original complete version tuple. A catalog
correction or order decision cannot change what PI-07 considers exact replay.

Queries must show accepted registration provenance, pending/dissenting support
and unsupported fields, independently of media presence. Ordinary Save remains
fenced for managed subjects. Registration invalidates affected query cursors;
restart/restore retain existing invalidation rules. A restored older schema
must report registration unavailable without automatic migration.

These decisions remain **blocking**, so no implementation slice is admitted by
this checkpoint:

| Decision to finish | Required concrete result |
| --- | --- |
| Initial accepted state | Choose constructor/encoding and additive tables that represent accepted registration at aggregate revision 1, then allow ordinary review without fabricated revisions or replaying an initialization as a later decision. |
| Multiple evidence authorities | Specify how core-operator ownership and offline pair/title provenance are bound and reviewed together despite the service's current single-source scope; include detached/revoked and restart rebind semantics. |
| Version manifest growth | Choose an append-only manifest evolution that preserves historical order snapshots and comparisons while new versions are unknown in effective order. |
| Canonical bytes and collision scope | Fix complete command/result field order, tags, set normalization and cross-kind collision checks against review/order/adoption/import keys without mutating their accepted encodings. |
| Schema/query integration | Fix migration number/identity, new table constraints, size/count accounting and state-reader changes; prove old/new snapshot reconstruction and exact-schema restore. |

## Implementation decomposition to finalize

After the blocking decisions are resolved, use `gpt-6.1-sol` with high reasoning
for each separately admitted slice. The following disjoint ownership is a
planning boundary, not a complete implementation contract:

| Slice | Exclusive files below `next/backend/internal` | Dependency and acceptance |
| --- | --- | --- |
| F01-A domain | New `domain/catalog/registration.go`, `registration_test.go`; existing `decisions.go`, `order.go` and matching tests only for the selected state extension | Final contract first; canonical golden vectors, authority, bootstrap and manifest continuity tests. |
| F01-B application | New `application/catalog/registration.go`, `registration_test.go`; existing `service.go` only if selected binding design requires it | A; scoped extension interface in new file, callback-once, cancellation, exact replay and authorized readback tests. |
| F01-C persistence | New `adapters/sqlite/catalog_registration.go`, `catalog_registration_test.go`; existing `schema.go`, `catalog_repository.go` | A/B; atomic migration, durable replay, race, injection and query compatibility tests. Actual additional state-reader file scope must be enumerated before admission. |
| F01-D integration | New `adapters/sqlite/catalog_registration_integration_test.go`; existing `catalog_audit_test.go` | C; complete authoring/restart/restore/media path without direct SQL seeding of new identities. |

Each slice records its exact commit, local checks and required CI. Source rollback
is a focused revert while new registration remains disabled. Database rollback
uses a verified pre-upgrade backup and fenced exact-schema restore, retaining the
new database and media for reconciliation. No down migration, cleanup, automatic
evidence loss or production rollback claim is authorized.

## Mandatory negative and integration matrix

The finished contract must map every row to named tests and exact expected
typed errors, with before/after equality for rejected mutations:

| Family | Cases that must be covered |
| --- | --- |
| Input and bounds | Zero/malformed IDs, empty/control-bearing title/reason, invalid hash/AudioID, width overflow; exact and one-over counts/encoded bytes, aggregate overflow, duplicate support and malformed snapshot. |
| Authority | Zero/ingest-only/detached handles, spoofed principal/source/scope, forbidden ownership from offline source, revoked source before lookup and before commit, source revision mismatch, persisted revocation, authorized restart rebind. |
| Evidence | Missing, cross-subject, cross-version or changed same-ID support, record/artifact mismatch, unsupported pair, unequal/equal-unproven arrays, contradictory selected claims, confidence promotion, protection removal. |
| Identity and replay | Exact replay after later revision and restart; changed key payload/expected revision/reviewer; cross-kind key collision; existing unmanaged/managed Content; existing version under another Content; same fingerprint with distinct IDs remains ambiguous. |
| Order and media | Known/unknown original order mismatch, preserved accepted order after manifest growth, no inferred latest; exact PI-07 attachment/replay, changed tuple/rebinding rejection, unbound versus missing/corrupt media, unchanged receipt/Assignment values. |
| Atomicity and recovery | Every insert failure, same-revision independent-owner race, cancellation before/between writes/before commit, rollback failure, commit-before/after failure, lost response, unavailable/absent/conflicting authorized lookup, leaked repository, restore/close busy fence. |
| Query and migration | Real v7 upgrade, unchanged old checksums/rows, missing/dangling/corrupt relations, bounded ambiguous results and stale cursors, current and pre-registration schema restore, no silent migration. |

Milestone evidence must start from a newly migrated database and staged sanitized
records. Register two independent Contents (one protected custom), explicitly
retain competing provenance, register their versions through the new commands,
and exercise acceptance/correction, ambiguous shared fingerprints, unknown and
conflicting order, restart, exact media attachment and verified restore.
Existing legacy fixtures may still support migration tests; they cannot author
the new milestone subjects. F02 supersession is a separate remaining gate:
registration does not create supersedes edges, detect their cycles, retarget
assignments or close that finding. Public V3, hardware, full legacy import and
production migration evidence remain outside this refinement.
