# PI execution backlog and deferred findings

Status: issue-ready local registry; GitHub Issues are disabled (HTTP 410)

Stable IDs below are task IDs, not GitHub issue numbers. Once Issues are enabled,
search by exact ID, create only missing issues using the repository sprint
template, and record their URLs here. Do not copy personal usage measurements.
Every task is documentation-only unless a future PI explicitly admits implementation.

## Shared execution contract

Repository: `shentschel/teddycloud`. Inputs: the
[refined charter](pi-01-architecture-charter.md),
[dependency matrix](../current-system-dependency-matrix.md),
[model routing](../task-model-routing.md) and three linked explorations.
All tasks must include source/evidence, explicit unknowns, verification results
and deferred findings. Run `git diff --check` and check relative links in changed
Markdown. No production writes, credentials, migrations or hardware certification.
Rollback is a documentation revert. Personal quota records remain local.
Budget ceiling: 1.9 B5 per sprint and 5% weekly reserve. Estimates below are relative
scope, not invented quota percentages; delivery admission awaits PI-00 calibration.

## PI-00/R — Refine the architecture charter

- Model: `gpt-6-astra`; type: refinement; size: bounded; state: complete locally;
  GitHub issue publication deferred to F-05.
- Output: refined charter, this backlog, PI-00 plan/budget/review and local state.
- Dependencies: existing explorations at `aee1395`.
- Tasks: reconcile user journeys; map all seven owners; distinguish decisions
  from proposals; define PI-01 slices and review source-level assumptions.
- Done: charter acceptance scenarios, owners and evidence gaps are explicit;
  scoped checks pass; refinement published; before/after observation recorded.
- Not included: final architecture ADR acceptance or runtime implementation.

## PI-00/T — Review architecture documentation and calibration consistency

- Model: `gpt-5.6-sol`; type: debt; size: bounded; state: reserved.
- Inputs: published PI-00/R; findings F-04 and F-05 below.
- Output: corrected `docs/architecture/` terminology/links/estimates and updated
  PI-00 review. Do not rewrite accepted product requirements.
- Tasks: audit the roadmap and routing against actual R/A/B/T/D names; distinguish
  proposal from contract; reconcile quota formulas with model-specific evidence;
  identify remaining contradictions and publish issue records when available.
- Done: every discrepancy corrected or assigned to a linked task; docs checks
  recorded; local start/end usage measured; no unsupported velocity claim.
- Parallelization: independent file audits allowed; one agent owns final edits.

## PI-01/R — Confirm scope and evidence gates

- Model: `gpt-6-astra`; type: refinement; size: small-to-medium; state: candidate.
- Dependencies: PI-00 R/T calibration and admission; F-01 environment evidence.
- Output: `pi-01-plan.md`, `pi-01-budget.md`, `pi-01-review.md` and final scope
  section in `pi-01-architecture-charter.md`.
- Tasks: confirm reference deployment/hardware limits; prioritize J-01 through
  J-09; accept or revise proposed performance thresholds; admit A/B only after
  reserving T and documenting live capacity.
- Done: no unspecified hardware promise; every target has an evidence owner;
  all admitted work fits the conservative allowance or is explicitly deferred.
- Not included: repeating PI-00 exploration or obtaining live credentials.

## PI-01/A — Define system context and ownership

- Model: `gpt-5.6-sol`; type: delivery; size: medium; state: executing early as
  the bounded PI-00/A calibration slice.
- Dependencies: PI-01/R; F-02 writer inventory.
- Output: `docs/architecture/system-context.md` and
  `docs/architecture/component-ownership.md`.
- Tasks: draw gateway/core/UI/worker/external-service context; list data, event,
  credential and failure flows; assign one authoritative writer per operation;
  map each legacy repository and provisional API contract to an owner.
- Done: all seven enhancements covered; no direct worker database writes; legacy
  GET side effects represented; Copy flow and disconnected connector flow can
  each be followed end-to-end without overlapping mutation owners.
- Verification: compare against matrix and charter J-01/J-02/J-07; review one
  successful and one interrupted assignment flow on paper.
- Parallelization: context draft and endpoint inventory can proceed separately;
  merge by one owner before B consumes them. Former name: D1.

## PI-01/B — Record foundational architecture ADRs

- Model: `gpt-6-astra`; type: delivery; size: medium-to-large; state: candidate.
- Dependencies: reviewed PI-01/A; F-01/F-02 evidence; F-03 domain constraints.
- Outputs: ADRs under `docs/architecture/adr/` using `0000-template.md` for
  (1) control-plane/gateway sequencing, (2) technology/repository/deployment
  topology, and (3) persistence and write ownership/compatibility.
- Tasks: compare at least two credible options per decision; state consequences,
  evidence limits, licensing implications and rollback/cutover gates. Existing
  enhancement repos remain supported during migration; do not silently merge or
  replace them merely because the new core might use a monorepo.
