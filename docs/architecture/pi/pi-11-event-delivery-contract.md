# PI-11 typed event and delivery contract

Status: complete R decision set for parent review; prospective A/B/T delivery and
**Live tag/playback events are reliable** remain pending. Internal profile
`pi11-events/1`, request `pi11-r-event-delivery-20261118`, evidence base
`ac08326eaeaa7dee5c2414864977982ede0ff51d`. Read with the
[plan](pi-11-plan.md) and [budget](pi-11-budget.md). MUST and REJECT below are
new requirements, not statements of implemented behavior or a public V3 freeze.

## E1: precise evidence and compatibility ledger

All code observations below are pinned to the base above. Historical contract
status paragraphs do not override later acceptance recorded in the [index](README.md).

| Class | Source and exact evidence locator | Meaning and limit |
| --- | --- | --- |
| Observed legacy source | [SSE writer](../../../src/handler_sse.c), `sse_startEventRaw` lines 112-135, `sse_rawData` lines 138-156 | Named event plus JSON `{type,data}`; broadcast to active subscriptions without box identity. Source evidence, not a captured wire/runtime proof. |
| Observed legacy source | [Box state](../../../src/toniebox_state.c), `tbs_tag_placed` lines 24-39, `tbs_tag_removed` lines 42-55 | Placement emits `TagValid`/`TagInvalid` with compact UID; removal sends MQTT only. No observed server SSE removal. |
| Observed legacy source | Same file, `tbs_playback` lines 69-102 | `starting`, `started`, `stopped` SSE; local stopped cleanup does not establish physical removal. STARTING falls through to STARTED in this source; do not promote it to device acknowledgement. |
| Client compatibility | [PI-02 inventory](pi-02-contract-inventory.md), points 5-6; [deployment seams](pi-02-deployment-seams.md), leading-minus paragraph | Negative `TagValid` and RTNL removal are supported client inputs. Synthetic negative fixtures are not evidence that this server emits them. |
| Inherited deferred responsibility | [PI-05 plan](pi-05-plan.md), scope deferrals; [PI-07 storage contract](pi-07-taf-storage-contract.md), Proposed identity and publication | Outbox/Event Hub deferred here; media publication precedes DB metadata, with no atomic DB/filesystem transaction. |
| Existing owner constraints | [Tag contract](pi-06-tag-registry-contract.md), transaction/lifecycle and bounded storage; [Tag transaction](../../../next/backend/internal/adapters/sqlite/tag_transaction.go), `withinTagTransaction`; [Tag repository](../../../next/backend/internal/adapters/sqlite/tag_repository.go), `Insert`, `CompareAndSwap` | Identity and metadata remain Tag-owned; shared SQL callback and replay rules cannot be replaced by event inference. |
| Existing owner constraints | [Content ports](../../../next/backend/internal/application/contentstore/ports.go), `ImportResult`, `Session`, `MediaStore`; [blob repository](../../../next/backend/internal/adapters/sqlite/blob_repository.go), `RecordImport`, `transact` | Import result is committed metadata, not present-media availability. Filesystem I/O stays outside SQL transactions. |
| Existing owner constraints | [Catalog policy](pi-08-catalog-policy.md), D1-D6; [catalog transaction](../../../next/backend/internal/adapters/sqlite/catalog_transaction.go), `WithinCatalogTransaction`, `catalogTransaction`; [assignment policy](pi-09-assignment-policy.md), D1-D3/D5; [assignment transaction](../../../next/backend/internal/adapters/sqlite/assignment_transaction.go), `WithinAssignmentTransaction`, `assignmentSQLTransaction` | Catalog provenance/review and desired assignment have separate authority; callback-once, fencing, retained replay and indeterminate outcomes survive PI-11. |
| Existing lifecycle/query constraints | [Lifecycle owner](../../../next/backend/internal/adapters/sqlite/lifecycle_owner.go), `Restore`, `Close`, `WithinContentOperation`, `invalidateLibrary`; [Library contract](pi-10-library-query-contract.md), D1/D2/D4-D6 | One selected DB and common gate; Library is a derived query with its own volatile cursors, not an event producer or an applied-playback oracle. |
| Concrete future schema dependency | [Schema](../../../next/backend/internal/adapters/sqlite/schema.go), `init`, `SchemaMigrations`; [assignment schema](../../../next/backend/internal/adapters/sqlite/assignment_schema.go), `assignmentMigration`; [Library index](../../../next/backend/internal/adapters/sqlite/library_index.go), schema check | Active schema ends at 10. Catalog dispatch explicitly recognizes 9/10; Library recognizes 9/10. A must extend exact schema support and regressions, not bypass checksum/dirty checks. |
| New normative decisions | D1-D9 below | Canonical envelopes, box streams, durable journal, replay and reconciliation are selected future behavior. Legacy unscoped SSE cannot be silently translated into authoritative box events. |
| Future proof | F1 acceptance matrix and plan A/B/T | Real owned transactions, two-box isolation, restart, slow sockets, cleanup and synthetic trusted ingress. Hardware ingress/acknowledgement, remote authorization and physical power loss remain unproven. |

