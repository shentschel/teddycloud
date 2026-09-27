# PI-06 Tag Registry contract

Status: R design independently accepted; implementation pending.
Baseline: `2aedabbd141adb5aaf02746ae041eb7e5596317c`.

## Existing rules and scope

This contract extends the accepted [PI-04 domain rules](pi-04-plan.md) and
[PI-05 persistence boundary](pi-05-review.md), under
[ADR-0002](../adr/0002-technology-and-repository-topology.md) and
[ADR-0003](../adr/0003-persistence-and-projection-recovery.md).
It does not reopen their completed implementation.

Existing code is authoritative: `internal/domain/identity/id.go`,
`internal/domain/tag/uid.go`, `internal/domain/evidence/evidence.go`,
`internal/application/catalog/{ports,errors}.go` and
`internal/adapters/sqlite/{schema,content_repository,lifecycle_owner}.go`
under `next/backend`. The Tag package currently implements codecs, not a
persistent registry. The current application schema has one Content table.

PI06 owns physical identity and safe, provenance-bearing claim, ownership and
auth metadata. Credentials, credential verification, secret retrieval and secret
lifecycle belong to PI12. Content/blob availability belongs to PI07; assignments,
classification policy and `nocloud` belong to PI09. No legacy writer takeover,
HTTP endpoint, event outbox, connector call or production migration is included.

## Identity and normalization

| Input | Required treatment |
| --- | --- |
| UID | Exactly 23 ASCII bytes: eight two-digit hexadecimal bytes separated by colons. Use existing `tag.ParseUID`. |
| rUID | Exactly 16 ASCII hexadecimal digits. Use existing `tag.ParseRUID`; reverse bytes, not characters, to obtain UID. |
| Case | Accept upper/lower hexadecimal; render uppercase using existing `String` methods. |
| Both supplied | Validate independently, then call `tag.NewUIDPair`; mismatch rejects the entire command. |
| One supplied | Derive the other through the existing typed conversion. |
| Neither supplied | Invalid registration/query by physical identity. |
| Malformed input | Reject spaces, trimming, prefixes, alternate separators, compact UID and colonized rUID; never guess which codec was intended. |

The application input has explicit optional UID/rUID fields; presence is separate
from value. A supplied empty field is invalid. Zero-valued byte arrays are valid
under PI04 and must not be used as a missing-value sentinel. No manufacturer,
Original/Custom or system-card heuristic is added. Transport-specific compact
device-order input needs a later explicit adapter, not relaxed domain parsing.

Canonical physical equality is equality of the eight UID bytes in device order.
rUID is derived, never a second independently editable identifier. Synthetic
test example only: `11:22:33:44:55:66:77:88` and `8877665544332211`.

`identity.TagID` remains an independently allocated opaque identity: `tag_` plus
26 characters from PI04's lowercase alphabet. The application validates it with
the existing parser and rejects its zero value. Bounded internal registration
accepts a caller-supplied, independently allocated TagID; an external allocation
API is not part of this PI. Never derive TagID from UID, AudioID, model, title,
hash, path, auth capability or assignment.

One registry database has one TagID per canonical UID and one immutable UID per
TagID. No per-box or per-owner duplicate namespace is introduced. Exact repeat
registration of the same pair returns the existing record without revision
advance. Same UID/different TagID or same TagID/different UID returns a typed
identity conflict with no writes. No upsert, reassignment, merge or delete is
allowed. Different UIDs remain different Tags even when all metadata matches.

## Independent metadata and transitions

Registration creates revision 1 and unknown metadata. Revision is a positive
integer bounded by signed 64-bit storage; overflow rejects before mutation.
Parsing success is not a stored protocol-valid observation.

The initial registry accepts four independent fact keys: `protocol_valid`,
`claimed`, `cloud_auth`, and `owned`. Each has the values unknown, observed true,
observed false, or conflict. These are evidence projections, not permission to
authenticate, contact a cloud service or infer physical classification.

Use existing `evidence.Observation`, `Source`, confidence and review values.
Subject is the canonical TagID, values are a closed boolean vocabulary, and
source fields are logical, non-secret locators. Confidence never accepts a fact.
Review and confidence remain independent. Observation identity is immutable;
same-ID/same-payload replay is a no-op and same-ID/different-payload is a conflict.
Changing review creates a new observation ID; it never rewrites old evidence.

| Command/input | Effect on addressed key | Other state |
| --- | --- | --- |
| Record pending/disputed/rejected observation | Retain evidence; no accepted-value change | Unchanged |
| Record first accepted observation | Unknown becomes its boolean value | Unchanged |
| Record agreeing accepted observation | Preserve value and add support | Unchanged |
| Record contrary accepted observation | Conflict; retain both values and supporting observations | Unchanged |
| Explicit reviewed resolution with expected revision | Select the active accepted support set for this key; require nonempty agreeing support and `evidence.Accept` success | Retain all superseded/dissenting observations and the resolution record |
| Stale revision, invalid support or reused observation identity | Reject whole mutation | No partial evidence or revision changes |