- Done: accepted ADRs name the decision owner and acceptance basis; unknowns are
  explicit gates; no uncontrolled dual writer; SQLite/blob consistency and secret
  ownership are assigned; no final V3 schema or hardware claim inferred from an ADR.
- Verification: walk J-02/J-03/J-09 through the proposed boundaries; check every
  rejected alternative has a reason and every deferred concern has a task.
- Parallelization: draft options independently, but accept sequencing first and
  then resolve dependent storage/topology decisions. Former name: D2.

## PI-01/T — Close the architecture consistency review

- Model: `gpt-5.6-sol`; type: debt; size: bounded; state: mandatory candidate.
- Dependencies: admitted PI-01 deliverables; if a slice is deferred, review the
  delivered portion and mark the PI milestone incomplete.
- Output: consistent charter, ownership matrix, ADR links and PI-01 review.
- Tasks: reconcile Tag/Content/Version/Set vocabulary; check SDK/core boundaries;
  ensure all deferred risks retain task IDs; verify every acceptance claim against
  an artifact; document next-PI admission without inventing throughput.
- Done: no unexplained contradiction or missing owner; all local checks pass;
  actual CI state reported; milestone accepted or explicitly incomplete.

## F-01 — Establish hardware, deployment and performance evidence

- Priority: P1; owner: PI-01/R agent; hardware observations: product owner.
- Model: `gpt-6-astra`; dependencies: existing device inventory if available.
- Scope: document actual box generation/firmware, OS/CPU/service/container/storage
  baseline and browser test profile without private IDs, keys or certificates.
- Done: evidence matrix says observed, fixture-only or unverified for every
  supported workflow; exact reference profile exists for performance tests.
- Blocks: support commitments in PI-01/B; hardware cutover in PI-38/47.
- Does not block: writing the bounded context/ADR proposals.

## F-02 — Inventory legacy writes before shadow import

- Priority: P1; owner: PI-01/A, completed with PI-02 contracts.
- Model: `gpt-6-astra` for ambiguous ownership analysis; bounded inventory may
  move to Sol only with documented routing rationale.
- Evidence: `getTagInfoJson` calls `saveTonieInfo` in `src/handler_api.c`.
- Tasks: trace index/info/content/claim side effects; document which gateway
  operations write JSON/cache/auth state; define a disposable-snapshot capture
  procedure and reconciliation reports before production API comparison.
- Done: each operation has an authoritative owner, side effects and replay
  policy; snapshot comparison detects unexpected writes; no assumed read-only GET.
- Blocks: authoritative-store cutover and live shadow-import claims.

## F-03 — Specify collision, version and Set-member evidence

- Priority: P1; owner: PI-04/R and PI-15/R; input to PI-01/B.
- Model: `gpt-6-astra`; evidence: pair lookup in `src/toniesJson.c`.
- Tasks: cover exact/crossed pairs, unequal arrays, shared audio under multiple
  products, conflicting verified models, unknown member models and unordered
  version history. Distinguish file digests from legacy header/catalog hashes.
- Done: synthetic positive/negative scenarios state resolved, ambiguous or
  unknown; latest-version rule has provenance; zero fabricated official models;
  Custom Card protection and set series/member episode/cover survive correction.
- Blocks: automatic cleanup/migration or definitive V3 schema acceptance.
- Non-goal: fixing the legacy C lookup during this documentation sprint.

## F-04 — Reconcile exploratory contracts and quota terminology

- Priority: P2; owner: PI-00/T; model: `gpt-5.6-sol`; state: complete.
- Tasks: resolve the V3 sketch's duplicated membership (`content.set` versus
  `sets.members`); mark cardinality as unresolved until PI-04/13. Reconcile
  `tonies.query.filters` with server-owned pagination; it cannot remove arbitrary
  records or decide identity. Normalize D1/D2 versus A/B and PI-00 optional A;
  avoid describing one measurement as a statistical confidence bound.
- Done: `sets[].members[]` is the sole proposed membership relation while its
  cardinality remains open; filter contributions use validated server queries;
  R/A/B/T naming and model-specific calibration rules are consistent.

## F-05 — Enable issue tracking and provide proportional documentation checks

- Priority: P2; owner: repository maintainer for settings, PI-00/T for docs checks;
  state: docs check complete, issue-setting work open and non-blocking.
- Model: `gpt-5.6-sol` for bounded implementation after admission.
- Evidence: GitHub returns HTTP 410 for issue creation; current workflows target
  full builds/container publication and have no dedicated Markdown link check.
- Tasks: enable Issues through an authorized repository-settings action and
  publish the exact task IDs idempotently. The path-scoped docs check is now
  implemented in `scripts/check_architecture_docs.py` and its dedicated workflow.
- Done: IDs link to real issues and documentation changes have an appropriate
  validation path; no personal quota metadata is published.
- Does not block: reviewable Git commits with this local task registry.
