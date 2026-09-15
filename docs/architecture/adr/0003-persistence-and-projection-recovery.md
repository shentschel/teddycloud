# ADR-0003: One command owner with recoverable blob and legacy projections

Status: accepted for implementation planning; failure-injection tests required
Date: 2026-09-15
Decision owners: executing architecture agent; storage/assignment module owners
Related PI/sprint: PI-01/B; F-06

## Context

Relational state, large immutable TAFs and legacy JSON have different durability
boundaries. The former context phrase "atomically publish blob and version"
does not specify a real transaction across those stores. SQLite transactions
cover database state; durability also depends on filesystem flush behavior.
[SQLite atomic commit](https://sqlite.org/atomiccommit.html) documents those
assumptions. Blob publication requires an explicit application protocol.

## Decision drivers

- Never acknowledge a usable version backed by a partial/missing staged file.
- Preserve the prior assignment and make retries and stale commands explicit.
- Keep one authoritative writer, including during gateway coexistence.
- Distinguish content identity, file identity and physical tag identity.

## Considered options

### Option A: Continue mutable JSON and independently update SQLite

- Benefits: quick compatibility with existing paths.
- Costs and risks: competing writers, uncertain commit order and lost updates;
  cannot satisfy the assignment and rollback requirements.

### Option B: SQLite core state with immutable local files and journaled projections

- Benefits: one relational transaction boundary; streaming blobs; explicit
  recoverable side effects; suitable scope for one host.
- Costs and risks: crash recovery, orphan retention and backup coordination are
  application responsibilities; no universal cross-store atomic commit.

### Option C: Server database/object store or all media inside the database

- Benefits: server storage supports other deployment scales; database BLOBs put
  media and metadata in one transactional store.
- Costs and risks: operational complexity or larger database/backup write load
  without a measured requirement on the reference household deployment.

## Decision

Choose B. Only the core owns authoritative database access. Domain modules share
its transaction boundary; workers keep separate checkpoints and call core APIs.
Store TAFs/covers as immutable content-addressed files. Official audio IDs and
legacy catalog/header hashes are evidence fields, separate from the complete
file digest. An exact audio pair may still map to multiple catalog products;
keep ambiguity and provenance. Do not invent official model numbers.

Start with SQLite WAL on local storage, a serialized write queue, bounded read
transactions and deliberate busy/retry handling. WAL supports concurrent readers
with one writer and requires same-host shared memory; a network-mounted database
is outside this design. [SQLite WAL](https://sqlite.org/wal.html) documents these
constraints. PI-05 pins/configures the driver, enables foreign-key checks for
every connection and verifies durable synchronization (FULL) and checkpointing.

### Blob import protocol

1. Create a scoped import ID and stage data in the Content Store's local staging
   area. Limit size/path; validate TAF format, full-file digest and completeness.
2. Flush the complete staged file; publish into the immutable namespace on the
   same filesystem using a no-overwrite operation. Flush the containing directory.
   An existing digest path is reused only after validating its content.
3. In one database transaction, record the blob reference and version, import
   idempotency result and durable event/outbox record. Success becomes visible
   only after this commit. Do not keep a database transaction open while encoding.
4. Dispatch events after commit. Retry the same import key with the same payload
   returns its recorded result; a different payload conflicts. Duplicate event
   delivery is allowed and subscribers reconcile/deduplicate by stable identity.

If publication succeeds but the database transaction fails, an unreferenced
complete blob may remain. Recovery retains it for reconciliation; garbage
collection cannot remove it while an import, assignment, backup or rollback
retention pin can refer to it. Reference acquisition and GC must be serialized by
the Content Store. Deleting content is a separate validated command, never an
import cleanup shortcut. External corruption is reported as unavailable content.

### Assignment and legacy projection protocol

After the ownership gate in ADR-0001, the Assignment Service atomically records
the desired assignment, predecessor, revision, idempotency result and projection
outbox entry. Validate target identity, expected current revision and available
TAF first. A request with an obsolete revision conflicts instead of overwriting.

The gateway adapter serializes work per tag, applies a fenced revision and
verifies readback. Desired and applied revisions are separate. Report
`pending_projection` until the gateway confirms the same current revision; only
then report playback assignment as applied. A timed-out write is uncertain,
not failed-with-no-effect: read back before retry. A stale acknowledgement cannot
mark a newer revision applied. Legacy files are projections and are never an
independent source of assignment truth after ownership transfer.

Until an adapter can enforce these properties, legacy remains the sole writer
for that operation. An unmodified `/content/json/set/` endpoint does not provide
the required revision fence just because its HTTP request succeeded.

### Failure walkthrough

| Interruption | Durable result and recovery | Test owner |
| --- | --- | --- |
| Download/conversion before publication | Staging only; resume or discard this import; prior content stays usable | PI-07/31/32 |
| Blob published, DB not committed | Retained orphan; retry validates/reuses it; no version is advertised | PI-05/07 |
| DB commit succeeds, response/event lost | Idempotency readback returns result; durable outbox replays event | PI-07/11 |
| Assignment commit, gateway unavailable | Desired revision pending; prior applied revision recorded; no applied-success claim | PI-09/36 |
| Gateway applied, acknowledgement lost | Read back and reconcile revision before retry; retain uncertain status until verified | PI-09/36 |
| Older projector/ack races newer assignment | Per-tag fence rejects obsolete writes; stale ack cannot advance current applied state | PI-09/36 |
| Restart during any projection | Recover durable outbox and reconcile remote revision before further mutation | PI-09/36 |
| Disk full, flush failure or corrupted blob | Reject import/unavailable content; preserve previous assignment; report failure | PI-07/48 |
| Rollback with pending projection | Fence commands, reconcile/export committed revisions; keep maintenance if unresolved | PI-34/37/38 |

## Consequences

- Positive: failures have explicit states; retries do not silently remap cards.
- Negative: recovery, retention and projection fencing are required implementation
  work; a core commit alone cannot certify what the existing box plays.
- Follow-up work: F-06 design is resolved here; its runtime proof belongs to
  PI-05/07/09/36/37. F-02 still inventories every legacy mutation. F-03 still
  defines exact Set cardinality, collision/version fixtures and the V3 schema.

## Compatibility, migration and rollback

Use the SQLite backup interface for a consistent database snapshot; copying a
live database file alone is not the backup protocol. The
[SQLite backup API](https://sqlite.org/backup.html) supplies a database snapshot,
not a coordinated backup of external media or credentials.

The core backup command fences relevant changes or establishes a retention epoch,
pins every referenced immutable blob, snapshots the database and writes a digest
manifest. Copy pinned blobs and verify the manifest before declaring completion.
Secrets have a separate protected backup/restore procedure. Do not release pins
until completion/abort is durably recorded. PI-37 proves restore of all three
parts; a snapshot without blobs/secret references is explicitly incomplete.

Preserve known Custom Cards and their assignments independent of catalog origin.
Keep verified set series labels, member episode/cover and unresolved model
evidence. Choosing one preferred Original is a query policy; destructive cleanup
requires its own exact target/recovery plan. Source URI projection never overrides
full-file verification or explicit identity conflicts.

## Security and privacy

The core database is unavailable to plugins/workers. Staging paths are generated
by the Content Store, not arbitrary caller filesystem paths. Secret storage is
owned operationally by Settings/Secrets; only the connection-owning gateway or
connector receives scoped material. Audit records/events contain references and
redacted outcomes, never private keys or NFC authentication payloads.

## Validation

J-02 has desired/applied assignment state and retry/revision checks. J-03 keeps
version and Set evidence separate from blob identity. J-09 requires snapshot,
media and secrets reconciliation. The failure table is a design review, not a
passing crash test. F-06 runtime acceptance requires failpoints at each listed
boundary, replayed commands and an invariant check for protected assignments.

Acceptance basis: PI-01/R constraints, single-owner requirement, SQLite documented
limits and the corrected PI-00/A flow. No new schema, live migration or durability
certification is approved by this document.