The active support set initially includes all accepted observations for that key.
Only an explicit resolution may remove observations from active support; it
records the prior revision and selected IDs. Later accepted observations join
that active set and can create a conflict again. Resolving a conflict cannot
erase its history. An accepted true-to-false change therefore requires an
explicit resolution; time, confidence, source revision and input order never
silently choose a winner. Clearing to unknown is deferred, not inferred from
timeout or missing input. `evidence.Accept` validates selected current support;
all historical evidence is retained separately, not fed back as an automatic
override of the explicit selection.

`cloud_auth=false` says only that accepted evidence reports no capability.
`claimed=true` does not imply `owned=true`; ownership does not imply cloud auth.
Absence is unknown, not false. Unreachability cannot revoke auth metadata.
`exists` is Content Store availability; `nocloud` is assignment/runtime policy.
Neither is stored as a registry authority or used to classify a card.

The [PI-02 compatibility inventory](pi-02-contract-inventory.md) distinguishes
`hasCloudAuth = _has_cloud_auth && !cloud_override` from download eligibility.
PI06 must not collapse those legacy values into one inferred fact. Overlay policy
and legacy interpretation remain later adapter/Settings work. Unknown mappings
remain unresolved; no raw legacy payload is stored through the metadata port.

Optional future secret references are non-secret opaque references, never proof
that a credential exists or works. This delivery exposes no secret-reference
mutation until PI12 defines its type, authorization and lifecycle; no auth bytes,
passwords, tokens, credential hashes or fetched secret material enter the schema,
errors, fixtures or public documents. This is a deliberate empty reference seam.

## Application ports and transaction ownership

Proposed files: `internal/application/tagregistry/{ports,errors,service}.go`.
Ports accept domain values, `context.Context` and bounded command/result types:

- `TagRepository.FindByID(ctx, TagID)` and `FindByUID(ctx, tag.UID)` return
  `(Tag, found, error)`; rUID lookup normalizes to UID first.
- `Insert(ctx, Tag)` inserts a new identity or reports a conflict; registration
  replay is resolved explicitly by the service within the same transaction.
- `CompareAndSwap(ctx, expectedRevision, TagChange)` atomically persists a
  validated bounded change, evidence and resolution history. No generic Save
  that overwrites identity or unconditional metadata upsert.
- `Transactor.WithinTagTransaction(ctx, func(TagRepository) error)` gives a
  callback-scoped repository. Query services also use that bounded scope.

The distinct method name avoids a Go overload collision with the existing
catalog `WithinTransaction`. Keep the catalog port intact. Implement the tag
port on the existing SQLite `LifecycleOwner`, with the same gate, selected
Database and one-connection pool. Do not create another owner/connection pool
or cache a Database/repository outside the callback. Adapter-private extraction
of shared transaction plumbing is allowed only with catalog regression coverage.

The service owns validation and command atomicity; the adapter owns SQL,
connection settings, commit/rollback and lifecycle fencing. One mutation covers
identity, evidence, support selection and revision together. A callback executes
at most once. No nested catalog/tag transaction, cloud/file I/O or unbounded
retry inside it. Cross-domain assignment transactions are a PI09 design input.

Restore/close must drain Tag and Content operations through the same gate;
waiting operations obey their contexts. Restore uses the already implemented
verified new-path/exact-schema flow. A tag operation against a restored schema
without tag tables reports unavailable; it must not recreate schema implicitly.
No down migration or production handle switch is authorized here.

Application-owned errors distinguish invalid input, missing Tag, identity
conflict, revision conflict, evidence conflict, limit exceeded, contention and
unavailable storage. Preserve context cancellation/deadline and caller callback
errors without exposing SQL text, table names, paths, driver types or physical
identifiers. Storage corruption is not a not-found result.

After an error during commit/cleanup, outcome may be uncertain. Read back by
TagID/UID and immutable observation/resolution identity before any retry. Exact
replay is checked before stale-revision rejection; a fully present identical
command is a no-op, a partial/different replay conflicts. No automatic replay
and no durable generic idempotency/outbox subsystem is added in PI06.

## Bounded storage design

Use additive, checksummed forward migrations after the existing schema; choose
the next number at implementation time, never edit the applied Content migration.

| Proposed adapter table | Constraints and responsibility |
| --- | --- |
| `tc_tags` | TagID primary key; UID BLOB unique/not-null, type BLOB and length 8; positive bounded revision. UID is immutable. No stored rUID. |
| `tc_tag_observations` | Observation ID primary key; TagID FK RESTRICT; closed fact/value/review/confidence enums; source fields, UTC time; immutable payload. |
| `tc_tag_decisions` | Immutable metadata decision ID, TagID FK RESTRICT, expected/result revision and fact key; preserves explicit support selection and replay identity. |
| `tc_tag_decision_support` | Unique decision/observation pair; FKs RESTRICT; application and composite-key constraints prevent support crossing Tags. |

