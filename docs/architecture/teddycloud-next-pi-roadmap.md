# TeddyCloud Next product-increment roadmap

Status: capacity-calibrated rolling plan
Scope: Greenfield control plane, Set-Tonie metadata, extensible UI plugin system,
feature migration and eventual protocol replacement

Related explorations:

- [Greenfield rewrite](greenfield-rewrite-exploration.md)
- [Set-Tonie metadata](set-tonie-metadata-exploration.md)
- [UI plugin extensions](ui-plugin-extension-exploration.md)

Prepared execution artifacts:

- [Current-system dependency matrix](current-system-dependency-matrix.md)
- [ADR template](adr/0000-template.md)
- [PI execution files](pi/README.md)
- [PI-00 quota calibration](pi/pi-00-quota-calibration.md)
- [PI-01 architecture-charter refinement](pi/pi-01-architecture-charter.md)
- [Task model routing](task-model-routing.md)

## Capacity model

This roadmap uses the Codex limits included with ChatGPT Plus. They are quota
windows, not working hours.

- `B5`: 100% of one five-hour Codex quota window.
- Sprint limit: `2 * B5 * 0.95 = 1.9 B5`.
- `BW`: 100% of one weekly Codex quota window.
- PI limit: `BW * 0.95 = 0.95 BW`.
- A PI runs from one weekly reset to the next.
- There is no documented fixed conversion from `B5` to `BW`.
- Model, reasoning effort, context and task type can change consumption.

The number of sprints in a PI is therefore dynamic. Each PI reserves these two
sprints before feature scope is admitted:

1. `R`: one refinement/exploration sprint, limited to `1.9 B5`.
2. `T`: one technical-debt/refactoring sprint, limited to `1.9 B5`.

Remaining weekly capacity may be filled with ordered delivery sprints, each also
limited to `1.9 B5`. The PI stops at 95% weekly consumption even when a sprint or
milestone is incomplete. The remaining 5% is never planned.

## PI-00: quota calibration

The first PI calibrates capacity instead of promising feature scope:

1. Record five-hour and weekly usage at PI start.
2. Execute one representative exploration sprint and record both deltas.
3. Execute one representative refactoring sprint and record both deltas.
4. If capacity remains, execute one small end-to-end delivery sprint.
5. Calculate median weekly percentage consumed per completed sprint.
6. Set the next PI's sprint count to the conservative lower confidence bound.

Calibration must be repeated after changing the primary model or reasoning
effort. Historical person-hour estimates remain useful for relative sizing, but
must not be converted directly into Codex PIs.

## Rolling capacity formula

At each weekly reset:

```text
pi_limit = 95 weekly percentage points
reserved = predicted_weekly_cost(R) + predicted_weekly_cost(T)
delivery_capacity = pi_limit - reserved
delivery_sprints = floor(delivery_capacity / conservative_sprint_cost)
```

If `R + T` cannot fit below the PI limit, reduce their scope while retaining both
outcomes. Do not borrow from the next weekly window and do not consume the 5%
reserve.

## Agent execution contract

At the start of a PI, create one issue per admitted sprint named
`PI-NN/SN — <outcome>`. Every PI must include `PI-NN/R` and `PI-NN/T`. Each issue
must contain:

- objective and non-goals;
- repository and exact components in scope;
- dependencies and required inputs;
- implementation tasks;
- acceptance criteria;
- verification commands and manual checks;
- migration and rollback impact;
- security and compatibility notes;
- resulting documentation changes.

Execution rules:

1. Do not begin delivery before the refinement decisions and acceptance criteria
   are recorded.
2. Do not declare a PI complete while required tests or migrations are missing.
3. Preserve existing APIs and data unless the PI explicitly introduces a tested
   migration and rollback.
4. Ambiguous Tonie matches remain review candidates; no first-hit fallback.
5. UI extensions use semantic slots; DOM selectors are never public contracts.
6. Record deferred findings as linked issues before closing the debt sprint.
7. If a milestone does not fit, stop adding scope and replan the unfinished work
   into the next PI.
8. Copy the task's recommended model from the model-routing matrix into its issue;
   overrides require a written rationale before execution.

## Definition of done for every PI

- The milestone acceptance test passes.
- Unit and integration tests for changed behavior pass.
- No unresolved high-severity security or data-loss finding remains.
- Upgrade and rollback paths are tested when persistence changes.
- Public contracts and operational instructions are documented.
- The branch is reviewable, CI is green and deferred work has issue IDs.

## Roadmap overview

