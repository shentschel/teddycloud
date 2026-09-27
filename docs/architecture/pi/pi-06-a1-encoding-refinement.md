# PI-06/A1 internal encoding refinement

Status: bounded internal design accepted after independent parent review; implementation pending.
Inspected baseline: `8ac16a2`. No implementation or full A1/A acceptance.

The [contract](pi-06-tag-registry-contract.md), [plan](pi-06-plan.md),
[review](pi-06-review.md), [count checkpoint](pi-06-a1-counts-checkpoint.md),
[budget](pi-06-budget.md) and [routing](../task-model-routing.md) remain controlling.
The pre-dispatch override routes retained representation and encoding ambiguity
to Astra refinement; Sol implementation follows accepted decisions only. This
is a small, uncalibrated partial checkpoint, not an estimate for all remaining R.
Parent owns independent review, integration, issue publication and live admission;
its allowance and uncertainty reserve are not available to extend this work.

## Accepted decisions and boundary

Define a private, complete Tag representation and deterministic internal encoding
`TREG/1`. Its encoded length, not Go heap size or SQLite file size, is subject to
8388608 bytes. No caller-provided size is trusted. Encoding is an internal quota
and round-trip representation, not a public wire contract or an additional stored
JSON authority. Relational storage remains the contract's four-table design.
No legacy connector, PI12 secret/reference mutation, new owner or public DTO API.

Current code has identity-only `Tag`, four private state fields, observation and
decision-ID codecs, and allocation-free count gates. SQLite has only migration 1
for Content; no Tag loader exists. `evidence.Canonical` and `evidence.Accept`
copy/sort, so neither can precede byte preflight. Current service comparison
`byID != byUID` also requires replacement when Tag contains slices: use bounded
domain equality after validation, never compare slice backing pointers.

Extend the existing `domain/tag` aggregate, not `evidence.Fact` as a retained
aggregate (its getters clone). The following is the complete proposed logical
shape; private fields and read-only internal traversal preserve immutability.

| Part | Retained fields and invariants |
| --- | --- |
| Identity | Nonzero canonical `identity.TagID`, device-order eight-byte UID, positive signed-64 revision; rUID derived only. Registration revision 1, no history, four unknown states. |
| Four facts | In fixed order protocol_valid, claimed, cloud_auth, owned: state and active observation-ID set. Active IDs are unique, belong to this Tag/key and have Accepted review. Total active links at most observation count (4096), because an observation has exactly one key. |
| Observation entry | Existing complete `evidence.Observation` plus `introducedRevision`, identifying the registry mutation that first retained it. Preserve ID, all three Source strings, subject, key, boolean value, observedAt, confidence and review. Replays never change introducedRevision. |
| Decision entry | Existing `DecisionID`, fact key, expected revision, result revision, and selected observation-ID set. These are immutable explicit resolution records; ordinary observation recording adds no resolution record. Selection nonempty, unique, agreeing and accepted; result = expected + 1, with checked arithmetic. |
| History | All observations and all resolution decisions, including superseded selections and dissent. No separate duplicate history array, truncation, deduplication or GC. |

`introducedRevision` is required to distinguish accepted observations added after
a resolution from superseded observations. Without it, reloading the latest
selection cannot reconstruct current active support. This is persistence of
existing ordering semantics, not a new time-based winner rule. Values are 2 through
the current revision; decisions reference observations introduced no later than
their result revision. The addressed-key command may add observations and resolve
at the same revision. Applying that resolution selects its support, then only
accepted observations at strictly later revisions join automatically. B1 must
retain its existing atomic command/replay rules, including partial replay conflict.

Reconstruct active support per key from the latest decision by result revision
plus later accepted observations, or all accepted observations if no decision.
Unknown requires empty support; true/false require nonempty agreeing support;
conflict requires both boolean values. Validate stored/encoded projections against
this reconstruction. A prior decision's selected support remains valid historical
support even when later evidence disagrees. Distinct keys never share active IDs.
Resolution revision order is registry order, not timestamp/source priority.

Persist introduced revision on observation rows and the decision fields above;
derive active state on load, avoiding a second authority/table. These are proposed
future additive schema fields, not a migration delivered here. No extra creation,
decision or update timestamp is invented: the retained timestamp is observedAt.

## TREG/1 exact encoding