Validate canonical IDs, enum domains and constructor invariants on write and
read. Bound identifiers to existing PI04 lengths; source name/revision/record to
128 UTF-8 bytes each, with existing control-character rejection. A command has
at most 64 new observations and addresses one fact key; a Tag retains at most
4096 observations and 4096 decisions. Each decision has at most 4096 unique
support IDs, and all retained decisions together have at most 16384 support
references per Tag. An observation referenced by several decisions counts once
for each decision/observation link. These limits apply to retained history,
including superseded decisions; they do not permit a 4096-by-4096 link graph.

The complete encoded aggregate has a hard limit of 8 MiB (8388608 bytes),
including identity, observations, decisions, support references, active state
and all encoding overhead/escaping. Count and encoded-size checks must reject
before cloning inputs, constructing aggregate copies or materializing joined
rows. Use overflow-safe size accounting and bounded decoding; the adapter
preflights persisted counts/sizes and stops decoding at the same limits.
Oversized commands return limit exceeded without writes; oversized persisted
state is unavailable/corrupt, never a partial successful read. No truncation or
garbage collection is allowed. Per-field maxima are simultaneous upper bounds,
not permission to exceed the aggregate budget.

Decision IDs use a distinct internal type with exactly `dec_` plus 26
characters from the existing PI04 lowercase opaque-ID alphabet. Reject other
prefixes, lengths, noncanonical characters and zero values. This spelling is
fixed for internal use, not a public wire contract.

Point reads return one bounded aggregate; no unbounded list/export API or scan
is included. Unique UID/TagID indexes support lookups. Reads share PI05's
serialized connection and context deadline, not a promised parallel reader pool.
No uniqueness on AudioID, title/model, auth, ownership or assignment exists.
No Content/Version/blob FK is introduced.

These are fixed acceptance limits for PI06, not measured performance claims.
A1 verifies the worst-case permitted representation against the 8 MiB budget,
including maximum escaped source text, all retained history and 16384 support
references. Test exact-limit acceptance and one-over rejection for each bound,
checked arithmetic and rejection before aggregate-copy allocation. Larger
combinations must be rejected even when their individual field limits pass.
Reaching a limit preserves existing evidence; expanding it requires separate
refinement and does not silently introduce pagination or history pruning.

## Failure and acceptance matrix

The named tests below are future acceptance targets, not tests already present.

| Case | Required observable result | Owner/test target |
| --- | --- | --- |
| Malformed/mismatched codecs, omitted versus zero UID | Invalid input/no write; valid zero bytes round-trip | A1 `TestTagRegistrationNormalization` |
| Same pair replay or competing identity | One identity; no revision change on replay; conflict never overwrites | A2 `TestTagIdentityUniqueness` |
| Two simultaneous registrations/metadata updates | One identity/winner; other exact replay or typed conflict; no lost update | A2/B1 `TestTagConcurrentCommands` |
| Independent facts and contradictory evidence | Only addressed key changes; conflict/history retained | B1 `TestTagMetadataTransitions` |
| Fault after evidence insert before revision update | All writes roll back together | B1 `TestTagMetadataAtomicity` |
| Callback error, busy, pool wait, cancellation | Bounded error; callback at most once; subsequent access recovers | B2 `TestTagTransactionFailures` |
| Commit/cleanup error or lost response | No false no-effect claim; readback and exact replay avoid duplicate state | B2 `TestTagUncertainOutcome` |
| Restart with uncommitted metadata | Last committed identity/evidence/revision remain intact | B2 `TestTagRestart` |
| Corrupt row/invalid enum/missing support | Unavailable storage, never zero-value success/not-found | B2 `TestTagCorruptRecord` |
| Restore/close racing Tag and Content calls | One shared fence; no callback uses an old handle after restore | B2 `TestTagLifecycleFence` |
| Upgrade/snapshot/restore with tag FKs | Preserve Content and Tag/evidence graph; reject broken FK snapshot before selection | B2 `TestTagSchemaRecovery`, gated by #18 |
| Missing external blob | Unproven here; not simulated by a missing Tag | PI-07/T-01 #17 |

[F-PERSIST-01 (#18)](https://github.com/shentschel/teddycloud/issues/18) owns
explicit foreign-key snapshot validation. Before accepting any FK-bearing Tag
schema backup/restore, require its evidence for `foreign_key_check` in addition
to integrity, digest and ledger checks, including a structurally valid snapshot
with an orphan relation. Link/reuse that issue; do not implement or duplicate it
inside R. B2 acceptance waits if its dependency remains unresolved.

[PI-07/T-01 (#17)](https://github.com/shentschel/teddycloud/issues/17), recorded
previously as PI-07/T-01, retains missing-blob validation. Tag persistence does
not prove media availability, credential restoration or production rollback.
