# PI-09 assignment, original and custom policy

Status: R contract complete; A/B/T and the authoritative-service milestone are
not delivered. Internal policy `pi09-assignment/1`; no public V3 vocabulary freeze.
Request: `pi09-r-assignment-semantics-contract-20261031`.
Evidence baseline: `90301bfe2b5badd684229c3d5d140fa975842d91`.
See the [plan](pi-09-plan.md) and [budget](pi-09-budget.md).

## Source ledger and inherited ownership

The following repository sources establish existing behavior. Decisions D1-D7
below are the selected future contract, not claims that this service exists.

| Source at baseline | Constraint inherited by PI-09 |
| --- | --- |
| [Roadmap PI-09](../teddycloud-next-pi-roadmap.md), [routing](../task-model-routing.md) | R refines semantics; A assigns; B validates source URIs and supplies history; T consolidates domain classification. |
| [PI-06 Tag contract](pi-06-tag-registry-contract.md), [Tag ports](../../../next/backend/internal/application/tagregistry/ports.go) | Canonical UID and independently allocated TagID remain immutable; ownership/auth facts are independent. Cross-domain assignment transaction is deferred to PI-09. |
| [PI-07 storage contract](pi-07-taf-storage-contract.md) | ContentVersion, BlobID and verified receipt are distinct; assignment cannot certify media or delete it. |
| [PI-08 policy D1-D6 and deferrals](pi-08-catalog-policy.md), [PI-08 B2 recovery boundary](pi-08-plan.md) | No automatic source precedence, custom merging or retargeting. Durable assignment recovery was explicitly not proven by PI-08. PI08-D05 hands assignment changes to PI-09. |
| [Assignment prototype](../../../next/backend/internal/domain/assignment/assignment.go), [prototype tests](../../../next/backend/internal/domain/assignment/assignment_test.go) | One Assignment ID/Tag per history; immutable, explicit half-open intervals; no overlap, exact replay and 4096-entry limit. Open intervals cannot be closed by editing old records. |
| [Migration prototype](../../../next/backend/internal/domain/migration/migration.go), [migration audit](../../../next/backend/internal/domain/migration/audit_test.go) | Explicit Unclassified/Original/CustomCard inputs; no classification from flags or metadata; no production importer. |
| [Catalog queries](../../../next/backend/internal/application/catalog/queries.go) | ProtectedCustom is independent of physical classification; supersession Terminal never retargets assignments; unbound media is legitimate. |
| [Catalog ports](../../../next/backend/internal/application/catalog/ports.go), [SQLite owner](../../../next/backend/internal/adapters/sqlite/lifecycle_owner.go), [schema](../../../next/backend/internal/adapters/sqlite/schema.go) | One scoped lifecycle owner, no nested transaction or unmanaged Save; migrations 1..9 are active at this baseline. No assignment persistence exists here. |

Historical pending labels in PI-06/07/08 contracts do not reopen already accepted
work; the [execution index](README.md) records those later software acceptances.
Only their assignment-facing invariants are inherited, not hardware evidence.

## D1: authoritative terms are separate dimensions

| Term | Normative meaning |
| --- | --- |
| Tag | PI-06 physical identity represented by TagID; UID/rUID are codecs, not classification. |
| Content / version | Editorial ContentID and an explicitly selected immutable ContentVersionID belonging to it. Equal fingerprints or blobs do not merge identities. |
| Public catalog content | Content with retained public-source provenance under PI-08. This describes acquisition/support, not an original physical card or permission to play/download. |
| Custom content | Independently identified operator/custom-origin content. Retain PI-08 ProtectedCustom where present; do not promote an unreviewed custom claim to accepted authorship. Protection may coexist with public-source support. |
| Original / CustomCard | Explicit reviewed physical-Tag classification. Values are unknown, original, custom-card or conflict; accepted contrary evidence produces conflict until explicit resolution. Custom content does not classify the Tag holding it. |
| Original content for a Tag | An explicitly reviewed baseline relation from that Tag to a Content/version tuple, with support. It is neither the first assignment nor the first catalog entry. Missing baseline means unknown; it is never an implicit fallback. |
| Tag assignment | An explicit desired association from one Tag/Assignment aggregate to one Content/version tuple. One aggregate per Tag, unique in the database; many Tags may target the same version. |
| Effective assignment | The desired target selected by D3 at explicit query time and knowledge revision. It says nothing about applied gateway state, playback, availability, ownership or auth. |
| Provenance | Immutable command, core-bound actor/capability, policy revision, reason, subject, support references/safe snapshots and selected revisions. Source text and confidence are not authority. |
| Source URI | Optional sanitized descriptive locator of assignment evidence, admitted by D4. It cannot replace the typed target or authorize any I/O. |
| Reassignment | A new decision selecting a target, including an explicit repeat of the same target with changed provenance. Earlier decisions survive. |
| History | Append-only accepted commands/results and their evidence, ordered by aggregate revision; rejected attempts are not fabricated successful events. |