## D1: closed vocabulary and canonical envelope

Owner: Event application contract maintainer, with each producer below retaining
semantic authority. Invariant: only a valid, versioned, bounded typed fact enters
the journal. Failure: `InvalidEvent`, `UnsupportedVersion`, `PayloadTooLarge` or
`UnauthorizedProducer`, with no append; malformed persisted bytes fence delivery
as `Unavailable`, never skip a row. Evidence: `TestEventEnvelope`,
`TestEventProducerAuthority`, `FuzzEventEnvelope` in F1.

Envelope fields are mandatory unless explicitly nullable:

| Field | Version 1 representation and invariant |
| --- | --- |
| `format`, `type`, `payload_version` | Literal `pi11-events/1`; one closed name from D2; integer 1. New meaning requires a new payload version; envelope changes require a new format. No unknown-type success. |
| `event_id`, `sequence`, `stream` | Event ID `ev1:<epoch>:<sequence>`; epoch is nonzero 32 lowercase hex, unique per stream incarnation; sequence is canonical decimal string 1..9223372036854775807. `stream` is `{tenant_id,kind,box_id,epoch}`, kind `control` or `box`; control box is null, box kind requires its exact box ID. |
| `source` | `{owner,instance_id,epoch,sequence}`: closed owner in D2, core-bound nonzero 32-hex instance/epoch, canonical positive decimal sequence. Persisted owners use their durable event-source counter; observation owners use their leased ingress sequence. Not request-supplied authority or wall-clock order. |
| `aggregate` | `{kind,id,revision}`. Kind in D2; existing Tag/Content/Assignment IDs keep their own canonical codecs. Box aggregate ID equals box ID. Revision is canonical decimal or null where owner has no revision (unmanaged content/media observation); never invent an aggregate revision from event sequence. |
| `occurred_at`, `committed_at` | `occurred_at` nullable, producer-reported UTC fixed 9-fraction RFC3339 timestamp; null if unknown. `committed_at` same format, core clock sampled once inside the successful append transaction immediately before COMMIT. It is the transaction's commit label, not a measured fsync completion instant. Publish only after commit confirmation/readback. Clock reversal never reorders events. |
| `causation_id`, `correlation_id` | Nullable opaque references, maximum 128 ASCII bytes each, `[A-Za-z0-9_.:-]`; core-generated or validated owner command identity, never a URL/token/actor name. Causation identifies direct command or observation; correlation groups related work but grants no authority/order. |
| `payload` | Exactly the closed fields of its D2 variant; no raw owner aggregate, SQL, path, URI, title, auth header, account identifier, UID/rUID, media bytes, evidence text or exception string. No arbitrary metadata map. |

Tenant and box IDs are new internal nonzero 32-hex opaque IDs, bound by the core,
not derived from UID, address, client text or overlay index. No household identity
claim follows. Source/stream epochs have the same codec; reject zero and reuse.
Strings are valid UTF-8; duplicate/unknown keys, noncanonical IDs, floats in
integer fields, control characters and invalid nullability reject. The encoder
uses the field order above, compact UTF-8 JSON, fixed nested field order as shown,
no insignificant whitespace; payload order follows D2. All integers except the
small version fields are decimal strings. Golden bytes fix escaping (JSON quote,
backslash and control escapes; no HTML escaping) and null representation.
Closed source owner spellings are `tag-registry`, `content-store`, `catalog`,
`assignment`, `gateway-observation`, and `lifecycle`; catalog-scope has owner
`catalog`, storage-scope has owner `content-store` or `lifecycle`, and box
invalidation has owner `gateway-observation` or `lifecycle`.
Maximum encoded payload 4096 bytes, full envelope 8192 bytes, measured including
escaping before copying/enqueueing; both limits apply. Decoder checks raw size
before allocation. Only redacted categories and counters reach logs; event IDs
may be used in scoped debug evidence, never raw payloads or cursors.

## D2: authoritative producers and fact semantics

Owner: the listed state owner, with Event application validating its bound
producer capability. Invariant: no producer writes or infers another owner's
facts. Failure: reject wrong type/scope/revision/lease without state change;
missing evidence remains unknown. Evidence: `TestEventProducerAuthority`,
`TestEventOwnerCoverage`, `TestEventObservationContinuity`.

