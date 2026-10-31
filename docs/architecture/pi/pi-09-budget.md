# PI-09 budget and admission

Status: R-only contract complete; no A/B/T budget or milestone acceptance follows.
Request `pi09-r-assignment-semantics-contract-20261031`.
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
| A2a / A2b | Sol 6.1 high; staged schema / persistence activation | Re-estimate from A1 plus migration/owner/restore evidence, never copy A1 cost or omit activation tests. |
| B1 | Sol 6.1 high; pure URI admission | Include parser vectors, fuzzing, old-receipt compatibility and review. |
| B2 | Sol 6.1 high; history/recovery | Include real helper processes, failure diagnosis and retained-data verification; split before unsafe admission. |
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