Use UTF-8 JSON arrays with exactly the positional grammar below. Emit no BOM,
whitespace or terminal newline. All brackets, commas, string quotes and escapes
count. Empty collections are `[]`, never `null`; no optional/omitted fields.

```text
tag = ["TREG",1,tagID,uid,revision,[fact,fact,fact,fact],[observation,...],[decision,...]]
fact = [state,[activeObservationID,...]]
observation = [observationID,introducedRevision,subject,key,value,
               sourceName,sourceRevision,sourceRecord,observedAt,confidence,review]
decision = [decisionID,key,expectedRevision,resultRevision,[selectedObservationID,...]]
```

The displayed line breaks are grammar notation only. UID encodes its existing
23-byte uppercase colon form. Subject repeats the actual 30-byte TagID in every
observation and is counted; do not replace it with an empty identity placeholder.
All ID strings are the exact existing 30-byte canonical codecs, including `dec_`.
Key numbers are protocol_valid=0, claimed=1, cloud_auth=2, owned=3. Boolean claim
strings `"false"`/`"true"` map to numeric 0/1; no other claim spelling accepted.
State numbers are unknown=0, true=1, false=2, conflict=3. Confidence numbers are
unknown=0, tentative=1, corroborated=2; review pending=0, accepted=1, disputed=2,
rejected=3. These are explicit version-1 mappings, not serialization of incidental
Go enum storage. Unsupported enum values/version fail, never default to unknown.

Revision integers use shortest base-10 positive ASCII without sign/leading zero;
maximum 9223372036854775807 is 19 bytes. Do not convert through float64. Observation
timestamps normalize with existing Round(0).UTC(), then render exactly
`YYYY-MM-DDTHH:mm:ss.nnnnnnnnnZ` (30 ASCII bytes before quotes), including nine
fractional digits. Accepted Tag-specific range: UTC years 0000 through 9999,
excluding the existing zero-time sentinel. Validate real calendar dates and
nanoseconds 0..999999999. Parent acceptance is limited to this Tag-specific representation; the generic
evidence constructor continues to accept wider Go times. Reject out-of-range
Tag input explicitly, never silently truncate it or change evidence globally.
Never silently truncate timestamps or introduce a UnixNano overflow boundary.

Arrays of observations and decisions sort by canonical ID bytes; support sets
sort by observation-ID bytes. Fact position is fixed. Sorting is after preflight;
length is independent of order. Duplicate IDs/links are semantic errors, not an
encoder's opportunity to coalesce them. Existing exact replay is still decided
by the application before constructing a proposed retained change.

String escape rule E is fixed, independent of library defaults:

- Validate UTF-8 and existing control rejection before encoding. Source name,
  revision and record are each nonempty and at most 128 UTF-8 bytes, not runes.
- Quote and backslash emit `\"` and `\\` (two bytes). ASCII `<`, `>` and `&`
  emit lowercase `\u003c`, `\u003e`, `\u0026` (six bytes).
- U+2028 and U+2029 emit `\u2028` and `\u2029` (six bytes each).
- Every other permitted rune emits its original UTF-8 bytes; slash is unescaped.
  Controls U+0000..001F and U+007F..009F are invalid, not escaped into validity.
- No Unicode normalization or invalid-UTF-8 replacement. Two surrounding quotes
  cost two bytes. IDs, UID and timestamps require only those surrounding quotes.

This explicit JSON convention matches useful existing Go JSON escape behavior
without making reflection, field names or future library defaults the contract.
An encoder may use a library only with parity tests for these exact rules.

Let Q(s)=2+E(s), D(r)=decimal digit count, and
A(lengths)=2+sum(lengths)+max(0,number-of-elements-1). Every production above
uses A; string elements use Q, integer enums use 1, revisions use D. In particular:

```text
fact length        = A(1, A(32 repeated active-count times))
observation length = A(32,D(introduced),32,1,1,Q(name),Q(sourceRev),Q(record),32,1,1)
decision length    = A(32,1,D(expected),D(result),A(32 repeated selected-count times))
complete length    = A(6,1,32,25,D(revision),A(four fact lengths),
                       A(observation lengths),A(decision lengths))
```

There is no omitted envelope/header/length prefix/checksum/padding. SQLite indexes,
page layout and Go slice headers are not part of TREG/1; their memory/storage costs
remain bounded separately by count and field gates. Active links and historical
decision links are both encoded and both charged in bytes. Only the latter use
the 16384 cumulative-link quota, including repeated links across decisions.

