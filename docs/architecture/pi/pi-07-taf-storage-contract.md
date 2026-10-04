# PI-07 TAF storage contract checkpoint

Status: R design accepted; implementation evidence pending.
Baseline for this refinement: `aef53a69ab10f1480c1dade9797ce4dd9ce645bd`.
The normative decisions below supersede the earlier checkpoint proposals.
Derived from [ADR-0003](../adr/0003-persistence-and-projection-recovery.md).

## Evidence and authority

At baseline, `domain/catalog/content.go` separates editorial Content from
ContentVersion; `application/catalog/ports.go` and SQLite `content_repository.go`
persist editorial facts only. `lifecycle_owner.go` serializes Content/Tag access
and selects restored DB handles. `backup_impl.go` verifies database snapshots,
including foreign keys; it does not back up media. Extend those boundaries,
not a parallel database owner.

Legacy evidence: `proto/toniebox.pb.taf-header.proto`, `src/handler.c:isValidTaf`,
`getLibraryCachePath`, `moveTAF2Lib`, and `src/handler_cloud.c` range/chapter paths.
Legacy library naming uses AudioID; payload SHA1 and length validation can be
optional. Neither is complete-file identity. Do not copy legacy deletion,
source-JSON mutation or incomplete-stream behavior into the immutable store.

## Proposed identity and publication

- Blob identity is `sha256:<64 lowercase hex>` over every byte of the complete
  TAF, including its header. Version the algorithm explicitly; reject unknown
  algorithms rather than reinterpret old digests. A future algorithm requires
  an explicit migration/alias contract. AudioID, header SHA1, model, title,
  TagID and ContentID remain separate evidence/identities.
- Layout version 1: `blobs/sha256/aa/bb/<digest>.taf` under one configured root;
  prefixes are derived from the digest. Store no caller path in authoritative
  references. Use strict ASCII canonical identifiers; reject separators, dot
  segments, NUL, percent-encoded paths and alternate case spellings. User-facing
  filenames are metadata only.
- Core-owned `staging/` and `quarantine/` share the blob filesystem. Generate
  unpredictable exclusive temporary names, stream with explicit byte/time
  bounds, validate complete format/declared length and full digest, flush file,
  atomically publish without replacement, then flush affected directories.
  Selected Linux primitive: descriptor-relative no-replace rename; no ordinary
  overwrite rename or copy-on-EXDEV fallback. Startup capability checks must
  reject unsupported durability semantics before admitting imports.
- Existing digest destination is reused only after regular-file, size and full
  digest validation. Mismatch returns corruption/collision, never overwrites.
  Same bytes share a blob, not automatically a Content or ContentVersion.
- Commit blob reference, explicit version binding and import idempotency result
  in one short database transaction only after durable publication. Never hold
  that transaction while receiving/hashing media. Same import key and canonical
  command fingerprint returns the recorded result; changed payload conflicts.
  Lost response requires readback, not another identity allocation.
- There is no atomic DB/filesystem transaction. Publication before DB failure
  leaves a complete orphan; retain and reconcile it. A DB commit does not prove
  eternal file availability. Event/outbox integration remains PI-11; PI-07 must
  not claim the full ADR event-delivery protocol is already implemented.

## Reads, ownership and threat boundary

Application ports accept typed blob IDs and numeric ranges, never filesystem
paths. Proposed range is half-open `[offset, offset+length)` in complete-file
bytes; reject negative/overflow/out-of-bounds requests. Empty range at EOF is
valid; short read before its expected end is unavailable, not success. HTTP
Range/206/416 and header-stripping belong to later transport adapters.

Open relative to retained root/directory descriptors with no-follow behavior
for every component; verify regular-file identity through the open handle.
String prefix checks and Lstat-then-open alone do not prevent TOCTOU. Reject
symlinks, special files, unexpected hard links and cross-device publication.
Root ownership excludes untrusted writers; hostile root/same-owner arbitrary
in-place mutation is outside the guarantee and must not be called prevented.
Case-folding filesystems are unqualified until an explicit capability test.

Full envelope validation is required at import and explicit verification/recovery.
Availability distinguishes missing record, missing blob, invalid path, digest
mismatch, unsupported format and unavailable DB. The bounded read-verification
policy is fixed below. Errors expose stable categories without
raw paths, SQL, credentials or identifiers in nested causes.

