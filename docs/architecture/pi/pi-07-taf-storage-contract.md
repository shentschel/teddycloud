# PI-07 TAF storage contract checkpoint

Status: proposed design; unresolved gates below block acceptance and delivery.
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
  Proposed Linux primitive: descriptor-relative no-replace rename; no ordinary
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

Full validation is required at import and explicit verification/recovery.
Availability distinguishes missing record, missing blob, invalid path, digest
mismatch, unsupported format and unavailable DB. Read verification policy and
its resource cost remain a gate below. Errors expose stable categories without
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

## Gates still requiring refinement

1. Pin complete TAF validation: header/padding boundaries, unknown protobuf
   fields, streaming sentinel, tracks/offset constraints and generated valid
   fixture builder; legacy permissiveness is not the specification.
2. Fix concrete import byte/time/concurrency limits, range verification policy,
   inventory page/deadline limits and quarantine/cleanup retention defaults.
3. Specify import key/fingerprint encoding, version-binding schema and immutable
   conflict rules without conflating catalog identity with blob digest.
4. Finalize descriptor/no-replace primitive and capability tests; specify lock
   order across filesystem leases and existing SQLite lifecycle to avoid deadlock.
5. Name executable failure tests, define quarantine recovery and deletion/pin
   protocol completely. Until then cleanup remains inventory-only.

[PI-07/T-01 #17](https://github.com/shentschel/teddycloud/issues/17) stays open:
remove a real generated fixture after import, check range/availability failure,
then restore bytes by re-import without a second identity. A missing DB row is
not a substitute. Assignment persistence is later work; do not claim production
assignment/media/secret recovery from a domain-only invariant check.