## Preflight before copies and before materialization

### Existing input memory

Caller input slices already occupy memory; the promise is no additional aggregate
copies before admission, not retroactively preventing caller allocations.

1. Check actual top-level retained/proposed counts before visiting members; check
   the independent 64 new-observation command bound. Walk actual decision support
   lengths with the existing overflow-safe count policy. Also bound each active
   set and its total. No clones, sorting, maps or calls to evidence.Accept yet.
2. Traverse retained plus proposed entries directly, using borrowed immutable
   views, not append/concatenate or copied getters. Compute the complete TREG/1
   length from actual field contents. Validate field lengths before rune scanning;
   reject malformed identity, enum, timestamp or source. Bound work by the counts.
   Include the proposed final active support, revision and retained history.
3. Only on success may semantic validation allocate bounded indices/support
   projections and clone/sort the accepted representation. Run existing evidence
   acceptance over selected support, not all superseded evidence. Publish a new
   immutable Tag only after full validation. Recheck encoder length on emission.

The borrowed view and counter live inside domain/tag; a metadata command can be
counted as base plus delta without inventing a new application DTO or accepting
an integer `encodedBytes`. To derive proposed active support before allocations,
use bounded passes over borrowed IDs/observations, with scratch indices only
after byte admission; no hidden clone in an accessor. Exact replay does not add
history; this design does not move replay behind a new count rule or infer replay
from equal source text. Incoming command count gating still applies independently.

Use signed int64 bounded counters. For each addition require
`0 <= total <= limit` and `0 <= add <= limit-total` before addition. For a product
require nonnegative operands and `a == 0 || b <= (limit-total)/a` first. Apply the
same rule to revision increments with MaxInt64. Avoid `n*width` in native int and
avoid SQL SUM over unchecked attacker-sized lengths. Convert to native int for
allocation only after <=8388608, valid on both 32- and 64-bit targets. Counters
are derived locally, never parameters callers can forge to bypass validation.

### SQLite load in one snapshot

All phases run in the same callback transaction through the existing lifecycle
owner and selected handle. No joined aggregate query, JSON aggregation, group
concatenation, `SELECT *` or read-all-then-check helper is allowed.

1. SQL scalar count preflight on base tables: observations <=4096, decisions
   <=4096, each decision's links <=4096, total retained links <=16384. Count
   bounded subqueries with LIMIT bound+1 to detect overflow, and stop on the
   first violating per-decision count; do not fetch result payloads. Missing Tag
   alone means not found. Orphan/wrong-Tag links are corrupt, not ignored by joins.
2. Before fetching any text/blob payload, query storage types and
   `length(CAST(field AS BLOB))` for all variable fields (including identifiers).
   Check exact ID/UID lengths, source 1..128 bytes, integer revision/enum domains,
   and timestamp width. SQL text length counts characters and stops at NUL; it
   is not the required source-byte check. This phase returns bounded numeric
   metadata only, rejecting oversized individual values before driver scanning.
3. Read one bounded base row at a time to validate actual strings and compute
   their exact escaped lengths. Check at most bound+1 rows defensively and close
   each cursor before the next query on the one-connection pool. Do not retain
   observation rows. Stream decision support IDs separately, never join source
   strings onto every support row. Reconstruct active support counts with bounded
   revision/key queries against the same snapshot; count the latest selection
   and later Accepted observations, checking union membership without silently
   deduplicating corrupt rows. No complete active/history slice is built yet.
4. Calculate the complete formula including derived state/active IDs, IDs of
   decisions, all revision widths and overhead. Source-byte totals alone cannot
   prove this size: 128 '<' bytes encode to 768 bytes. Only a successful exact
   preflight admits a second pass materializing the bounded complete aggregate.
5. Second pass uses the same row/field/count limits and counter, verifies parity,
   references, projections and domain constructors. Any invalid row, missing
   support, over-limit, mismatch or interrupted read returns unavailable storage
   (or preserved cancellation/deadline), never partial success or not found.

SQL intermediate execution and bounded driver row buffers are not zero-allocation
claims. The guarantee is that neither joined rows nor the complete aggregate is
materialized before successful count and exact-byte admission. Scanning already
joined rows, even discarding them quickly, does not meet this requirement. A raw
field check and payload read cannot race because they share the transaction.
SQL source-byte preflight is mandatory even when schema CHECK constraints exist.