Reference acquisition, active reads, import publication, quarantine and future
GC need one serialization/lease protocol; restore drains/fences that protocol
before selecting a DB. Old handles must not acquire new references afterward.
Backup/rollback pins must be considered before any deletion. Existing DB-only
restore can retain metadata while media is missing: report unavailable and keep
identity, editorial facts and protected card/assignment state unchanged.

## Recovery matrix (future executable evidence)

| Fault | Required outcome | Slice |
| --- | --- | --- |
| Partial write, cancellation, disk full before publication | No committed reference; own staging retained or safely discarded; prior content preserved | A/T |
| File/directory fsync fails | No success acknowledgement; reconcile possible published orphan | A/T |
| Crash after publish, before DB commit | Verify/reuse complete orphan on retry; no advertised version | B/T |
| DB commit/response uncertain | Read import result; exact replay; no blind compensation deletion | B/T |
| Duplicate concurrent imports | One immutable path; command-key conflict or replay; no inferred catalog merge | B/T |
| Symlink/path swap, traversal, special file | Fail closed through descriptor-bound operations | A/T |
| EXDEV or unsupported no-replace/directory sync | Reject; no non-atomic fallback | A/T |
| Missing or tampered referenced file | Typed missing/corrupt availability; preserve DB facts; repair via verified re-import | T/#17 |
| Restore races import/read/reference acquisition | Fence and drain; select restored handle; reconcile external files | B/T |
| Interrupted quarantine/inventory | Resume bounded scan; no automatic destructive cleanup | B/T |

Orphan detection compares canonical files against committed references and
pending imports/pins under the common owner. Inventory must be paged and
context-bounded. Quarantine preserves suspect bytes under generated names;
moving a referenced file cannot silently erase its reference. No automatic
orphan/quarantine deletion is admitted in this checkpoint. A later bounded
cleanup command needs revalidation under the same fence, explicit limits and
retention/backup pins; age or filename alone is never deletion authority.

## Validation profile: taf-envelope-v1

This is a deliberately bounded storage acceptance profile, not an upstream
format specification or a playback certificate. `isValidTaf` accepts some files
without checking size/hash and permits a protobuf size up to 4096; the encoder
aims for 4092 bytes. Neither behavior proves arbitrary layouts interoperable.
PI-07 chooses the following exact rules; no permissive fallback is allowed.

1. Input is a finite complete file. Bytes 0..3 are an unsigned big-endian
   protobuf length, exactly 4092. Bytes 4..4095 are exactly one proto2 message.
   Audio payload starts at absolute byte 4096. Reject shorter/longer headers,
   truncated messages and any padding outside the declared protobuf message.
2. Use `proto/toniebox.pb.taf-header.proto` field numbers and wire types.
   Required singular fields 1 (bytes), 2 (uint64), 3 (uint32), 5 (bytes) occur
   exactly once. Fields 6..9 are optional uint64 varints, at most once each.
   Field order is arbitrary. Field 4 accepts packed or unpacked uint32 values,
   including multiple segments, concatenated in wire order. Empty track lists
   are valid. Reject wrong wire types, tag zero, duplicate singular fields,
   unterminated/overflowing varints and lengths exceeding remaining header.
   Decode unsigned varints with at most ten bytes and tenth byte at most 1;
   uint32 values must fit 32 bits. Non-minimal but bounded varints are accepted;
   the original bytes are retained. Unknown fields (including groups) return
   unsupported format, not silently skipped evidence. No recursive decoding.
3. Field 1 has exactly 20 bytes. Field 5 is present, may be empty and otherwise
   contains only zero bytes. Its length must fit the message; its field need
   not be last. Fields 6..9 are retained only in the immutable bytes and have
   no authority over offsets, identity, completeness or version ordering.
4. `num_bytes` is positive and equals actual file length minus 4096. Hash all
   payload bytes with SHA1 and require equality with field 1; compute SHA256
   over the complete original file separately. Read exactly the declared
   payload, then prove EOF with one bounded extra-byte read; reject trailing
   bytes, early EOF, read errors and integer overflow before allocation/I/O.
   Payload need not be a multiple of 4096. No trailing padding is ignored.
