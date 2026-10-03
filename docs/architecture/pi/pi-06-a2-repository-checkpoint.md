# PI-06 A2 transactional Tag repository checkpoint

Status: accepted A2 checkpoint

Commit `2bf15857e0fcdbadf569ef52c3d445001b2f29ba` completes the
identity-only durable Tag repository started by the additive migration-2
checkpoint.

## Delivered behavior

- A callback-scoped SQLite `TagRepository` supports insert and point lookup by
  TagID and UID; application rUID lookup normalizes to the same UID path.
- The distinct `WithinTagTransaction` port shares the lifecycle gate and the
  currently selected database handle with Content without exposing SQL types.
- Callback errors and panics roll back, and escaped repositories are revoked
  before commit, rollback, restore or close can proceed.
- Primary-key and UID uniqueness remain database-enforced, including writes
  through concurrent owners.
- Context, contention, conflict and unavailable failures cross the adapter only
  through the Tag application taxonomy. Corrupt identity rows fail closed.
- Existing Content transaction behavior is preserved through shared private
  transaction plumbing.

## Evidence

The implementation agent ran Go 1.27.1 formatting, the complete backend suite,
`go vet` and ten repetitions of the focused Tag tests on Linux. Parent review
checked the complete production and test diff. GitHub Actions run
[37119353589](https://github.com/shentschel/teddycloud/actions/runs/37119353589)
passed all four jobs, including deterministic workspace gates and the focused
SQLite/Tag regression and race job.

## Boundaries

This checkpoint does not add mutable metadata, assignments, classification,
blobs, authentication, HTTP behavior or production deployment. B1 metadata and
B2 recovery/failure evidence remain separate work. The parent environment had
no local Go toolchain after restart, so the independent parent runtime gate was
the pinned GitHub CI; the agent's Linux checks supplied the local pre-push gate.