All payload fields listed here are exhaustive. ID fields use their owner codecs;
revision fields use D1 decimal strings. Nullable is explicit, not omitted.

| Type; stream; aggregate kind | Producer and payload | Meaning / trigger |
| --- | --- | --- |
| `tag.registry.changed`; control; `tag` | Tag Registry; `{tag_id,revision,change}` where change is `created` or `metadata` | Successful new identity or metadata revision only. A placement never creates a Tag or asserts owned/valid/claimed/cloud auth. |
| `content.reference.changed`; control; `content` | Content Store metadata; `{content_id,version_id,change}` with `bound` | New committed version/blob binding after PI-07 publication. Exact import replay emits nothing; no promise of current playable bytes. Aggregate revision null. |
| `catalog.changed`; control; `content` | Catalog; `{content_id,revision,change}` with revision nullable for unmanaged Save; change `baseline`, `adoption`, `review`, `order`, `registration`, `supersession`, `media` | One changed Content per transaction. Multiple catalog mutations on it collapse to change `multiple` (also admitted). Revisions remain catalog-owned. |
| `catalog.scope.invalidated`; control; `catalog-scope` | Catalog; `{reason}` = `source-registration`, `source-revocation`, `import` | Global producer operations without a bounded list of affected Contents. Aggregate ID is tenant ID; revision null. Invalidate catalog/Library queries, never synthesize changed titles/assignments. |
| `assignment.changed`; control; `assignment` | Assignment service; `{assignment_id,tag_id,revision,change}` = `assign`, `unassign`, `classification`, `original-target`, `retire`, `multiple` | New retained command result, including future-effective desired decisions. Requery PI-09 at explicit time/knowledge revision; no timer-based applied event, auto-retarget or playback claim. |
| `tag.placement.observed`; box; `box` | Leased gateway observation port; `{tag_id,protocol_valid}`; tag ID nullable, validity `valid`, `invalid`, `unknown` | Explicit placement report; unresolved physical identity stays null. No UID broadcast, classification or registry write. |
| `tag.removal.observed`; box; `box` | Same port; `{tag_id}` nullable | Explicit removal report only; never derive from stopped playback, disconnect, absent assignment or invalid tag. |
| `playback.observed`; box; `box` | Same port; `{state,content_id,version_id}`; state `starting`, `started`, `stopped`, `unknown`; IDs both null or a producer-verified pair | Reported playback, not hardware acknowledgement or inferred applied assignment. Do not fill IDs from the desired assignment or catalog match. |
| `box.state.invalidated`; box; `box` | Observation/lifecycle owner; `{reason}` = `source-gap`, `source-replaced`, `restart`, `disconnected` | Last reported placement/playback is stale/unknown; not removed/stopped. |
| `storage.state.invalidated`; control; `storage-scope` | Content Store/lifecycle; `{reason}` = `reconcile`, `quarantine`, `restart` | Requery availability through PI-07. Aggregate ID tenant ID, revision null. No filesystem fact inferred from DB references. |

Box aggregate revision is the durable last-observation revision maintained by the
observation port, incremented with each accepted report/invalidation. State
snapshots identify both last reported value and freshness; fresh means continuity
of that producer lease, not independent device verification. Unknown Tag IDs
are never allocated here. Real device registration, observation authentication and
hardware acknowledgement are later gateway/device responsibilities. A/T use a
trusted synthetic producer and must label that provenance.

All existing successful mutation paths must be covered: Tag Insert/CAS; unmanaged
Content Save; catalog adoption/review/order/import/registration/supersession/media
binding and source rows introduced by import/registration; content import;
assignment commit. `source-revocation` is a reserved Catalog-owned reason: no new
source administration command is implemented by A. Until an authoritative owner
command exists it is Unsupported for runtime ingress, with synthetic contract
fixtures only. Direct external SQL mutation is unsupported, not an event producer.
The SQLite adapter encodes each owner's already-accepted result at those paths;
it is not a second domain policy engine. No-op replay and read-only calls append
nothing. Import/source-wide changes may use the specified coarse invalidation
instead of enumerating unbounded subjects. Local Library index rebuild is not a
catalog change. Content filesystem operations are not journal-atomic: persist a
storage invalidation intent before an authorized quarantine/reconciliation
mutation (including import publication/re-import repair), release SQL before
file I/O, then perform the PI-07 operation. Intent
means recheck, including on failure. Also append a completion invalidation after the file
operation succeeds or fails, outside filesystem I/O; if that append fails, mark
storage observation freshness unavailable and fence event admission until recovery.
Invalidation failure does not rewrite a confirmed owner result;
return the original PI-07 outcome with event freshness unavailable, preserving
its indeterminate-result rules.
The pre-intent alone cannot close a requery-before-filesystem-change race.
Restart invalidates storage again before serving. Availability reads themselves
do not append, preventing read/event loops.