5. At most 99 track entries; zero entries denotes undivided audio. With tracks,
   the first is zero and subsequent values strictly increase. For each page p,
   checked uint64 arithmetic must yield `4096*p < num_bytes`; its absolute
   offset is `4096 + 4096*p`. The last interval ends at the actual file end,
   never at a rounded-down page. No chapter may start at EOF or be empty.
   This validates byte intervals, not that the offset starts a decodable packet.
6. `audio_id` may be any uint32 at envelope-validation level. Binding to the
   existing catalog ContentVersion requires nonzero AudioID because its
   constructor already rejects zero; binding failure creates no DB state.
   The hash/AudioID pair is evidence, never an implicit Content lookup.
7. Live/growing input is not admitted. Legacy `encode.stream_max_size` defaults
   to 251658239 and is configurable; there is no universal on-disk sentinel.
   Do not blacklist that literal if a complete, correctly hashed file really
   has that size. An incomplete sentinel header fails actual length/SHA1/EOF.
   An explicitly streaming source is rejected before reading. EOF on a closed,
   immutable source is required; stalled/growing sources hit the deadline.

The payload is opaque for this profile: no Ogg page CRC, Opus decoding, EOS,
packet-continuation or audio-duration claim. Requiring EOS would not follow
from the inspected legacy encoder. Codec/playback validation belongs to later
media/gateway work; storage returns `envelope_verified`, never `playback_valid`.
This explicit boundary avoids making an invented codec validator authoritative.

A owns `internal/domain/content/taf.go` and its malformed/boundary tests, plus
the shared test-only builder `internal/testutil/taffixture/builder.go` used by
A/B/T. It creates deterministic synthetic payload, exact padded protobuf,
SHA1 and SHA256, permits zero/one/99 tracks and bounded malformed mutations.
Its outputs are structurally valid envelope fixtures, explicitly not playable
recordings. Independent hand-encoded header vectors prevent builder/parser
self-confirmation. No downloaded/private TAF is a test dependency.

Compatibility policy: a file outside this profile is rejected unchanged, with
a typed unsupported/invalid reason; do not rewrite its header to make it pass.
It stays with its source owner, not in the authoritative blob namespace.
Additional profiles require reviewed fixtures, a new profile number and explicit
acceptance. Profile 1 semantics never silently change. Real-device and broad
legacy corpus compatibility remain unproven, not open choices for A to guess.

## Fixed resource and read policy

MiB means 1048576 bytes. Configuration is validated at owner startup and cannot
weaken hard ceilings or change mid-operation; zero is not unlimited. Earlier
caller cancellation/deadline always wins. These are conservative prototype
bounds for the 1-CPU/2-GiB reference, not measured throughput guarantees.

| Resource | Default / hard ceiling; treatment |
| --- | --- |
| Complete import bytes | 512 MiB / 1 GiB; configurable from 4097 bytes, checked against declared and actual length |
| Import duration including waits, receive, validation, publication, commit | 10 minutes / 15 minutes; configurable positive duration |
| Active content operation | 1 / 1 under the existing serialized lifecycle gate; no unbounded queue, admission wait at most 5 seconds |
| Streaming buffer / decoded header / tracks | 64 KiB / 4096 bytes / 99; fixed, no whole-file memory buffer |
| Staging retained bytes / entries | 2 GiB / 32 fixed; reserve declared bytes before create, count partial files at actual size after restart |
| DB transaction | 5 seconds hard including pool/busy wait; callback once, no automatic retry; earlier operation deadline wins |
| One range result | 8 MiB / 64 MiB; configurable positive length cap; stream to scoped sink, no returned open file |
| Verification plus range duration | 60 seconds / 120 seconds; configurable, includes full hash before range output |
| Inventory page / inspected entries | 128 / 1024 fixed per invocation; count directory entries and invalid names, not just returned blobs |
| Inventory duration / cursor | 2 seconds / 256 bytes fixed; one active scan, generation-bound opaque cursor |
| Quarantine retained bytes / entries | 2 GiB / 128 fixed; full scan at startup bounded by inventory policy before admitting content mutations |
| Staging/quarantine/orphan retention | Indefinite; no TTL and no automatic deletion in PI-07 |

