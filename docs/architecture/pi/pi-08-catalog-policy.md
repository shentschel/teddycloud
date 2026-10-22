# PI-08 catalog-source and provenance policy

Status: final bounded R decision set; acceptance requires the publication gate
in the [plan](pi-08-plan.md). This is an internal delivery contract, not public V3.
Baseline: `96b8cdcbf0bf0986afe3a23fecfbff4a508f2cef`.
Issue: [PI-08/R #25](https://github.com/shentschel/teddycloud/issues/25).

## Authority and inherited contracts

The [R1 inventory](pi-08-r1-catalog-inventory.md) is the pinned evidence ledger;
its alternatives are settled below only for this bounded PI. Preserve PI-04
[identity](pi-04-plan.md)/[acceptance](pi-04-review.md), PI-05
[ownership](pi-05-plan.md)/[recovery](pi-05-review.md), PI-06
[Tag contract](pi-06-tag-registry-contract.md)/[acceptance](pi-06-review.md),
and PI-07 [storage contract](pi-07-taf-storage-contract.md)/[acceptance](pi-07-review.md).
Later accepted review sections supersede historical pending paragraphs.

The trusted core is the sole database owner. PI-08 supplies internal services
and an offline, bounded import adapter. No public endpoint, external plugin
entry point, provider authentication, network acquisition, deployment or legacy
writer transfer is admitted. A trusted in-process caller is a security assumption,
not proof of authentication. PI-12 must supply authenticated authorization before
remote callers can reach these commands. Compromised core code is outside this
boundary; untrusted input selecting core privileges is inside it and forbidden.

## D1: source identity is bound by the core, never by source text

Select a core-managed, versioned source registry plus immutable import batches.
The core composition root installs a bounded allowlist of source registrations;
imports cannot register, rename, promote or rebind a source. Each registration
has an opaque SourceID, immutable registration revision, adapter identity/version,
issuer principal reference, acquisition class, allowed claim keys and an opaque
scope. Changing issuer, account scope or adapter contract creates a new
registration revision; old observations retain the old binding. Revoking a
registration blocks new ingestion and acceptance, preserves history and makes
the current disabled status queryable; it does not retroactively alter facts.

These distinct pieces are mandatory:

- Asserted publisher/origin: untrusted descriptive text, including upstream labels.
- Acquiring issuer: core-bound principal/adapter, never read from the artifact.
- Acquisition context: offline, local verification or baseline snapshot;
  provider-authenticated is not available in PI-08.
- Artifact: core-computed SHA256 of admitted bytes, byte length, batch identity,
  parser contract revision, opaque source revision if present and record locator.
- Observation: subject, field/relation, typed value, batch/record reference,
  source registration revision, observed time and internal confidence.
- Decision: core-bound reviewer principal, policy revision, expected/result
  aggregate revision, selected support and reason.

A digest proves which bytes were admitted, not their publisher. A URL, source
label, account name, copied custom record, confidence score or user-supplied
`review=accepted` proves neither authentication nor permission. Reserved issuer,
acquisition and review-authority fields in an import are rejected, not trusted.

| Admitted registration class | Allowed evidence; limits |
| --- | --- |
| Offline public/bundled artifact | Candidate editorial fields and explicit fingerprint assertions. Origin is asserted; no authenticated upstream or freshness claim. |
| Offline custom artifact | Same candidate fields, with protected custom provenance retained. A custom file is not evidence of physical Custom Card classification. |
| Core operator entry | Explicit typed claims and review commands through separately scoped ingest/review capabilities. Actor comes from trusted composition, never request text. |
| Local TAF verification | Existing PI-07 receipts and verified envelope facts only; no editorial title/model/ownership authority. |
| Existing Next snapshot | Mechanical baseline provenance for existing rows/receipts. It certifies what was stored at upgrade, not who authored it or that it was reviewed. |

No operational MyTonies, NFC, V2 or HTTP-download source is admitted. Unavailable
sources return unavailable acquisition; they do not retract prior evidence.
A mirror and its asserted publisher remain distinct; provider-account identity
merging requires later review. Source locators are bounded opaque text, not
paths to open or URLs to fetch. Keep tokens, UID/rUID, credentials, auth headers,
private account names and full local paths out of catalog evidence and errors.

## D2: field provenance plus retained record snapshots

Select immutable field/relation observations with immutable raw record snapshots.
A record snapshot retains all admitted safe bytes, record position and parser
revision. Observations point to exact source coordinates; accepted title,
optional model, optional article, ContentVersion ownership and fingerprint pair
each have explicit support. AudioID plus payload SHA1 is one inseparable claim,
not independently selectable columns. Model and commercial article are separate
facts. ContentID and ContentVersionID are independently allocated opaque IDs;
they never derive from any external key or digest.

Subject mapping is explicit: an importer stages a source-scoped record candidate;
a reviewer supplies a new independent ID or explicitly associates the candidate
with an existing subject under its expected revision. Model/title/pair matching
may suggest candidates, never accept identity. No global uniqueness constraint
on title, model, article, AudioID, fingerprint or shared BlobID is permitted.

Reuse internal confidence `unknown`, `tentative`, `corroborated`; reuse evidence
representation checks without altering Tag rules. Adapter observations default
to unknown. A review command may attach a new assessment observation with
support and reason; it never edits the original. Provider scores/scales remain
opaque raw assertions with their stated scale and no numeric conversion.
Corroboration is not a vote, source priority or permission to accept/overwrite.
Review state is independent of confidence.

Unsupported safe fields (series, episodes, language, cover, track labels and
unknown extensions) remain in snapshots with a field-path disposition report.
They do not enter the accepted Content projection. Unknown enum/structural
versions, duplicate JSON keys, malformed known fields, invalid UTF-8 and excess
limits reject the record; no coercion, truncation or quiet field drop. Parse
unknown values within the same depth/byte bounds and do not execute them.
A report with unsupported fields is explicitly incomplete for that source
format; it must never advertise lossless semantic migration.

Only sanitized catalog artifacts may be retained. Reject known credential/auth,
physical identifier and private-account fields before persistence; never log
their values or hash them as a substitute for redaction. Opaque prose cannot
be certified secret-free automatically: trusted caller sanitization is required.
Rejected sensitive input is not retained, including its artifact digest; return
a safe reason and record coordinate only. No general private provider payload
archive is introduced. Original source ownership remains with the caller.

## D3: explicit acceptance, no automatic source precedence

Select explicit review for every accepted fact and every later change; no
automatic non-conflicting enrichment policy in PI-08. Policy revision
`pi08-catalog/1` grants no source-wide priority. Retained pending/disputed/rejected
observations never change the accepted projection.

For one field/relation, accepted support initially accumulates through explicit
AcceptSupport commands. Agreeing support preserves the value; accepted contrary
support produces an explicit conflict with all alternatives. ResolveFact selects
a nonempty, agreeing support set under the current aggregate revision, records
why earlier support was superseded, and retains all dissent/history. New
contrary accepted support can reopen conflict. A conflicted field has no scalar
winner; a query may expose the last resolved value only as historical context.
Missing model/article is unknown, not a source instruction to clear a fact.
Clearing accepted facts is deferred; no absence, null, timeout or new snapshot
implicitly clears anything.

Observation ID plus payload is immutable. Changed same-ID data conflicts.
An acceptance decision binds its complete canonical command, principal,
policy/source revisions, subject, support and expected revision; command identity
is retained for exact replay. Fully identical committed replay is checked before
stale-revision rejection and returns the original result without increment.
Partial/different replay conflicts. Replay does not restore revoked authority:
only currently authorized callers may retrieve/replay their permitted result.

One successful new mutation advances a positive aggregate revision once;
overflow beyond signed 64-bit storage rejects before mutation. Ingesting new
evidence also advances the revision, invalidating earlier review candidates.
Two same-revision commands cannot overwrite each other: one commits, the other
receives revision conflict and must reread. No automatic callback replay.
Source revision strings, timestamps and delayed arrivals have no freshness
ordering by default. Review must explicitly select newer support and retain the
old support; a stale candidate cannot silently replay as a fresh command.

Existing unconditional ContentRepository.Save is not an acceptance API.
Delivery must prevent it from altering a managed catalog aggregate. Keep the
legacy prototype port for unmanaged baseline callers, but reject writes to
managed subjects; only the guarded transaction can update their accepted
materialized Content projection. Guard the persistence boundary as well as
service use. Reads must expose provenance/review state so an unmanaged baseline
row is never presented as newly accepted PI-08 data.

## D4: catalog-only versions are first-class, media is independent

Select an additive catalog query/write seam using the existing immutable
ContentVersion identity and row. A reviewed explicit ContentID/fingerprint pair
can create a version with no blob binding or import receipt. No placeholder blob,
zero digest, artificial receipt or filesystem probe is allowed. Incomplete or
ambiguous pairs stay candidates and cannot create an accepted version.

PI-08 durable version creation admits nonzero AudioID through uint32, matching
the current schema/media boundary. The wider uint64 domain remains unchanged.
Larger valid domain/source values are retained as unsupported-width candidates;
never narrow, wrap or reject the whole artifact merely because one record cannot
become a durable version. Zero or malformed values are invalid. Domain widening
does not imply that persistence or PI-07 can accept them.

| Catalog/media result | Meaning |
| --- | --- |
| Version not found | No such version identity in the selected database; distinct from missing media. |
| Catalog-only / unbound | Version exists, no binding or receipt, no availability claim. |
| Media-backed / unchecked | Immutable binding exists; metadata query has not verified local bytes. |
| Missing / corrupt / unavailable | Explicit PI-07 verification outcome for a bound version; preserve catalog and receipt facts. |
| Envelope verified | Current PI-07 verification succeeded; not playback, ownership or provider authenticity. |
| Invalid persisted relation | Corrupt/unavailable storage, never disguised as catalog-only or not found. |

Queries load version rows independently of the media-oriented inner join.
A missing binding is legitimate for a catalog-only version; a dangling binding,
receipt without its required binding, invalid row or changed immutable facts is
corruption. An accepted version without reviewed editorial fields may be
reported with explicit field conflict/unknown states; no invented title.

Later media attachment uses the existing PI-07 verified Import command with the
exact stored ContentID, version, fingerprint and original order. The same
immutable version acquires its first binding and receipt atomically; differing
facts or rebinding conflict. PI-08 itself writes no blob, binding or PI-07 receipt.
An exact existing media receipt remains valid even after catalog corrections.
Byte deduplication never merges Content, custom identities or versions.

## D5: later corrections and order are separate decisions

Select separately revisioned accepted ordering/correction evidence. Preserve the
original ContentVersion tuple, its order fields, PI-07 canonical command bytes,
receipt fingerprints and assignments exactly. Do not teach PI-07 replay to ignore
changed order. The existing pure CompareVersions behavior remains historical;
the catalog service returns an explicitly named effective-order view with its
own decision revision and support.

Each effective-order decision targets one Content and maps supported versions
to positions in one accepted namespace. Namespace identity is core-bound to a
source registration/contract scope, not merely equal source strings. Only
explicit sequence evidence is admitted in PI-08; opaque revisions/timestamps
remain observations, not sortable order. All positions and relation support
must belong to this Content. A later ordering correction uses expected aggregate
revision and explicit supersession; old decisions remain queryable.

No effective order is accepted implicitly from old stored order fields:
migration retains those fields as historical baseline evidence. Comparison
reports same only for identical version identity/facts; missing accepted order
is unknown, different active namespaces or equal positions for distinct versions
are conflict, supported distinct positions in one namespace give before/after.
A latest-version query succeeds only when the complete bounded candidate set has
a unique maximum in one conflict-free accepted namespace. Partial pages, unknown
order or any conflict cannot produce a latest winner. Sorting IDs for stable
output never determines authority or recency.

Title/model/article corrections use ResolveFact and keep identity. Correcting
an immutable fingerprint or Content ownership requires a new independently
allocated version plus a reviewed supersedes relation, with support and reason;
the old version remains queryable and the old assignment is unchanged. Reject
cycles and cross-Content supersession; mistaken cross-Content association needs
later identity reconciliation. Supersession is not latest order, automatic
media attachment, merge or assignment retargeting. PI-09 owns assignment change.

## D6: array admission and protected custom compatibility

Choose unresolved candidates for every legacy parallel array whose pairing
has not been established independently. Equal lengths, array index, file order,
custom API validation and a familiar provider label are insufficient evidence.
Unequal arrays are rejected with a safe structural report; equal unproven or
crossed arrays retain original coordinates and report ambiguous pairing.
Explicit pairs can be staged separately, with pair-specific source support.
Multiple pairs represent separate version candidates; they never become a
cross-product, single chosen version or implicit Set-member mapping.

No pinned legacy generator contract is approved here. A later source-specific
positional decoder requires PI-14 review and independent generator evidence.
TeddyBench offset conversion is likewise not admitted; preserve the raw value
with unsupported-normalization status until the adapter contract is proven.

Choose an explicit compatibility-unresolved outcome for conflicting custom/public
candidates. Custom-first is confined to the unchanged legacy service; PI-08 does
not copy its first-hit algorithm or replace any old endpoint. A future facade
may expose an explicitly reviewed preferred view while retaining alternatives,
but must define old-client status/body fixtures before writer transfer.
It must not convert an unrepresentable ambiguity into a successful scalar match.

Preserve protected custom provenance and all separate identities even for equal
title/model/pair/blob. Custom protection can be added on import; no PI-08 command
can remove it, delete/deduplicate a protected record or overwrite its correction
through refresh. Explicit authorized reviewed metadata correction is allowed,
with retained prior support and revision fencing. Missing protection evidence
does not prove an Original. Catalog operations cannot change Tag classification,
claim/ownership/auth, assigned model, source model, nocloud or assignment.
Set membership and member order require their own relations; tracks and array
positions are never members and a Set article never becomes a member model.

## Commands, results and transaction boundary

Names below fix semantic responsibilities, not Go signatures or public routes.
Ingest and review capabilities are separate handles installed by the trusted
composition root; import payloads cannot construct either privilege.

The missing registration implementation is tracked in the
[F01 registration contract](pi-08-f01-registration-contract.md). Its R2 decision
set is implementation-ready; individual implementation slices need fresh admission;
version supersession remains the separate F02 finding.

| Command/query | Preconditions and result |
| --- | --- |
| StageCatalogBatch | Allowed active source/adapter binding; bounded sanitized bytes. Atomically retain batch, candidates and dispositions. Exact batch/bytes replay returns original report; changed identity conflicts. No accepted facts. |
| RegisterContent | Review capability, expected absent identity, supported title and explicit source-record association; creates managed revision 1. Existing unmanaged content instead requires explicit adoption of its baseline evidence at expected revision. |
| RecordObservations | Existing subject and expected revision; immutable supported candidates become retained pending evidence only. |
| AcceptSupport / ResolveFact | Review capability, active registration, supported allowed field, current expected revision, complete same-subject support and reason. Persist decision, support, projection and revision atomically. |
| RegisterCatalogVersion | Review capability, expected Content revision, explicit supported ownership and pair, independent VersionID. Insert unbound immutable row plus decision; identical replay is a no-op, changed identity conflicts. |
| DecideVersionOrder / SupersedeVersion | Review capability and current expected Content revision; enforce D5 with retained support/history. Never touch PI-07 receipts, bindings or assignments. |
| GetContent / GetVersion / GetDecision / GetBatch | Bounded point queries under the selected owner; return accepted state, candidates/history references and provenance status, with explicit availability semantics. |
| FindVersionsByPair / FindVersionsByAudioID / ListVersions | Bounded complete-set resolution or revision-bound pages; no-match, unique, ambiguous, incomplete/limit or stale-cursor. No model fallback. |
| LookupCatalogCommand | Authorized readback of retained canonical command/result; absent, exact result or identity conflict. Required after uncertain commit. |

Failures distinguish invalid input, unauthorized issuer/operation, unsupported
policy/field/width, missing subject, identity/evidence/revision conflict, limit,
busy, canceled/deadline, schema unavailable and corrupt/unavailable storage.
Unknown lookup is not permission to create or choose a subject. Rejected writes
have no partial evidence/projection; commit/cleanup uncertainty is explicitly
indeterminate until command readback, not a promise of no effect. No automatic
retry, compensating delete, secret echo or raw storage cause in wrapped errors.

Reuse the lifecycle owner, one serialized connection and a distinct scoped
catalog transaction method; no SQL/driver types cross the application boundary.
Repositories are revoked after callback exit. All catalog writes/read pages
share restore fencing. No network/media I/O inside a catalog transaction.
Stage parsing occurs before the short transaction, with the operation deadline
already active. Check context immediately before commit. A restore invalidates
catalog cursors and selects only the verified current handle.

## Fixed internal resource limits

These are conservative acceptance bounds, not measured capacity or V3 constants.
All hard limits apply together; excess returns a typed failure without truncation.

| Resource | PI-08 hard limit |
| --- | --- |
| Active source registrations including retained revisions | 256; each opaque source/revision/record/principal reference at most 128 UTF-8 bytes, controls rejected |
| One offline batch | 1 MiB total input, at most 32 records, 64 KiB per record, depth 16, at most 256 object fields per record; no compression |
| One mutation | One Content subject, at most 64 new observations and 256 selected support references |
| Retained Content graph | 1024 versions, 4096 observations, 1024 decisions, 16384 cumulative support links and 8 MiB complete retained encoding |
| Text values | At most 4096 UTF-8 bytes per title/reason/value; narrower existing typed codecs still apply |
| Query | At most 128 entries and 1 MiB encoded response; stable ID order, explicit continuation, no successful silent truncation |
| Identity resolution | At most 1024 inspected candidates; overflow is incomplete/limit, never unique |
| Deadline | 10 seconds end-to-end including admission/parsing; each DB transaction at most 5 seconds including pool/busy wait; earlier caller deadline wins |
| Admission/cursor | One active catalog service operation; another gets busy. Cursor at most 256 bytes, bound to query, aggregate/catalog generation and owner generation; restore/restart or relevant writes make it stale. |

Charge retained raw snapshots, observations, decisions, indices of support and
canonical command/result bytes to the complete graph budget, including escaping.
Shared record bytes count against each referring subject's admission budget;
unattached batches remain subject to a separate fixed 64 MiB / 256-batch retained
staging budget. Accepted batches still count until later separately designed
retention; PI-08 has no purge/TTL. Check counts and overflow-safe encoded sizes
before copies/join materialization; bound persisted reads before reconstruction.
Exact-boundary/one-over tests must cover cumulative links and actual encoding.
Old rows exceeding new bounds become explicit unsupported/unmanaged query
results, never deleted or silently clipped; malformed persisted rows fail closed.

No unbounded aggregate export or scan is implicit. Exhausted retention stops new
ingestion while bounded existing reads remain available. Raising limits or adding
GC needs refinement. Uninterruptible kernel/storage operations may outlive a
deadline; do not release the owner while an operation can still commit.

## Additive migration and rollback

A2 appends the next explicitly bound migration after current version 4; it must
record the selected number/identity before implementation and preserve checksums
1..4. No destructive table rewrite or public V3 schema is selected by R.
Use additive provenance/source/batch/decision/support and revision structures,
referencing existing Content/version IDs with restrictive FKs. Keep media tables
and original commands untouched. Existing version rows support absent bindings;
the new query must not equate that absence with corruption.

Existing rows acquire mechanical baseline provenance only through a bounded,
idempotent adoption checkpoint. Queries label pre-adoption state unmanaged/
unreviewed; migration cannot invent upstream sources, review decisions or
custom classification. Guarded adoption snapshots original facts and freezes the
old Save path for that subject atomically. A historical PI-07 import may continue
creating media versions; without separate catalog acceptance their metadata is
reported as baseline/unreviewed. No bypass grants an accepted PI-08 label.

Use PI-05 verified pre-upgrade backup, checksum/ledger/integrity/FK verification
and the shared closed/fenced new-path exact-schema restore. A2/B2 prove current
and pre-PI-08 restore, retained original database and unchanged Tag/Content/
version/blob/receipt values. An older restored schema makes PI-08 commands
unavailable and is never silently upgraded by a read. Preserve media and orphan
bytes; database rollback does not trigger GC or rewrite assignments.

Rollback to a pre-upgrade snapshot omits later catalog decisions: retain the
post-upgrade database separately and reconcile/export admitted evidence before
any production rollback. This is development recovery evidence, not a zero-loss
production downgrade promise. No dual-write, down migration or legacy file
write-back. Documentation rollback is a focused revert of these four files.

## Deferred owners and acceptance boundary

| Stable deferral | Owner and required evidence before extension |
| --- | --- |
| PI08-D01 public vocabulary | PI-13: V3 schema, confidence/provenance wire vocabulary and fixtures; internal policy names are not a public freeze. |
| PI08-D02 full legacy import/projection | PI-14/PI-35: V1/custom pairing contracts, model rename/sidecar transaction behavior, TeddyBench conversion, all unsupported fields and ambiguity-compatible client fixtures before writer transfer. |
| PI08-D03 provider integration | PI-14/PI-32, auth boundary PI-12: MyTonies/NFC issuer/account scopes, safe credential isolation, retained locator contract and provider replay fixtures. |
| PI08-D04 identity/Set/review UX | PI-13/PI-15/PI-16/PI-17: cross-provider merges, Set cardinality/membership/order, fallback lookup and review/clear workflows; no automatic heuristic authority. |
| PI08-D05 assignment/operations | PI-09 and PI-34/PI-36/PI-37: explicit assignment change, production migration/ownership transfer, reconciliation and coordinated media/secret rollback. |

These deferrals are tracked under #25 and the linked roadmap owners; separate
delivery issues must reuse these IDs when published. None is permission for
PI-08 implementers to guess a policy. The [plan](pi-08-plan.md) maps all R1
questions to decisions and executable future tests. R accepts this design only;
A/B/T execution and the queryable-version milestone remain separate gates.