## D3: ordering, atomicity, replay and crash recovery

Owner: SQLite LifecycleOwner and Event application dispatcher. Invariant: a
successful state mutation and its journal rows commit in the same existing SQL
transaction; no direct network calls, detached callback or second transaction
closes the gap. Failure: append/capacity/validation error rolls back the whole
mutation using the existing owner's error taxonomy; uncertain commit follows
owner readback/fencing. Evidence: `TestEventCommitAtomicity`,
`TestEventOwnerCoverage`, `TestEventRecovery`, `TestEventIdempotency`.

Use one durable ordered stream per tenant control scope and one per box. Order
is commit order within that stream, with strictly contiguous sequence allocation
inside the transaction; rollback does not consume a committed sequence. Multiple
rows in one transaction order by `(owner,type,aggregate.kind,aggregate.id)` ASCII
order, after deduplicating identical invalidations per aggregate. One stream's
sequence is never comparable to another's; timestamps/correlation do not imply
cross-stream total order. Existing single-writer/lifecycle fences remain intact.
Bound append to 64 events and 64 KiB canonical envelopes per transaction;
source-wide changes use D2 invalidation, not truncated per-entity lists. A
transaction exceeding these bounds fails with no owner mutation; batches must
not be secretly split into separately committed partial success.

The outbox IS the retained journal; there is no delete-on-send queue or global
"delivered" bit. Consumer delivery is at-least-once within available retention,
conditioned on reconnect/drain and available storage. No exactly-once processing
or indefinite replay claim. Durable source mutation replay first uses each
owner's existing retained witness; identical retry generates no new event even
after event retention expires. A new explicit command with identical resulting
values may be a new fact under that owner's rules. Consumers deduplicate by full
event ID and apply only increasing sequence within the same epoch; replay never
executes the original command or side effect. Detect gaps, never silently ignore.

Observation ingress uses one active lease per box, core-issued source epoch and
strict contiguous source sequence. Same sequence and exact canonical last input
returns the retained outcome; changed bytes conflict; older-than-last is
`StaleObservation` (no append); ahead-of-next requires `source-gap` invalidation
and a new lease before accepting a fresh full observation. Retain only the last
input/result and high-water mark per lease, bounded by D1. Replaced leases reject
late reports. Transport guarantees begin at durable append acknowledgement;
loss before append is not recoverable without later gateway retransmission.

Wakeups after commit are hints; dispatcher always reads committed journal rows.
A missed wakeup is recovered by a bounded scan at most one second later while
healthy (scheduler policy, not an end-to-end latency SLA). No publication from
uncommitted objects or an uncertain caller response. Resolve uncertain commit
against durable command/observation identity under the existing owner; confirmed
commit may publish even if caller cancellation subsequently wins. Failed rollback
or cleanup fences the owner until validated reopen, without inventing success.
Crash before commit gives silence; crash after commit/before send replays; crash
after send/before client checkpoint may duplicate. On process restart validate
schema, journal framing, counters, retained suffix and owner consistency before
admitting subscribers. Revoke ingress leases, durably invalidate old observation
freshness and storage state before live admission; retained observation history
remains historical. Do not reconstruct missed physical actions.
Startup invalidation runs in bounded batches with all admission closed until
completion; a failed batch fences startup. This is lifecycle preparation, not
permission to split one accepted domain command into partial commits.

One additive forward migration introduces stream metadata, bounded journal,
observation latest-state/lease records and persistent cursor signing key and
authorization generation. Source counters, stream epochs and observation revisions
never wrap; exhaustion rejects writes as `CapacityExceeded` until an explicit
owner-controlled epoch transition/reconciliation. Existing aggregate revision
exhaustion retains that owner's rejection rule. At this base the candidate is version 11; choose/reconfirm the next unused number at A
admission. Constraints include unique `(epoch,sequence)`, unique epoch, exact
scope/type binding, nonnegative floor/high-water counters and bounded bytes.
No historical domain rows become fabricated historical events. Enabling on an
existing DB starts a fresh epoch with floor/high-water zero and requires D5
reconciliation. Preserve old migration bytes/checksums and existing owner gates.

## D4: finite retention and reconnect cursor

Owner: journal/retention adapter and Event cursor codec. Invariant: only an
unbroken authorized retained suffix can replay; eviction is explicit. Failure:
`InvalidCursor`, `ReconcileRequired`, `Unavailable` as distinguished below.
Evidence: `TestEventRetention`, `TestEventCursor`, `TestEventRestoreEpoch`.

