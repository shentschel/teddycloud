# PI-08/A2.5c review-state write checkpoint

Status: accepted bounded Review write checkpoint; PI-08/A2 and the PI milestone remain incomplete.

## Accepted implementation

- Implementation: `4b8c00754329076f618bbe36b7770d5b45246f30`
- Prerequisite read adapter: `f77185213626c0abf38b36028e385964fd4c57aa`

The v6 SQLite adapter now commits Review state atomically. It revision-fences
the aggregate and Review projection, validates the exact domain transition and
result, persists normalized source, batch, record, observation, decision and
support rows, stores a length- and SHA-256-bound state envelope, and reads the
result back through the existing corruption checks before commit.

Exact replay is idempotent without duplicate rows. A changed command carrying
the same identity, stale revisions, revoked authority, invalid results,
constraints, corruption, contention and cancellation fail closed and roll back.
Current authorization is checked again immediately before commit. Storage does
not reconstruct or mint capabilities.

Existing v5 behavior remains read-only for catalog state. `CommitOrder` remains
unsupported, and the accepted query/read paths are unchanged.

## Verification

Digest-pinned Go 1.27.1 Docker checks passed the SQLite, application catalog,
domain catalog and architecture suites, ten repeated `CommitReview` runs and
the race-enabled suite. Next CI run 38864702518 passed all four required jobs:
persistence/race, deterministic build/smoke, two-work-directory reproducibility
and advisory checks.

One unchanged cancellation test failed transiently in the first CI attempt and
passed on rerun. This checkpoint does not claim that intermittent behavior fixed.

## Deferred

Order-state commits, full A2 recovery/migration evidence and the remaining A2
acceptance matrix are still open.
