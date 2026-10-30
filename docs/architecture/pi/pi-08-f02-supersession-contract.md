# PI08-T-F02 immutable version-supersession contract

Status: **implemented; D2j-M2 closed by D2l disposition (2026-10-30);
independent F02/T acceptance remains pending**.
M1 remediation is implemented at `fae704ab5365c20da4af590f27429266833cdaa1`
with exact-head Next CI 41070315658 green, as supplied by the D2l request.
The [D2l disposition](#d2l-accepted-ci-tier-disposition-2026-10-30) supersedes
only the unqualified repeat-all/race wording and current M2 status. Historical
refinement/D2j findings remain below; normative limits are unchanged.
Delivery remains open
under [PI08-T-F02 #28](https://github.com/shentschel/teddycloud/issues/28).
Request: `pi08-t-f02-refinement-20261024`, 2026-10-24.
Inspected base: `abe0c956e8f38c20aaae966faafe4115feb409e4`, branch
`docs/pi-00-r-charter`. This document does not complete F02, PI-08/T or PI-08.

## Evidence, precedence and scope

The [plan](pi-08-plan.md), [policy D5](pi-08-catalog-policy.md),
[R1 inventory](pi-08-r1-catalog-inventory.md),
[F01 contract](pi-08-f01-registration-contract.md) and
[T checkpoint](pi-08-t-audit-checkpoint.md) supply the inherited requirements.
The parent reports F01-D independently accepted and exact-head
[Next CI 40056183655](https://github.com/shentschel/teddycloud/actions/runs/40056183655)
fully green. This refinement inspected the local implementation; it does not
claim a new independent F01 audit or rerun that CI. Earlier statements that
registration is absent describe historical checkpoints. F01 and all accepted
R/A/B checkpoints, encodings and evidence remain intact.

Paths in this document are relative to `next/backend/internal` unless stated
otherwise. Inspection establishes these concrete integration constraints:

| Current code | Consequence for F02 |
| --- | --- |
| `domain/catalog/content.go`, `resolver.go`, `version_order.go` | Version identity includes owner, fingerprint and original order. Equal fingerprints may identify distinct versions. No tuple update or implicit latest. |
| `domain/catalog/order.go` | `CatalogOrderCommand.Supersedes` contains **command IDs** and deactivates earlier order decisions. It is not a version edge. |
| `domain/catalog/registration.go`, `application/catalog/registration.go` | F01 creates supported versions and grows the order manifest. F02 links two already committed versions; registration is a separate operation. |
| `application/catalog/service.go`, `adapters/sqlite/catalog_transaction.go` | Reuse callback-once, cancellation, shared owner, restore fence and sanitized errors. `readStates` currently accounts for aggregate advances using review/order events only. |
| `adapters/sqlite/{schema,catalog_registration}.go` | Migrations 1..8 exist. Registration checks exact v8 and eight migration records; accepting v9 requires explicit compatibility changes, not just a new table. |
| `adapters/sqlite/{catalog_repository,catalog_registration}.go` | Query digests and retained-byte/event accounting must include F02. Old read/review/order paths must understand F02 aggregate advances. |
| `domain/assignment/assignment.go`, `adapters/sqlite/blob_repository.go` | Assignments retain explicit VersionID; PI-07 receipts retain exact tuples. No persistent assignment repository is claimed at this checkpoint. |

Root README, applicable AGENTS.md and [model routing](../task-model-routing.md)
were read. This is the architecture decision gate; bounded implementation slices
below use Sol 6.1/high, with separate Astra review for independent acceptance.
No new issue, production code, live data, deployment or GitHub edit is authorized
by this refinement. The only document writes are this contract, T checkpoint
and PI README. The proposed issue body at the end is for the parent to publish.

## Decision: an append-only set of disjoint linear chains

An edge `old -> new` means an explicitly reviewed replacement of an immutable
version within the **same Content**. Both IDs must already exist, differ and
match their full stored tuples. F02 neither allocates nor registers a version.
If F01 registration succeeds but a later F02 command fails, the new version
remains a valid, unrelated version; there is no compensating delete or implied
two-command atomicity. Callers reread the aggregate after registration.

Each version has at most one incoming and one outgoing edge. A new edge may
extend a chain's terminal version to an **isolated** version (no incoming or
outgoing edge). An isolated predecessor is also allowed. Reject self-links,
cycles, branches, merges, shortcuts, linking into an existing chain and replacing
an already superseded predecessor. Traverse and validate the complete bounded
graph before applying a command, including when local degree checks would
already reject it. A different command for an existing edge conflicts; only
exact replay of its original command succeeds. No unlink, edge amendment,
retraction, source refresh replacement or chain compaction exists in F02.
Correcting a mistaken link needs separately refined reconciliation; retained
history cannot be rewritten. Extending the terminal is not an undo operation.

Both endpoints must be managed version subjects under this Content, present in
the current validated order manifest. The predecessor may be an explicitly
adopted baseline version with unknown/unreviewed fingerprint or owner support;
its historical status remains visible. The successor must have accepted,
nonconflicting fingerprint and ownership fields exactly matching its immutable
tuple at the pinned review revision. F01 registration or subsequent explicit
review may supply these facts. F02 never promotes baseline facts. Contradictory
or missing successor support rejects the new edge. Later review changes remain
visible but do not retroactively invalidate an accepted historical edge.

Different IDs with identical tuples except ID, including the same fingerprint,
are allowed with explicit relation evidence. Supersession is not deduplication.
Cross-Content ownership correction is rejected even when metadata or media
matches; policy D5's ownership-correction wording does not authorize moving
versions across Contents. Cross-Content reconciliation remains PI08-D04.

All original tuples, records, bootstrap selections, pending/dissenting evidence,
review/order decisions, protected custom provenance, bindings, receipts and
assignment values remain retained. Protection is inherited unchanged from the
aggregate; no request flag may lower it. There is no additional implicit order:
effective order and original order continue to use their existing rules over
all versions, including superseded ones. Order-command `Supersedes` cannot name
or erase an F02 edge; F02 commands cannot supersede order commands.

## Exact domain and application boundary

Add `CatalogSupersessionCapability` through
`NewCatalogSupersessionCapability(review ReviewCapability)`. Construction is an
explicit trusted-core grant, requires an active, non-detached `CoreOperator`
source and ownership claim permission, and binds the complete source registration
and reviewer. Ordinary review/ingest/order/registration handles alone are not
this capability. Do not add a sixth claim key or alter source/claim encodings.
The grant has no imported scope/namespace selector. Rebinding after restart
uses the existing trusted source restoration with full ID, registration revision,
issuer, adapter/version, acquisition, claims, scope and reviewer equality;
decoded persisted handles never authorize a command.

Add immutable `CatalogSupersessionEvidence` with input
`ID evidence.ID`, `Content`, `OldVersion`, `NewVersion`, `Coordinate`,
`AssertedOrigin`, `ObservedAt`, `Confidence`. The constructor requires a live
ingest handle for the same operator source and ownership permission. Coordinate
is an existing validated `RecordCoordinate` with retained sanitized record bytes.
The typed relation, not parsed arbitrary prose, asserts the pair. Origins,
timestamps and confidence never confer authority. Offline artifacts may support
endpoint registration; they cannot directly authorize this operator relation.

The application caller DTO `SupersedeVersionCommand` has exactly:

| Field | Requirement |
| --- | --- |
| `ID CatalogCommandID` | Independent nonzero opaque command identity, retained across retries. |
| `Content ContentID`, `Old, New ContentVersion` | Complete immutable tuples; both owners equal Content. No ID-only substitution. |
| `ExpectedAggregate, ExpectedSupersession CatalogRevision` | Positive current revisions; no absent/zero union. |
| `ExpectedOldReview, ExpectedNewReview CatalogRevision` | Positive pinned endpoint review revisions. |
| `Evidence []CatalogSupersessionEvidence`, `Support []evidence.ID` | 1..64 distinct admitted evidence values; Support names every value exactly once, no extra or omitted value. All assert this Content and pair. |
| `Reason string` | Nonempty valid text, at most 4096 UTF-8 bytes. |

The domain `CatalogSupersessionCommand` adds the core-bound `Source`; reviewer
comes from the capability, never caller text. No version creation, order change,
media action, assignment ID or authorization field is accepted in this DTO.
There is no separate F02 pending-evidence ingestion API: all supplied relation
evidence is selected in this one decision. Endpoint pending/dissenting evidence
remains in the existing review states and is queryable there.

`CatalogSupersessionState` contains Content, the current immutable version
manifest and chronological accepted command/result pairs. Empty state for a
managed Content has supersession revision 1, no edges. Each new edge advances
supersession revision and aggregate revision by exactly one. Review revisions,
order revision, manifest, accepted fields and protection do not change. F01
growth may extend the manifest between F02 commands without advancing
supersession revision; reconstruction validates historical endpoints against
their retained tuples rather than demanding the old manifest equal today's.

`Apply(capability, command, context)` returns new state plus
`CatalogSupersessionResult`, or the original state and error. Context supplies
the owner-validated aggregate, pinned endpoint review states, order revision and
protection. Result fields are `Command, Content, Old, New, AggregateRevision,
SupersessionRevision, OldReviewRevision, NewReviewRevision, OrderRevision,
ProtectedCustom, Support`. Old/New are the exact original tuples. The result
records the committed edge, not a moving terminal pointer. All returned slices
are owned copies; canonical bytes and historical results are immutable.

Add application `SupersessionService` wrapping existing `Service.run` and the
new capability, verifying identical source/reviewer binding at construction and
invocation. Methods: `SupersedeVersion`, `ReadSupersession` (complete original
command), and `QuerySupersession` as specified below. The additive callback-scoped
`SupersessionRepository` port exposes `LookupSupersession(ctx, operation)`,
`CommitSupersession(ctx, operation)`, `QuerySupersession(ctx, query)`; operation
contains capability and complete domain command. Lookup returns result/found/error;
commit returns result/error. Repository computes the successor under the same
owner transaction. Application validates the returned result against the command
before callback exit; malformed result forces rollback. No nested transaction,
extra connection, ordinary Save, automatic callback replay or public route.

Live authorization precedes any lookup, including exact replay/readback; persisted
revocation is checked inside the transaction. The owner rechecks live and
persisted operator authority and cancellation immediately before commit. Missing
or revoked trusted bindings deny new commands and command readback. Historical
query provenance remains readable through an independently authorized active
catalog service; revocation neither erases nor rewrites prior acceptance.

### Validation, replay and exact errors

Order: current authority; syntax/bounds and clean supported schema; complete
canonical key lookup/collision; managed subjects and immutable identity;
aggregate/supersession/endpoint revision fences; relation evidence and successor
support; graph transition; prospective accounting; writes and final owner checks.
Corruption discovered during any required persisted read fails closed before
using those data. Resource preflight precedes allocation regardless of this order.

Exact committed replay returns the original result before checking today's
revisions, successor support or terminal status. It performs no writes, advances
no revision and adds no budget charge. It still validates retained command/result,
support and graph consistency; corrupt storage is not a successful replay.
Changed reason, tuple, expected revision, evidence, support or authorized reviewer
under the same key returns `ErrCommandConflict`. Lookup of an authorized absent
key returns `(zero,false,nil)`; it does not require current revision equality.
Invalid or revoked authority cannot discover whether a key exists.

| Failure | Application outcome |
| --- | --- |
| Zero/malformed IDs/revisions, invalid text/time/tuple, duplicate evidence/support, self-link | `ErrInvalidInput` |
| Missing/ingest-only/detached/revoked grant; nonoperator source; forbidden ownership scope | `ErrUnauthorized` |
| Different live source registration or evidence coordinate source | `ErrSourceMismatch` |
| Cross-Content endpoints/evidence, wrong evidence pair | `ErrSubjectMismatch` |
| Missing or unmanaged Content/version subject | `ErrSubjectNotFound` |
| Changed stored tuple, occupied command key, duplicate edge with another key, branch/merge/cycle/target not isolated | `ErrCommandConflict` |
| Any current expected revision differs | `ErrRevisionConflict` |
| Signed aggregate revision overflow | `ErrRevisionOverflow` |
| Changed same-ID evidence/artifact/record | `ErrEvidenceConflict` |
| Missing/extra selected evidence, absent/conflicted/mismatched successor accepted fields | `ErrUnsupported` |
| Valid domain AudioID outside durable uint32 width | `ErrUnsupported` |
| Any admission/count/byte bound, including graph events | `ErrLimitExceeded` |
| Unsupported/dirty/mismatched-checksum schema | `ErrUnsupported` |
| Persisted dangling/malformed/contradictory state, failed write or rollback without commit attempt | `ErrRepositoryUnavailable` |
| Busy/lock timeout; cancellation/deadline before commit | Existing `ErrRepositoryContention`; `context.Canceled`/`context.DeadlineExceeded` (cancellation dominates contention) |
| Commit attempted with unknown outcome, or cleanup fails after attempted commit | `ErrIndeterminate`, dominating cancellation |

Reuse mapped domain errors; normalize `ErrVersionIdentityConflict` to
`ErrCatalogCommandConflict` before it crosses the existing service sanitizer.
No new error string may expose SQL, paths, record bytes or private values.

## Canonical encoding and bounds

Use existing `N(x)` = unsigned 64-bit big-endian, `T(b)` = N(byte length) + bytes,
booleans N(0/1), no separators/padding. IDs use their existing canonical String:
Content/version typed lowercase IDs, evidence IDs in the existing 30-byte `obs_`
form; command/source/registration-revision/principal/batch nonzero lowercase
hex32. SHA1 uses lowercase hex; SHA256 is 32 raw bytes.
Text uses exact valid UTF-8 with existing control rejection; no trimming, Unicode
normalization or JSON reserialization. Time is UTC RFC3339Nano without monotonic
state. Reject unknown tags, inactive-field garbage, trailing bytes and integer
overflow before conversion/allocation. Count bytes before copying.

`V(v)` is `T(versionID),T(contentID),N(audioID),T(SHA1),T(originalNamespace),
N(originalPosition),N(originalKnown)`. Unknown order is empty/0/0; known order
uses existing validation and full uint64 position. Durable AudioID remains
1..MaxUint32. `S(s)` is F01's Source sequence: T ID, revision, issuer, adapter,
adapterVersion, scope; N acquisition, N claimCount, ascending N claim keys.
`C(c)` is T batchID, raw SHA256, N artifactByteLength, N recordCount,
T parserRevision, T sourceRevision, N index, T locator, T exactSnapshotBytes.

`E(e)` is T evidenceID, T ContentID, T oldVersionID, T newVersionID, S(source),
C(coordinate), T assertedOrigin, T UTC time, N confidence (0..2).
Evidence and Support sort independently by their ID String; duplicates reject,
even identical ones. All evidence must use the command's complete operator S.
The command tag fixes the single operation kind; there is no implicit variant.

```text
Command = T("pi08-version-supersession/1"), T("pi08-catalog/1"),
 T(commandID), T(contentID), V(old), V(new),
 N(expectedAggregate), N(expectedSupersession),
 N(expectedOldReview), N(expectedNewReview), S(operator), T(reviewer),
 T(reason), N(evidenceCount), E(evidence)*, N(supportCount), T(supportID)*

Result = T("pi08-version-supersession-result/1"), T(commandID), T(contentID),
 V(old), V(new), N(aggregateRevision), N(supersessionRevision),
 N(oldReviewRevision), N(newReviewRevision), N(orderRevision), N(protected),
 N(supportCount), T(supportID)*

State = T("pi08-version-supersession-state/1"), T(contentID),
 N(supersessionRevision), N(versionCount), V(version)*,
 N(edgeCount), (T(completeCommandBytes), T(originalResultBytes))*
```

State manifest sorts by version ID; edges sort by resulting supersession
revision. State is a bounded derived encoding, not another persisted full-history
snapshot. Decoding replays every edge, validates original results, exact endpoint
tuples and monotonic (not necessarily adjacent) aggregate revisions. Old review,
order, registration and PI-07 encodings/tags remain byte-for-byte unchanged.

All policy limits apply jointly: <=1024 versions, <=1023 edges (also subject to
the shared 1024-event limit), <=64 relation evidence per mutation, <=4096 total
observations including relation and order evidence, <=16384 cumulative support
links, <=8MiB retained aggregate, <=256 sources, <=256 staged batches/64MiB.
Inherited batch/record/text limits remain 1MiB/32, 64KiB/depth16/256 fields and
4096 prose/128 references. Supersession revision = edgeCount+1. Each individual
command/result/derived state must also fit 8MiB. Query limits are 128 entries,
1MiB, 1024 inspected candidates, 256-byte cursors; deadlines stay 10s/5s.
No truncation, eviction or quota reset makes room for a new decision.

Golden fixtures for `TestSupersessionGoldenVectors`: use F01's rule of 15 zero
bytes plus integer for opaque values, with current typed ID rendering. Content=1,
old=2, new=3, command=4, operator source=5/revision=6/issuer=7, reviewer=8,
evidence=9, batch=10; Source adapter=`op`, adapterVersion=`1`, scope=`catalog`,
acquisition=3, claims=[5]. Old AudioID=1/SHA1=20 bytes 0x11; new AudioID=2/SHA1=20
bytes 0x22; both original orders unknown. Coordinate snapshot exact `{}`, SHA256
of `{}`, byteLength=2/count=1, parser=`1`, sourceRevision empty, index=0,
locator=`0`; assertedOrigin empty, time=`2026-01-01T00:00:00Z`, confidence=0.
Reason=`correct`; expected aggregate/supersession/oldReview/newReview=3/1/1/1.
Context orderRevision=3/protected=false; result=4/2/1/1/3/false and support=[9].
State contains the two versions and this single pair. A second vector adds
evidence=11/batch=12 with otherwise identical coordinate and relation, and both
support IDs; reversed input arrays must encode identically. Freeze expanded hex
literals from an independent encoder in slice A before B/C admission. Include
N(0)=`0000000000000000`, T("A")=`000000000000000141` and a known-order variant
whose old version uses namespace=`legacy`, position=7, known=true (all other
first-vector fields unchanged, including that tuple in command/result/state);
changed expected/reviewer/reason/source/tuple must change command bytes. These
are required future tests, not evidence already executed by this refinement.

## Query, assignment and reconstruction semantics

Extend `CatalogRecord` with `Supersession` containing `Status`,
`SupersessionRevision`, `Predecessor`, `Successor`, `Terminal`,
`IncomingCommand`, `OutgoingCommand`. IDs absent from an edge are zero typed IDs.
Status values: 0 `Unavailable` (schema <9 or unmanaged), 1 `Current`,
2 `Superseded`; Content-only rows use Unavailable and empty relation fields.
For a validated managed version on v9, Current means no outgoing edge, Terminal
is itself; Superseded has a successor and the bounded terminal of its chain.
IncomingCommand/OutgoingCommand identify direct edges; they do not grant latest,
availability or assignment authority. A malformed graph is unavailable storage,
never Current, no-match, partial success or best-effort traversal.

Existing GetVersion/ListVersions/pair/AudioID queries retain **all matching
versions**, with these additive annotations. Looking up an old fingerprint
returns the old version; it does not substitute a terminal with another pair.
Shared fingerprints remain ambiguous even when related by supersession. Existing
editorial conflict precedence remains Conflicted; partial pages remain Incomplete.
Add `ListCurrentVersions` as an explicit query kind with the same Content-scoped
input rules as ListVersions; it filters terminal/isolated versions only after
validating the entire graph. A complete list may contain several terminals;
one terminal means one non-superseded candidate, never proof of latest order.
On older schema or unmanaged Content this new kind returns `ErrUnsupported`.
Legacy queries continue with Unavailable annotations, not inferred Current.

`QuerySupersession` input is Content, optional Version, PageRequest. It returns
`HistoryPage{Entries, AggregateRevision, SupersessionRevision, Complete, Next,
Inspected, EncodedBytes}`. Each entry has `Command, Result, SourceRevoked`:
complete canonical command/result bytes and the operator's currently persisted
revocation boolean. Entries include relation support and coordinates, in resulting
supersession-revision order. Without Version, include all edges; with Version, include the entire
containing chain, not only direct neighbors. Missing Content/version returns an
empty complete page; an existing isolated version also has empty history.
Call GetVersion to distinguish these cases. Version without Content is invalid.
On schema <9 or existing unmanaged Content return `ErrUnsupported`. History is
readable with active service authority, without live grants for historical
revoked sources; command-key readback still requires the original current grant.
If one history entry exceeds 1MiB, return `ErrLimitExceeded`, never truncate its
evidence. Larger accepted commands remain recoverable via authorized bounded
8MiB command readback; paginated ordinary history has this explicit limitation.

History response accounting is exactly T(`pi08-version-supersession-page/1`),
N(aggregateRevision), N(supersessionRevision), N(complete), T(nextCursor),
N(inspected), N(entryCount), followed by T(command), T(result), N(sourceRevoked)
per entry. EncodedBytes reports this length and is not itself recursively
encoded. An absent Content reports aggregate/supersession revisions 1/1, following
the existing absent-aggregate query convention; existing subjects report their
actual revisions. Inspected counts all validated graph edges before filtering,
not just page entries. History query cursors additionally bind the optional
Version filter, last emitted supersession revision and source-revocation state.

Charge new fields in actual response-byte accounting before materialization.
Bind cursors to query kind/filter, aggregate revision, complete graph digest,
existing catalog snapshot and owner generation. Include F02 command/result and
evidence/support digests in scoped and global find snapshots. F02 writes stale
old cursors; restart/restore invalidates every prior owner cursor. Snapshot
checks still include media relations and unmanaged writes as they do today.

No assignment is retargeted: existing current, future and historical intervals
continue referencing the exact old VersionID with unchanged revision and dates.
Playback/range requests by explicit old ID retain their PI-07 behavior. A
consumer must not follow Terminal while resolving an assignment. Any future
retargeting requires an explicit separately authorized PI-09 assignment command
and its own concurrency/effective-time contract. F02 adds no assignment table,
SQL trigger, migration, automatic copy or desired/applied gateway change.

## Migration 9, constraints and accounting

Reserve additive version 9, ID `0009-catalog-version-supersession`. Preserve every
statement/checksum in migrations 1..8. Create empty STRICT tables below, with
NOT NULL columns unless explicitly stated otherwise; all FKs use RESTRICT for
UPDATE and DELETE. All three tables reject UPDATE/DELETE via abort triggers.
No graph-head cache or mutable relation projection is authoritative.

| Table | Columns and constraints |
| --- | --- |
| `tc_catalog_supersessions` | `command_id` PK hex32; `content_id` FK managed; `old_version_id,new_version_id` distinct, each composite FK `(content_id,version_id)` to versions and `(subject_id,content_id)` to subjects; `source_id,registration_revision` composite FK sources; `reviewer_principal` hex32; `expected_aggregate,result_aggregate` positive, result=expected+1 without overflow; `expected_supersession,result_supersession` positive, result=expected+1 and <=1024; `old_review_revision,new_review_revision,order_revision` positive; `protected_custom` boolean; `encoding=1`; `canonical_command,canonical_result` BLOB 1..8MiB each; `command_sha256,result_sha256` 32-byte BLOB. UNIQUE(old_version_id), UNIQUE(new_version_id), UNIQUE(content_id,result_aggregate), UNIQUE(content_id,result_supersession), UNIQUE(command_id,content_id,old_version_id,new_version_id,source_id,registration_revision). |
| `tc_catalog_supersession_evidence` | `evidence_id` PK in existing canonical 30-byte `obs_` form; `command_id,content_id,old_version_id,new_version_id,source_id,registration_revision` composite FK exact supersessions tuple; `batch_id,record_index` FK records; `(batch_id,source_id,registration_revision)` FK batches; `asserted_origin` UTF-8 <=128 bytes (empty allowed), `observed_at` canonical time text, `confidence` 0..2. UNIQUE(command_id,evidence_id). |
| `tc_catalog_supersession_support` | `command_id,evidence_id` composite PK and composite FK evidence. Every evidence row has exactly one support row, verified before commit and on reconstruction. |

Typed version/content IDs use existing schema spelling checks; all hex32 fields
must be canonical and nonzero. Bytes/hash/result decoding, canonical timestamps,
complete source spec and historical review references require repository checks
in addition to SQL constraints. Cross-v1/v2 review-state FKs cannot express the
union: validate pinned endpoint snapshots explicitly, including accepted
successor fields at that historical revision and their aggregate <= expected.
Validate the recorded order snapshot similarly; original review/order rows stay
immutable. Its revision must be the latest at the command's expected aggregate;
the two pinned endpoint review revisions must likewise be the latest then.
Validate result protection against retained protection evidence at that aggregate,
not a later increased flag; current queries use current monotonic protection.
Hashes detect corruption, not hostile-writer authenticity.

Insertion triggers enforce: current managed aggregate equals result_aggregate;
previous maximum result_supersession (or 1 if empty) equals expected_supersession;
the predecessor has no outgoing edge and target has neither edge; a recursive
reachability check rejects cycles; source is active CoreOperator with claim 5;
subjects and tuple owners agree. Trigger traversal uses finite UNION on version
IDs; repository bound validation happens first. Cross-kind aggregate result
collisions are checked against existing review/order events in both directions.
These are statement guards; multirow completeness is validated before commit
and every read, not falsely claimed as a deferred SQLite cross-table CHECK.

F02 keys use F01's database-wide exclusion scope: review/order commands,
registration commands, adoption batch IDs and all artifact/import batch IDs.
F02's own evidence batches cannot equal its command. Extend old writer preflight
guards and additive triggers on normalized command/batch tables; order keys
embedded in canonical snapshots require bounded decoding/preflight under the
same transaction. Do not infer collisions from arbitrary byte substrings.
Pre-existing legacy overlaps remain readable; do not retroactively impose global
uniqueness or change PI-07's separate import-key namespace. Relation evidence IDs
have a separate typed storage namespace from ordinary/sequence observations;
within F02 they are globally unique and may not be reattached under another key.

Every supported v9 writer that can grow retained catalog state must run the
combined accounting before commit, including registration, review, order and
adoption/import paths as applicable. Keep existing F01 charges and add:

1. Command and result byte lengths once for each physically retained BLOB.
2. Supersessions' non-BLOB columns encoded in the table's listed order using
   T(text)/N(integer); charge both stored SHA256 columns as 32 bytes each.
3. Every evidence/support row's columns in listed order using T/N, plus one
   full C(coordinate) encoding per evidence reference, even for shared records.

No additional persisted State BLOB is introduced. Shared old coordinate charges
are not subtracted. Add one event per edge, one observation per relation evidence,
and one support link per support row to existing F01 combined counters; no double
event charge for the same edge. Exact replay charges zero new bytes/events.
SQLite page/index overhead is not the logical budget; index relations are charged
as specified above. Check prospective totals before enabling writes, then verify
actual persisted totals before commit. Bound lengths/counts before selecting
BLOBs or reconstructing the graph; impossible persisted sizes fail closed.

`readStates` must UNION F02 result_aggregate into its existing event audit, so
aggregate-1 events remain distinct and contiguous from 2. Count registration
growth only once through its existing order event. Do not create fake review or
order snapshots to fill F02 revisions. Review/order revisions remain locally
contiguous; their aggregate revision gaps are legitimate F02 advances. All
read/replay/query/registration paths validate graph/evidence consistency through
a shared nonrecursive validation helper; avoid readStates -> graph -> readStates
recursion. Load raw bounded snapshots once, then validate cross-references.

Update transaction schema dispatch to supported clean 5..9. Update F01's exact
v8/count==8 gate to explicit supported {8,9}, checking the exact migration prefix
for the selected version. F02 commands require clean exact v9. Future versions
remain unsupported. Old queries/writes and F01 continue on their supported older
schemas; restoring v8 disables F02 without automatic upgrade. Neither table
absence on v9 nor a missing migration record is an empty graph.

## Transaction stages, recovery and compatibility

One owner operation, one transaction, no I/O outside SQLite. Named injection
stages are stable test labels:

1. `preflight`: authorize, schema/canonical lookup, key collision, complete bounded
   graph/subject/review/order validation, revision/transition/budget checks.
2. `aggregate-cas`: enable writes through the existing seam; update only managed
   aggregate revision WHERE expected; require exactly one row. Protection unchanged.
3. `provenance`: validate/reuse exact source/batch/record rows; insert only absent
   operator provenance. Apply existing staging bounds and immutable identity rules.
4. `edge`: insert command/result, indexed endpoints, revision/context and hashes.
5. `evidence`: insert every relation evidence row; inject after each insertion.
6. `support`: insert every support link; inject after each insertion.
7. `validate`: reconstruct normalized data against canonical bytes, historical
   support, graph, event continuity and actual accounting; validate result envelope.
8. `before-commit`: owner rechecks current/persisted authority and cancellation;
   `commit-attempt`/`after-commit` distinguish durability uncertainty.

Any failure before commit rolls the transaction back, including newly inserted
provenance. No Content facts, version tuple, review/order state, blob/binding,
receipt or assignment row is written. Same-revision writers serialize/CAS:
at most one commits; loser is revision conflict or bounded contention, never an
automatic retry. F01/review/order racing F02 share the same aggregate fence.
Identical concurrent commands may return one commit plus exact replay, or bounded
contention requiring explicit readback; no double edge or charge is possible.

Cancellation before commit with confirmed rollback has no effect. Failed cleanup
without commit attempt is unavailable, not proof of durability. After an attempted
commit with uncertain outcome, return Indeterminate; preserve the original command
and use authorized readback after the earlier operation can no longer commit.
Exact found returns the original result; absent permits an explicit exact retry;
conflicting/unavailable readback must not generate another ID or compensation.
Keep the owner fenced while unresolved driver work can still commit. Leaked ports
fail after callback exit; close/restore races retain the existing owner behavior.

Reopen reconstructs from bounded immutable events and referenced review/order
snapshots, checks hash/length/bytes/columns/source/record/support agreement, unique
degrees, acyclicity, revision continuity and manifest membership. Source revocation
is displayed separately from historical acceptance; it cannot silently hide an
edge. Broken FKs, missing supports, missing historical snapshots, duplicate
revisions, forged results or cyclic persisted edges return RepositoryUnavailable.
Never repair, drop, relink or invent history during reads.

Migration tests upgrade a real accepted v8 fixture containing registered and
adopted subjects, review/order history, custom protection and PI-07 receipts.
Verify byte/value equality and checksums 1..8, plus unchanged versions/bindings.
Use accepted verified backup and fenced exact-schema restore to a new database
path, preserving originals. Test v9 restore/rebind/readback and v8 restore with
F01 operational/F02 Unsupported; re-upgrade is a separate explicit action.
Failed backup/migration/verification cannot publish a partial new owner. Retain
post-v9 database and media for reconciliation if restoring an older snapshot;
this is not a data-preserving in-place downgrade. No down migration, deletion,
production migration, physical power-loss or hardware playback claim.

## Implementation slices and acceptance gates

These are proposed write sets for separately admitted work, not authorization to
start implementation in this refinement. Matching test files below are explicit.
Common per-slice gate: meaningful named tests (zero matched tests is failure),
architecture boundaries, docs/diff checks, exact commit and CI evidence; preserve
F01 vectors/integration tests. No F02 feature exposure until C and D pass.

| Slice | Concrete write set | Dependency and milestone gate |
| --- | --- | --- |
| F02-A domain | New `domain/catalog/supersession.go`, `supersession_test.go` | This refinement reviewed; constructors, grant/rebind, immutable graph Apply, bounded decoding and frozen golden literals. Existing encodings unchanged. |
| F02-B application/query contract | New `application/catalog/supersession.go`, `supersession_test.go`; existing `queries.go`, `queries_test.go` | A; wrapper/port, result validation, explicit current/history query DTOs, callback-once and typed failures. No persistence claim. |
| F02-C SQLite | New `adapters/sqlite/catalog_supersession.go`, `catalog_supersession_test.go`, `catalog_supersession_schema_test.go`; existing `schema.go`, `catalog_transaction.go`, `catalog_repository.go`, `catalog_registration.go`, `catalog_adoption.go`, `catalog_import.go` | A/B; migration 9, every old/new writer key/accounting guard, v8 F01 compatibility, graph-aware revisions/query/cursors, atomicity and recovery. Own all new helpers here; no implicit write permission beyond these files. |
| F02-D integration and independent audit | New `adapters/sqlite/catalog_supersession_integration_test.go`, `catalog_supersession_recovery_test.go`; existing `catalog_audit_test.go`; evidence updates to this contract, T checkpoint and PI README | C; real helper kill/reopen, exact replay/media/assignment compatibility, milestone fixture through authoring commands, separate independent review, green exact-head CI. Findings remain under #28 unless parent scopes another task. |

Source rollback before exposure is a focused reverse-order D/C/B/A revert. After
v9 is used, old binaries cannot open it as a supported schema: disable the feature
and retain the new reader or use verified fenced pre-upgrade restore with originals
retained. A source revert alone is not database rollback. Do not edit applied
migrations, lower counters, remove edges or erase provenance to fit older code.

| Required test family | Concrete assertions |
| --- | --- |
| `TestSupersessionGoldenVectors`, `TestSupersessionBounds` (A/C) | Complete command/result/state bytes, permutation equality, changed fields differ; exact/one-over counts and actual encoded bytes, arithmetic/MaxInt64 overflow, retained shared charges, replay zero-charge and pre-allocation rejection. |
| `TestSupersessionAuthority` (A/B/C) | Spoofed source/reviewer, ordinary capability, detached rehydrate, ingest-only, wrong scope/revision, offline relation evidence, revoked before lookup/before commit, persisted revocation, full trusted rebind. |
| `TestSupersessionTransitions` (A/C) | A->B->C and independent D; self, B->A cycle, A->C branch/shortcut, D->B merge, target already superseded, cross-Content, changed tuple/order, stale four fences, accepted successor support and adopted predecessor status. |
| `TestSupersessionReplayEvidence` (A/B/C) | Changed same key/reason/reviewer/expected/evidence, changed same-ID artifact/record, missing/extra/cross-pair support, every cross-kind key collision in both directions, exact replay after later edges/reviews/revocation (denied)/restart. Original result bytes unchanged. |
| `TestSupersessionAtomicRecovery` (B/C/D) | Fail before/after every named write stage and each evidence/support insert; cancel before admission/between writes/before commit; rollback/cleanup failure, commit-before/after failure, lost response, absent/exact/conflicting/unavailable readback, single callback, leaked port, close/restore contention. Compare all relevant database rows before/after rejected confirmed-rollbacks, not WAL file bytes. |
| `TestSupersessionConcurrency` (C/D) | Separate-owner same-revision different edges, identical commands, and F01/review/order versus F02; at most one new aggregate event, stable loser outcome, no evidence leak or duplicate accounting. |
| `TestSupersessionQueryReconstruction` (C/D) | Historical old lookup, current terminals, equal-fingerprint ambiguity including across Contents, unknown/conflicting order unchanged, history pages and >1MiB entry limit, source revocation provenance, stale cursors after edge/restart/restore, corruption of every FK/hash/field/count/result/revision and cycles/branches forced only in corruption fixtures. |
| `TestSupersessionMigrationRecovery` (C/D) | Real v8->v9 upgrade, unchanged 1..8 checksums/data, failed migration/backup, current/pre-v9 restore, trusted rebind, F01 on v8/v9, unsupported dirty/future schemas; helper termination before commit and after durable commit with lost response. |
| `TestSupersessionMediaAssignmentCompatibility` (D) | Exact PI-07 attachment and old command replay after supersession, unchanged receipt bytes/bindings/tuples; changed tuple/rebinding rejected with existing contentstore errors; old Assignment current/future/history references and intervals value-equal; explicit old-ID media lookup not redirected. |

Extend `TestCatalogMilestoneQueryable` from F01's command-authored fixture: register
two independently identified Contents (one custom), retain dissent/shared-pair
ambiguity, register a correction and explicitly link old->new, append a second
correction, query old/current/history after fresh reopen and verified restore,
prove order/receipt/assignment independence. SQL seeding is allowed only for old
migration fixtures and deliberate corruption, not milestone authoring. Fuzz bounded
supersession decoding and transitions with named `FuzzCatalogSupersessionState`.

Delivery checks follow the plan's package/full-backend/vet/race and `make -C next
check` gates. SQLite repetition and race MUST use the exact accepted
[D2l CI tier](#d2l-accepted-ci-tier-disposition-2026-10-30) below; it replaces
the original unqualified tenfold `TestSupersession`/race requirement only for
the explicitly named fixtures. Run the new fuzz target for 10s/parallel=2;
preserve existing F01 and qualified-filesystem jobs.
Independent T must review the exact implementation commit and all negative cases;
green tests alone do not close #28. No unresolved High/security/data-loss finding
permits F02, PI-08/T or milestone acceptance. Hardware, production migration,
deployment, public V3/auth APIs, legacy/provider import expansion, automatic
assignment changes, cross-Content reconciliation and relation retraction remain
explicitly outside this contract.

## D2j independent acceptance checkpoint, 2026-10-30

Historical checkpoint: the findings and acceptance status in this section
refer to `fbc8f9996e2cd8d9842048002f67dbf5a2cda2c5`. For M1 remediation
and M2 closure, use the subsequent D2l disposition; this audit is not rerun here.

Inspected implementation: `fbc8f9996e2cd8d9842048002f67dbf5a2cda2c5`.
[Exact-head Next CI 40989601324](https://github.com/shentschel/teddycloud/actions/runs/40989601324)
is independently verified green, but **F02, PI-08/T and the milestone remain
unaccepted**. The [audit checkpoint](pi-08-t-audit-checkpoint.md#f02-d2j-independent-acceptance-checkpoint-2026-10-30)
disposes the four original D2 gaps, every test-family/D gate, and the transaction
caches. It records two Medium contract/acceptance findings: combined accounting
before enabling writes is missing in old writers, and the delivered CI tier does
not literally implement the original repeat-all/race gate. No High, security or
data-loss defect was confirmed in the inspected scope. No feature exposure or
production migration is authorized by this checkpoint.

### Support-count boundary: accepted dominance proof, not a limit change

The command-authored SQLite support-count exact/+1 scenarios are unreachable
under the unchanged encoding and 8,388,608-byte aggregate budget. For this one
boundary, a proof of nonexistence discharges the impossible fixture obligation;
it does not discharge byte-boundary tests, independent counter checks, corruption
checks or the remaining acceptance gates. The 16,384-link check stays mandatory.

An independent conservative proof at the inspected SHA is as follows. Let `L`
be cumulative links for one Content, `O` the order links (including order-command
`Supersedes`), and `B` its charged retained bytes. These counts are exactly those
assembled by `adapters/sqlite/catalog_registration.go:814-986`; F02 additions
are in `catalog_supersession.go:608-633`. No SQLite page-size estimate is used.

* Every non-order link costs at least 650 bytes. A selected review observation
  has a conservative 464-byte encoding floor: ID 38, subject 46, claim 40,
  source 168, coordinate 128, origin/time/confidence 44. Empty optional text and
  empty inactive fields are allowed in this lower estimate. The five additional
  support-ID copies cost 190 bytes: typed/canonical command and result in its
  review snapshot, plus the separately stored canonical command and result.
  Thus even this deliberately loose floor gives 654 > 650. Reuse across review
  decisions does not share their newly persisted snapshots. See
  `domain/catalog/decisions.go:154-193,482-634` and
  `domain/catalog/registration.go:512-525`. Registration retains its selected
  observation in both canonical command and review bootstrap; F02 retains its
  relation plus command/result support, normalized evidence and support row.
  Both also exceed 650. These charges occupy different BLOBs/rows from order.
* Each order command adds at most 256 links, including `Supersedes`
  (`domain/catalog/order.go:331-369`). Every added link contributes at least one
  framed ID of 38 bytes to that command's immutable canonical bytes (superseded
  command IDs actually cost 40). Every later order snapshot still includes it,
  even after superseding a decision. Both v1's `encodeOrderState` and v2's
  `encodeGrowthState` retain that history (`order.go:638-737`,
  `registration.go:731-773`). The accounting sums all physical snapshots, not
  just the latest one. Registration growth and other overhead only add bytes.
* Packing links into the latest possible commands minimizes those copies. With
  `O = 256*q + r`, `0 <= r < 256`, at least
  `S(O) = 256*q*(q+1)/2 + (q+1)*r` copies remain. Consequently
  `B >= 650*(L-O) + 38*S(O)`, for every integer `0 <= O <= L`.
  This relaxation ignores other caps and all additional positive charges, so
  its minimum is a valid lower bound, not a claim that a minimizing fixture exists.

The marginal order cost is `38*ceil(O/256)`. It is below 650 through block 17
(646) and above it from block 18 (684); hence for these `L` the minimum is at
`O=4352`. Exhaustive integer minimization independently gives:

| Cumulative links L | Minimum retained bytes | Excess over 8 MiB |
| --- | ---: | ---: |
| 16,129 | 9,143,434 | 754,826 |
| 16,384 | 9,309,184 | 920,576 |
| 16,385 | 9,309,834 | 921,226 |

These stronger lower bounds independently corroborate the earlier conservative
8,413,526 / 8,556,006-byte impossibility claims; neither is an actual storage-size
measurement. A writer adds at most 256 links, so reaching 16,385 would require an
already invalid prestate of at least 16,129 links. Reaching 16,384 is itself
invalid. RegisterContent starts absent; adoption adds no support; observation-only
and import operations cannot reach this boundary by increasing links.

Reproducible arithmetic, with no repository writes:

```python
for L in (16129, 16384, 16385):
    def bound(O):
        q, r = divmod(O, 256)
        return 650*(L-O) + 38*(256*q*(q+1)//2 + (q+1)*r)
    print(L, min((bound(O), O) for O in range(L+1)))
```

The proof applies to valid complete retained histories, including supported v8
histories upgraded to v9; forged counters, missing snapshots and malformed
storage remain corruption cases, never admissible fixtures. Counter checks in
`TestSupersessionSQLiteAccounting` and inherited domain limit tests remain in
place. The six byte exact/+1 writer pairs remain required. Any future encoding,
retention, accounting or limit change must reopen this proof before acceptance.
No user decision to change a numeric limit or encoding is needed for this
boundary. At the D2j checkpoint, this support-boundary disposition did not
waive either finding; subsequent M1 remediation and M2 disposition are below.

## D2l accepted CI-tier disposition, 2026-10-30

Request: `pi08-t-f02-d2l-ci-tier-contract-disposition-20261030`.
Basis: `fae704ab5365c20da4af590f27429266833cdaa1`. The user explicitly
accepts the already delivered D2e/D2i tier as a risk-based test allocation.
**D2j-M2 is closed by this normative contract decision.** This is a bounded
exception to fixture repetition/instrumentation, with no reduction of assertions,
limits, normative 10s/5s operation deadlines or functional acceptance gates.

The authoritative selectors are those in repository-root
[`.github/workflows/next-ci.yml`](../../../.github/workflows/next-ci.yml),
job `sqlite-persistence`, at the basis commit. Commands below run from `next/`.
All steps MUST remain mandatory; the four named fixtures MUST run uncached
non-race with `-count=1`, even when other lanes also select them.

| Lane | Exact accepted allocation |
| --- | --- |
| Mandatory accounting one-shot | `TestSupersessionSQLiteCombinedWriterBytes`, `TestSupersessionSQLiteCombinedWriterObservations`, `TestSupersessionSQLiteCombinedWriterEvents`, `TestSupersessionSQLiteCombinedRegisterContentBytes`; `-count=1`, non-race, no skipped assertions. |
| Focused SQLite/architecture regression | Skip only the observation/event fixtures explicitly run above. Tag/tagregistry regression remains required. |
| Ordinary SQLite repetition | `-timeout=20m -count=10`; skip only `TestSupersessionSQLiteCombinedWriterObservations` and `TestSupersessionSQLiteCombinedWriterEvents`. Every other SQLite test, including both byte fixtures, retains ten repetitions. |
| Persistence/domain race | Uncached `-count=1 -timeout=25m -race`; skip only the four named accounting fixtures. All other SQLite tests and all six companion packages retain their existing race coverage. |

The exact commands, including anchored selectors and race package set, are:

```sh
go test -count=1 -run '^TestSupersessionSQLiteCombined(WriterBytes|WriterObservations|WriterEvents|RegisterContentBytes)$' ./backend/internal/adapters/sqlite
go test -skip '^TestSupersessionSQLiteCombinedWriter(Observations|Events)$' ./backend/internal/adapters/sqlite ./backend/internal/architecture
go test ./backend/internal/domain/tag ./backend/internal/application/tagregistry
go test -timeout=20m -count=10 -skip '^TestSupersessionSQLiteCombinedWriter(Observations|Events)$' ./backend/internal/adapters/sqlite
go test -count=1 -timeout=25m -race -skip '^TestSupersessionSQLiteCombined(WriterBytes|WriterObservations|WriterEvents|RegisterContentBytes)$' ./backend/internal/adapters/sqlite ./backend/internal/architecture ./backend/internal/domain/tag ./backend/internal/domain/content ./backend/internal/application/tagregistry ./backend/internal/testutil/taffixture ./backend/internal/adapters/contentfs
```

The persistence job's 45-minute timeout and other package/job timeouts stay
unchanged. Qualified Linux contentfs `-count=10`, Darwin boundary compilation,
package/full-backend/vet, `make -C next check`, fuzz, deterministic build/smoke
and reproducibility gates remain required. Existing advisory semantics remain
unchanged. No workflow or test implementation is changed by this disposition.

Every reachable exact/+1 assertion, typed rejection, pre-mutation admission
check, complete rollback/leak comparison and zero-charge replay check remains
required. The independent support-count dominance proof above retains its
original limited scope. Owner/callback lifecycle, cross-writer concurrency,
cache invalidation/FK recheck and other ordinary SQLite tests retain repetition
and race coverage. Previously supplied focused domain/application race and
boundary evidence is retained; no new execution is claimed here.

Residual risk is explicitly accepted: observation/event full-boundary fixtures
are not rebuilt ten times in the repetition lane; none of the four full-boundary
fixtures runs under the race detector. Intermittent failures and races unique to
those complete large-fixture paths may therefore escape this tier. A mandatory
uncached one-shot proves their assertions for that execution, not repeatability
or race freedom. Ordinary concurrency/cache/owner race evidence does not fully
substitute for instrumenting these exact large paths. Any expanded exclusion or
change to the named fixtures' coverage requires a new explicit disposition;
this decision authorizes no broader weakening.

The request supplies M1's pre-mutation evidence and implementation at the basis
SHA, plus green [exact-head Next CI 41070315658](https://github.com/shentschel/teddycloud/actions/runs/41070315658).
This records M1 as implemented and exact-head-CI-green; it does not perform an
independent M1 re-audit. M2's wording mismatch is closed. Full F02, PI-08/T and
milestone acceptance under #28 remain reserved for a separate independent final
review of the implementation and evidence. This docs commit has no new remote
CI claim, and authorizes no push, issue edit, exposure or production migration.

Execution policy: optimize context and polling, reuse identified exact-head
evidence and avoid duplicate full validation. Limit each 5h window to at most
2–3 substantively complete jobs; total work including parent review, integration,
CI and delay must fit within 95% of that window and the remaining 40% weekly
budget, keeping 5% of the window free. Budget pressure cannot waive a mandatory
fixture or functional gate; defer further work instead. This disposition needs
only architecture/link/workspace/diff checks and the exact three-file commit.

## Refinement publication and proposed issue #28 body

The following is the unchanged 2026-10-24 publication proposal, not the current
delivery status or authorization to push this D2l commit. Current status and
execution scope are defined above.

For this documentation-only request run from repository root:

```sh
python3 scripts/check_architecture_docs.py
git diff --check
git diff --cached --check
git diff --cached --stat
```

Review the complete newly added file after staging and verify the exact three-file
write set. Commit/push only that reviewed set; report local checks and remote SHA
separately from any CI result. This request changes no GitHub issue. The parent may
replace #28's body with the exact text below after reviewing the published commit;
retain the existing issue identity and open state.

```markdown
Stable ID: PI08-T-F02
Status: refined/implementation-ready; implementation and acceptance remain open.
Refinement request: pi08-t-f02-refinement-20261024
Inspected base: abe0c956e8f38c20aaae966faafe4115feb409e4
Recommended model: gpt-6.1-sol/high for A/B/C delivery; gpt-6-astra for independent acceptance.

Normative contract: [pi-08-f02-supersession-contract.md](https://github.com/shentschel/teddycloud/blob/docs/pi-00-r-charter/docs/architecture/pi/pi-08-f02-supersession-contract.md).

F01-D is parent-accepted; exact-head Next CI 40056183655 is green. Preserve F01 and accepted R/A/B evidence. This refinement does not implement or accept F02, PI-08/T or the milestone.

Implement explicit immutable old->new version edges within one Content. Require current core operator authority, complete canonical command/result retention, aggregate/graph/endpoint-review fencing, exact authorized replay and retained provenance/support. Allow only disjoint linear chains extended to an isolated successor; reject self-links, cycles, branches, merges, cross-Content links and changed identities. Keep order-command Supersedes separate. Existing assignments, version tuples, bindings and receipts remain unchanged; no implicit retargeting or latest inference.

Deliver additive migration 9, bounded reconstruction and accounting, all-writer collision guards, current/history queries preserving ambiguity, cursor invalidation, v8/F01 compatibility and fenced recovery. Use the contract's exact schemas, encodings, errors, transaction stages and negative-test matrix.

- [ ] F02-A: domain/grant/graph and independently frozen golden vectors.
- [ ] F02-B: application command/readback/query contracts and lifecycle tests.
- [ ] F02-C: SQLite migration, atomic writes, query/accounting/replay and corruption/concurrency evidence.
- [ ] F02-D: command-authored milestone, helper-process recovery, media/assignment compatibility and independent exact-head audit/CI.

Each slice needs fresh budget admission and its specified write set, dependency and rollback gate. Acceptance requires all slices, no unresolved High/security/data-loss finding, and independent T review. Hardware, deployment, production migration, public APIs, cross-Content reconciliation and assignment retargeting are out of scope. Keep this issue open until implementation acceptance; do not create a duplicate issue.
```