Admission limits the number of retained callers before they wait; if one
content operation is active/admitted, another gets busy immediately. Content/Tag
transactions retain their existing bounded lifecycle wait. A content operation
may hold the common gate for its bounded duration; this deliberate initial
serialization can delay metadata calls. Optimizing concurrency is later work
and cannot bypass the restore fence.

Input/sink ports require context-aware Read/Write and Close that unblocks pending
I/O; plain arbitrary blocking readers are not accepted. Filesystem calls can
still hang in the kernel: do not release locks or announce cancellation while
an I/O worker may still publish. A deadline prevents further steps once control
returns; uninterruptible kernel I/O is an operational limitation, not a hard
wall-clock guarantee. Failed closeout leaves the owner unavailable for mutation.

Every range/availability query opens the same regular-file descriptor, checks
size/link count, hashes the entire file against BlobID, and verifies its stored
envelope profile before returning available or emitting any range bytes. Check
descriptor stat before/after verification and after delivery. Never use mtime,
inode or a prior availability result as a hash cache. On a short subsequent
read or changed stat return unavailable; already-emitted bytes cannot be recalled.
Hostile same-owner in-place mutation remains outside the trusted-root guarantee.
Empty EOF ranges still require verification. Metadata-only lookup makes no
availability claim. Large valid files exceeding a configured verification
deadline are unavailable for that attempt; they are not marked corrupt.

## Import command, canonical encoding and schema

One core DB is the namespace. `ImportKey` is exactly ASCII `imp_` plus 26 bytes
from `0123456789abcdefghjkmnpqrstvwxyz`, allocated independently by the caller;
no trim/case folding and no filename/user/Tag-derived key. One key describes
one command for its retained lifetime. The caller supplies an existing ContentID,
independently allocated ContentVersionID, expected complete SHA256/size, expected
nonzero AudioID and payload SHA1, and explicit version-order evidence (or unknown).
It must compute these from a finite source before this command, not discover a
catalog identity from the uploaded bytes. No auto-created Content or default
"latest" ordering. Validate typed constructors before allocation or staging.

Fingerprint version 1 is SHA256 of the following concatenation, exactly:

```text
ASCII "TCIMPORT" (8 bytes), byte 1
ContentID (30 ASCII), ContentVersionID (30 ASCII)
byte 1 (blob algorithm SHA256), raw complete digest (32 bytes)
complete size (uint64 big endian), profile number (uint16 big endian = 1)
AudioID (uint32 big endian), raw payload SHA1 (20 bytes)
order-known (byte 0 or 1)
if known: namespace byte length (uint16 big endian), namespace ASCII,
          position (uint64 big endian)
```

No optional padding, JSON, timestamps, paths or terminating NUL. Unknown order
has no following order bytes; known namespace follows the existing 1..128-byte
printable-ASCII constructor, position includes zero through MaxUint64. ImportKey
is the DB lookup key and is not part of the fingerprint. Profile and encoding
versions are independent of DB schema version. Store canonical command bytes
(at most 275 bytes) as well as the 32-byte fingerprint and compare bytes on
replay, so even a fingerprint collision cannot alias two commands. Fixed-length
golden vectors and known/unknown-order tests are A acceptance requirements.

B adds migration 4, `0004-content-blobs`, after existing migrations 1..3, whose
checksums stay unchanged. If the base schema advances, stop and explicitly
rebind this migration before delivery; never choose a number silently.
All tables are STRICT; FK deletion is RESTRICT and all immutable rows are
insert-only through callback-scoped ports. Proposed schema is:

| Table | Columns, keys and invariant |
| --- | --- |
| `tc_blobs` | `digest` BLOB(32) PK, `algorithm` INTEGER CHECK =1, `size` INTEGER CHECK 4097..1073741824, `profile` INTEGER CHECK =1; no persisted available boolean |
| `tc_content_versions` | canonical `version_id` PK, `content_id` FK `tc_catalog_content`, `audio_id` INTEGER CHECK 1..4294967295, `audio_sha1` BLOB(20), `order_known` INTEGER 0/1, nullable `order_namespace` TEXT and `order_position` BLOB(8 big endian); unknown requires both NULL; known requires both present and valid; UNIQUE(content_id,version_id) |
| `tc_version_blobs` | `version_id` PK/FK versions, `digest` FK blobs; one immutable binding per version, many versions may share a digest |
| `tc_blob_imports` | canonical `import_key` PK, `encoding` INTEGER CHECK =1, `command` BLOB length 137..275 bounded by actual encoder, `fingerprint` BLOB(32), `version_id` FK bindings; result is this immutable version/binding, not mutable availability |

