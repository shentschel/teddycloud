# PI08-T-F01 registration contract

Status: **implementation-ready, R2 decision set**. No production implementation
or PI-08/T acceptance is claimed. R2 completes the incomplete R1 checkpoint
`d51b74536b2f4f5099461eed51f2df4ab0ce52b9`. The following decisions govern
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
tags and result layout are fixed below; existing encodings
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
Offline ownership cannot be inferred; the multi-source/core-operator support
design below requires an independently admitted operator observation.

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

The five R1 decisions are closed by the normative sections below. Fresh job
admission is still required; the implementation and independent audit remain open.

| Decision to finish | Required concrete result |
| --- | --- |
| Initial accepted state | Separate bootstrap support, review revision 1, no ordinary decision; review-state/2. |
| Multiple evidence authorities | Registration service binds one operator plus an explicit source/reviewer allowlist. |
| Version manifest growth | Append events advance order revision, retain historical snapshots, never assign position. |
| Canonical bytes and collision scope | Registration/1 grammar and database-wide new-key exclusion below. |
| Schema/query integration | Migration 8, parallel v2 state tables, restrictive links and bounded reconstruction below. |

## R2 normative decisions

### Bootstrap and authority

Add domain `CatalogRegistrationCommand`, `CatalogRegistrationResult` and
`NewRegisteredCatalogReviewState(capability, command)`. The command has kind
1 (Content) or 2 (Version). Expected condition is a closed union: absent for
kind 1, positive aggregate revision for kind 2. Zero is only the absent union's
wire payload, never a `CatalogRevision`. Both new subjects start review revision
1. Content starts aggregate revision 1 and an empty order state revision 1;
Version increments the existing aggregate once. Bootstrap is one explicit
registration action with selected support, not an `AcceptSupport` decision.

Store bootstrap observations and selected support separately from ordinary
decisions. `Field` seeds its selected-ID set from bootstrap support, then folds
ordinary decisions in original order. `ResolveConflict` clears that set for its
key, including bootstrap selections; historical bootstrap evidence remains.
First ordinary `Apply` advances review revision 1 to 2. Unselected observations
remain pending/dissenting. Content requires exactly one selected title value;
model/article are optional, but each present value needs selected support.
Version requires exactly one selected fingerprint and owner equal to its Content.
Multiple independent supports for the same value are allowed; competing selected
values reject the registration. Dissent is retained by leaving it unselected.
No claim is inferred from confidence. Protection is OR of inherited aggregate
protection and every admitted `OfflineCustom` observation; it never decreases.

Add domain `NewCatalogRegistrationCapability(operatorReview, evidenceReviews)`.
Operator source must be `CoreOperator`; evidence reviews are an allowlist of
live source/revision handles with the same reviewer principal as the operator.
Every observation requires its own source's review authorization for its key,
including unselected evidence. Operator additionally needs title scope for kind
1, ownership scope for kind 2. Offline sources may supply title/model/article/
fingerprint, never ownership. Ownership requires a separate `CoreOperator`
observation over a retained sanitized operator record. No generated fake source,
import promotion or synthetic review command. Allowlist includes the operator;
no duplicate source/revision binding; unused evidence bindings are excluded from
canonical command bytes. Scope text remains opaque configuration, equality is
the complete binding, not textual scope matching.

Application adds `RegistrationService` in its new file, constructed from a
`Service` bound to the operator and the domain registration capability. It calls
the existing `Service.run` and type-asserts an additive `RegistrationRepository`
port. Its methods are `RegisterContent`, `RegisterCatalogVersion`,
`ReadRegistration`; callers cannot supply authority or reviewer strings.
The port provides `LookupRegistration(ctx, operation)` and
`CommitRegistration(ctx, operation)` returning the typed result; lookup returns
`found=false` for an authorized absent key. Operation contains capability and
complete command. Commit computes/validates all state inside the owner callback.
The owner adds all used source-authorizations to its pre-commit checks and checks
persisted source revocation inside the transaction. Authorize before any lookup,
including replay/readback. Detached or revoked handles deny. On restart use the
existing trusted rebind path with complete persisted source spec/revision and
reviewer identity; decoded data alone remains persistence-only. Missing trusted
binding denies commands but does not erase readable historical provenance.