### Writer and optional future internal reader

A1 requires exact counting/encoder parity and validation of actual domain fields,
not a new serialized-input endpoint or custom JSON parser. Relational load has
its own bounded phases above. The reader specification below is a conditional
future seam only: do not implement it unless a real internal consumer is refined
and admitted separately. No JSON authority is introduced into SQLite.


The internal decoder consumes at most limit+1 bytes, validating version and the
fixed grammar with maximum depth six. It must bound individual tokens before
allocation: source tokens <=770 encoded bytes including quotes, decoded source
<=128 bytes, ID tokens 32, UID 25, timestamp 32, revision tokens 19. Reject unknown
fields/shapes, duplicate/out-of-order IDs, noncanonical numbers/escapes, trailing
data and all semantic violations. Do not decode through generic `any` or an
unbounded JSON Unmarshal. For a forward-only stream, spool at most limit+1 bytes
to a bounded buffer before constructing the aggregate; the buffer is not a Tag
copy. Borrowed byte inputs need no spool. Validate/count first, construct second.

Use one grammar traversal with counting and bounded-writing sinks, so emitted
bytes and preflight cannot drift. Reader parity below applies only when a reader
is later implemented for an admitted consumer. The reader recomputes the same formula after
validating canonical tokens. Limit+1 is a detection allowance only: exactly the
limit succeeds, the next byte fails; never return a truncated successful Tag.
Reject unsupported format versions; future format changes require fresh budget
analysis and migration design, not silently reinterpreting version 1.

## Concrete boundary fixtures and acceptance tests

Per-field maxima can exceed 8 MiB under this format: three sources of 128 '<'
bytes each give 2304 escaped source bytes per observation; 4096 observations
alone contribute 9437184 source bytes before quotes, identity or other overhead.
They are valid UTF-8 and contain no forbidden controls. Thus aggregate rejection
is real, not an invented caller-size test. An unescaped/binary format would have
a different reachable maximum and cannot reuse these exact-limit fixtures.

Construct a reachable history with synthetic canonical IDs: 4096 Accepted true
observations for protocol_valid, one per command at introduced revisions 2..4097.
Then 4096 explicit resolutions at result revisions 4098..8193. The first selects
six observations, the next 4094 select three each (alternate different agreeing
sets), and the last selects all 4096. Total retained links =
6 + 4094*3 + 4096 = 16384; active links = 4096; other keys remain unknown.
Expected revision is result-1. All observedAt values are
`2000-01-01T00:00:00.000000001Z`. Source triples may repeat; observation IDs may
not. Different resolution IDs and changing selections are not same-command replay.

Start with 128 '<' bytes in each source string. Determine its exact length M
using the formula (and assert it independently using the actual encoder once
implemented). To target B=8388608, replace k '<' bytes with 'a', removing 5 bytes
each, and remove r '<' bytes, removing 6 each, where r=(M-B) mod 5 in 0..4 and
k=(M-B-6*r)/5. Spread replacements over fields; remove at most one byte from
each of r separate fields so sources remain nonempty. This yields exactly B;
use B+1 in the same construction for a distinct semantically valid one-over
candidate. Assert k>=0 and enough available source bytes, and independently
validate all field/count/transition invariants before invoking byte admission.
No padding, secret field, arbitrary boolean text, invalid revision or timestamp
extension is allowed just to reach a desired length. Maximum 19-digit revisions
are separate scalar/overflow fixtures, not fabricated reachable history here.

The following are required future table tests, not passing tests in this change:

| Named test | Required rows/evidence |
| --- | --- |
| `TestTagEncodingCompleteRepresentation` | Nonzero TagID and zero/nonzero UID; revision; all four independent states; full observation fields; decision IDs; active and superseded support; exact golden bytes and round-trip. Empty/invalid Tag must fail. |
| `TestTagEncodingExactBoundary` | Construct the history above at B and B+1; accept exactly B, reject B+1 before clone/materialization; separately reject maximal escaped source fixture although every field/count passes. |
| `TestTagEncodingSourceEscapes` | ASCII, quote, backslash, '<>&', slash, U+2028/2029, multibyte UTF-8 at 128/129 bytes, mixed worst case; reject controls, NUL and invalid UTF-8. Verify 770-byte maximum quoted source token. |
| `TestTagEncodingHistoryCounts` | 4096/4097 observations and decisions, 4096/4097 per-decision links, 16384/16385 retained links, 64/65 command observations; active counts separate; repeated historical references count repeatedly; invalid duplicate/cross-key active IDs fail. |
| `TestTagEncodingPreflightOrder` | Existing count gates allocate zero with inputs built outside measurement; oversized counts do not visit members. Byte-over inputs reach no clone/sort/evidence.Accept/aggregate builder hook. Caller-supplied or cached smaller byte totals cannot authorize a changed payload. |
| `TestTagEncodingCheckedArithmetic` | Table helpers with 0, negative, limit, limit+1, MaxInt32, MaxInt64, sum/product overflow and revision overflow; no huge allocations. Run on amd64 and 386, distinguish compile-only from executed evidence. |
| `TestTagEncodingParity` | Count == emitted length for golden, permuted-input, full-history, exact-limit and worst-text fixtures; fuzz bounded domain inputs. Reader count/invalid-token parity is deferred with the optional reader, not A1 scope. |
| `TestTagEncodingTimestampRevision` | Nanosecond precision, UTC offset normalization, zero-time, proposed min/max years and outside range; shortest decimal, 19-digit scalar maximum, MaxInt64+1 and checked expected/result relation. |
| `TestTagRepositoryPreflightOrder` | Instrument phase calls: counts reject before payload queries; raw source byte overflow before Scan of text; byte overflow before full row builder. No joined materialization query; validate same-snapshot behavior and second-pass parity. |
| `TestTagRepositoryInvalidEncoding` | Oversized persisted sources/history, wrong SQLite types, invalid IDs including dec_, enums, timestamps, orphan/cross-Tag support and inconsistent projection all unavailable with no partial successful return. |

## Parent acceptance and runnable handoff

Parent review accepts the private retained fields, version-1 counting/encoding
rules, introduced-revision reconstruction and Tag-specific timestamp range.
Q1 is resolved: UTC years 0000..9999 with explicit rejection outside the range;
generic evidence remains unchanged. Existing user/legacy data is not migrated.
A different timestamp requirement needs a new narrow refinement, not truncation.
No B1 transition, automatic dedup/replay rule or secret storage changes.
The optional reader is outside current A1 scope until it has an actual consumer.

Independent arithmetic yields M=10816539 bytes for the maximal escaped fixture.
For exactly 8388608 bytes, 485585 replacements and one removal suffice; for
8388609 bytes, 485586 replacements and zero removals suffice. Both use fewer
than the available 1572864 source bytes and preserve nonempty fields. This is
reviewed design arithmetic, not executed encoding or transition evidence.
Implementation must prove actual encoder and aggregate parity before acceptance.

After acceptance, route bounded implementation to Sol, with files relative to
`next/backend`:

- `internal/domain/tag/registry.go`, proposed `retained.go`, `encoding.go` and
  matching tests: complete immutable representation, borrowed traversal,
  preflight, encoder and bounded equality. Reader implementation is deferred. Reuse `history_limits.go`,
  `decision_id.go`, `uid.go`, `identity` and `evidence`; no evidence policy rewrite.
- `internal/application/tagregistry/service.go` and tests: replace comparable-Tag
  assumptions, validate complete returned aggregates. Later B1 `metadata.go` and
  errors/ports integrate the accepted TagChange/CAS seam and error mapping only
  when metadata behavior is implemented, not placeholder new DTO APIs in A1.
- A2/B1 `internal/adapters/sqlite/tag_repository.go`, metadata tests, additive
  `schema.go` migrations and `lifecycle_owner.go` integration: scalar/raw-byte
  preflight, bounded base-row passes and transaction snapshot guarantees. Preserve
  Content behavior and existing migration checksums. FK recovery remains gated
  by #18; this checkpoint supplies no substitute evidence.

Documentation validation from checkout root:

```sh
python3 scripts/check_architecture_docs.py
git diff --no-index --check /dev/null docs/architecture/pi/pi-06-a1-encoding-refinement.md
git status --short
```

Future implementation validation from `next` includes the domain/tag,
application/tagregistry, adapters/sqlite and architecture package tests with the
named targets above confirmed to execute. Run native and supported 386 execution;
record unavailable execution honestly. This document neither runs those future
tests nor completes A1, A2, B, T, #18 or the transactional Tag milestone.