For all BLOB columns enforce `typeof='blob'` and exact stated length, canonical
ID byte length/alphabet constraints mirror existing identity tables. The
command length bounds must match A's golden encoder: unknown order is exactly
137 bytes; known order adds two length bytes, 1..128 namespace bytes and eight
position bytes, hence 148..275 bytes. Reject other encodings even if the coarse
SQL length check passes. Validate on
read and reconstitute existing `catalog.ContentVersion` constructors; compare
all version facts, stored command hash and binding. Invalid persisted state is
corrupt/unavailable, never not-found or a partial result. No unique constraint
on AudioID, audio fingerprint, title/model or blob across versions.

Import always verifies supplied bytes and their expected digests, length and
header facts before a durable result, even for an existing key. A separate
`LookupImport(key, canonicalCommand)` returns recorded outcome without media
I/O and explicitly says nothing about present availability. Same key/different
bytes or command conflicts; same key/same command returns the same committed
version/binding. Same version ID/different facts or binding conflicts even with
a new key. Same immutable version and blob with a new key records another
idempotency receipt, without another identity or rebinding. Different versions
sharing bytes remain distinct. Replay comparison occurs before attempted insert.
No generic expected revision is needed for this insert-only protocol.

Within one short transaction, verify Content exists; insert/verify the blob,
version, binding and receipt together. No SQL transaction crosses filesystem
I/O. An uncertain commit is resolved through LookupImport; do not automatically
rerun the callback. Missing media may be repaired by exact verified re-import
under the same command or a new key; corrupt existing bytes require explicit
quarantine first. Never overwrite a corrupt destination. Historical receipt
does not imply that repair or playback has succeeded today.

Upgrades/snapshots/restores use the existing lifecycle and accepted FK verifier.
After exact restore to schema 1..3 content-store commands return schema
unavailable without implicit migration; Tag/Content behavior follows the selected
schema. Schema 4 backup is DB-only, not a media backup. Files survive restore as
orphans or referenced bytes; no scan deletes them. B tests both upgrade and exact
older/current restore. Events/outbox remain PI-11.

## Linux publication and lock order

Use the already pinned `golang.org/x/sys/unix` on Linux. Open the configured
root once using an absolute, deployment-controlled path walked from `/` with
no symlink components; keep descriptors throughout owner lifetime. Inside the
root use `openat2` with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS |
RESOLVE_NO_MAGICLINKS | RESOLVE_NO_XDEV`; directories use O_DIRECTORY, files
O_NOFOLLOW and O_CLOEXEC. Create missing shard directories by `mkdirat` mode
0700 through retained parents, reopen/verify and fsync every newly changed
parent. Open temporary files O_CREAT|O_EXCL|O_RDWR mode 0600 with random
128-bit hex names; attempt at most three names. Require same device, regular
file, link count 1, trusted owner and no group/world-write directory at each
step. Do not accept an arbitrary caller path or follow a later path replacement.

The root retains `.owner.lock` (regular, link count 1, same owner, 0600) and
holds `flock(LOCK_EX|LOCK_NB)` for the entire lifetime. A second cooperating
owner fails startup. This extends the existing single-process assumption only
for this root; it does not claim protection against a privileged hostile writer.

After complete validation, fsync the staged file, then
`renameat2(stagingFD, temp, shardFD, name, RENAME_NOREPLACE)`. On success fsync
both shard and staging directories before the DB phase; a newly created shard
chain has already been synchronized. EEXIST triggers full verification of the
existing descriptor, including exact size/profile/digest; keep the staged copy
on mismatch. EXDEV, ENOSYS, EINVAL or unsupported fsync fails closed; no copy,
link/unlink or ordinary-rename fallback. A failed directory flush may mean the
file was published: return uncertain/unavailable and retain it for reconciliation.

Startup requires a local ext4 or XFS root (fstatfs plus descriptor mount-ID
matched to the exact filesystem type in mountinfo, distinguishing ext2/ext3), same-device
staging/quarantine/shards and the calls above. A generated scratch probe verifies
no-replace success, EEXIST preservation of old bytes, symlink rejection and
successful file/directory sync. Probe-created files only may be unlinked and
their directories synced after checking recorded inode identities. An interrupted
probe is retained as an unknown staging entry. Other filesystems, including
overlay/network/case-folded modes, fail qualification; reject case-folded
directories through inode flags and a case-distinct probe. Passing probes
establishes syscall behavior, not crash/power-loss hardware certification.
Linux CI must run on a qualified temporary ext4/XFS location; lack of it is a
blocked required job, not a skipped green test.

Lock order is fixed, with reverse-order release:

```text
content-operation admission slot -> existing lifecycle gate
 -> digest lease -> descriptor filesystem operations -> short SQLite transaction