A custom-card Tag may be assigned public content; an original Tag may be assigned
custom content. Neither transition changes classification or original baseline.
`protocol_valid`, `claimed`, `owned`, `cloud_auth`, `exists`, `nocloud`, filenames,
directory paths, AudioID offsets, array positions, titles, models and article
numbers cannot decide original/custom or a target. PI-09 does not infer `nocloud`;
cloud execution/settings semantics remain deferred. No boolean `isOriginal`
collapses unknown/conflict, and no default custom classification replaces them.

## D2: explicit precedence, review and admission

The assignment service is the sole writer of assignment aggregates, their
original-baseline relations and Tag-classification evidence. Tag Registry remains
the identity/boolean-metadata owner; Catalog remains Content/version/provenance
owner; Content Store remains media owner. Domain classification rules live in
`domain/assignment`, shared by future adapters, not copied into transport code.

An in-process core capability binds actor and permitted commands; request text
cannot mint either. External authorization remains PI-12. Classification and
original-baseline changes require a review capability, supported same-Tag claims,
expected revision and reason. Operator assertions are explicitly labeled operator
evidence, never authenticated manufacturer evidence. Pending/disputed/rejected
claims remain non-authoritative. Agreeing accepted support accumulates; contrary
accepted support yields conflict. Explicit resolution selects a nonempty agreeing
support set, retains dissent and can be reopened by new contrary accepted support.
Assignment commands never resolve classification or baseline conflicts incidentally.

Only an explicit Assign command selects a target. A unique catalog lookup may
suggest that tuple, but cannot write it. Ambiguous/incomplete/conflicted lookup
has no winner; never choose first, latest, custom-first or public-first. The
caller must provide the exact tuple and current dependency revisions. An
explicit tuple may coexist with editorial/classification conflict because those
facts are not selection prerequisites; expose their conflict flags. Conflicting
or missing version ownership is a rejection, not editorial uncertainty.

The scoped transaction verifies the Tag and Content/version exist and that the
stored version belongs to that Content. A managed catalog version must have
unambiguous accepted ownership/fingerprint support matching its immutable tuple;
unmanaged or unreviewed versions require separate PI-08 adoption/review first.
Unbound catalog-only versions and explicitly selected superseded versions are
allowed, annotated as such. Supersession/order, new imports, custom protection,
metadata edits, unavailable upstream and missing bytes never change a target.

## D3: immutable decisions and temporal projection

Select a new append-only decision ledger in addition to the existing interval
prototype. Keep `Assignment`, `Interval`, `NewHistory` and `CurrentAt` prototype
semantics unchanged; do not feed overlapping open-ended decisions to NewHistory
or mutate its historical Until fields. New ledger/projector names must be distinct.
No persisted prototype history exists to convert at the evidence baseline.

Each aggregate has an independently allocated existing-format Assignment ID and
immutable TagID. Absent aggregate is revision 0; the first successful command
creates revision 1, subsequent commands advance exactly one, up to signed 64-bit
maximum. Each retained event contains command ID, command kind, expected/result
revision, prior event reference, actor, policy/encoding revision, canonical input,
canonical result, reason and bounded support. Command IDs are independent nonzero
32-character lowercase hexadecimal values, unique across assignment aggregates;
changed cross-Tag reuse conflicts. Recorded-at is a core-supplied UTC audit time,
not precedence or part of caller-controlled replay identity. Target-bearing commands retain both
ContentID and ContentVersionID plus Tag/catalog dependency revisions checked at
admission. IDs are never generated from metadata, URI, UID or fingerprints.