Initial normative test profile (not yet measured): maximum 64 box streams plus one control stream
for the single admitted tenant; at most 4096 rows and 4 MiB encoded envelopes
per stream, 32768 rows and 32 MiB per owner. Retention maximum age 24 hours by
core commit label; no minimum offline duration is promised. Evict complete oldest
prefixes transactionally, recording each stream's retained floor; never holes.
Global pressure evicts from the largest charged stream first (tie by epoch), then
rechecks all caps. Slow consumers do not pin storage. No silent coalescing of
committed rows. Allocation at the box cap rejects `CapacityExceeded`; no automatic
box eviction/reassignment. Startup and bounded maintenance enforce age pruning;
clock reversal can shorten/extend age eligibility, never relax count/byte caps.
Bytes mean canonical retained envelopes, not a claim about physical SQLite/WAL
size; file-size/vacuum policy stays with storage operations and is not new deletion
authority. Journal storage failure rolls back appends instead of dropping events.

Sequence high-water and floor survive pruning and normal restart. A cursor
carries version, tenant, stream kind/box, epoch, last-completely-received sequence,
authorization generation and projection version; fixed canonical binary encoding,
base64url without padding. Exact cursor bytes: uint8 version 1, tenant ID 16 raw
bytes, uint8 kind (0 control/1 box), box ID 16 raw bytes (all zero only for
control), epoch 16 raw bytes, uint64 big-endian sequence, uint64 big-endian
authorization generation, uint8 projection 1, then 32 HMAC bytes. MAC is
HMAC-SHA256 over ASCII `pi11-cursor/1` followed by those preceding bytes, using
an owner-persisted random 32-byte key. External token is `ec1.` plus base64url
of all 99 bytes (136 ASCII bytes total); reject noncanonical encoding, unused
values and counters above signed-64 maximum. Maximum raw token 512 ASCII bytes;
strict decode/length/MAC and scope validation (valid v1 tokens are exactly 136).
Keys never appear in envelopes/logs. This is internal integrity
and binding, not PI-12 login/authentication. Rotate on restore or key loss; do not
reinterpret another owner's/library/catalog/assignment cursor.

Authorize before cursor lookup. Malformed/forged/oversize/wrong-profile is
`InvalidCursor`; valid but foreign scope returns the same `InvalidCursor` without
revealing its stream. Valid same-scope future sequence, retired epoch, changed
authorization generation or expired/pruned prefix requires reconciliation, never best-effort tailing. Let floor F be the largest
evicted sequence and H the committed high-water: replay only if `F <= cursor <= H`
and the suffix `(cursor,H]` is intact; cursor F is valid. An internal suffix hole
is corruption: fence `Unavailable`, do not disguise it as ordinary expiry.
Ordinary reopen preserves valid cursors; close revokes live subscriptions only.
Restore creates new epochs and rotates the key before serving, even if restored
values/counters match old values (ABA). In the new restored DB, atomically reset
only the private replay journal/floors to empty new streams before admission;
revoke observation leases and mark latest reports stale. Retain every domain
ledger, reference and receipt unchanged. Event history is not an audit archive.
Rotated-key tokens fail MAC as `InvalidCursor`; consumers perform the same
fresh-start flow. No key resurrection.

## D5: SSE framing and authoritative reconciliation

Owner: SSE adapter and reconciliation application service. Invariant: connection
success never means state is synchronized; a gap requires owner reads. Failure:
close with explicit outcome, preserve last complete cursor, reconcile before
claiming fresh state. Evidence: `TestEventSSE`, `TestEventReconciliation`.

Internal HTTP handler only, mounted by trusted test composition in B; no public
route/SDK/browser deployment is admitted. One connection selects exactly one
stream through an already-authorized capability; no all-box wildcard or client
filter. Control and box connections are separate. Frame:

```text
id: <opaque cursor through this event>
event: <canonical type>
data: <single-line canonical envelope JSON>

```

Use UTF-8 `text/event-stream`, no cache, flush complete frames, reject CR/LF
injection, and release DB read scope before network writes. `Last-Event-ID` is
exactly the opaque cursor, not raw event ID. One header only, no query override;
empty/absent requires reconciliation. No legacy `{type,data}` nesting in this
internal profile. Heartbeats are `: keep-alive` comments every 15 seconds of idle
connection, carry no ID and do not advance progress. Maximum complete frame
9216 bytes; preflight envelope, cursor and framing together.