### Review and order state encodings

Old encoding 1 bytes, readers and historical rows remain unchanged. New
registered review states use `pi08-review-state/2`: length-framed tag, complete
registration command bytes, original registration result bytes, then a framed
review-state/1-shaped body whose tag is `pi08-review-tail/2`. The tail uses the
exact v1 subject/revision/protection/links/observations/decisions field sequence.
It contains all observations once (bootstrap first in ID order, later additions
in existing append order); links includes bootstrap selections. Decode by
validating bootstrap, then replaying the ordinary tail decisions against it;
never feed this tail to the v1 constructor or fabricate initialization decisions.
Registered subjects always stay v2; baseline-adopted subjects remain v1.

Add `AppendRegisteredVersion` to order state. It validates immutable version
identity, preserves all accepted order evidence/results and appends a growth
event, advancing order revision once without a review/order command. Each growth
event references its registration command and result aggregate revision. Current
manifest is the old manifest union registered additions sorted by opaque ID.
New versions have unknown original order: namespace empty, position 0, known 0.
Known original order supplied to registration is rejected as unsupported; later
explicit order commands are the supported path. This conservative choice avoids
inventing source sequence authority in F01.

After first growth, order encoding is `pi08-order-state/2`: framed tag, framed
unchanged last v1 snapshot, event count, chronological events. Growth event is
`N(1), T(commandID), N(resultAggregate), VersionTuple`; ordinary order event is
`N(2), T(existing canonical order command), T(existing canonical order result)`.
VersionTuple is `T(versionID), T(contentID), N(audioID), T(lowercase SHA1),
T(original namespace), N(position), N(known)`. Replay events sequentially against
the manifest at that event, validate each ordinary result using existing order
semantics. One event advances order revision by one; event count plus base
decisions/growth must stay <=1024 and order revision <=1025. No snapshot changes
retroactively. Old comparisons remain valid absent a new explicit conflict;
new-versus-old is unknown, self comparison is same, mismatched immutable tuple
is conflict. Effective order is unknown when additions lack positions, retains
old positions, and preserves existing conflict precedence. No latest inference.

### Canonical registration grammar and vectors

`N(x)` is unsigned 64-bit big-endian; `T(b)=N(byteLength(b)) || b`; booleans are
N(0)/N(1). Concatenation below is literal, without padding or separators. IDs use
their existing lowercase `String()` representation; absent version is empty.
No trimming, Unicode normalization, case folding of prose, or JSON reserialization.
Time is existing UTC RFC3339Nano without monotonic component. Hash uses lowercase
hex; artifact SHA256 stays 32 raw bytes. Reject invalid UTF-8/control-bearing
text through existing validators. Counts are checked before copying/allocation.

Command field order is exactly:

```text
T("pi08-registration/1"), T("pi08-catalog/1"), N(kind), T(commandID),
T(contentID), T(versionID-or-empty), N(expectedKind), N(expectedValue),
Source(operator), T(reviewer), N(sourceCount), Source(usedSource)*,
T(reason), N(observationCount), Observation*, N(selectionCount),
(N(claimKey), T(observationID))*
```

expectedKind is 0/1 for absent/revision; kinds 1/2 require respectively (0,0)
and (1,1..MaxInt64-1). Source includes ID, revision, issuer, adapter,
adapterVersion, scope as T fields, acquisition as N, allowed-claim count as N,
then ascending N keys. Used sources include operator and every observation source,
sorted by source ID then revision. Observation is exactly the existing
`catalogEncoder.observation` sequence: T observation ID, T ContentID, T VersionID,
claim (N key, T text, N AudioID, T SHA1, T owner), Source, T batch hex ID,
raw SHA256, N artifact byte length, N record count, T parser revision,
T source revision, N record index, T locator, T exact snapshot bytes,
T asserted origin, T UTC time, N confidence. Zero inactive claim components use
the existing claim encoder representations. Observations sort by ID; selections
sort by key then observation ID. Reject duplicate observation IDs and duplicate
selection IDs, even identical duplicates. Every selection resolves to an admitted
observation of that key. No optional-field flags are needed: facts derive solely
from selected claims; original order is the fixed unknown tuple above.

Result field order:

```text
T("pi08-registration-result/1"), N(kind), T(commandID), T(contentID),
T(versionID-or-empty), N(aggregateRevision), N(reviewRevision),
N(orderRevision), N(protected), N(fieldCount),
(N(key), N(status), N(alternativeCount),
 (Claim, N(supportCount), T(supportID)* )* )*
```

Fields are keys 1,2,3 for Content; 4,5 for Version, always present in ascending
order. Missing optional fields encode unknown/status 0 and zero alternatives;
accepted fields status 1 with one alternative; registration cannot return
conflicting selected facts. Claim uses the five-component grammar above.
Support IDs ascend. Results are original committed values, never reconstructed
from later current revisions for replay. No timestamp or media availability.

Golden vectors are normative token vectors using this exact grammar (expand N/T,
never JSON): `N(0)=0000000000000000`, `N(1)=0000000000000001`,
`T("A")=000000000000000141`, `T("")=0000000000000000` in hex.
`TestRegistrationGoldenVectors` must freeze complete bytes independently of the
production encoder for these fully specified fixtures: opaque 16-byte values
are 15 zero bytes plus their indicated integer; typed IDs are their current
canonical rendering of that value. Content=1, version=2, command=3, operator
source=4/revision=5/issuer=6, reviewer=7, batch=8, observation=9; operator spec
adapter=`op`, version=`1`, scope=`catalog`, acquisition=3, claims=[1,5].
Use a second source=10/revision=11/issuer=12, adapter=`offline`, version=`1`,
scope=`catalog`, acquisition=1, claims=[1,4]. All records index=0, locator=`0`,
parser=`1`, sourceRevision empty, assertedOrigin empty, time=`2026-01-01T00:00:00Z`,
confidence=0; snapshot exact `{}`, artifact length=2/count=1 and SHA256 of `{}`.
Content vector: reason=`create`, one title observation `A` from offline, selected
key 1/observation 9, expected absent; result aggregate/review/order=1/1/1,
protected=0. Version vector: command=13, expected aggregate=1, reason=`version`,
fingerprint observation=14 from offline batch=15, AudioID=1/SHA1=20 bytes 0x11;
owner observation=16 from operator batch=17, owner=Content1. Both selected;
result aggregate/review/order=2/1/2, protected=0. Unused scalar claim fields are
zero/empty as above. Test also reversed input source/observation/selection order
produces identical bytes, and changed reason/expected/source/reviewer differs.
Freeze the expanded hex literals in A's test before admitting B/C; these fixtures
are not claimed executed by this documentation refinement.

Limits: <=256 used sources, <=64 incoming observations, <=256 selections,
<=4096 retained observations, <=1024 versions, <=1024 total ordinary decisions
plus registration/growth events per aggregate, <=16384 retained support links.
References <=128 bytes, prose <=4096, product identifiers <=64; batch <=1MiB/32
records, snapshot <=64KiB/depth16/256 fields. Each encoded command/result/state
and their combined retained aggregate accounting must fit 8MiB. Count exact
encoded bytes plus immutable historical snapshots and relational support indices;
charge each physical retained copy, never charge exact replay again. Staging
remains 64MiB/256 batches; no quota reset or dropping history. Overflow maps to
the existing `LimitError` resource and application `ErrLimitExceeded`.

New registration command keys are excluded database-wide from review/order keys,
adoption command batch IDs and all import/artifact batch IDs. Evidence batch IDs
cannot equal their registration command. Pre-existing legacy overlaps remain
readable; no retroactive uniqueness migration. Under the same transaction, all
old writers must reject a key occupied by registration, including artifact writes.
Same registration key plus different complete bytes returns `ErrCommandConflict`;
exact registration replay is checked before stale revision. A different command
targeting existing Content/version returns `ErrCommandConflict`, including exact
same immutable facts; do not silently adopt. No PI-07 key/receipt format changes;
its separate command namespace is intentionally outside this collision scope.

### Migration 8 and readers

Reserve migration version 8, ID `0008-catalog-registration`. Do not alter 1..7.
All new tables are STRICT, immutable (BEFORE UPDATE/DELETE abort triggers), with
RESTRICT FKs. Add these tables; names and columns are fixed:

| Table | Columns, keys and constraints |
| --- | --- |
| `tc_catalog_registrations` | `command_id` PK canonical nonzero hex32; `kind` 1/2; `subject_id,content_id` composite FK subjects; `expected_kind,expected_revision,result_revision` with absent/0/1 for kind1 or revision/positive/expected+1 for kind2; `review_revision=1`; positive `order_revision<=1025`; `protected_custom` boolean; `canonical_command,canonical_result` nonempty BLOB each <=8MiB; UNIQUE(subject_id), UNIQUE(content_id,result_revision). |
| `tc_catalog_registration_sources` | `command_id,source_id,registration_revision` PK, command FK and composite source FK; `reviewer_principal` canonical nonzero hex32; `is_operator` boolean; exactly one operator enforced by partial unique index and repository validation. |
| `tc_catalog_registration_support` | `command_id,observation_id` PK; command FK; `subject_id,source_id,registration_revision,claim_key,observation_id` composite FK observations; composite command/source/revision FK registration_sources; key 1..5; insertion trigger enforces registration subject/content agreement. |
| `tc_catalog_review_states_v2` | Same subject/content/aggregate/review/state/state_bytes/state_sha256 columns and FKs as v6, encoding=2; PK(subject_id,review_revision), UNIQUE(subject_id,aggregate_revision); `bootstrap_command` FK registrations. Header is review-state/2, bytes 1..8MiB, exact length and SHA256 verified before decode. |
| `tc_catalog_order_states_v2` | Same content/aggregate/order/state/state_bytes/state_sha256 as v6, encoding=2; PK(content_id,order_revision), UNIQUE(content_id,aggregate_revision); header order-state/2; bytes 1..8MiB and exact length/hash verified. |
| `tc_catalog_order_state_versions_v2` | content/order/version PK and composite FK new order snapshot; composite FK existing content_versions. <=1024 entries per snapshot. |

No FK cascade, no inferred identity from fingerprints. Existing observation rows
carry bootstrap provenance; bootstrap selections use the new support table,
never migration-5 ordinary decisions. Sources/batches/records use existing exact
identity validation. Registration result facts are validated against these rows,
version tuple and bootstrap snapshot. Add cross-table append triggers preventing
overlapping/reversed revisions between old/new state tables. Review v2 first row
is revision1; order v2 first row is latest v1 revision+1; subsequent append must
increment corresponding revision exactly once and match current managed aggregate.
All snapshot rows are historical; only latest manifest must match current versions.
Old snapshot manifest checks use its historical manifest, not all current rows.

Atomic write order: authorize/lookup/collision/bounds; insert Content for kind1
or CAS existing aggregate for kind2; insert managed/subject/version as applicable;
persist sources/artifacts/records/observations; insert registration/result;
insert source/support links; insert review bootstrap and order snapshot/manifest;
validate relational reconstruction/accounting; final authorization/cancel; commit.
For Content insert managed aggregate1; for Version CAS increments aggregate once,
sets monotonic protection, adds subject review1 and unbound immutable version.
Rollback every stage together. No blob row, binding, receipt or Assignment write.
Migration creates empty new tables; it does not rewrite current data, projections,
old hashes or snapshots. New triggers also prohibit ordinary writer collisions
with registration keys. New aggregate accounting adds registrations, all v2 state
bytes and new source/support index encodings to existing retained-byte charges;
index charge is the N/T encoding of every logical column value, not page size.

Reader dispatch accepts schema 5..8 as today with added 8; registration requires
exact supported clean v8, otherwise `ErrUnsupported`. Current review/order picks
the highest validated revision across v1/v2; ties, gaps, dangling links, incorrect
hash/length/result/projection or unauthorized-shaped persisted claims return
`ErrRepositoryUnavailable`, never a best-effort partial record. Query folds
bootstrap plus later ordinary decisions, exposes accepted provenance and pending
dissent through existing query models, and retains unsupported import reports.
Use existing bounds 128 entries/1MiB and all-candidate ambiguity semantics. Include
registration and v2 state digests in catalog snapshot/cursor MAC input; any relevant
registration invalidates prior cursors (`ErrStaleCursor`). Existing owner generation
continues invalidating restart/restore cursors.