Commands: Assign, Unassign, RecordClassification, ResolveClassification,
RecordOriginalTarget, ResolveOriginalTarget and RetireAssignment. Review commands
retain immutable evidence and resolutions using D2. No command rewrites Tag
identity, catalog facts, PI-07 receipts or older events. An original-target
assertion also validates the referenced tuple but never creates an assignment.
The first command may be a review command; that creates a known, unassigned
aggregate. A new command repeating a target is a new explicit decision, not a
silent no-op; exact replay alone is a no-op.

Assign/Unassign/RetireAssignment carry explicit UTC effective time, with monotonic
process clock data removed. These times strictly increase across those three
kinds; gaps, equal or earlier transition times are not repaired. Classification
and baseline events do not move the assignment timeline. At query time `t` and
retained knowledge revision `r`, select the last transition with revision <= r
and effective time <= t. Assign selects its tuple; Unassign selects none;
Retire selects a tombstone. Before the first transition there is no assignment.
Its derived interval ends at the next transition, excluding that endpoint; this
is a projection, never an update to the earlier event. Example: Assign V1 at 10,
Assign V2 at 20, Unassign at 30 gives V1 on [10,20), V2 on [20,30), none thereafter.

Times are caller-supplied intended effective times, not proof of physical action.
A later admitted event may describe a past time only after the last transition;
queries with the earlier knowledge revision still return the earlier known view.
Future transitions are allowed and do not win early. Inserting before/equal to an
existing transition, rescheduling/canceling it by rewriting history, and arbitrary
retroactive timeline repair are deferred. Retire must follow all queued transitions;
it seals the aggregate against further new commands immediately, even when its
effective time is future. Exact authorized replay/readback remains available.

Canonical replay binds all inputs, including expected dependency revisions,
effective time, source URI and actor. Authenticate/authorize first, then check
retained command identity before stale-revision/current-target admission. Exact
replay returns the original result with no revision/event change, even after
later catalog changes or retirement. A reused command ID with any changed
canonical input conflicts. Same expected revision raced by different commands:
one commits, the other conflicts and must reread; no last-writer-wins or automatic
callback replay. Canonical encodings and collision-safe equality need A1 golden
vectors; digests alone are not full-command equality.

## D4: source URI is a pure, conservative admission profile

Assign may omit the locator. Present empty is invalid. Profile `pi09-uri/1` accepts
only absolute hierarchical HTTPS locators; this is deliberately narrower than all
valid URIs and is not a general downloader policy.

1. Bound input at 2048 ASCII bytes before parsing. Reject whitespace, controls,
   DEL, backslashes, non-ASCII input, malformed escapes, relative/opaque forms,
   userinfo, query (including empty `?`) and fragment (including empty `#`).
2. Require case-insensitive `https`, a DNS-form host with at least two nonempty
   labels, ASCII letters/digits/internal hyphens, labels <=63 and total <=253
   bytes; reject trailing dot, IP literals, brackets and numeric-only final label.
   Reject alternate numeric IP syntax. Optional port is exactly `443`; other or
   empty ports fail. Lowercase scheme/host and remove explicit port 443.
3. Empty path canonicalizes to `/`. Otherwise allow `/` and ASCII unreserved
   characters (`A-Z`, `a-z`, `0-9`, `-._~`) only, with percent escapes allowed
   only for those unreserved ASCII bytes.
   Decode those once, then reject `.`/`..` segments and repeated slashes. Encoded
   slash/backslash/control/percent, reserved characters and double decoding are
   rejected. Preserve path case; never trim or repair malformed input.
4. Persist canonical locator and profile revision, not rejected raw input.
   Canonically equivalent accepted spellings replay identically; changed canonical
   locator conflicts under the same command ID. Future parser changes cannot
   reinterpret old commands: keep their profile/codec readable.

