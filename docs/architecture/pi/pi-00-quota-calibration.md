# PI-00: Codex Plus quota calibration

Status: complete for initial R/T calibration; delivery remains uncalibrated
Milestone: establish a conservative sprint-to-weekly-quota conversion

## Guardrails

- Prefer starting immediately after the weekly Codex reset. A missed automatic
  start records its actual later start; never backdate measurements.
- Leave at least 5% of the weekly limit unused.
- Limit each sprint to 95% of two five-hour quota windows.
- Keep model and reasoning effort unchanged within each measured sprint; retain
  separate samples for Sol and Astra as required by task-model-routing.md.
- Read usage immediately before and after each sprint.
- Stop when weekly usage reaches 95%, even if work is unfinished.

## Sprint R — representative refinement

Recommended model: `gpt-6-astra`

Refine PI-01 using the supplied charter. Produce explicit decisions, unknowns,
acceptance criteria and issue boundaries. Do not implement runtime code.

## Sprint T — representative debt/refactoring

Recommended model: `gpt-5.6-sol`

Review architecture documentation for contradictions, broken links, duplicate
terminology and untracked assumptions. Correct documentation and record deferred
findings as issues.

## Optional sprint A — end-to-end documentation change

Recommended model: `gpt-5.6-sol`

If safely admitted by the weekly limit, create one accepted ADR from PI-01,
verify its links and complete it through review and merge.

## Measurement record

Personal start/end observations are recorded in ignored local automation state.
The public [budget method](pi-00-budget.md) documents sample quality and admission
without exposing account usage or reset timestamps. See the [plan](pi-00-plan.md)
and [review](pi-00-review.md) for actual delivery status.

## Calculation

For each completed sprint, calculate its weekly percentage delta. Keep separate
consumption samples for Sol and Astra and for materially different effort levels.
Until three comparable samples exist, use the largest relevant observed delta
plus an explicit uncertainty allowance. With three or more comparable samples,
record the median and largest value and retain a conservative estimate justified
from that distribution. Recalibrate whenever the model or effort changes.

## Exit criteria

- Refinement and debt sprint consumption are recorded in local state.
- The temporary conservative R/T rule is documented in the budget method.
- PI-01 delivery remains conditional rather than claiming unsupported capacity.
- Uncertainty and incomplete work are visible in the PI review.