Restore current v8 through the accepted owner fence/FK/checksum verifier; query
and exact authorized readback survive fresh trusted rebind. Restore a verified
v7 snapshot exactly, preserving originals: existing v7 operations work, registration
returns `ErrUnsupported`, no implicit upgrade. A later explicit normal migration
may upgrade it. Unsupported future/dirty schema fails closed. These are software
test requirements, not production migration evidence.

## Final implementation decomposition

Use `gpt-6.1-sol` with high reasoning for each separately admitted slice.
The following exclusive file ownership includes matching new tests. Existing
test files are owned only where explicitly listed; no other file edits are
implicitly admitted. B may begin after A; C after A/B; D after C.

| Slice | Exclusive files below `next/backend/internal` | Dependency and acceptance |
| --- | --- | --- |
| F01-A domain | New `domain/catalog/registration.go`, `registration_test.go`; existing `decisions.go`, `order.go`, `decisions_test.go`, `order_test.go` | Canonical golden vectors, authority, bootstrap and manifest continuity tests; old encoding vectors unchanged. New v2 helpers stay in registration.go. |
| F01-B application | New `application/catalog/registration.go`, `registration_test.go` | A; scoped extension interface, service wrapper, callback-once, cancellation, exact replay and authorized readback tests. Existing Service.run and sanitizer unchanged. |
| F01-C persistence | New `adapters/sqlite/catalog_registration.go`, `catalog_registration_test.go`; existing `schema.go`, `catalog_repository.go`, `catalog_transaction.go`, `catalog_adoption.go`, `catalog_import.go` | A/B; atomic migration, durable replay, race, injection, v1/v2 readers, cross-kind guards, accounting and query compatibility tests. Helpers and new tests remain in new files. |
| F01-D integration | New `adapters/sqlite/catalog_registration_integration_test.go`; existing `catalog_audit_test.go` | C; complete authoring/restart/restore/media path without direct SQL seeding of new identities. |

Each slice records its exact commit, local checks and required CI. Source rollback
is a focused revert while new registration remains disabled. Database rollback
uses a verified pre-upgrade backup and fenced exact-schema restore, retaining the
new database and media for reconciliation. No down migration, cleanup, automatic
evidence loss or production rollback claim is authorized.

## Mandatory negative and integration matrix

Each family below is bound to the named subtest suite following the table.
Every rejected mutation requires before/after database equality.

| Family | Cases that must be covered |
| --- | --- |
| Input and bounds | Zero/malformed IDs, empty/control-bearing title/reason, invalid hash/AudioID, width overflow; exact and one-over counts/encoded bytes, aggregate overflow, duplicate support and malformed snapshot. |
| Authority | Zero/ingest-only/detached handles, spoofed principal/source/scope, forbidden ownership from offline source, revoked source before lookup and before commit, source revision mismatch, persisted revocation, authorized restart rebind. |
| Evidence | Missing, cross-subject, cross-version or changed same-ID support, record/artifact mismatch, unsupported pair, unequal/equal-unproven arrays, contradictory selected claims, confidence promotion, protection removal. |
| Identity and replay | Exact replay after later revision and restart; changed key payload/expected revision/reviewer; cross-kind key collision; existing unmanaged/managed Content; existing version under another Content; same fingerprint with distinct IDs remains ambiguous. |
| Order and media | Known/unknown original order mismatch, preserved accepted order after manifest growth, no inferred latest; exact PI-07 attachment/replay, changed tuple/rebinding rejection, unbound versus missing/corrupt media, unchanged receipt/Assignment values. |
| Atomicity and recovery | Every insert failure, same-revision independent-owner race, cancellation before/between writes/before commit, rollback failure, commit-before/after failure, lost response, unavailable/absent/conflicting authorized lookup, leaked repository, restore/close busy fence. |
| Query and migration | Real v7 upgrade, unchanged old checksums/rows, missing/dangling/corrupt relations, bounded ambiguous results and stale cursors, current and pre-registration schema restore, no silent migration. |

Exact application errors below are the existing sentinels in `errors.go`;
domain tests assert their existing mapped domain equivalents. Identity collisions
normalize to `ErrCatalogCommandConflict` before crossing the service boundary,
so no new unmapped `ErrVersionIdentityConflict` escapes. Error precedence is
current authorization, syntactic/bounds validation, canonical key replay/conflict,
subject existence/revision, evidence validation, persistence. No-match readback
is `(zero,false,nil)`, never a success result with fabricated revision.