Examples: `HTTPS://Catalog.Example:443/a%62` becomes
`https://catalog.example/ab`; `https://catalog.example` becomes
`https://catalog.example/`. Reject `file:`, `http:`, `data:`, `javascript:`,
custom executable schemes, `/local/path`, `https://u:p@catalog.example/a`,
`https://catalog.example/a?token=x`, `%2e%2e`, `%2f` and `%252f` path segments.

No DNS, HTTP, redirects, filesystem access, MIME probing, credential lookup,
provider resolution or trust promotion occurs in parsing, command admission or
query. DNS-shaped hosts may still resolve privately or maliciously; acceptance
is explicitly not an SSRF-safe fetch permit. A future fetcher must independently
validate DNS/address/redirect/auth policy under its own capability. URI text is
not source registration, origin proof, custom/original evidence or availability.
Caller sanitization is required even for path text; arbitrary secrets cannot be
recognized syntactically. Errors expose fixed codes/coordinates, never raw URI,
credentials, UID/rUID, local paths or raw database causes.

## D5: persistence, references and bounded reads

Add a distinct `WithinAssignmentTransaction` port on the existing LifecycleOwner.
The callback receives assignment mutation/read ports plus read-only Tag and catalog
reference snapshots from that same database transaction. It must not call other
services' transactors, hold another owner, cache a repository or perform I/O.
Check expected Tag/catalog revisions in this transaction; changes between lookup
and mutation conflict. Use scoped capabilities, one serialized connection, one
callback execution, context check before commit and repository revocation on exit.

Atomically persist event, evidence/support, command/result, head revision and
rebuildable projection. Persistence validates semantic equivalence and count/byte
bounds; a generic Save/upsert cannot bypass ledger guards. Restore/close fences
all domains together. Precommit rejection is no effect; commit/cleanup uncertainty
is indeterminate until authorized LookupAssignmentCommand. A lost response is not
permission to issue a new ID or compensate with deletion.

Use restrictive references from aggregate to Tag, events to aggregate, tuples to
Content/version ownership and support to same-subject evidence. Reference owners
cannot cascade-delete retained assignment evidence; history pins referenced Tag,
Content, version and evidence identities. Shared blob references do not merge
assignments. Unassign is an explicit transition, not row deletion; RetireAssignment
is the retained logical tombstone. No hard delete, TTL, GC, reuse of an Assignment
ID, replacement aggregate for the same Tag, or automatic resurrection is admitted.
A Tag deletion/merge or removal of referenced content needs a later reviewed
cross-owner retention contract. Tombstones do not revoke PI-06 identity or delete
media; queries before retirement still resolve the retained historical target.

| Query / condition | Required outcome |
| --- | --- |
| GetEffectiveAssignment(TagID, at, optional knowledge revision) | Tag not found; known Tag/no aggregate; unassigned; assigned exact tuple; or retired, with head/knowledge revisions. No fallback from no aggregate/unassigned. |
| Assigned tuple | Include catalog-only/bound-unchecked and explicit classification/baseline unknown/conflict, custom protection and supersession annotations. Metadata read performs no media verification. |
| GetAssignment / ListAssignmentHistory | Include tombstone and retained revision-ordered events; stable bounded pages. `as-known` target derives from retained events; classification/original-baseline projections use the selected knowledge revision; live catalog annotations carry their own revisions and are not historical claims. |
| LookupAssignmentCommand | Absent, original exact result, or command-identity conflict; current caller permission still required. |
| Invalid/missing referenced persisted row or impossible projection | Corrupt/unavailable storage, never not-found, unassigned or catalog-only. |
| Old schema, unmanaged unsupported state, stale cursor, limit, busy, canceled/deadline | Distinct typed outcomes; no silent upgrade, truncated success or implicit retry. |

