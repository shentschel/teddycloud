# PI-10 budget and admission

Status: this R only; no A/B/T admission or milestone completion.
Request `pi10-r-library-query-pagination-20261114`, basis
`b75799cf767b00c5717d7ad20fdd8f42f61266d6`, requested
`gpt-6-astra`, high. Read with the [plan](pi-10-plan.md),
[active 95% policy](../teddycloud-next-pi-roadmap.md#admission-by-window-active-policy)
and [routing](../task-model-routing.md).

## Whole-job ceiling and active policy

The integrated R ceiling is **68 additional five-hour percentage points** and
**19 additional weekly percentage points**. Both include worker reading/writing,
parent orchestration/review, corrections, checks, commit, parent publication,
exact-head CI observation/repair if authorized, final measurement and delayed
accounting. These are independent units, not time estimates, remaining capacity,
a target to spend or a conversion ratio. One B5 is 100 five-hour points; the
inherited <=1.9-B5 sprint ceiling still applies to later admitted sprints.

Protect exactly **5 global percentage points in each window after all closeout
and delayed charges**: planned consumption cannot exceed 95%. More unused
capacity is welcome; five is the safety margin, not an instruction to consume
the rest. No second per-child 5% margin, no reserve calculated from remaining
capacity, no presumed reset, reset credit, paid/API credit or model-based quota
renewal. Live availability can be strictly smaller than 68/19.

This request admits only R. There is **no flat reserve for later A/B/T jobs**,
no inherited PI-09 four-point T placeholder and no speculative future five-hour
allowance. Estimate and admit every next complete job separately. This does not
release already-admitted mandatory weekly commitments elsewhere: the active
roadmap policy still protects those, exactly once. When later delivery scope is
admitted, enumerate its actual remaining mandatory refinement/audit obligations
and estimate them explicitly; do not silently drop T or reserve unadmitted work.

For each window w, retain the original job baseline and apply:

```text
job_spent[w] + integrated_remaining_R[w] <= job_cap[w]       # 68 / 19
live_used[w] + integrated_remaining_R[w]
             + other_outstanding_commitments[w] <= 95
```

integrated_remaining_R includes remaining parent/CI/closeout and conservative
not-yet-posted charges. other_outstanding_commitments excludes everything already
inside that estimate, and includes actual admitted mandatory weekly obligations
where applicable. Known incurred work awaiting posting is counted once in the
forecast, then transferred to actuals when observed. A run executed by the parent
is not charged again as worker CI; necessary later reruns are distinct runs.

## Initial integrated estimate and calibration quality

| Component, counted once | Five-hour points | Weekly points |
| --- | ---: | ---: |
| Evidence, contract and coherent A/B/T manifests | 20-28 | 4-6 |
| Parent review and bounded corrections | 5-8 | 1-2 |
| Local checks, fixes, one commit and handoff | 5-8 | 1-2 |
| Parent publication and exact-head CI work | 4-6 | 1-2 |
| Conservative uncertainty, final measurement and delayed posting | 3-5 | 1-2 |
| **Integrated range** | **37-55** | **8-14** |

These are planning estimates, not measurements or a spend entitlement. Admit the
upper bound only when both live inequalities hold. They already include parent
review and CI; adding another blanket parent/CI allowance would double count.
No push is authorized for the worker; the publication line budgets the parent's
later authorized action and does not assert it has occurred.

The parent supplied the last comparable **PI-09/R** aggregate measurement through
the orchestration context; PI-09/A2b and B2 replan costs are not substitutes. The
sample covers the same Astra/high refinement class through parent review,
publication and exact-head CI, but does not separately evidence its complete
delayed-accounting tail. Its private percentages remain only in the ignored local
status and are intentionally not copied here. It falls inside this R's integrated
planning range; because it is a single sample with an incomplete tail split, the
upper estimate and explicit uncertainty remain unchanged. This is a calibration
limitation, not an unresolved Library semantic choice or permission to exceed a
live bound.

When that sample is available, use it once as an integrated comparator. If its
parent/CI work is already included, do not add those components again; if coverage
is partial, identify and estimate only missing work. One sample is not a reliable
upper bound: preserve explicit uncertainty. With fewer than three comparable
complete samples use the largest relevant complete observation plus uncertainty;
with three use the roadmap's comparable-sample policy and observed dispersion.
Never substitute five-hour deltas for missing weekly data or map Sol cost to
Astra. An observed larger cost replaces the forecast, not the original baseline.

## Calibration checkpoints and actual stop rule

Measure live windows at entry, before edits, before commit and at worker handoff;
parent measures before publication, after exact-head CI and after delayed posting.
Keep observations session-only. At approximately **35%, 60%, 80% of either
68/19-point job ceiling**, compare attributed/coarsely observed consumption with
remaining integrated work; thresholds are 23.8/40.8/54.4 five-hour points and
6.65/11.4/15.2 weekly points. They are calibration triggers, not automatic stops,
not percentages of the remaining window and not global-use milestones. Recheck
sooner for scope surprises, failed runners, CI repair or concurrent account use.

At each check retain complete validation, parent review, commit/publication/CI,
final measurement and conservative tail in the forecast. At 80% explicitly verify
that the remaining full R still fits; do not deliberately spend to reach a mark.
Stop new work when current use plus conservative tail and remaining complete
closeout would exceed either global 95% boundary, when either integrated job cap
would be exceeded, or when a clean complete R is no longer credible. Do not stop
merely because an early estimate or calibration mark has been reached if the
whole corrected forecast remains safe. Do not weaken tests/semantics or create
an artificial micro-checkpoint to describe incomplete R as complete.

If safe completion becomes impossible, report exact finished/unverified scope
and a reviewable incomplete result; do not knowingly overspend to make a commit.
Missing live usage is unknown, not zero. Concurrent shared-account activity and
coarse percentages limit attribution; conservatively include unexplained growth.
At a verified window reset segment consumption and retain total job cost; no
counter discontinuity silently resets the job's 68/19 ceiling. Small reset-time
metadata drift is not reset evidence.

## Later jobs and final accounting

| Next job | Independent estimate before admission |
| --- | --- |
| A | Entire index/prepare/lifecycle/cursor manifest, bounded integrity and capacity fixtures, existing regressions, parent review, publication/CI, corrections and tail. Connection/fence work is not a free prerequisite. |
| B | Entire normalization/sort/filter/index manifest, complete shape oracle and byte/cursor regressions, timing baseline, review/CI and closeout. |
| T | Complete large-library profiling matrix, tracing/instrumentation, measured fixes if any, independent composition review and exact-head CI. Include cold preparation and runner qualification. |

Use each accepted predecessor's exact head and actual test/runtime evidence to
estimate the next whole job. No numeric A/B/T quota is awarded by R. Preserve
all required acceptance work even if a later job must wait for verified capacity.
No duplicate umbrella integration job: T owns composed milestone proof; R parent
review is part of this R. Count each check/CI execution and each correction once.

Only methods, job ceilings, estimates and evidence limitations belong in Git.
Personal usage percentages, account/reset metadata and prior private ledger
values do not. Do not read or write personal usage history or local status files.
Live tool readings requested for this job stay in this session/handoff; no ledger
is persisted. A worker-local commit can be technically ready while publication,
exact-head CI and integrated final accounting remain pending with the parent.
Never report the final global 5% margin or the full integrated actual as verified
until the parent has included its work and delayed posting. The worker reports
current readings/deltas, retained remainder and a forecast, not future actuals.
