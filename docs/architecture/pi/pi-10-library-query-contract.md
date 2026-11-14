# PI-10 library query and pagination contract

Status: complete R decision set for parent review; implementation and milestone
acceptance remain pending. Internal profile `pi10-library/1`.
Request `pi10-r-library-query-pagination-20261114`; evidence basis
`b75799cf767b00c5717d7ad20fdd8f42f61266d6`.
The [plan](pi-10-plan.md) owns delivery and proof; the
[budget](pi-10-budget.md) owns integrated admission. MUST/REJECT below describe
new PI-10 requirements, not capabilities already delivered by PI-08 or PI-09.

## E1: observed baseline and evidence limits

| Source at the basis commit | Observed fact; not a PI-10 performance claim |
| --- | --- |
| [Catalog queries](../../../next/backend/internal/application/catalog/queries.go), `QueryKind`, `PageRequest`, `QueryResult`, `validateQueryResult` | Six kinds; explicit Limit/Cursor/ExpectedRevision; ContentID then VersionID order; bounded Entries/Next/Inspected/EncodedBytes. No Library query, title search or principal ownership filter. |
| [Catalog ports](../../../next/backend/internal/application/catalog/ports.go), `CursorBinding` | Complete typed query, continuation IDs, catalog/aggregate and owner generations; restart/restore/relevant writes invalidate. |
| [SQLite catalog repository](../../../next/backend/internal/adapters/sqlite/catalog_repository.go), `catalogFind`, `catalogContent`, `catalogVersions`, `catalogAnnotations` | Global find first bounds the entire version population at 1024, validates and reconstructs Contents, then filters. Scoped queries reconstruct the owning aggregate. Existing query code is an integrity oracle, not an indexed large-library implementation. |
| Same repository, `catalogPageSize`, `catalogCursorMAC`, `catalogCursorAfter` | Actual JSON response accounting including escaping; authenticated query-bound cursor; snapshot digest and connection change counters. A maximum aggregate revision alone is not a database revision. |
| [Catalog query tests](../../../next/backend/internal/adapters/sqlite/catalog_query_test.go), `TestCatalogRevisionBoundCursor`, `TestCatalogGlobalFindCursor`, `TestCatalogQueryBounds` | Existing fixtures cover typed-query changes, tampering, same-value ABA, external writes and generation invalidation, plus 128/129 entries, 256/257 cursor bytes and 1024/1025 candidates. Reading a test is not executing it. |
| [Catalog limits](../../../next/backend/internal/domain/catalog/limits.go) and [errors](../../../next/backend/internal/application/catalog/errors.go) | 128 entries, 1 MiB response, 256-byte cursor, 1024 resolution candidates; 10-second operation and 5-second transaction ceilings. Typed invalid/limit/stale/unsupported/unavailable/contention outcomes exist. |
| [Schema](../../../next/backend/internal/adapters/sqlite/schema.go) | Stored Content/version, evidence, review/order and supersession relations are authoritative. No PI-10 index is present. R changes no migration or checksum. |