| Phase | Candidate PI slices | Phase milestone |
| --- | ---: | --- |
| A. Architecture and evidence | 01-04 | Approved architecture and executable skeleton |
| B. Core control plane | 05-12 | Headless control-plane alpha |
| C. Set metadata and lookup | 13-17 | Unambiguous Set-Tonie metadata beta |
| D. UI plugin platform | 18-24 | Versioned plugin-platform beta |
| E. React UI and feature migration | 25-33 | Feature-complete control-plane beta |
| F. Migration and rollout | 34-38 | Production control plane on legacy gateway |
| G. Protocol replacement | 39-47 | New gateway validated on real hardware |
| H. Product hardening | 48-50 | TeddyCloud Next 1.0 release candidate |

## Candidate PI backlog

Each row is an independently demonstrable milestone candidate. The four work
columns describe refinement, two ordered delivery slices and debt work; they are
not a promise that exactly four sprints fit into one weekly quota. At refinement
time, split or combine delivery slices using PI-00 velocity. If a row does not
fit, its milestone continues in the next PI and must be reported as incomplete.

| Candidate PI | R — Refinement / exploration | Delivery slice A | Delivery slice B | T — Debt / refactoring | Milestone |
| --- | --- | --- | --- | --- | --- |
| PI-01 | Confirm scope, users, non-goals and success metrics | Write system context and domain boundaries | Record language, storage and deployment ADRs | Review terminology and remove overlapping responsibilities | Architecture charter approved |
| PI-02 | Inventory every box/API/plugin contract | Build sanitized HTTP/protobuf fixture catalog | Add initial contract-test harness | Normalize fixtures and remove secrets/duplication | Reproducible compatibility evidence exists |
| PI-03 | Refine repository and release topology | Scaffold backend, Web UI and SDK workspaces | Add CI, linting, tests and dependency checks | Pin tools and remove unsafe defaults | Empty product builds reproducibly |
| PI-04 | Refine identity invariants and migration risks | Implement domain types for Tag, Content and Version | Prototype Set, Assignment and provenance schema | Review schema normalization and migration seams | Domain/schema prototype accepted |
| PI-05 | Define database ownership and failure model | Add SQLite migration framework | Add repositories, transactions and backup/restore smoke test | Remove persistence leakage from domain layer | Versioned database can upgrade and roll back |
| PI-06 | Refine UID/rUID/auth state machine | Implement Tag Registry reads and writes | Add normalization, uniqueness and ownership rules | Property-test identifiers and simplify state transitions | Tags are managed transactionally |
| PI-07 | Refine TAF lifecycle and storage invariants | Implement content-addressed TAF storage | Add import, digest, range-read and orphan detection | Harden path handling and interrupted writes | TAF storage is safe and resumable |
| PI-08 | Define catalog sources and provenance policy | Implement Content and ContentVersion service | Add provenance/confidence and catalog import adapter | Remove parallel-array assumptions | Versioned catalog records are queryable |
| PI-09 | Refine assignment/original/custom semantics | Implement tag-to-content assignments | Add source URI validation and assignment history | Consolidate classification rules in the domain | Assignments have one authoritative service |
| PI-10 | Define library query and pagination contract | Implement indexed library queries | Add deterministic sort, search and ownership filters | Profile queries and remove N+1 access | Large libraries return stable pages quickly |
| PI-11 | Define typed event vocabulary and delivery guarantees | Implement internal event hub | Add SSE adapter, reconnect cursor and invalidation events | Test lifecycle cleanup and slow consumers | Live tag/playback events are reliable |
| PI-12 | Refine administration threat model | Implement auth, settings and secrets boundaries | Add audit log, health and diagnostics endpoints | Review authorization and redact sensitive diagnostics | Headless control-plane alpha complete |
| PI-13 | Finalize V3 metadata and Set invariants | Publish JSON Schema and fixtures | Implement schema validation and compatibility projection | Simplify identifiers and validate provenance vocabulary | V3 Set-Tonie contract frozen |
| PI-14 | Refine V1/custom/MyTonies import precedence | Implement deterministic V1-to-V3 importer | Add custom metadata and MyTonies adapters | Reconcile malformed records without silent repair | Existing metadata imports with reports |
| PI-15 | Define exact and ambiguous lookup outcomes | Implement audio-ID/hash/content indexes | Add unique-audio/model fallbacks and ambiguity results | Correct cross-pair behavior and benchmark lookups | Lookup never silently chooses conflicts |
| PI-16 | Refine set-member evidence and ordering | Implement Set and SetMember APIs | Import pilot Checker Tobi and WAS IST WAS sets | Remove title/track heuristics from authoritative path | Pilot sets expose correct independent members |
| PI-17 | Design human review and correction workflow | Implement match-candidate/review API | Add provenance-aware corrections and regression fixtures | Consolidate duplicated enrichment code | Set metadata beta is reviewable and stable |
| PI-18 | Define extension registry and command semantics | Implement typed registry and command service | Add toolbar and card-action host slots | Add deterministic ordering, cleanup and error tests | Copy command can register without DOM access |
| PI-19 | Finalize Manifest V2 and compatibility policy | Add schema validation and API-version negotiation | Implement lazy trusted-module loading | Isolate loader errors and remove app-internal imports | Compatible modules load predictably |
| PI-20 | Refine card extension contexts | Add card header, body and action slots | Migrate Copy action and card badge prototype | Review rendering performance and accessibility | Tonie cards are safely extensible |
| PI-21 | Define plugin filter/query responsibilities | Add filter and toolbar contributions | Add server-backed query contribution contract | Prevent arbitrary transforms from corrupting core state | Plugins can extend overview filtering |
| PI-22 | Refine detail/library/settings contexts | Add detail-panel and library-action slots | Add plugin settings slot and namespaced storage | Normalize context types and invalidation behavior | Major UI surfaces expose stable slots |
| PI-23 | Define public SDK surface and design tokens | Publish typed SDK and approved UI components | Add i18n, dialogs, events and notifications | Add API extractor checks and remove accidental exports | Plugin SDK has a stable compatibility contract |
| PI-24 | Refine trust modes and iframe capabilities | Implement sandboxed message bridge | Add legacy iframe adapter and compatibility test kit | Harden CSP, integrity and capability cleanup | Versioned plugin-platform beta complete |
| PI-25 | Refine application shell and responsive behavior | Build React shell, routing and navigation | Integrate theme, i18n, notifications and plugin slots | Remove duplicated providers and layout primitives | Extensible application shell is usable |
| PI-26 | Refine normal Tonies overview journeys | Build paginated Tonies view against new API | Add search, filters, selection and card details | Profile first render and stabilize component boundaries | Core Tonies overview replaces read-only manager view |
| PI-27 | Refine TAF/library workflows | Build library/file browser against content service | Add upload, download, playback and file actions | Consolidate file validation and error handling | Main library workflows operate in new UI |
| PI-28 | Map remaining Tonie Manager behavior | Migrate Custom Cards and Details toggles | Add UID/rUID multi-card presentation and preferred view | Delete duplicated manager domain logic | Separate Tonie Manager is no longer required |
| PI-29 | Refine live/current and copy workflows | Migrate Current Tonie event panel | Complete Copy-to-placed/registered-card flow | Consolidate assignment refresh and dialog handling | Current and copy plugins are native extensions |
| PI-30 | Refine unknown-content trust and review rules | Migrate unknown Tonie candidate search | Add controlled metadata enrichment and correction | Remove browser-owned catalog mutations | Unknown Tonie workflow uses authoritative APIs |
| PI-31 | Refine media-worker isolation and job model | Implement YouTube2Tonie worker contract | Migrate playlist, thumbnail, metadata and assignment UI | Harden subprocess, URL, quotas and cleanup | YouTube2Tonie runs as an isolated extension |
| PI-32 | Refine connector scheduling and credentials | Implement NFC/MyTonies connector contract | Add TAF sync, reconciliation and daily scheduling | Harden rate limiting, secret handling and idempotency | Connectors synchronize without UI patching |
| PI-33 | Run feature-gap review against current installation | Complete missing control-plane workflows | Execute end-to-end plugin and data scenarios | Remove compatibility shortcuts found in review | Feature-complete control-plane beta demonstrated |
| PI-34 | Define import inventory and reconciliation rules | Import tags, assignments, custom metadata and settings | Import TAFs/covers and generate discrepancy report | Make imports repeatable and transactional | A production snapshot imports without data loss |
| PI-35 | Inventory legacy API consumers and status codes | Implement read compatibility facade | Implement required mutation/event compatibility | Contract-test every existing plugin endpoint | Existing clients work against the new core |
| PI-36 | Refine shadow/dual-run conflict policy | Add old-vs-new read comparison | Add controlled dual-write and divergence reporting | Remove nondeterminism and noisy differences | Shadow operation produces actionable zero-loss reports |
| PI-37 | Define packaging, upgrade and rollback UX | Build installer/package and service definitions | Automate backup, migration, health check and rollback | Minimize privileges and eliminate mutable install drift | Upgrade and rollback are one documented operation |
| PI-38 | Define production cutover and abort thresholds | Deploy control plane beside legacy gateway | Run soak test and complete production cutover | Resolve soak findings and freeze compatibility baseline | New control plane runs production workload |
| PI-39 | Refine gateway boundary and protocol fixture coverage | Scaffold Go protocol gateway | Replay TLS/HTTP fixtures through adapter | Separate protocol parsing from application services | New gateway passes offline protocol harness |
| PI-40 | Refine time, claim and freshness semantics | Implement time and claim endpoints | Implement freshness protobuf handling | Fuzz parsers and normalize error responses | Basic box lifecycle works in hardware lab |
| PI-41 | Refine Content V1 authentication/cache behavior | Implement V1 content requests | Add cache, source resolution and failure fallback | Benchmark streams and remove full-file buffering | TB1 V1 content plays through new gateway |
| PI-42 | Refine V2 reverse-UID/password/range behavior | Implement V2 authentication and routing | Add byte ranges, resume and partial-content tests | Harden cancellation and interrupted transfers | V2 playback and resume pass hardware tests |
| PI-43 | Refine certificate inventory and legacy TLS needs | Implement certificate selection and validation | Add legacy TLS profile behind explicit policy | Audit key handling and isolate compatibility code | Supported old boxes establish reliable TLS |
| PI-44 | Refine RTNL framing and event semantics | Implement RTNL TLS/protobuf stream | Connect playback/tag events to core event hub | Add reconnect, malformed-frame and load tests | RTNL works without the legacy daemon |
| PI-45 | Refine TB2/V3 endpoint coverage | Implement required TB2/V3 requests | Add fixtures and hardware verification | Consolidate shared TB1/TB2 protocol primitives | Supported TB2 scenarios pass |
| PI-46 | Refine OTA, logging and cloud-proxy scope | Implement required OTA/log operations | Add explicit cloud proxy/reset behavior | Remove unnecessary proxy surface and test limits | Operational box functions reach parity |
| PI-47 | Define device matrix and cutover gates | Run all supported hardware/endurance tests | Switch production traffic with rollback guard | Resolve protocol debt and freeze fixture baseline | New protocol gateway replaces C gateway |
| PI-48 | Refine performance SLOs and failure scenarios | Optimize library, event and streaming hot paths | Run power-loss, disk-full and network-failure tests | Remove bottlenecks and flaky tests | Product meets performance and resilience SLOs |
| PI-49 | Refine final threat/licensing/supply-chain review | Resolve security and dependency findings | Complete SBOM, signatures and license notices | Simplify privilege boundaries and dead code | Release passes security and compliance gate |
| PI-50 | Refine release checklist and support model | Complete user/admin/plugin developer documentation | Build, install and validate release candidate | Final architecture cleanup and deferred-issue audit | TeddyCloud Next 1.0 release candidate published |