| Named suite (subtests cover every corresponding family case) | Exact outcomes |
| --- | --- |
| `TestRegistrationInputBounds` (A) | Malformed IDs/text/hash/snapshot and duplicate IDs: `ErrInvalidInput`; unsupported durable width: `ErrUnsupported`; each resource exact-bound succeeds and one-over is `ErrLimitExceeded`; aggregate MaxInt64: `ErrRevisionOverflow`. |
| `TestRegistrationAuthority` (A/B/C) | Zero/ingest-only/detached/revoked/forbidden ownership: `ErrUnauthorized`; wrong live source or revision: `ErrSourceMismatch`; spoofed reviewer: `ErrUnauthorized`; persisted revocation and before-commit revoke: `ErrUnauthorized`; exact trusted restart rebind succeeds. |
| `TestRegistrationEvidence` (A/C) | Missing selection, unproven arrays, unsupported pair or contradictory selection: `ErrUnsupported`; cross-subject/version: `ErrSubjectMismatch`; changed same-ID observation/artifact/record: `ErrEvidenceConflict`; wrong artifact source: `ErrSourceMismatch`; confidence alone does not select; custom protection is monotonic, attempted caller-supplied protection override: `ErrInvalidInput`. |
| `TestRegistrationReplay` (B/C) | Exact later/restart replay returns original result; changed key payload/expected/reviewer and cross-kind key collision: `ErrCommandConflict` after current authorization; different command for occupied identity: `ErrCommandConflict`; absent managed parent: `ErrSubjectNotFound`; stale aggregate: `ErrRevisionConflict`; distinct shared fingerprints succeed and query ambiguous. |
| `TestRegistrationManifestAndMedia` (A/D) | Supplied known original order: `ErrUnsupported`; immutable tuple reuse: `ErrCommandConflict`; old positions retained/new comparison unknown; exact PI-07 attachment/replay succeeds; changed tuple/rebinding asserts the existing PI-07 errors in `TestCatalogMediaAttachment` and `TestCatalogReceiptAfterCorrection` without remapping their contentstore boundary to catalog errors. Unbound/missing/corrupt remain the existing typed media dispositions, receipts/Assignments byte/value-equal. |
| `TestRegistrationAtomicRecovery` (B/C/D) | Injected insert/rollback/cleanup-without-commit failure: `ErrRepositoryUnavailable`; same-revision race loser: `ErrRevisionConflict` or bounded lock loser `ErrRepositoryContention`; pre-commit cancellation/deadline: `context.Canceled`/`context.DeadlineExceeded`; commit attempted with uncertain outcome: `ErrIndeterminate`; lost response uses exact authorized lookup. Absent/conflict/unavailable lookup: false+nil/`ErrCommandConflict`/`ErrRepositoryUnavailable`; leaked port: `ErrRepositoryUnavailable`; restore/close race uses existing owner contention outcome, no new catalog operation enters. |
| `TestRegistrationMigrationQuery` (C/D) | Valid v7 upgrade/current restore succeed; missing/dangling/corrupt relations: `ErrRepositoryUnavailable`; query input bound: `ErrLimitExceeded`; ambiguous pages retain ambiguity; invalidated cursor: `ErrStaleCursor`; v7 registration after restore: `ErrUnsupported`; no automatic migration. |

Each suite's slash subtests use the case spellings in the family table, including
each individual write stage for failure injection. D extends
`TestCatalogMilestoneQueryable` to use commands; direct SQL may construct old
migration fixtures or deliberate corruption only, never new milestone identities.
Before acceptance run the plan's appropriate backend/race/check jobs and CI on
the implementation commit. Source rollback is reverse dependency order D/C/B/A;
keep registration disabled until all required slices pass. F01-A alone or this
contract does not close F01. F02 still gates final independent T acceptance.

Media error assertions specifically use `contentstore.ErrConflict` for changed
tuple/rebinding, `contentstore.ErrCorrupt` for corrupted media,
`contentstore.ErrMissing` for missing media and `contentstore.ErrUnavailable`
for unavailable storage. An unbound catalog version is queryable without a media
binding; absence of that binding is not reported as verified missing media.

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
