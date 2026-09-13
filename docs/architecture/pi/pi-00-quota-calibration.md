# PI-00: Codex Plus quota calibration

Status: R refinement complete; T not started; overall calibration incomplete
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

## Optional sprint D — end-to-end documentation change

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
consumption samples for Sol and Astra. Use the larger of the model-specific
observed median and largest representative delta as its initial conservative
sprint cost. Recalibrate after three comparable delivery sprints or whenever the
model/reasoning effort changes.

## Exit criteria

- Refinement and debt sprint consumption are recorded.
- A conservative weekly sprint cost is selected and justified.
- PI-01 contains only scope predicted to fit below 95% weekly use.
- Uncertainty and incomplete work are visible in the PI review.
