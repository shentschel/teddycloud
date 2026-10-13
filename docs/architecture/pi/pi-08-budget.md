# PI-08 budget method

Status: bounded R closeout; delivery checkpoints need fresh admission.
Use the [active roadmap policy](../teddycloud-next-pi-roadmap.md#admission-by-window-active-policy)
and [model routing](../task-model-routing.md).

## Units, privacy and current refinement

Budgets are percentage points of separate quota windows, never person-hours.
B5 is one complete five-hour quota window; BW is one complete weekly window.
There is no fixed B5/BW conversion. Changing PI label, model or chat replenishes
neither window.

The task-authorized final R target is at most 28 additional five-hour percentage
points; make a hard checkpoint before 35 additional points. Count the full
slice from the agreed start, including earlier work, review, integration,
verification, CI handling and issue publication. A later tool snapshot does not
reset that baseline. The user's checkpoint takes precedence over a partial
session measurement. At the checkpoint, stop expanding analysis and finish the
reviewable set; never silently consume the five-point safety reserve.

No personal live usage values, account identifiers, reset times or private
ledger contents belong in Git or issue comments. Record only the method and
estimates here. Private observations stay in the existing ignored local state,
when writable; otherwise use session-only tracking and report the limitation.
This R does not claim a persisted private ledger when local tooling is unavailable.

## Admission and checkpoints

For each next whole bounded checkpoint, estimate implementation plus parent
integration, independent review, verification, publication and uncertainty once:

```text
five_hour_remaining >= integrated_next_job + outstanding_commitments + 5
weekly_remaining >= integrated_next_job_weekly
                    + outstanding_commitments_weekly
                    + remaining_mandatory_R_T_weekly + 5
```

Future T does not reserve capacity in an earlier five-hour window. Weekly
capacity does reserve remaining mandatory R/T for admitted scope. When that
job starts, move its estimate from future reserve into current work exactly
once. Sprint ceiling remains 1.9 B5; weekly planned consumption stays below
95%. The tighter user R ceiling governs this slice.

Read both windows at entry, before publication and at closeout. Check sooner
when a failure, scope change or the user's budget warning makes the estimate
unsafe. After the user-directed closeout checkpoint, only the three artifacts,
index update, docs/scope checks, commit, CI verification and evidence comment
remain authorized work in this slice. Do not open implementation or broaden
source research to fill spare capacity.

If a window resets, retain segmented deltas rather than subtracting across
resets; verify reset evidence, not timestamp drift alone. Concurrent account
activity and coarse percentages make observed deltas upper-bound shared usage,
not exact task attribution. Missing measurements mean unknown, never zero.

## Delivery calibration

A1, A2, B1, B2 and T are independently admitted checkpoints inside A/B/T.
Their [plan](pi-08-plan.md) gives exact scopes and acceptance; no cost claim is
made before model-specific observation. Use separate model, reasoning and work
class samples, including parent review and failed attempts. With fewer than
three comparable complete samples, use the largest relevant observed integrated
delta plus explicit uncertainty. Do not treat older Sol samples as measured
Sol 6.1 costs or Astra routing as a fresh quota pool.

Before dispatch, record an estimated interval for both windows and each
component: implementation, integration/review, verification/publication and
uncertainty. Unknown weekly conversion requires conservative independent
weekly admission, not a guessed fixed ratio. Missing safe capacity means reduce
to a reviewable checkpoint or wait; never remove rollback tests or mandatory T.

At target consumption, prioritize closeout. Before the hard R checkpoint,
record completed artifacts, remaining checks and exact blocker; do not expand
scope or claim acceptance if evidence is missing. A failing local process runner
gets one bounded attempt, then GitHub verification or an exact blocker report.
Do not spend the remaining allowance retrying unavailable process tools.

## Acceptance distinction

Quota compliance is not technical acceptance. R needs green documentation CI
for the published SHA and the plan's reviewed decision crosswalk. A/B/T need
their own implementation/recovery/audit evidence; none is supplied by R.
No budget numbers in this file are personal live measurements or observed
delivery costs.
