# PI-08/A2.6c helper-process recovery checkpoint

Status: accepted bounded helper-process recovery evidence; PI-08/A2 and the PI milestone remain incomplete.

## Accepted implementation

- Test evidence: `710964b67ef2e7fc594596cc9e5a8488918e1a55`
- Production code changed: none

The SQLite catalog path now has real process-boundary evidence using the test
executable, a readiness pipe, SIGKILL and process reap. Termination before
transaction commit leaves no partial catalog state. Termination after durable
commit but before response preserves the complete state and supports exact
lookup and idempotent replay after reopen without duplicate effects.

Two independent reopens compare complete schema and row snapshots and verify
foreign keys and detached state envelopes. Old content sessions, repository
handles, owners and cursors are rejected after reopen. Busy and already-canceled
admission remain bounded and sanitized, and the transaction callback executes
exactly once.

This proves software/process recovery at two named positions. It is not physical
power-loss evidence.

## Verification

Digest-pinned Go 1.27.1 Docker checks passed the focused helper-process tests,
ten repetitions, the relevant SQLite/catalog/architecture suite and its
race-enabled run. Next CI run 38910248688 passed all four required jobs.

## Open finding

The previously observed intermittent cancellation-test failure remains open.