Before headers: unauthorized 403; malformed cursor 400; valid gap/expired/epoch
409 with stable `reconcile-required`; capacity/unavailable 503; no raw internal
errors. Authorized empty cursor also gets 409. These are internal test semantics,
not frozen public API. After headers, attempt `event: stream.reset` with a bounded
reason (`gap`, `slow-consumer`, `revoked`, `shutdown`, `unavailable`) and no ID,
then close. A blocked socket may prevent that control frame: transport EOF/error
itself MUST mark the view stale and trigger reconnect validation. Never falsely
acknowledge a cursor or require a terminal frame to guarantee loss visibility.
Unknown event/profile at the consumer also invalidates its view; no skip-and-ack.

Reconciliation is a barrier protocol, not an unbounded cross-owner snapshot:

1. Authorize exact scope and obtain current epoch/high-water H plus a signed
   start cursor under the journal owner. A fresh empty stream has H=0; zero is
   admitted only for this cursor, never as an event sequence.
2. Read relevant bounded authoritative snapshots via existing owner APIs:
   Tag identity/metadata, Catalog/Library pages, Assignment at explicit time and
   knowledge revision, Content availability only when requested, and the new
   observation owner's last-reported state/freshness. These reads must begin
   after H capture; each retains its own revision/cursor and limits. No nested
   owner transactions, filesystem scan, global reconstruction or source writes.
3. Replay every event after H while/after snapshots load. Treat domain events as
   invalidations requiring owner reread, not older payloads to overwrite newer
   snapshots. For box observations reread the latest observation state instead
   of applying historical reports as fresh physical truth. Buffer is bounded by
   D6; expiry/overflow/restore/revocation restarts the barrier, never splices epochs.
4. Capture tail K, drain through K, reread any invalidated component, and validate
   its own snapshot token. Mark that component reconciled only with no unapplied
   invalidation through K. Newer events immediately make it dirty again. There
   is no globally simultaneous physical/database snapshot promise. Continuous
   changes may keep a component stale; return `ReconcileBusy` after 5 seconds
   and let the caller retry with bounded backoff, never an endless server loop.

PI-10 cursors are never event cursors; expired Library pagination restarts only
that owner read while replay remains intact. Box scope permits only its own
observation snapshot; it grants no Tag/Catalog/Assignment detail read. Those
reads require a separate control capability and control-stream reconciliation. Assignment time progression can
change desired projection without a new commit; clients reread at effective-time
boundaries using PI-09 semantics, not a fabricated playback event. B exposes the
barrier/read coordination as internal ports and a tested adapter; later UI/public
snapshot routing is deferred and cannot be claimed delivered by B.

## D6: subscriber lifetime, bounded work and fairness

Owner: Event hub and SSE adapter, lifecycle owner for close/restore. Invariant:
no socket stalls producers, another box, lifecycle close or unbounded allocation.
Failure: reject admission or disconnect the affected subscriber and require
cursor validation/reconciliation; never silently drop queued events and continue.
Evidence: `TestEventSlowConsumer`, `TestEventFairness`, `TestEventLifecycle`.

Maximum 64 subscribers per owner, 8 per stream, one per capability/stream;
subscriber queue at most 128 events AND 128 KiB actual canonical frame bytes.
Global queued bytes at most 2 MiB; charge full bytes per subscription even when
sharing immutable backing storage. Reserve before enqueue; an overflow closes
that subscriber (release all its charges), without blocking append/other readers.
At global pressure reject the enqueue's subscriber, not an unrelated tenant/box.
Admission beyond a cap returns `CapacityExceeded` before allocating a goroutine.

Round-robin ready streams and then subscribers, at most 16 events/128 KiB per
turn, one bounded DB batch per turn; do not hold a DB transaction/gate across
network writes. Maximum one writer goroutine per subscriber and one dispatcher
per owner; waits use contexts, not unbounded goroutines. One frame write/flush
has a 5-second deadline; inherited SQL/operation deadlines are not increased.
Batches preflight sizes; an event cannot exceed the per-turn byte cap. Poll timer,
heartbeat and cancellation cleanup share bounded owned tasks.

Cancel/unsubscribe idempotently marks closed, cancels blocked work, removes the
registration and releases queues/timers exactly once. No enqueue after close.
Shutdown stops admissions/ingress first, cancels and joins hub/SSE tasks, then
lets the existing lifecycle owner drain/close. Drain has a 5-second bound; on
expiry force connection cancellation, return `ShutdownIncomplete` if tasks still
remain, and do not claim clean shutdown. Restore first revokes subscriptions and
leases, drains through the common gate, selects verified DB, changes event epoch,
then reopens event admission after validation. No callback/repository survives
its scope. Network deadlines and cancellation are required capabilities of the
adapter; unsupported writers reject before subscription.

