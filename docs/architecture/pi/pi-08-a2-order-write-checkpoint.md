# PI-08/A2.5d order-state write checkpoint

Status: accepted bounded Order write checkpoint; PI-08/A2 and the PI milestone remain incomplete.

## Accepted implementation

- Implementation: `76ded04a5b6c1b4919af125e8e6a48f2a6f66ff6`
- Prerequisite Review write: `4b8c00754329076f618bbe36b7770d5b45246f30`

The v6 SQLite adapter now commits bounded Order state atomically. It validates
the exact domain transition and returned result, fences aggregate and Order
revisions, persists the SHA-256-bound state envelope and version manifest, and
reads the new state through the accepted corruption checks before commit.

Exact replay remains idempotent without duplicate rows. Changed replay,
concurrent/stale revisions, wrong or revoked namespace authority, constraints,
corruption, contention, cancellation and callback errors fail closed and roll
back. Current source authorization is checked immediately before commit.
Storage rehydrates detached state and never mints capabilities.

Review and Order commits interleave without losing protected-custom state.
Existing Review writes and catalog queries remain covered. v5 writes stay
unsupported.

## Verification

Digest-pinned Go 1.27.1 Docker checks passed the relevant SQLite, catalog and
architecture suites, ten repeated `CommitOrder` runs and the race-enabled
suite. Next CI run 38867614518 passed all four required jobs: persistence/race,
deterministic build/smoke, two-work-directory reproducibility and advisories.

The previously observed intermittent cancellation-test behavior remains
unresolved and is not claimed fixed.

## Deferred

Full A2 migration/recovery evidence and the remaining A2 acceptance matrix are
still open.