```

The existing gate serializes operations; keep that behavior for this PI. Add an
owner-scoped `WithinContentOperation(ctx, callback)` that admits once and supplies
a revocable session. Its adapter-private transaction method uses the selected
Database directly without re-entering `WithinTransaction`/`WithinTagTransaction`.
Never leak Database/SQL into application ports. The session carries owner
generation and is revoked at callback exit; no detached goroutines/returned FDs.
Acquire the digest lease from the declared digest before staging; verify it
against actual bytes later. Only one digest per operation, no lock upgrade or
nested operation. Existing Content/Tag entry points need no digest lease and
cannot call a content operation from their callback.

The gate spans receiving, publishing and DB commit, but the SQL transaction
spans only DB work. This intentionally favors a simple demonstrable restore
fence over concurrent imports. Restore/close first enters the same lifecycle
gate with existing waiter priority; it waits for the active operation, holds
no digest lease while waiting, then increments generation/revokes sessions
before handle selection. Deadline while waiting leaves the active operation
and owner intact. No service caches the selected DB across callbacks. Reads,
inventory and explicit quarantine obey the same gate; thus none can relocate
a file while a range is open. A process-level root lock outlives all these locks.

## Inventory, pins, quarantine and deletion

Inventory is observational, never deletion authority. Startup remains content-
mutation-disabled until an incremental bounded scan has counted staging and
quarantine entries/bytes; overflow, unexpected entries or unreadable subtrees
remain diagnostics requiring maintenance. Catalog/Tag DB access can continue.
Each page opens directories relative to retained descriptors. Use an opaque
in-memory cursor over retained bounded directory iterators, with root/owner
generation and mutation counter; never accept an arbitrary path token. A
mutation/restore/restart invalidates the cursor: restart scan rather than miss
an entry. One scan, at most four directory descriptors, 60-second idle expiry,
at most 1024 entries inspected per call including directories; unfinished scans
return partial+cursor, never a complete/clean claim. Directory iterator order
is not a stable public order. No unbounded ReadDir/sort or full DB materialization.

Inventory classifications: referenced (a version binding exists), unreferenced
canonical candidate, staging, quarantine, or unexpected. Inspect file type and
size without trusting names; list verification as unchecked until a separate
bounded Verify operation. Orphan labels mean unreferenced at that gated instant,
not safe to delete. Import leases are transient pins; every committed binding is
a permanent pin in PI-07. All unknown, staging, orphan and quarantined files
also remain retained. DB-only backup/rollback therefore cannot lose bytes to
GC; there is no release-pin API. Coordinated backup manifest/pin epochs are
PI-37 work, not claimed here. Snapshot metadata alone is no deletion authority.

An explicit internal maintenance `Quarantine(BlobID)` is allowed only after a
fresh verification reports corruption, under gate+digest lease, with no active
reader. Missing/healthy files are not moved. Destination is a generated unique
`q-<128-bit hex>.taf` within retained quarantineFD; check caps before no-replace
rename and sync both source/destination directories. It preserves bytes and all
DB references; references now report missing instead of corrupt. No metadata
update is required for truth or recovery. Include no supplied filename/secret
in its name or diagnostic. Quarantine inventory may report unchecked objects
without recovering their original digest; no automated restore from quarantine.
After crash, source only, quarantine only or an uncertain placement is diagnosed
by re-inventory; never infer success from a stale receipt. Full quarantine or
flush failure returns unavailable and retains any already-moved file. Re-import
is a separate exact-byte repair, verified against the original digest.

PI-07 exposes no deletion, GC, purge or age-based cleanup command. Even own
failed staging is retained (bounded by caps) for manual offline maintenance;
successful publication consumes its stage by rename, and EEXIST duplicate stages
are retained and charged. This conservative choice avoids a second crash-prone
delete protocol. Operational capacity may fill: stop imports, keep reads and
DB facts usable, report capacity exceeded. Destructive offline maintenance
requires a later separately authorized plan and is not an acceptance bypass.

## Executable recovery acceptance map

All names are proposed tests to implement, not evidence of execution. FS means
`internal/adapters/contentfs`, CS `internal/application/contentstore`, SQL
`internal/adapters/sqlite`; test failpoints cannot replace actual helper-process
termination and reopen where explicitly required.

| Recovery row / exact test | Required assertions and owner |
| --- | --- |
| Partial/cancel/full: FS `TestBlobStageFailures` | A: inject short/error write, cancellation, ENOSPC at each chunk; no binding/public path, prior bytes intact, retained stage counted; no worker publishes after return |
| Flush failure: FS `TestBlobFlushBoundaries` | A/T: file, new-shard parent, publish destination and staging fsync faults; never acknowledge, enumerate possible complete orphan; retry verifies it |
| Publish/crash: CS `TestImportCrashAfterPublish` | B/T: kill helper after both dirs sync but before commit; reopen actual files/DB, no binding, retry reuses digest and creates exactly one version/receipt |
| Commit/response uncertainty: SQL `TestImportCommitUncertainty`, CS `TestImportLostResponse` | B: precommit failure, ambiguous commit return and lost postcommit response; readback distinguishes absent/present, exact replay, no delete compensation |
| Duplicates/conflicts: CS `TestImportReplayAndConflict` | B: identical/different key commands, same/different version, digest sharing, queued concurrent attempts retried after busy; one immutable path, exact receipts and no catalog merge |
| Path attacks: FS `TestBlobDescriptorConfinement` | A/T: swap components, symlink/magic link, traversal, FIFO/device and hard link; deny before content output or publication outside retained root |
| Capability: FS `TestBlobCapabilityFailures` | A/T: EXDEV, unavailable renameat2/openat2, directory-sync error, wrong fs/case mode, second owner; closed admission and no fallback |
| Missing/corrupt: CS `TestMissingBlobReimport`, FS `TestBlobVerifiedRanges` | T/#17: import real generated file, remove it via test fixture access, range/availability unavailable, facts unchanged, exact re-import repairs without new identity; mutate same-size bytes and detect corruption before output |
| Lifecycle: SQL `TestContentStoreLifecycleFence`, CS `TestContentStoreRevokedSession` | B/T: blocked import/read, restore priority, deadline, current/pre-v4 restore, old session rejection, no stale DB callbacks or reference after switch |
| Quarantine: FS `TestBlobQuarantineRecovery` | B/T: healthy refusal, caps, kill helper before/after rename and each sync; retain bytes/DB facts, restart inventory locates actual placement and repair requires verified re-import |
| Inventory/pins: CS `TestBlobInventoryBoundsAndRetention` | B/T: entry/page/time/cursor/restart bounds, mutation invalidation, referenced/orphan/stage/unknown/quarantine retained across restore; no deletion API or expiry removes pins |

A additionally supplies `TestTAFEnvelopeProfile`, `TestTAFEnvelopeLimits`,
`TestImportCommandGolden`, and `FuzzTAFEnvelopeHeader` in domain/content.
B supplies SQL `TestBlobSchemaRecovery` (migration checksum/FK/older restore),
`TestBlobCorruptRecords`, and CS `TestContentStoreResourceLimits` (all table
limits, exact/one-over, blocked I/O/close, cancellation before publication).
T supplies `TestBlobSanitizedErrors` covering the complete wrapped/joined chain,
and cross-checks each matrix row independently. No production card data is used.

[PI-07/T-01 #17](https://github.com/shentschel/teddycloud/issues/17) stays open:
remove a real generated fixture after import, check range/availability failure,
then restore bytes by re-import without a second identity. A missing DB row is
not a substitute. Assignment persistence is later work; do not claim production
assignment/media/secret recovery from a domain-only invariant check.