## D7: tenant, box and authorization boundary

Owner: trusted composition supplies producer/read capabilities; PI-12 owns the
future administration auth/threat model. Invariant: no input-minted authority,
cross-box broadcast or personal ownership inference. Failure: deny before storage
or metadata leakage, revoke active streams on capability generation change.
Evidence: `TestEventIsolation`, `TestEventRevocation`, `TestEventRedaction`.

Version 1 admits one explicit tenant per LifecycleOwner/database. Tenant fields
are mandatory even in that deployment; multi-tenant hosting in one DB is
Unsupported, not row filtering after replay. Multiple owners have disjoint keys,
epochs, queues and capabilities. Core issues exact control-scope or one-box read
capability, separately from producer capability. Box access alone never grants
control-catalog/assignment visibility. Binding is immutable for a stream; tenant
or box reassignment requires new epoch and reconciliation, never retagging rows.

Check capability at subscribe, before replay/read and before each frame begins;
revocation cancels pending I/O and clears queued frames. Bytes already written
cannot be recalled. Snapshot and cursor access have the same authorization.
Absent PI-12, no remotely reachable listener is wired; tests inject trusted
capabilities, including denied/expired cases. MACs, public catalog provenance,
Tag owned flags and an asserted box ID are not user authentication. B must not
silently add permissive HTTP auth, browser SDK access or token-in-URL shortcuts.

## D8: operational bounds, diagnostics and rollback

Owner: Event service/storage maintainer. Invariant: failures remain observable
without leaking payloads or changing owner state. Failure: fail closed and expose
bounded typed counters; no successful-delivery claim from a write/flush alone.
Evidence: `TestEventRedaction`, `TestEventRecovery`, `TestEventRollback`.

Internal counters: appends, append failures, replayed events, pruned rows/bytes,
active subscribers, queued bytes, slow disconnects, gaps, denied admissions,
reconcile failures and unfinished shutdown tasks. Bounded labels are type/reason;
no per-UID, arbitrary actor, raw cursor or unbounded per-entity label. T measures
commit-to-enqueue/write distributions with runner/load/profile, including cold
recovery and stalled consumers. No universal latency SLA or device delivery
acknowledgement before measurement. A flushed SSE frame is transport progress,
not proof the application processed it.

R rollback is a revert of the four documentation files. Future feature rollback
stops hub/SSE admission and revokes live capabilities while owner writes continue
to append bounded journal facts on the supported schema. Do not disable only
append hooks while still promising replay. If journaling must stop, disable the
whole event capability, mark a new epoch before re-enabling, and require snapshots.
Never delete source ledgers/media or rewrite old commands to repair an event.
Future schema rollback is forward-compatible disable, not down migration. Restore
only through existing verified new-path LifecycleOwner flow; pre-event-schema
restores make events Unsupported until explicitly upgraded, and do not auto-migrate
on read. Old binaries unable to validate the newer schema must not open it as a
rollback shortcut. Physical power-loss and production migration need later proof.

## D9: acceptance boundary and exclusions

Owner: parent architecture reviewer and later A/B/T owners. Invariant: refinement,
software delivery and external acceptance remain distinct. Failure: reject
admission/completion if a required decision, file or proof is missing. Evidence:
`TestEventMilestone` plus the plan's scope/link/check gates and parent review.

R changes only the four allowed docs. A/B/T are whole dependency-ordered jobs,
not schema-only activation or codec micro-slices. No runtime/schema/workflow,
issue/status/ledger, deployment, hardware, public API/V3 freeze, auth
implementation, production migration or push is authorized by R. Event journal
is not a business audit ledger, universal broker, durable per-client ack store,
command bus, distributed transaction, media cache or historical playback truth.
Hardware ingress, MQTT-to-core mapping, public routing, browser reconnect UX,
remote tenant provisioning and applied device state remain later PIs. Software
milestone proof must state that limitation; R alone proves no live delivery.

## F1: failure matrix and named future acceptance evidence

Names below are mandatory prospective test families, not existing/pass claims.
Every D-section's owner, invariant and failure disposition above is binding;
this matrix supplies positive and negative acceptance evidence and placement.

