# ADR-0001: Replace the management plane before the protocol gateway

Status: accepted for implementation planning; deployment gates remain open
Date: 2026-09-15
Decision owners: executing architecture agent; product owner for production cutover
Related PI/sprint: PI-01/B

## Context

The [charter](../pi/pi-01-architecture-charter.md) requires correct Set identity,
protected Custom Cards and plugins that extend the standard UI. These depend on
new management contracts, while device TLS/protobuf/RTNL compatibility still
needs hardware evidence. The [reference environment](../pi/pi-01-reference-environment.md)
qualifies the existing server, not a new protocol implementation.

The source-reviewed `getTagInfoJson()` writes metadata during reads. A shadow
deployment therefore cannot assume every legacy GET is observational. F-02 in
the [backlog](../pi/pi-01-backlog.md) gates captures and write ownership transfer.

## Decision drivers

- Preserve household playback, assignments and curated identity information.
- Deliver Set-aware metadata and semantic UI slots without waiting for TLS work.
- Keep one authoritative writer for each operation throughout migration.
- Make interrupted migration reversible using reconciled durable state.

## Considered options

### Option A: Replace the whole daemon and UI together

- Benefits: one new stack and no prolonged compatibility adapter.
- Costs and risks: protocol uncertainty blocks UI/catalog progress; failure has
  a large rollback scope; current device matrix is insufficient for acceptance.

### Option B: New control plane beside the existing gateway

- Benefits: management contracts can be tested independently; existing playback
  has a smaller change surface; ownership can move at explicit checkpoints.
- Costs and risks: adapter maintenance and projection recovery are real work;
  the current C process contains both management and protocol code.

### Option C: Extend the existing management model indefinitely

- Benefits: smallest initial deployment change.
- Costs and risks: identity heuristics and UI customization remain tied to the
  current model; each feature needs another compatibility workaround.

## Decision

Choose B. Retain the existing device-facing daemon initially. Build the new core,
Set-aware catalog and standard UI extension contracts together. Calling it a
gateway is a target boundary; the current daemon is not already isolated.

| Stage | Active writer | Gate to advance |
| --- | --- | --- |
| Offline fixtures | Legacy installation unchanged; disposable copies only | PI-02 captures request and file side effects |
| Shadow comparison | Legacy remains authoritative; new database is a derived import | Repeated import and semantic discrepancy reports |
| Management ownership transfer | Core owns each admitted command; gateway submits observations through an adapter | Old mutation paths fenced; projection and rollback tests pass |
| Control-plane production | Core commands and tested legacy playback projection | PI-38 device matrix, soak and explicit cutover authority |
| Gateway replacement | New gateway owns device sessions, core retains domain truth | PI-47 real-device evidence and rollback acceptance |

No stage allows two independent assignment authorities. Unadapted legacy GET
write-back blocks transfer for that operation. A read-only filesystem export of
a disposable snapshot is the initial import path; live API comparison waits for
the side-effect inventory.

## Consequences

- Positive: Set/lookup/UI development can advance on offline fixtures.
- Negative: the adapter must expose divergence and both committed and applied
  assignment revisions; it cannot hide failed playback projection.
- Follow-up work: PI-02 inventories contracts; PI-09/35/36 implements adapters;
  PI-38/47 validates cutovers. [ADR-0003](0003-persistence-and-projection-recovery.md)
  defines the recovery boundary.

## Compatibility, migration and rollback

Keep existing clients and all seven enhancement repositories supported while
their public contracts migrate. Preserve source URI and wrapper semantics in
the facade. A model or title alone cannot merge independent Set members.

Before ownership transfer, stop writes, reconcile the last snapshot and record
the owner/revision. Rollback after transfer first fences new commands, drains or
reports projection work, exports the committed state and compares protected
assignments and blob digests. If reconciliation fails, remain in maintenance;
do not start the old writer against stale files. Earlier backup restoration is
an explicit data-recovery operation with disclosed lost changes, not a silent
equivalent of rolling back application binaries.

## Security and privacy

Device private keys remain at the existing gateway until a tested replacement
requires them. Connector and media identities are scoped; the UI receives no
external-service credentials. Preserve existing source licenses/notices; this
decision does not authorize a license change or claim upstream acceptance.

## Validation

J-02 follows one command authority with explicit projection state. J-03 stays in
the catalog independent of gateway version. J-09 has a fenced transition and
semantic rollback comparison. These are design walkthroughs, not runtime tests.
PI-02/36/38 must supply the fixtures, failure injections and device results before
the corresponding stage advances.

Acceptance basis: the authorized control-plane-first roadmap, source-reviewed
write-back risk and observed deployment envelope. Hardware qualification remains
F-01; no new generation/firmware support is accepted by this ADR.
