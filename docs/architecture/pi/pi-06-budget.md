# PI-06 budget and admission

Status: R design accepted; delivery requires fresh admission.
All numbers below are uncertain planning bands or authorized ceilings, not
personal measured usage. Five-hour and weekly points are independent units.

## Separate allowances

| Work | Model / reasoning | Five-hour points | Weekly points | Interpretation |
| --- | --- | ---: | ---: | --- |
| R design checkpoint | `gpt-6-astra` / high | 8–12 target; 36 hard maximum | 1–3 estimate; 6 hard maximum | Useful reviewable files should exist by the first checkpoint; exploration must stop if scope grows |
| Parent R integration | Parent-selected | 8 maximum | 2 maximum | Separate from the R agent ceiling |
| A1 domain/ports | `gpt-5.6-sol` / high | 8–14 | 1–3 | Historical pre-execution band; A1 accepted |
| A2 persistence seam | `gpt-6.1-sol` / high | 12–20 | 2–4 | Legacy Sol 5.6 band only; re-estimate before admission |
| B1 metadata/evidence | `gpt-6.1-sol` / high | 12–22 | 2–4 | Legacy Sol 5.6 band only; re-estimate before admission |
| B2 lifecycle/recovery | `gpt-6.1-sol` / high | 12–24 | 2–5 | Legacy Sol 5.6 band only; re-estimate; #18 may block acceptance |
| Future T planning | `gpt-6-astra` / high | 40 | 8 | Five-hour estimate used when T executes; weekly reserve protected from R/A/B |

R, parent integration and T sum to up to 84 five-hour points and
16 weekly points in aggregate planning, not a current five-hour reservation. This is not a grant of live capacity,
a shared agent budget, or an estimate that all work fits the current window.
A/B parent integration and any external #18 work require separate estimates
and admission; they are not silently included in R or the T reserve.

## Model transition

PI-06/A1 and R are historical and keep their actual model/effort. A2, B1 and B2
now route to Sol 6.1; their numeric bands above are pre-switch planning
comparators, not an admission ceiling for the new model. At the next eligible
window, establish a bounded Sol 6.1 checkpoint and record both live-window
deltas including parent integration, verification and uncertainty. Until a
comparable complete interval exists, use the larger relevant integrated sample
plus an explicit uncertainty margin. Do not infer any Codex Plus quota saving
from API token prices or model marketing. Keep the future Astra T reserve and
five-percent safety unchanged.

## Measurement and stop rule

The parent owns the private limit ledger and admission. Sample both available
windows before/after bounded work and at checkpoints, storing observations only
in private orchestration state. Shared-account movement includes concurrent
parent/other work and cannot be claimed as this agent's actual consumption.
Check availability without copying account details into public artifacts.

Calibrate from integrated account-wide intervals grouped by work type and
routed model/reasoning. Record sample quality privately: complete comparable
interval, mixed concurrent work with known overlap, or incomplete/unavailable.
Complete integrated intervals remain usable even though parent orchestration,
review and checks are included; isolated agent consumption is not required.
Known overlap lowers attribution quality and increases the uncertainty margin;
never subtract guessed concurrent usage or label account movement agent usage.

Until three comparable usable intervals exist, use the largest relevant
integrated sample or provisional estimate plus an explicit quality-dependent
margin, independently for both windows. Then apply the roadmap's rolling
calibration, retaining the quality labels and conservative allowance for mixed
work. Do not double-count parent integration already included in a calibrated
interval; the separate authorization ceilings above still apply. Incomplete
samples remain unavailable, never zero; elapsed minutes and token counts are
not a quota conversion. Public files retain methodology and uncertain bands,
not personal measurements.

At the first checkpoint, hand off files even if review is pending. On scope
growth, stop exploration, document the unresolved decision and return a runnable
documentation checkpoint. Never consume the parent or T allowance to extend R.
At either R ceiling, stop and report remaining work; do not reduce acceptance
tests or mark the design accepted to fit the budget. If limits become unavailable,
hand off the current checkpoint and let the parent re-admit work.

Follow the roadmap's sprint/weekly ceilings and leave its five-percent reserve.
The parent must fit current work, integration, uncertainty and five-percent
safety inside the live five-hour capacity. Future mandatory R/T costs are
protected only in the weekly gate; do not deduct them again in an earlier
five-hour window. Current R/T work is not counted twice as a future reserve. Reset credits, purchased capacity
and future resets are not presumed. No personal actual-use figures, reset times,
execution identifiers or account information belong in these files.

## Current checkpoint disposition

R supplies an independently reviewed plan, contract, budget and review.
The parent records the completed integrated interval and quality privately;
no isolated agent consumption or delivered implementation is inferred.
The early-checkpoint target was not met; draft review required an explicit
bounded correction and fresh publication-only admission. Future Astra
refinements use this larger comparable interval plus uncertainty instead of
reusing the optimistic early target. This does not authorize A, B, T or #18.
If FK recovery or the bounded evidence representation needs broader design,
return that decision to refinement under a fresh admission instead of growing
the current job.

## A1 checkpoint calibration

The initial A1 band is a forecast, not an unused allowance after identity delivery.
The identity/service checkpoint produced an integrated comparable sample;
subsequent work is re-estimated by scope and quality, not admitted under a reset
of that forecast. The fixed two-file codec is a smaller work class, capped at
6/1 agent points plus 4/1 integration and 2/1 uncertainty.
Remaining aggregate-bound work provisionally uses a 35/6 integrated ceiling,
including parent review/verification, derived conservatively from the completed
service interval with independent uncertainty factors. Do not add parent overhead
again to that integrated estimate. It is not isolated agent billing or a promise.
The future T reserve is weekly only; five-percent safety remains required in
both windows. The 35/6 integrated job therefore needs 40 five-hour points and
19 weekly points while the future T weekly reserve remains 8.
If actual representation design broadens the remaining scope, refine/re-route
it first and update this ceiling rather than treating an empty aggregate or
caller-supplied byte count as proof of bounded decoding.

## Count-only follow-up scope

Actual-slice count preflight is a smaller settled implementation subset: agent
ceiling 12/2 plus parent 8/2 and uncertainty 4/1, independently by window.
It does not consume a new A1 allowance or accept encoding/storage behavior.
The remaining encoded representation must be refined before implementation
if it introduces an architectural decision, using the largest comparable
integrated Astra refinement sample plus uncertainty and the existing T reserve.
Do not use the small codec/count work class to discount architecture work.

## Bounded encoding refinement

The retained representation and exact size accounting are an architectural
question, so this narrow follow-up is routed to `gpt-6-astra` before execution.
Its small uncalibrated expenditure cap is 22/4 for the agent, 10/2 for parent
review/publication and 8/2 uncertainty: 40/8 integrated, plus protected T and
final safety reserves under the active window-specific policy. This buys a reviewable partial design checkpoint, not
an estimate or authorization to finish a whole refinement under that ceiling.
Produce an early usable handoff and stop on scope growth. Keep any unresolved
representation questions explicit; Sol implementation requires accepted design
and separate live admission. Do not discount full architecture work using the
codec or count-gate sample class.

This admission rule follows the [roadmap's active policy](../teddycloud-next-pi-roadmap.md).
