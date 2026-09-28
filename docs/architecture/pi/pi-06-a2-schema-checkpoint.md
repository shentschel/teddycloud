# PI-06/A2 Tag identity schema checkpoint

Status: verified partial A2 checkpoint; the durable Tag repository and lifecycle port remain open.

Migration 2 (`0002-tag-identity`) adds only `tc_tags`: canonical TagID primary key,
unique eight-byte BLOB UID and positive signed-64 revision. It stores no rUID,
metadata/evidence, Content reference or foreign key. Migration 1 is unchanged.

The v1-to-v2 test requires a verified pre-upgrade backup, preserves its Content
record and checks that the applied v1 checksum did not drift. Fresh/reopen,
canonical-ID, UID type/length/uniqueness, revision and defensive-copy tests pass.
Existing Content backup/lifecycle tests retain their historical-version intent.

Parent ran the full backend test suite, `go vet`, ten focused schema repetitions
and formatting/diff checks. [Next CI](https://github.com/shentschel/teddycloud/actions/runs/36431075242)
passed all four jobs for commit
[f2b6cb6](https://github.com/shentschel/teddycloud/commit/f2b6cb6d59c94e13547aa0aa1ab9bf0d07a83729).

Open A2 work: callback-scoped Tag repository/transaction integration through the
existing lifecycle owner, bounded read/write validation, concurrent UID
uniqueness and restart/rollback evidence. This checkpoint is not a
transactional-Tag or production-deployment milestone.
