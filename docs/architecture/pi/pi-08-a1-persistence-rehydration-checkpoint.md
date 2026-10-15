# PI-08/A1.3c persistence rehydration checkpoint

Status: accepted internal contract refinement; PI-08/A and the PI milestone remain incomplete.

## Accepted implementation

- Semantic implementation: `cb44922f1461b684f0bc7a9c85c096449b4ca4ab`
- Formatting and acceptance head: `1614a834e98443873219c0e7094998fb8397b94c`
- Scope:
  - `next/backend/internal/domain/catalog/decisions.go`
  - `next/backend/internal/domain/catalog/order.go`
  - `next/backend/internal/domain/catalog/policy.go`
  - `next/backend/internal/domain/catalog/persistence_test.go`

The catalog domain now strictly encodes and rehydrates complete review and
order state. Persistence-only source registrations and namespaces are detached
from live capabilities, so decoding stored state cannot mint authorization.
Opaque identifiers, registrations, namespaces, observations, evidence,
decisions, canonical commands, original results, revisions, links and bounds
are validated fail-closed. Round-trip and tamper tests cover the contract.

## Verification

Next CI run 38618038181 passed all four required jobs:

- network advisory checks;
- persistence/domain regression and race evidence;
- deterministic build and smoke checks;
- two-work-directory reproducibility.

The local launcher was unavailable, so this checkpoint makes no local execution
claim.

## Deferred

SQLite schema and adapter persistence, read-side ports, mutation paths and
recovery evidence remain open. This checkpoint supplies only the domain
rehydration prerequisite.
