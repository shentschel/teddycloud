# PI-01 refinement: architecture charter

Status: R/B implementation baseline recorded; final consistency review remains T
Candidate milestone: architecture charter accepted with explicit evidence gates

## Objective and authority

Define a self-hosted successor that combines a new control plane, correct
Set-Tonie identity and an extensible standard UI. Primary users are the household
administrator, listeners using existing boxes, and maintainers of the seven
enhancement repositories. Household playback and assigned Custom Cards are the
compatibility baseline; the administrator also needs reliable catalog correction,
media conversion and repeatable installation.

The product owner decides scope and accepts hardware-support limitations. The
executing architecture agent owns proposals, traceability and evidence. A merged
refinement is not evidence that hardware compatibility or a migration works.
PI-01/B records final architecture decisions in ADRs with their acceptance basis.

## Inputs and evidence boundary

- [Greenfield exploration](../greenfield-rewrite-exploration.md)
- [Set metadata exploration](../set-tonie-metadata-exploration.md)
- [UI plugin exploration](../ui-plugin-extension-exploration.md)
- [Dependency matrix](../current-system-dependency-matrix.md)
- [Roadmap](../teddycloud-next-pi-roadmap.md)
- [Executable backlog and deferred findings](pi-01-backlog.md)

Source baseline for this refinement: `aee1395`. The dependency matrix is a
planning snapshot; enhancement implementations and the live installation have not
been re-audited during this sprint. Existing catalog statistics are historical
exploration evidence, not a current inventory.

Three implementation observations constrain the future design:

1. [tonies_byAudioIdHash_base](../../../src/toniesJson.c) iterates every hash
   after finding an audio ID in a record. Crossed pairs can match. A new schema
   alone does not repair existing lookup behavior.
2. [getTagInfoJson](../../../src/handler_api.c) calls `saveTonieInfo`. Reading
   the legacy index/info API cannot be assumed to be a side-effect-free shadow
   import. Validate against a disposable snapshot before live comparisons.
3. The same API exposes `hasCloudAuth` using auth state and `cloud_override`,
   and may expose `sourceInfo` separately from `tonieInfo`. Catalog origin,
   displayed metadata, content assignment and cloud eligibility are distinct.

## Confirmed requirements and planning decisions

The [PI-01 execution plan](pi-01-plan.md) confirms scope and journey priority.
The [reference environment](pi-01-reference-environment.md) records observed
deployment facts and the unverified browser/device evidence. Correctness and
data-preservation gates are accepted requirements; numerical UI performance
targets remain provisional until PI-10/26 establishes the specified baseline.

| ID | Status | Rule and consequence |
| --- | --- | --- |
| C-01 | Product requirement | Preserve existing box playback while the management plane evolves. Replace the gateway only after hardware evidence and rollback gates. |
| C-02 | Product requirement | A physical Tag, logical Content, ContentVersion and Set are separate concepts. Set track descriptions do not prove member/version identity. |
| C-03 | Product requirement | Preserve known Custom Cards, assignments and user-curated metadata through enrichment, deduplication and migration. Catalog custom entries do not imply Custom Card hardware. |
| C-04 | Product requirement | Keep one usable preferred Original per resolved content/member: cloud-auth eligible, existing usable TAF, latest verified audio version; a stable rUID tie-break is acceptable for equals. Never infer latest from the largest audio ID alone. |
| C-05 | Product requirement | Standard Tonies and Library pages support Copy, filters, details and badges through versioned semantic extension slots. Classification and identity resolution belong in the core. |
| C-06 | Planning choice | Prefer control-plane-first, a modular core, and isolated media/connector workers. PI-01/B must compare alternatives and record the gateway boundary. |
| C-07 | Implementation baseline | Go backend, React/TypeScript UI, generated SDK, SQLite state and immutable local blobs; ADR-0002/0003 record topology and recovery. Exact versions and final V3 schema remain later decisions. |
| C-08 | Implementation baseline | Start with a Linux systemd service on the observed Debian 13 x86-64 LXC reference; container packaging follows qualification. Browser/runtime pins and backup tests remain evidence tasks. Windows/macOS are administration clients. |

