# PI-00: Codex Plus quota calibration

Status: prepared
Milestone: establish a conservative sprint-to-weekly-quota conversion

## Guardrails

- Start immediately after the weekly Codex reset.
- Leave at least 5% of the weekly limit unused.
- Limit each sprint to 95% of two five-hour quota windows.
- Keep model and reasoning effort unchanged throughout calibration.
- Read usage immediately before and after each sprint.
- Stop when weekly usage reaches 95%, even if work is unfinished.

## Sprint R — representative refinement

Refine PI-01 using the supplied charter. Produce explicit decisions, unknowns,
acceptance criteria and issue boundaries. Do not implement runtime code.

## Sprint T — representative debt/refactoring

Review architecture documentation for contradictions, broken links, duplicate
terminology and untracked assumptions. Correct documentation and record deferred
findings as issues.

## Optional sprint D — end-to-end documentation change

If safely admitted by the weekly limit, create one accepted ADR from PI-01,
verify its links and complete it through review and merge.

## Measurement record

| Point | Time | Model/effort | 5h used | Weekly used | Notes |
| --- | --- | --- | ---: | ---: | --- |
| PI start | | | | | |
| R start | | | | | |
| R end | | | | | |
| T start | | | | | |
| T end | | | | | |
| D start | | | | | optional |
| D end | | | | | optional |

## Calculation

For each completed sprint, calculate its weekly percentage delta. Use the larger
of the observed median and the largest representative delta as the initial
conservative sprint cost. Recalibrate after three delivery sprints or whenever
the model/reasoning effort changes.

## Exit criteria

- Refinement and debt sprint consumption are recorded.
- A conservative weekly sprint cost is selected and justified.
- PI-01 contains only scope predicted to fit below 95% weekly use.
- Uncertainty and incomplete work are visible in the PI review.