## Phase gates

### Gate A — after candidate slice PI-04

- Domain vocabulary and architecture ADRs are approved.
- Compatibility fixtures are sanitized and reproducible.
- The project builds and tests from a clean checkout.

### Gate B — after candidate slice PI-12

- Tags, content, assignments, library queries and events operate headlessly.
- Database upgrade, backup and rollback are demonstrated.

### Gate C — after candidate slice PI-17

- Set members have independent content identities and versions.
- Exact pair lookup and explicit ambiguity are covered by regression tests.

### Gate D — after candidate slice PI-24

- Trusted and sandboxed plugin modes are documented and tested.
- A plugin can add a Copy action and filter without touching the DOM.

### Gate E — after candidate slice PI-33

- Required current plugins have a mapped native module or isolated worker.
- The separate Tonie Manager is unnecessary for normal operation.

### Gate F — after candidate slice PI-38

- The new control plane runs the production dataset with the legacy gateway.
- Rollback is tested and reconciliation reports no unexplained data loss.

### Gate G — after candidate slice PI-47

- The new gateway passes protocol fixtures and the supported hardware matrix.
- Production can return to the legacy gateway without restoring data.

### Gate H — after candidate slice PI-50

- Installation, upgrade and rollback documentation is verified from scratch.
- Security, license, performance and resilience release gates pass.

## Replanning policy

At every phase gate, compare completed milestone slices and quota consumption:

- Use the median of the last three comparable sprints for admission control.
- Admit scope only while `predicted weekly use <= 95%`.
- Pull only already-refined work when the PI has measured spare capacity.
- If projected completion changes by more than 20%, write a scope/architecture
  decision record; do not silently reduce tests, migration safety or protocol
  coverage.
- The protocol replacement phase may be postponed indefinitely without blocking
  the production control-plane milestone represented by candidate slice PI-38.