| Family / job | Fault and required oracle |
| --- | --- |
| `TestEventEnvelope`, `FuzzEventEnvelope` / A | Golden bytes for every variant; missing/duplicate/unknown fields, enum/null/UTF-8/CRLF/ID/version/clock cases; exact/+1 raw decoder size guards, overflow-safe decode; fuzz 10 seconds, no panic/unbounded allocation. |
| `TestEventProducerAuthority` / A | Every type with correct/wrong owner, scope and expired lease; no UID/title/path/actor leakage; desired future assignment cannot manufacture playback/removal or Tag metadata. |
| `TestEventOwnerCoverage` / A | Real Tag, Content, all catalog mutation families, source inserts/imports, assignment writes and exact retries; reserved revocation
reason tested as Unsupported outside synthetic owner fixtures; mutate then rollback, no-op and multi-command transactions; no missing hook, no duplicate outbox rows. Old schemas preserve inherited supported behavior with events Unsupported. |
| `TestEventCommitAtomicity` / A | Fault before owner write, between write/append, after append/before commit, commit uncertainty and postcommit cancellation; owner state and journal agree or owner is fenced. Reads/rollback/rejected commands emit zero. No network/FS in SQL callback. |
| `TestEventIdempotency` / A | Owner command retry after pruning/restart; same/changed ingress duplicate, old/high sequence and lease replacement; one committed identity, no automatic callback replay. |
| `TestEventObservationContinuity` / A | Two interleaved boxes, placement-invalid/removal/stopped distinctions, source gap/restart/disconnect; latest reports stale until fresh leased report; no fabricated device state. |
| `TestEventRecovery` / A, reinforced T | Real helper-process crash before commit, after commit/before dispatch, after frame/before checkpoint; reopen same cursor, byte-identical IDs, duplicate tolerated; missing/corrupt suffix fences. DB-only media orphan/recovery remains PI-07-owned. |
| `TestEventRetention` / A | Exact/+1 64 boxes, per-stream/global count and bytes, batch count/bytes, 24-hour boundary, clock reversal, large/small stream fairness; floor updates atomic, cursor at F versus F-1; prove each fixture reaches its intended bound first. |
| `TestEventCursor`, `TestEventRestoreEpoch` / A | MAC tamper, wrong scope/profile/projection, oversize/unknown/future IDs; normal restart preserves replay; verified current/pre-event-schema restore and same-value ABA revoke; signing key rotation and unavailable schema fail closed. |
| `TestEventSSE` / B | Exact frame/id/event/data bytes, fragmented writes, heartbeat, Last-Event-ID absent/empty/repeated, invalid/gap status, disconnect mid-frame, unsupported writer, reset lost to blocked socket; no cursor advancement for partial/control frame. |
| `TestEventReconciliation` / B | Capture H then race snapshots/commits; prune, restore, owner cursor expiry, concurrent future-effective assignment, unavailable media and box stale state; drain through K or explicit stale/Busy, no newer snapshot overwritten by old event. |
| `TestEventIsolation`, `TestEventRevocation` / B | Two boxes plus control stream, two isolated owners/tenants, forged capability/cursor, revoke during replay/blocked write/snapshot; zero unauthorized rows/frames after revocation boundary; already-sent bytes explicitly excluded. |
| `TestEventSlowConsumer`, `TestEventFairness` / T (basic A/B regression) | Block one writer, saturate stream/global queues and subscriber caps, flood one box, keep another progressing; exact/+1 byte/count limits, bounded rotations, visible disconnect and replay/gap on reconnect; no producer blocking or silent loss. |
| `TestEventLifecycle` / T (basic A/B regression) | Repeated subscribe/cancel, concurrent publish/unsubscribe, close/restore with blocked writer, deadline and panic/error cleanup; zero residual registrations/queue charges/timers/tasks after successful shutdown; race detector and repetition. |
| `TestEventRedaction`, `TestEventRollback` / T | Inject credentials/paths/UIDs/errors; validate bounded diagnostics and no leakage. Disable/re-enable/restore cannot preserve a false cursor or modify domain facts/media. |
| `TestEventMilestone` / T | Composed real SQLite hub + SSE + barrier + synthetic ingress; ordered live/reconnect delivery, ownership/no-inference, two-box isolation, slow consumer and restart matrix; measure distributions and resource ceilings, compare source state/receipts unchanged except intended owner commands. |

Boundary evidence must distinguish guard limits from reachable canonical values.
The closed v1 payload grammar cannot fill its defensive 4 KiB payload/8 KiB
envelope ceilings: prove decoder oversized-input rejection and calculate the
maximum valid encoding, without claiming a successful canonical exact/+1 pair.
Count and queue/journal byte fixtures must separately demonstrate which limit
is reached first; adjust test compositions, never invent padded payload fields.

A/B/T checks must preserve inherited normative deadlines, F02 accounting
assertions/tier disposition and PI-10 query ownership/cursor semantics. T cannot
repair a missing A/B invariant by weakening its test or calling it a hardware
unknown. The [plan](pi-11-plan.md) assigns every family and writable path.
