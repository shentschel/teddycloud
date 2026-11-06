# PI-09 budget and admission

Status: B1 locally complete and published with exact-head CI green at the
supplied baseline. B2 replan only. Implementation admission and milestone
acceptance for B2 remain pending.
Original request `pi09-r-assignment-semantics-contract-20261031`.
Use the exact [active roadmap policy](../teddycloud-next-pi-roadmap.md#admission-by-window-active-policy)
and [model routing](../task-model-routing.md).

## Units, ceilings and accounting

Percentage points of the five-hour (B5) and weekly (BW) windows are independent;
100 points is one full window, not labor time. No fixed B5/BW conversion exists.
This R's whole-job ceiling is **22 additional weekly points**, including source
review, writing, integration, verification, commit and handoff. It must also fit
the parent's available **90 five-hour points**. These are authorized maximums,
not live availability, predicted consumption or an allocation to spend fully.
The live-window test below can impose a strictly tighter cap.

Exactly **5 percentage points of each global window remain free**, not 5% of
remaining capacity or 5% per child. Do not subtract the reserve twice or allow a
child budget to spend it. Stop planned consumption at 95 weekly points; each
sprint remains <=1.9 B5 even if its checkpoints span verified five-hour windows.
A label/model/chat change gives no fresh quota. Never consume reset credits,
API/bought credits, hypothetical resets or future-week capacity.

For the next bounded whole checkpoint, including closeout:

```text
R_spent_weekly + integrated_remaining_R_weekly <= 22
R_spent_five_hour + integrated_remaining_R_five_hour <= parent_allocation_remaining
five_hour_remaining >= integrated_next_job_five_hour
                       + outstanding_commitments_five_hour + 5
weekly_remaining >= integrated_next_job_weekly
                    + outstanding_commitments_weekly
                    + remaining_future_mandatory_R_T_weekly + 5
```

The parent allocation is at most the stated 90 points, reduced by already charged
work/commitments; it does not replace the live global checks. The same integrated
estimate is tested against both applicable caps, not charged twice. Include parent
orchestration/review/integration exactly once inside the job. Outstanding means
other committed work excluded from that estimate. Do not reserve the current R
again in future R. Move a mandatory T estimate from future reserve into current
work when T starts; never release it by renaming/skipping T. Future T holds no
five-hour reserve in today's window, but admitted scope protects it weekly.

## Integrated planning estimates

These are conservative initial planning envelopes, not measurements or promises.
Components include the full task, not an extra allowance atop a parent's estimate.
Intervals in the two windows are independent judgments, not a conversion ratio.

| This R component | Five-hour points | Weekly points |
| --- | --- | --- |
| Evidence review and contract writing | 16-28 | 3-6 |
| Parent integration/review and scope reconciliation | 4-8 | 1-2 |
| Checks, fixes, commit and handoff | 4-8 | 1-2 |
| Uncertainty, including runner failure | 4-8 | 1-2 |
| **Integrated R total** | **28-52** | **6-12** |

Use the upper end for admission until comparable calibration supports less.
A measured delta already incurred reduces remaining work, not the total baseline.
For this R-only admission, provisionally protect **4 weekly points** for a later
bounded mandatory T audit/replan; that is not admission of implementation T after
an unknown amount of A/B work. Re-estimate and enlarge that reserve before A/B is
admitted. Any other parent weekly commitments must also fit; this task neither
claims them absent nor consumes their allocation. If the parent cannot establish
safe remaining capacity, close a reviewable checkpoint before expansion.

| Later checkpoint | Model / work class | Estimate requirement before dispatch |
| --- | --- | --- |
| A1 | Sol 6.1 high; domain/application | Independent integrated B5/BW intervals for exact [A1 manifest](pi-09-plan.md), golden vectors, tests, parent review and uncertainty. |
| A2a | Sol 6.1 high; staged schema, complete | Historical actuals remain charged; no new A2a allocation. |
| PI09-A2b.1 | Sol 6.1 high; inert repository | Independently estimate the two-file read/reconstruction checkpoint, canonical fixtures, five focused tests, common gates and closeout. |
| PI09-A2b.2 | Sol 6.1 high; inert transaction components | Independently estimate the four-file checkpoint, real SQLite race/fault/scope fixtures and required regression gates. |
| PI09-A2b.3 | Sol 6.1 high; coordinated activation | Independently estimate the entire final manifest, exact v9/v10/v11 matrix, real owner/bypass tests, restart/restore smoke and inherited regressions. No split activation admission. |
| B1 | Sol 6.1 high; pure URI admission, complete | Historical actuals remain charged. No new B1 allocation. |
| PI09-B2.1 | Sol 6.1 high; history and integrity | Estimate bounded read fixtures, stale cursors, tombstones and corruption against the exact manifest. |
| PI09-B2.2 | Sol 6.1 high; command outcome and restart | Estimate in-process failure fixtures, exact lookup/replay and restart with regressions. |
| PI09-B2.3 | Sol 6.1 high; fenced restore | Estimate current/pre-schema restore, failure injection and retained-data verification. |
| PI09-B2.4 | Sol 6.1 high; process recovery | Estimate real helper termination and final composed B2 gates, including review and exact-head CI. |
| T | Astra high; independent audit/consolidation | Re-estimate before A/B admission; preserve weekly mandatory reserve once. Initial 4-point R-only placeholder is not a full-delivery T estimate. |

No numeric A/B implementation allocation is invented before bounded comparable
samples/actual dispatch context. With fewer than three complete comparable
model/effort/work-class samples, use the largest relevant integrated observed
delta plus explicit uncertainty. If none exists, label estimates uncalibrated and
use a conservative small checkpoint; an absent weekly sample is not zero cost.
Never infer Astra consumption from Sol or use person-hours as quota conversion.

## Measurement, stop and privacy

Read both live windows at entry, before commit and at closeout; recheck sooner
on runner failure, broader scope or unsafe estimate. Shared-account coarse deltas
include concurrent work and are not precise task attribution. Keep original
baseline across tool/model changes. Verify actual reset evidence and segment
deltas at a reset; timestamp drift alone is not a reset. No reset is presumed.

When a remaining whole slice plus closeout/uncertainty no longer fits, stop new
research/implementation and report a coherent checkpoint. Preserve required tests
and report a blocker rather than weaken assertions, increase normative timeouts
or expand the four-file scope. Reserve closeout before reaching the ceiling;
at the ceiling stop work rather than knowingly overspend to finish a commit.
A blocked command permits a bounded local alternative, not repeated exploration
or unauthorized remote publication.

Only methods and estimates belong in Git. Personal percentages, account IDs,
reset times and private ledger values stay session-only for this request: its
four-file write scope forbids updating the ignored automation ledger. Report
measured start/end deltas and limitations in the handoff; do not claim a persisted
ledger. Quota compliance is separate from technical acceptance and remote CI.

## A2b bounded-replan admission and sequential accounting

Request `pi09-a2b-bounded-replan-20261104`, baseline commit
`88d27472b927baf90cdd6d69488f74cdd9ee80cd`, requests Astra high for this docs-only
replan. Its target is **at most 18 incremental five-hour points and 8 weekly
points**, including evidence review, editing, review, checks, commit, handoff and
delayed accounting. This is separate from the historical R envelope above and
admits no implementation. Retain the supplied live baseline in session; entry
measurement does not reset it. Check near 60% total five-hour use, before commit
and at closeout. Before edits, reserve enough for a clean reviewed commit; if it
cannot fit, stop without edits. The global stop is **95% in each window**,
including parent work, other commitments and delayed accounting. No reset credits,
API billing, bought credits or presumed reset capacity.

The [three sequential manifests](pi-09-plan.md#a2b-checkpoint-rules) partition
A2b's work, not the A2 sprint ceiling. A2a actuals plus all A2b checkpoint actuals
still count against the inherited A2 ceiling (<=1.9 B5). The A2b parent has no
additional implementation or generic CI allowance. Replan actuals are recorded
once as replan/parent work; do not copy them into every child. Personal readings
and account data remain session-only; no status JSON or automation ledger edit.

Before each dispatch, record the exact accepted dependency SHA, file manifest,
test selection and runner, then independent B5/BW lower/upper estimates for:

| Integrated component, counted once | Dispatch-specific scope |
| --- | --- |
| Implementation and fixtures | Only that checkpoint's exact manifest and acceptance criteria. |
| Verification and repair | Focused tests plus required inherited/common gates, runner setup, CI diagnosis if authorized; preserve PI-08 exceptions. |
| Parent review/integration | Manifest/diff review and acceptance of this slice, including any authorized CI observation. |
| Closeout and uncertainty | Commit, handoff, delayed accounting and bounded runner/fix risk. |

Sum these components once per window and admit the upper bound. Parent-run checks
and child-run checks must be assigned to one executor/cost entry each; one CI run
is not charged again as parent CI. Necessary later regression runs are new work
in the later slice, not a second charge for the earlier run. Do not schedule a
redundant A2b umbrella integration job; final composition verification belongs to
PI09-A2b.3. Remote exact-head CI remains separately evidenced, never inferred
from local checks or assumed green without a run.

At dispatch, apply both live-window inequalities above using this integrated
checkpoint estimate instead of historical R cost. Retain outstanding commitments,
remaining mandatory weekly T reserve and delayed usage exactly once. Re-estimate
that T reserve for delivery; the old four-point R placeholder is not sufficient
evidence of full-delivery coverage. Later A2b slices receive fresh admission only
after their prerequisite is accepted; reserve no speculative future five-hour
windows and invent no numeric implementation allocation from this docs replan.

With fewer than three comparable complete samples, use the largest relevant
observed integrated delta plus explicit uncertainty; otherwise mark the estimate
uncalibrated. Missing weekly data is not zero. The first eligible A2b dispatch
under that replan was **PI09-A2b.1**, exactly the two repository files and five
named focused tests in the plan, plus common regression gates. This was
eligibility, not quota admission.
If even that complete checkpoint cannot fit with closeout, wait for verified
capacity. If PI09-A2b.3 later cannot fit, replan inert prerequisites before writing;
never activate a subset of its schema/owner/gates/public-port boundary.

## B2 bounded-replan admission and sequential accounting

Request `pi09-b2-bounded-replan-20261106`, baseline
`28b97bf41587a3e0a1158d7ec389ae60eb076831`, requests `gpt-6.1-sol` high for this
docs-only R-class replan. This request-specific routing leaves the standing
PI-09 R/T routing in place. This job has hard additional ceilings of **88 B5
points and 20 BW points** to the 95% boundary, including parent review,
integration, publication, CI/accounting and closeout. Use the tighter of those
ceilings and current global availability after outstanding commitments and
mandatory weekly reserves. Exactly five points of each global window stay free
at the boundary, reserved once across the PI.

| Replan component, integrated once | B5 points | BW points |
| --- | ---: | ---: |
| Bounded evidence review and two-file replan | 10–18 | 2–4 |
| Parent review and integration | 4–7 | 1–2 |
| Local validation, commit and handoff | 4–6 | 1–2 |
| Publication/CI allowance, delayed accounting and uncertainty | 6–9 | 2 |
| **Integrated planning range** | **24–40** | **6–10** |

Use the upper bound for initial planning. These independent estimates are
uncalibrated and separate from the original R and A2b replan envelopes. The
publication/CI allowance accounts for any separately authorized parent work.
This replan ends at the local commit and handoff. It has no push or remote
CI run. Do not treat the allowance as an actual, a second parent charge or an
implementation allocation.

Read both live windows at entry, before commit and at closeout. Reforecast
near 35%, 60% and 80% of either replan planning bound consumed, and when live
global usage crosses those marks. If a global mark has already passed at entry,
calibrate immediately from the live reading and retain the original baseline.
At each check, compare measured deltas and remaining integrated work, including
parent commitments and delayed usage. At 80% revalidate scope and the clean
validation/commit/handoff path before proceeding. Before edits, reserve that
complete path. Stop before either global 95% ceiling, the job's incremental
ceilings or the point where a complete clean replan plus closeout no longer
fits. Report the precise blocker and any reviewable incomplete draft as such.
Use no reset credits, paid credits or assumed future capacity. Keep personal
readings session-only and write no private accounting artifact.

The [B2 manifests and acceptance gates](pi-09-plan.md#b2-complete-history-queries-uncertainty-and-recovery)
define the sequential implementation boundary. PI09-B2.1 is the first eligible
dispatch after parent acceptance of this replan. Eligibility does not admit it
against quota. Before every dispatch, record the accepted prerequisite SHA,
exact manifest, required tests and runner, then independently estimate B5/BW
using the integrated components above for A2b, scoped to that B2 checkpoint.
Use the upper estimate in both live-window inequalities, retain outstanding
commitments and re-estimate the protected mandatory weekly T reserve before
implementation. Apply the comparable-sample rule above and keep the inherited
1.9 B5 sprint ceiling across B1/B2 work. If a full checkpoint and closeout will
not fit, stop before writing. Preserve the acceptance criteria and common gates.

Charge replan, each implementation checkpoint and necessary later regression
runs once each. Assign parent/child verification and exact-head CI runs to one
executor and cost entry per run. PI09-B2.4 owns the final composition checks.
No extra B2 umbrella integration or CI allowance. Independent T stays reserved
weekly until admitted after B2 acceptance.