Additional reads have concrete dependencies: [catalog transaction](../../../next/backend/internal/adapters/sqlite/catalog_transaction.go)
and [lifecycle owner](../../../next/backend/internal/adapters/sqlite/lifecycle_owner.go)
establish callback, restore and connection fences; [database](../../../next/backend/internal/adapters/sqlite/database.go)
establishes the single-connection pool. [PI-08 policy D3-D6](pi-08-catalog-policy.md#d3-explicit-acceptance-no-automatic-source-precedence),
[F02 query semantics](pi-08-f02-supersession-contract.md#query-assignment-and-reconstruction-semantics)
and [PI-09 D1/D2/D5](pi-09-assignment-policy.md#d1-authoritative-terms-are-separate-dimensions)
constrain projections and authority. These reads authorize no edits to those files.

## D1: library membership, identity and projection

A Library is the local catalog's **version inventory**, scoped to one active
LifecycleOwner/database incarnation. One item is the immutable pair
`(ContentID, ContentVersionID)`. Equal title, fingerprint, AudioID, BlobID or
source does not merge items. A version without media belongs; a Content with
zero versions has no inventory item and remains available through GetContent.
All retained versions, including superseded and unmanaged versions, belong.
No default latest, current-only, playable-only, assigned-only or custom-first rule.
The Library is not a filesystem inventory, household collection or Tag list.

Each item is a compact, revision-labelled summary with exactly these fields:

- ContentID, VersionID, immutable AudioID/hash tuple, Content aggregate revision;
- CatalogReviewStatus and ProtectedCustom, preserved independently;
- stored baseline title and optional stored model/article, labelled baseline;
- Title/Model/Article summary: existing unknown/accepted/conflict status and a
  scalar only for accepted. Conflict carries no selected scalar; full alternatives,
  support, receipts and history remain in the existing catalog detail APIs;
- existing Supersession annotation, including unavailable/current/superseded and
  its revision/edge IDs; no traversal as an assignment or playback redirect;
- stored media relation only: catalog-only or bound-unchecked. No probe of bytes,
  availability upgrade or reuse of an old verification as a current claim.

The title key uses only the accepted reviewed title. Unknown/conflicted title
has no scalar key, even if baseline text exists. Baseline is visible context,
never an accepted fallback. Summary omission of evidence arrays is an explicit
projection, not evidence loss or a change to the PI-08 detail contract. A detail
read at a newer revision must be labelled newer, never spliced into the old page.

## D2: query surface, ownership and visibility

Internal application operation `ListLibrary(Query, Page, ReadScope)` returns a
page. Names specify semantic fields, not public Go/HTTP/SDK/V3 signatures.

| Field | Normative admitted values |
| --- | --- |
| OwnerContentID | Absent = all Content owners; present = one canonical nonzero ContentID. Exact version-to-Content ownership equality, not a principal or physical Tag claim. |
| Versions | `all` (default), `current` (validated terminal/isolated managed versions only). Unavailable/unmanaged annotations never match current. |
| Search | Absent or a normalized title prefix, per D3. No full-text, token, substring, fuzzy, wildcard, model/article or AudioID search in this profile. |
| Sort | `identity-asc` (default), `title-asc`, `title-desc`. Search requires title-asc or title-desc; search with identity-asc is Unsupported, not an expensive fallback sort. |
| Page | Mandatory Limit, optional Cursor and ExpectedSnapshot as D4. |
| ReadScope | Core-issued, callback-scoped permission to read the entire local catalog, bound to owner incarnation and authorization generation. Cannot be constructed from request text. |

These are the complete filters; no hidden booleans, arbitrary sort fields, SQL,
OFFSET, count-all or client-selected projection. All specified filters intersect.
A well-formed unknown OwnerContentID produces an empty complete page. Duplicate
or unknown fields at a future transport boundary reject rather than disappear.
Absent defaults canonicalize before cursor binding. A present zero/malformed ID,
unknown enum, wrong field type or invalid UTF-8 is InvalidInput.

**Ownership is structural in PI-10.** OwnerContentID implements the already
established ContentVersion ownership relation. It does not mean account ownership,
license, acquisition, Tag `owned`, custom protection or assignment. Personal
`mine`/`shared`/principal-owner filters are Unsupported. This is a deliberate
bounded decision, not permission to infer an owner until PI-12 exists.

Visibility in this profile is all-or-denied: the trusted local catalog reader can
see every inventory item, including protected-custom and unreviewed rows; other
callers receive Unauthorized before storage or cursor inspection. No anonymous
or public access follows from public-source provenance. Revoke scope on callback
exit, owner close/restore or authorization-generation change; recheck before
publishing the response. Partial ACL filtering is not supported and must never
be applied after pagination. PI-12 owns any future principal/row policy.

A list always remains a list: one item on a page is not uniqueness evidence.
Equal fingerprints retain separate items across owners and supersession chains.
Unknown/conflicted editorial facts remain explicit. A corrupt ownership relation,
missing support or malformed supersession graph is Unavailable, not no-match or
an editorial conflict. Listing never chooses or changes an Assignment target.

## D3: normalization and total ordering

Choose the deliberately small, frozen `ascii-prefix/1` profile; no locale or
SQLite NOCASE dependency, Unicode database upgrade, transliteration or implicit
NFC/NFKC equivalence. First validate UTF-8. Reject U+0000..U+001F and U+007F,
including tabs/newlines. Trim leading/trailing U+0020, collapse repeated U+0020,
and map ASCII A..Z to a..z; preserve every other code point exactly.
Apply the same function to accepted titles and search inputs. Stored source text
is never rewritten. An empty accepted normalized title has no title key.

Search raw UTF-8 input and normalized prefix are each <=256 bytes; overflow is
LimitExceeded before copying. Present empty/space-only search canonicalizes to
absent. Other whitespace, accents and combining sequences remain literal.
Examples: `  WOOZLE  Test ` -> `woozle test`; `Ä` differs from `ä`, and composed
`é` differs from `e` plus U+0301. `%`, `_` and backslash are ordinary characters;
use a bound binary range, not unescaped LIKE. One code point is a valid prefix.
Search matches only accepted nonempty normalized title keys; conflict alternatives
and baseline text are not searchable accepted titles. Multiword input is one
literal prefix. These limits/semantics are frozen until a separately refined
profile version supplies migration and cursor invalidation evidence.

Order is a total bytewise lexicographic order, independent of insertion order:

| Sort | Exact tuple and directions |
| --- | --- |
| identity-asc | `(ContentID ASC, VersionID ASC)` using canonical ASCII ID bytes |
| title-asc | `(missingTitle ASC, normalizedTitle ASC, ContentID ASC, VersionID ASC)` |
| title-desc | `(missingTitle ASC, normalizedTitle DESC, ContentID ASC, VersionID ASC)` |

missingTitle is 0 for a nonempty accepted key and 1 otherwise; missing rows sort
last in both title directions and use empty key bytes. No relevance score,
wall-clock, physical rowid or fingerprint breaks ties. Filtering occurs before
ordering and the keyset boundary uses the exact comparator, including mixed
ASC/DESC directions. Normalization collisions are valid and resolved by the IDs.

## D4: snapshot and cursor protocol

Each page reads one complete immutable derived-index generation. Snapshot is an
opaque 32-byte token for `(profile, source epoch, index generation, owner
incarnation)`; it is not a Content revision, MAX(revision), timestamp or content
hash alone. Rebuilding even identical data creates a new generation. Page item
revisions describe the source state from which that generation was validated.
The external token spelling is exactly 43 unpadded base64url bytes for those
32 bytes; wrong shape is InvalidInput and an over-43-byte input is LimitExceeded
before decoding. No long-lived transaction or server-side per-client cursor
session is retained.

First page: empty Cursor, ExpectedSnapshot absent or an exact current token.
Continuation: both nonempty Cursor and ExpectedSnapshot are mandatory. Supplying
only a Cursor is InvalidInput; a valid-shaped but different/expired snapshot is
StaleCursor. An initial exact ExpectedSnapshot pins a retry to that generation.
A replay with the same canonical query/token returns the same items and Next
while the generation and authority are valid. A first-page retry without a token
may observe a new snapshot. Relevant writes invalidate, rather than silently
continue an historical snapshot; restart the listing explicitly at page one.

Cursor is opaque, canonical unpadded base64url, at most **256 encoded bytes**.
It authenticates profile, canonical filter/sort/Limit, read-scope binding and
authorization generation, Snapshot, owner incarnation and last emitted identity.
Use HMAC-SHA-256 with an owner-incarnation random secret and domain separation
`pi10-library-cursor/1`; compare tags in constant time. Payload contains fixed-size
version, Snapshot, last ContentID/VersionID and query/scope digest; the title key
is recovered by indexed identity lookup within the same immutable generation,
not embedded as up to 4096 title bytes. No SQL, file path or credential in tokens.
The exact binary envelope is `version=1` (one byte), Snapshot (32 raw bytes),
last ContentID (30 canonical ASCII bytes), last VersionID (30 canonical ASCII
bytes), query/scope digest (32 bytes), then MAC (32 bytes over the domain string
followed by all preceding envelope bytes): **157 raw bytes, 210 encoded bytes**.
No padding, extension bytes or alternate encodings. A must supply golden literals
for this selected encoding, not choose a different wire layout.

The query/scope digest is SHA-256 of length-prefixed UTF-8 profile
`pi10-library/1`, OwnerContentID (empty when absent), Versions (`all`/`current`),
Sort, normalized Search (empty when absent), followed by Limit (uint64 big-endian),
ReadScopeID (32 core-issued random bytes), authorization generation (uint64
big-endian) and owner incarnation (32 random bytes). Each text prefix is its
uint64 big-endian byte length. No language map order or delimiter ambiguity.
ReadScopeID identifies a renewable authorized scope across callbacks; each use
still receives a newly callback-scoped handle. Scope renewal changes that ID.
The 256-byte guard is defensive admission, not a claim that valid 256-byte tokens
are constructible: 256 malformed bytes reject stale; 257 reject limit.

Binding includes canonical defaults and normalized Search, not raw equivalent
spellings. Changed Limit, OwnerContentID, Versions, Sort, non-equivalent Search,
profile, caller scope or another database rejects as StaleCursor after successful
authorization. Bad encoding/version/length/MAC/unknown last identity also rejects
as StaleCursor without exposing which test failed. More than 256 input bytes is
LimitExceeded before decoding. Cursors from PI-08/PI-09 are never accepted.
Tokens authenticate continuity; they grant no permission and provide no secrecy.

The source epoch must detect same-connection writes, external connection commits,
unmanaged Save, review/order/registration/import/supersession, source revocation
and media binding changes, including same-value ABA. Conservative invalidation on
any database write is permitted and selected for profile 1. A digest comparison
alone is forbidden. A combines stable physical-connection incarnation with
`total_changes()` and `PRAGMA data_version`, sampled on that same connection
around a source read transaction; a replacement connection invalidates before
counter comparison. Counter regression/overflow or uncertain cleanup invalidates.
Do not compare data_version values from different connections. Index writes use a
separate private in-memory database and cannot mask source writes in the counters.

Check epoch before and after the source read transaction and again before response
release under the lifecycle fence. An external commit racing the final check may
linearize after that successful read; the next request must detect it. A changed
epoch during preparation or read discards the result; no automatic service retry.
Restore, close/reopen, process restart, connection replacement and index rebuild
invalidate all old tokens even if schema, values and revisions look identical.
Cross-owner ABA must not be defeated by memory-address reuse: fresh random owner
incarnation/secret and explicit physical-connection generation are required.

## D5: pages, bytes and typed failures

Limit is **1..128**, explicit and never silently clamped. Zero is InvalidInput;
129 or arithmetic overflow is LimitExceeded. Response has `Items`, `Snapshot`,
`Complete`, `Next`, `Inspected`, `EncodedBytes`. Complete means no further matching
item in this generation, not uniqueness, conflict resolution or trusted playback.
Empty match => Items empty, Complete true, Next empty. A nonfinal page has at least
one item and a nonempty Next; Complete is equivalent to Next being empty.

Read ordered matching candidates with one lookahead. Return the largest prefix
fitting Limit and **1 MiB actual encoded response**, including summary fields,
UTF-8/escaping, snapshot, Next and all envelope fields. Never truncate an item,
scalar, token or evidence value. If the first candidate cannot fit, LimitExceeded
with no Items/Next. After a byte-shortened page, continue strictly after the last
emitted item, never after the inspected/lookahead item. No successful empty
nonfinal page. Arithmetic and input bounds are checked before allocation.

Internal response accounting is canonical JSON UTF-8: fixed field order as listed
above, no whitespace, explicit empty arrays/strings and null absent scalars,
canonical ID/token strings, integer decimal values, standard JSON string escaping
without optional HTML or non-ASCII escaping. Each Items object has this exact key order: ContentID, VersionID, AudioID,
Hash, ContentRevision, Review, ProtectedCustom, Baseline, Title, Model, Article,
Supersession, Media. Baseline keys are Title, Model, Article; each reviewed-field
summary has Status, Value. Supersession keys are Status, Revision, Predecessor,
Successor, Terminal, IncomingCommand, OutgoingCommand. Reuse existing domain
numeric enum values; Hash is lowercase canonical hex; absent IDs/optional
values are null. Required title baseline remains a string (including empty).
Boolean values are JSON true/false. Snapshot is canonical unpadded base64url.
A supplies exact golden fixtures for this encoding. EncodedBytes is the length
of that complete encoding including its own decimal representation, solved to
a fixed point.
These are internal accounting bytes, not a public transport format. A transport
with another envelope must account its own limit independently.

Inspected counts index candidate rows consumed (including lookahead), not an
invented source-scan or uniqueness certificate. Physical SQL row visits, statement
counts and build work are separate T metrics. Successful query inspection is
<=Limit+1; a larger implementation scan cannot be hidden by relabelling this field.
The PI-08 1024 global-resolution limit stays unchanged; it does not cap the number
of Library items or become a larger PI-08 ambiguity search allowance.

| Condition | Typed Library outcome; no partial page on error |
| --- | --- |
| Invalid shape, zero Limit, invalid UTF-8/ID | InvalidInput |
| Input/response/index capacity exceeded | LimitExceeded |
| Missing/revoked scope | Unauthorized |
| Unsupported schema/profile/filter or personal ownership query | Unsupported |
| Invalidated, forged, cross-query or cross-owner token | StaleCursor |
| No completed fresh index on first-page request | IndexNotReady; no implicit build or stale fallback |
| Detected corrupt source/projection, missing references, storage failure | Unavailable; poison the generation |
| Gate/connection contention or another active operation | Busy |
| Cancellation or earlier deadline | Canceled / DeadlineExceeded, preserving cause category |

Validation order: authorization, bounded shape validation, supported-profile
check, cursor authentication, freshness/readiness, query. An authenticated old
token with a missing generation is StaleCursor, not IndexNotReady. Cancellation
may preempt work at any step. Existing PI-08 error values/behavior remain intact;
A defines a separate Library error mapping with redacted messages and no raw SQL,
search strings, tokens or principal details. Operation <=10 seconds and each
source/index transaction <=5 seconds, including contention; earlier caller wins.
No weakened timeout or implicit retry to make a performance test pass.

For a fixed generation and query, concatenating all successful pages equals the
full filtered sorted inventory **exactly once**, for every admitted Limit and
byte boundary. Mutations can force restart; no claim of gap-free enumeration
across different generations. Consumers must discard the old accumulated listing
when restarting, not append a new snapshot to it.

## D6: indexed implementation and integrity boundary

Select a **rebuildable private in-memory SQLite read index**, owned by the same
LifecycleOwner, separate from the authoritative database. No persistent migration,
new authority, source write trigger, background writer transfer or sidecar is
needed. Queries never build it. A provides explicit internal prepare/advance
operations; readiness is observable, not inferred from a successful open.
Close/restore discards the index. Failed/canceled build publishes nothing.
Supported source schemas are exactly clean v9 and v10 at this baseline; older,
dirty or unknown future schemas are Unsupported without upgrade. Existing
catalog behavior on other schemas remains unchanged. A future schema requires
explicit compatibility admission, not numeric greater-than acceptance.

Preparation reads all source Contents in identity order in bounded chunks, each
validating complete aggregate provenance, ownership, order and supersession using
PI-08 reconstruction rules, including relations outside a requested future page.
It does not call global Find repeatedly or manufacture accepted state from SQL
projection columns. An initial integrity/FK check plus complete aggregate
validation establishes the generation; no unchecked subset can be published.
Reuse only within the unchanged source epoch. A changed epoch between chunks
invalidates all staged work. No stale positive or negative result is served while
refresh is required. All integrity costs are charged to build/refresh evidence.
This new reuse rule applies only to the Library index, not existing query or
Assignment integrity checks. Unauthorized out-of-band file modification is not a
supported writer; detected damage fails closed, never silently repaired.

One builder per owner, at most 128 Contents per advance and at most the existing
1024 versions per Content, with 10-second operation/5-second transaction ceilings.
A chunk that cannot finish fails without publishing; caller may resume previously
completed chunks only if the epoch is still exact. Source admission is <=100,000
Contents and <=100,000 version items, <=128 MiB canonical summary/key staging
bytes, and <=256 MiB index database pages including indices. Test caps before
insertion, expose typed capacity failure and discard failed staging. These are
new PI-10 bounds, not PI-08 retained-budget increases or hardware RAM promises.
No automatic eviction, trimming or loss of source evidence. Old and new index
storage must not coexist above that combined bound; invalidation discards old
storage before preparing replacement. Refresh is resumable work, not an
unbounded transaction or promise that 100,000 rich aggregates build in 10 seconds.

A owns identity-key indexed queries, snapshot/readiness and all lifecycle fences.
B adds normalized title indices and owner/current predicates to the same complete
pipeline. For every supported query shape choose an equality partition followed
by the exact ordered key: `(versionLane, ownerPartition, orderKey)`.
versionLane contains all and, when eligible, current; ownerPartition contains
all-owners and the exact owner. At most four membership rows per inventory item;
one request selects exactly one partition, so this does not duplicate results.
Use three covering ordered indices for identity-asc, title-asc and title-desc,
plus exact item-identity lookup. Title prefix is a binary lower/upper range on the
title indices; compute the exclusive lexicographic prefix successor correctly,
including no-successor upper-bound handling. Bind all values as SQL parameters.
No OFFSET, global sort, per-item detail fetch, per-item service call or full source
reconstruction on a ready-page request. A's private index schema stays internal;
B can rebuild it without altering durable schema or old codecs.

## D7: performance and N+1 acceptance

N+1 means a page's database/service work grows by one or more requests per returned
item, owner, version, support row or annotation after a discovery request. Hiding
those requests in helpers/caches or using one correlated SQL statement with an
unbounded repeated scan is still a failure. Build-time aggregate validation is
separate measured work, never presented as page performance.

Each ready page uses <=12 SQL statements total, counting source epoch/schema and
fence checks, BEGIN/end, index identity lookup, candidate selection and any other
statement on either database; no media/network I/O. Candidate visits <=Limit+1,
identity lookup <=1, scalar metadata rows <=8; covering range seeks may incur
logarithmic B-tree navigation, which is reported separately from data-row visits.
Statement counts must remain constant for Limits 1, 16 and 128. No full source
scan or per-item reconstruction is allowed in the ready path. Preflight invalid-shape
input/unauthorized calls must perform zero SQL.

T must record actual SQL traces/counters and EXPLAIN QUERY PLAN for every supported
filter/sort shape, demonstrating bounded indexed seeks and no temporary full sort.
Inspected alone and EXPLAIN alone are insufficient. Row-visit instrumentation or
an equivalently checked VM-step/scaling experiment must include lookups and
correlated work; any unobservable metric is an explicit gate failure.

Profile 1k/10k/100k valid items across many small Contents, plus separately the
maximum valid per-Content history/evidence fixture. Include broad/rare/no-match
prefixes, one owner/many owners, current/all, equal titles/fingerprints,
unknown/conflicted titles, byte-short pages, early/middle/final pages and repeated
same-query runs. Compare against independently generated sorted fixture truth.
Measure cold prepare, each advance, refresh after write, first ready page and
warm continuation separately; report fixture construction separately as well.
Record runner/CPU/RAM/OS, Go/SQLite versions, schema/profile, dataset cardinalities,
cache state, command, sample count, p50/p95/max, allocation/peak memory, SQL count,
row visits, timeouts and failures. Use >=30 measured ready requests per case after
5 separately reported warm-ups; do not hide failures in percentile calculations.

No universal millisecond or Raspberry-Pi latency assertion is made. Acceptance
requires the structural query/scan bounds, reproducible raw timings and reviewer
explanation of scaling; a >2x p95 regression on the same runner/fixture versus B
must be investigated and fixed or explicitly dispositioned before T acceptance.
A hardware service-level objective belongs to a later target-qualified job.
The milestone means stable indexed software pages with this evidence, not a
production or hardware latency guarantee. T cannot waive the structural bounds,
integrity, memory caps or deadlines as a latency tradeoff.

## D8: compatibility and explicitly later owners

| Boundary | Preserved rule / owner and entry criterion |
| --- | --- |
| PI-06 Tags | No UID/rUID, owned/auth/classification inference or registry mutation. Tag-based personal collection filters require PI-12 authorization plus an accepted PI-06/09 cross-owner query contract before a later slice. |
| PI-07 TAF | No file/network open, media verification, import, deletion, range read or receipt change. Later availability filtering requires a separately bounded, revision-labelled contentstore query contract. |
| PI-08 Catalog/Version/F02 | Existing query kinds, 1024 candidate limit, provenance checks, conflict semantics, custom protection and all immutable IDs stay intact. Current is not latest; old versions remain separately queryable. |
| PI-09 Assignment/Classification | No assignment lookup inside page construction, target selection/retarget, physical classification rule or default original/custom label. Future assignment-aware filters require explicit time/knowledge/dependency binding accepted by the Assignment owner. |
| PI-12 authorization | Owns personal/household/row visibility and external principal grants. Entry: reviewed threat/ACL/revocation contract and pre-pagination authorization index; Until then only trusted local all-catalog ReadScope. |
| PI-13/public API owner | Owns V3 vocabulary, routes, DTO/SDK and public cursor exposure. Entry: accepted internal A/B/T plus public compatibility/security review. No public name is frozen here. |
| Hardware/deployment and production-migration owners | Qualify actual hardware, concurrency/workload SLO, deployment/rebuild scheduling, backup and production rollout separately. Entry: accepted software gates and an explicit target/rollback plan. No persistent migration or production acceptance is supplied by R. |
| PI-10 A/B/T | Own exact codec fixtures, index implementation, traces and measurements as specified here; these are implementation proofs, not unresolved semantic choices. Any failure to implement this contract requires a reviewed finding and replan before scope expansion. |

No unresolved semantic choice is delegated to an implementer. Future features
above are excluded with an owner and entry gate; they do not block this bounded R.