Bounds are simultaneous, conservative internal admission ceilings: one Tag per
mutation; 4096 total events per aggregate (including review/retirement); 64 new
observations and 64 selected support references per command; 4096 observations,
16384 cumulative support links and 16 MiB complete retained canonical encoding
per aggregate. Count command/results, snapshots, indices/support and escaped text,
not just projections; prospective totals before copying/joins. Reason <=1024 UTF-8
bytes; source/principal reference <=128 bytes; controls rejected. Query <=128
entries and <=1 MiB encoded response, cursor <=256 bytes bound to query/time/
knowledge/head revision, dependency snapshot and owner generation. Relevant writes,
restart or restore stale the cursor. Zero/over-limit pages reject, not truncate;
continuation explicitly distinguishes a page from complete history. Querying an
unknown future knowledge revision is invalid, not silently clamped.

Operation deadline 10 seconds including validation/admission; transaction <=5
seconds including contention; earlier caller deadline wins. One active operation
per service, another returns busy. Bounded reconstruction validates the whole
aggregate before pagination. Exhaustion stops new writes, retains bounded reads
and reports limit; no eviction of evidence. Verify each limit's reachability under
all simultaneous limits before making exact/+1 fixture claims. Oversized historic
state is unsupported; corrupt state fails closed. Uninterruptible storage may
outlive a deadline: owner fencing remains held while a commit is still possible.

## D6: additive migration and rollback boundary

A2 must bind the next free migration number and exact identifier before changing
schema; baseline has 1..9, so 10 is the candidate only if still free. Preserve all
applied checksums. Existing [catalog transaction gates](../../../next/backend/internal/adapters/sqlite/catalog_transaction.go),
[registration gates](../../../next/backend/internal/adapters/sqlite/catalog_registration.go),
[supersession gates](../../../next/backend/internal/adapters/sqlite/catalog_supersession.go)
and [query gates](../../../next/backend/internal/adapters/sqlite/catalog_repository.go)
explicitly select/restrict v9. Activating v10 without updating those enumerated
compatibility gates would disable or bypass catalog behavior. A2b must preserve
v9 semantics at v10 while still rejecting unsupported future schemas; never
replace exact supported-version checks with unrestricted `>=9` admission.
The additive assignment ledger must not manufacture events,
classification, original targets or assignments for existing Tags/catalog rows.
Known existing Tag/no assignment is a valid explicit query result. Legacy records
and the in-memory migration fixture remain unmanaged evidence, never auto-adopted.

Use PI-05 verified pre-upgrade backup, integrity/FK/ledger checks and closed/fenced
new-path exact-schema restore. A/B must demonstrate real v9 upgrade, current-schema
restart, current and pre-assignment restore, no partially committed assignment,
retained original database and unchanged Tag/catalog/version/blob/receipt data.
Restoring v9 disables assignment commands; reads never create assignment tables.
Keep post-upgrade DB and external media intact. A rollback snapshot omits later
assignments/history: reconciliation/export needs a later operational plan, not a
zero-loss downgrade assertion. Never run destructive down migration, dual-write,
legacy write-back or GC. Source revert is safe only before schema activation;
after activation use verified database recovery, not merely an older binary.

## D7: deferred owners and acceptance limits

| ID | Deferred scope / owner |
| --- | --- |
| PI09-D01 | Public V3 enum/URI/command serialization, Set targets and fallback lookup: PI-13/15/16. Internal terms do not freeze those APIs. |
| PI09-D02 | Real legacy Original/CustomCard/source/nocloud import, unsupported URI schemes, schedules requiring retroactive repair and complete importer reports: PI-14/35, with a separate temporal refinement if needed. No C/JSON writer transfer here. |
| PI09-D03 | Authenticated external commands and source acquisition, provider/cloud policy: PI-12/14/32. No network behavior or cloud eligibility from this contract. |
| PI09-D04 | Applied gateway state, event delivery, hardware playback and device acknowledgement: PI-11 and later protocol/device PIs. Desired assignment is not applied state. |
| PI09-D05 | Production migration, retention/deletion, coordinated DB/media/secret rollback and deployment: PI-34/36/37, with actual operational evidence. |

PI08-D05's assignment-design handoff is resolved here; its operational and recovery
acceptance work is not closed by R. Real helper-process interruption proves only
software recovery at tested boundaries, not physical power-loss. No hardware,
deployment, production migration or independent T acceptance is claimed by R.