C-04 governs preference and explicit cleanup planning, not automatic deletion
during metadata lookup. Distinct set members and unresolved matches cannot be
merged because they share a title/model. An eventual destructive cleanup must
enumerate targets, dependencies and recovery before execution; it must preserve
Custom Cards. An empty source string does not by itself prove a missing TAF.

## Mandatory journeys and acceptance scenarios

These are product targets to implement and measure in later PIs. This refinement
defines the checks; it does not claim they already pass.

| ID | Given / when | Required result | Evidence owner |
| --- | --- | --- | --- |
| J-01 | Original placed, removed, then another placed | Correct current tag and playback state; obsolete lookup responses cannot restore a removed tag | PI-02 fixtures; PI-11/29 integration |
| J-02 | Copy to a placed or registered Custom Card | Assignment readback resolves the intended TAF; failure leaves the previous assignment recoverable; unrelated cards do not change | PI-09/29 |
| J-03 | Set with four independent members and multiple versions per member | Independent member identity/title/cover; verified model or explicit unknown; actual audio tracks remain distinct from member names | PI-13/16/17 |
| J-04 | Exact, crossed or conflicting audio-ID/hash observations | Only an exact supported pair resolves automatically; conflicts stay explicit; no first-result fallback | PI-04/15 |
| J-05 | Same title/model on several originals plus owned Custom Cards | One preferred eligible Original for the resolved content; every owned Custom Card remains addressable, including multiple cards using one TAF | PI-09/10/28 |
| J-06 | YouTube playlist with more than 99 tracks | At most three card parts, visible limit, part-specific episode metadata and thumbnail; optional single TAF without chapter divisions | PI-31 |
| J-07 | NFC/MyTonies sync repeats or download is interrupted | Idempotent results, bounded retries, authenticated download where permitted, source/TAF reconciliation and explicit unresolved items | PI-32 |
| J-08 | Plugin adds Copy or a Custom Cards filter to the core UI | Uses public slots/commands; no DOM patching; disable removes handlers; one plugin failure leaves the core usable | PI-18/21/24/28 |
| J-09 | Import, backup, upgrade and rollback on a disposable snapshot | Zero unexplained loss of tags, assignments, TAF digests and curated metadata; repeated import is stable | PI-34/37/38 |

Series and episode are separate curated fields: preserve a verified set series
label while showing the individual member episode and cover. A synthetic
`content_id` must never be displayed as an official model number. If one exact
audio pair appears under multiple products, retain the conflict and provenance
until identity policy is decided; do not invent uniqueness by deleting a record.

When a hash is supplied and conflicts, a weaker audio-ID/model fallback must not
erase that conflict. When a hash is genuinely unavailable, unique audio-ID or
verified model evidence may narrow candidates under the later lookup policy;
a model alone does not establish the latest playable version.

## Ownership and extension boundaries

| Enhancement repository | Primary target owner | Supporting boundary / preserved behavior |
| --- | --- | --- |
| `teddycloud-plugin-common` | Public SDK | Generated clients/types; authoritative normalization, classification and writes in core |
| `teddycloud-tonie-manager` | Core Tonies/Library UI | Preferred queries in core; Custom Cards and Details toggles; all physical IDs accessible |
| `teddycloud-current-tonie` | UI extension | Typed events and shared Copy command; assignment writes in core |
| `teddycloud-unknown-tonie` | Core catalog review | Extension for candidate selection; core provenance and controlled corrections |
| `teddycloud-youtube2tonie` | Media worker | UI extension for jobs; core validates imported content and assignments |
| `teddycloud-nfc-sync` | Connector worker | Scheduled jobs, MyTonies/NFC adapters; scoped credentials; writes through core |
| `teddycloud-enhancements` | Release/installer | Versioned bundle, deployment compatibility, backup and rollback |

The core owns layout, pagination and validated query semantics. Plugins may
contribute filters, not mutate canonical identity or silently remove items.
Trusted same-origin modules have application-level browser privileges; manifest
permissions alone are not a sandbox. Untrusted modules need the isolated bridge.

