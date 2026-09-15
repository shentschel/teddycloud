# ADR-0002: Modular Go core, React UI and isolated workers

Status: accepted for implementation planning; exact tool versions deferred
Date: 2026-09-15
Decision owners: executing architecture agent; release owner for packaging gates
Related PI/sprint: PI-01/B

## Context

The target installation is one household Linux server. The observed reference
has one CPU and 2 GiB RAM. Existing enhancements span UI modules and media/sync
processes. The [component ownership](../component-ownership.md) already assigns
one core owner for each durable fact.

## Decision drivers

- Keep domain rules testable without HTTP, React, database or cloud clients.
- Avoid separate deployment units for tightly coupled catalog/tag/assignment rules.
- Isolate media subprocess failures and connector credentials.
- Publish stable plugin contracts independently of internal UI code.

## Considered options

### Option A: Go modular core with React/TypeScript and isolated workers

- Benefits: one core deployment and transaction boundary; browser code keeps
  typed SDK contracts; workers can retain their existing media ecosystems.
- Costs and risks: cross-language contract generation needs drift checks; gateway
  compatibility is still unproven; SQLite driver choice needs a build experiment.

### Option B: TypeScript or Python throughout the management stack

- Benefits: shares language/tooling with parts of the current enhancements.
- Costs and risks: a uniform language does not eliminate subprocess isolation or
  protocol adapters; it changes the established Go rewrite direction without
  measured benefit for the reference installation.

### Option C: Rust core or separately deployed domain microservices

- Benefits: Rust offers explicit resource ownership; separate services allow
  independent scaling where that is required.
- Costs and risks: this household deployment has no demonstrated need for that
  operational complexity; both add a different implementation/maintenance path.

## Decision

Choose A as the implementation baseline. Use a modular Go core, React/TypeScript
web application, versioned API description and generated TypeScript client.
Media and connector workers are separate unprivileged processes with explicit
command contracts. Their languages are selected per existing dependency needs;
they are not required to be rewritten in Go.

Dependency direction is adapters to application services to domain. Domain types
do not import HTTP handlers, SQLite drivers, React or worker clients. Application
services express use cases; infrastructure implements their storage/transport
ports. Keep ports narrow and based on actual use cases rather than abstracting
every helper. Tag, Catalog, Content and Assignment are modules, not standalone
network services. Persistence is detailed in
[ADR-0003](0003-persistence-and-projection-recovery.md).

Use this fork for the initial integrated workspace under a future `next/`
directory, with core, web, contract, SDK and test areas. This decision creates no
directory or repository yet; PI-03 scaffolds it. Keep the seven enhancement
repositories and their pipelines operational. Public SDK releases and migrated
extensions remain independently versioned; integration tests exercise supported
version combinations. A future repository move requires a separate documented
reason and migration; no repository name is invented or created here.

The initial deployment target is a Linux systemd service on the qualified
x86-64 reference profile. Package core/UI together; keep worker services and
credentials separate. Optional container packaging follows the same contracts
after LXC/container resource and storage tests. Windows/macOS remain supported
administration clients; native servers and ARM are outside current qualification.
PI-03 pins actual Go, Node, browser and SQLite driver versions in lockfiles/CI.

## Consequences

- Positive: one authoritative command boundary and a small operational footprint.
- Negative: generated client drift and compatibility matrices need CI coverage;
  workers need explicit timeouts, job state and resource limits.
- Follow-up work: PI-03 scaffolds reproducible builds; PI-12 defines auth and
  credentials; PI-18/23/24 implements slots, SDK lifecycle and iframe isolation;
  PI-31/32/37 defines worker limits and packaging. F-01 pins the browser profile.

## Compatibility, migration and rollback

Existing source/build/release paths continue operating while `next/` is developed.
Do not make new workspace builds publish inherited production containers.
Pin compatible SDK/API versions and negotiate capabilities at the boundary.
Existing iframe plugins use a legacy adapter; new Copy/filter/detail/badge
extensions use semantic slots on the standard pages, never DOM selectors.
Rollback before deployment is a source revert; runtime rollback follows
[ADR-0001](0001-control-plane-and-gateway-sequencing.md).

## Security and privacy

Same-origin trusted modules have browser application privileges; permissions in
a manifest do not sandbox them. Isolated iframe modules use a checked message
bridge. Workers do not mount the core database and receive only their own secret
references/material through protected operational interfaces. Preserve existing
licenses and notices; PI-03 inventories dependencies and PI-49 performs the
release license review. No relicensing or external source copying is implied.

## Validation

J-02 uses SDK to Assignment Service, J-03 uses Catalog independent of UI, and
J-09 uses the core backup/import contract. All seven enhancement owners remain
mapped in component ownership. These design walkthroughs pass; reproducible
builds and resource/performance measurements are PI-03/10/26/31/32 evidence.

Acceptance basis: authorized rewrite direction, existing enhancement boundaries
and observed single-host profile. The choice is reversible before runtime
adoption; language-specific performance superiority is not claimed.