## Compatibility contract to inventory in PI-02

- Legacy C gateway retains device-facing TLS, claim, freshness, content and RTNL.
  TB1 and TB2 appear in source, but neither generation is newly certified by this
  plan. Record actual device/firmware evidence and exclusions before any cutover.
- Capture `/api/getTagIndex`, `/api/getTagInfo`, `/content/json/get/`,
  `/content/json/set/`, catalog/custom catalog operations, `/api/sse`, content
  downloads, media jobs, settings and overlays used by current clients. Routes
  are observable in [src/server.c](../../../src/server.c); inventory is provisional.
- Preserve response wrappers (`tags`, `tagInfo`, `tonieInfo`, `sourceInfo`),
  UID/rUID byte order, source URIs, error semantics and event envelopes through a
  compatibility adapter. Authentication flags require semantic mapping, not a
  blind rename from `hasCloudAuth` to `cloud_auth`.
- Inventory writes made by the gateway and by legacy GET handlers. New storage
  cannot become authoritative until one writer owns each operation and legacy
  write-back is reconciled. No simultaneous uncontrolled file and database writers.
- Existing iframe plugins need an explicit legacy mode. Public SDK versions and
  deprecations are independent of internal React components and application paths.

## Measurable targets and missing baselines

| Measure | Proposed gate | How to establish evidence |
| --- | --- | --- |
| Lookup correctness | All accepted positive/negative fixtures pass; zero automatic ambiguous or crossed-pair assignments | Synthetic fixtures, then sanitized observed pairs; include uneven legacy arrays and shared audio |
| Data preservation | Zero unexplained differences in protected assignments and content digests after import/rollback | Before/after inventories on disposable data; compare semantic state as well as counts |
| Overview interaction | Proposed p95 toggle feedback below 100 ms and data update below 300 ms on a 10,000-tag synthetic dataset | PI-01/R records reference browser/server; PI-10/26 measures cold and warm cases separately |
| Live events | No stale current-tag after a completed removal event; reconnect reconciles authoritative state | Ordered, late-response and reconnect fixtures; latency target deferred until measured |
| Extension lifecycle | Disable/unmount leaves zero registrations/subscriptions; incompatible versions do not load | Host/SDK compatibility tests and a failing-plugin fixture |

Performance numbers are proposed acceptance thresholds, not observed results or
capacity claims. PI-01/R must record a reference environment and either accept or
adjust them before they become release gates.

## Ordered execution and exit gate

Use [PI-01/R, A, B and T](pi-01-backlog.md). `A` equals the former D1 slice;
`B` equals D2. A was delivered early as PI-00/A and revalidated by PI-01/R;
B consumes those reviewed boundaries without repeating A; T closes
the consistency review. Independent evidence collection may run in parallel;
agents must not concurrently rewrite the same charter/ADR.

PI-01 may be called complete only when:

- Scope, non-goals, reference deployment and hardware evidence limits are explicit.
- All seven enhancement owners and J-01 through J-09 have traceable contracts.
- Accepted ADRs cover sequencing, technology/deployment/repositories, and
  persistence/write ownership with compatibility and rollback consequences.
- Every deferred concern has an executable record; upstream coordination and
  licensing assumptions have owners and do not masquerade as approvals.
- Required checks pass; any absent CI or hardware evidence is reported.
- The next slice is admitted from completed, model-specific quota observations.

## Non-goals, migration and rollback

Accepted implementation decisions: [gateway sequencing](../adr/0001-control-plane-and-gateway-sequencing.md),
[technology/topology](../adr/0002-technology-and-repository-topology.md), and
[persistence/recovery](../adr/0003-persistence-and-projection-recovery.md).
Acceptance is by the executing architecture agent within the authorized roadmap;
it does not represent a production cutover decision or upstream approval.

PI-01 changes documentation only. It does not implement runtime code, deploy to
the host, modify live catalog/TAF data, replace the gateway, or freeze low-level
APIs/tables. A documentation revert restores the prior plan.
