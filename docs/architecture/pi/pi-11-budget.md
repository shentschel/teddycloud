# PI-11 R budget and integrated admission

Status: method and bounded R admission only; no A/B/T spend or delivery approval.
Request `pi11-r-event-delivery-20261118`, base
`ac08326eaeaa7dee5c2414864977982ede0ff51d`, requested `gpt-6-astra`, high.
Read with the [plan](pi-11-plan.md), [contract](pi-11-event-delivery-contract.md)
and [active roadmap policy](../teddycloud-next-pi-roadmap.md#admission-by-window-active-policy).
No personal usage observation, account/reset metadata or usage history belongs
in Git. Session tool observations stay in the handoff only.

## Independent ceilings and complete-job forecast

Worker integrated ceiling: **+68 five-hour points and +50 weekly points**.
It includes research/evidence reads, all four documents, coherence review,
corrections, checks, local commit and handoff. Points are additional percentage
points in two independent windows, not percentages of remaining capacity, time
or a fixed five-hour-to-weekly conversion. These ceilings are not targets to spend.

The parent's review, publication, required exact-head CI observation/corrections,
final accounting and delayed posting must additionally fit the global **95%**
boundary in both windows. R does not authorize worker push or CI/workflow repair.
Count each component once: worker self-review is not parent independent review;
parent reruns are distinct work, but a single shared check run is not charged
twice. Parent-supplied reserved tail, if larger than this estimate, replaces it.
No release of other already-admitted commitments is implied.

| Component, counted once | Five-hour planning points | Weekly planning points |
| --- | ---: | ---: |
| Worker evidence and semantic decisions | 12-18 | 6-10 |
| Worker four docs and exhaustive owner/file/test integration | 14-20 | 8-12 |
| Worker coherence fixes, checks, commit and handoff | 7-11 | 4-7 |
| Worker uncertainty and not-yet-posted charges | 5-7 | 3-5 |
| **Worker forecast** | **38-56** | **21-34** |
| Parent independent review and bounded corrections | 4-6 | 2-4 |
| Parent publication and exact-head CI follow-through | 3-5 | 2-4 |
| Parent final accounting and conservative delayed tail | 2-3 | 1-2 |
| **Parent reserved planning tail** | **9-14** | **5-10** |
| **Integrated R forecast** | **47-70** | **26-44** |

These are conservative planning estimates, not measured cost or available account
capacity. No private predecessor sample is copied. Confidence is limited: no
complete same-class measured sample is established by this request. Recalibrate
from actual observations and remaining scope, without reducing mandatory gates.
The worker ceiling and global admission are separate checks; parent tail cannot
be spent as spare worker capacity. The integrated upper forecast can exceed 68
because the supplied 68 ceiling is worker-only; it cannot exceed global headroom.
If the parent instead applies an additional tighter whole-R ceiling, honor it.

For each window w, retain the original worker baseline:

```text
worker_observed_delta[w] + conservative_worker_remaining[w] <= worker_cap[w]
current_global_used[w] + conservative_worker_remaining[w]
                      + parent_remaining_tail[w]
                      + other_outstanding_commitments[w] <= 95
```

Caps are 68/50. Unposted incurred charges are included once in remaining work,
then transferred to observed cost when posted. Do not subtract them twice.
Concurrent shared-account growth is conservatively included in worker delta
unless independently attributable; coarse percentage readings do not prove exact
worker billing. Missing readings/unknown parent commitments are not zero. At
entry and before commit use current live tools; parent retains responsibility for
commitments not exposed to the worker and reconfirms them before publication.

The weekly other-commitments term includes remaining mandatory refinement/audit
obligations of already-admitted scope, once. No A/B/T delivery is admitted here,
so do not invent a flat future A/B/T reserve or borrow PI-09/10 numeric reserves.
The five-hour window covers executing work and outstanding current commitments,
not unadmitted future sprints. Preserve at least five global points after all
closeout/delayed charges; do not impose a second child-level five-point reserve.

## Checkpoints and hard stop

Observe both windows at entry, before substantive edits, before commit and at
handoff, and sooner after a scope/runner surprise. Parent observes before
publication, after required CI and after accounting follow-up. Calibration deltas
are **23.8 / 40.8 / 54.4 five-hour points** and **17.5 / 30 / 40 weekly points**.
They focus scope and re-estimation; reaching one does not automatically stop.
At 80% explicitly verify the complete remaining R, checks and tail still fit.

Hard stop new work if observed use plus conservative remaining worker work and
parent-reserved review/publication/exact-head-CI/accounting tail, plus actual
other commitments, would exceed 95% globally in either window; also stop before
exceeding 68/50 worker caps or when a complete clean R is no longer credible.
Do not weaken decisions, omit checks, conceal findings or commit an incomplete
refinement labelled complete to fit a budget. Report an incomplete reviewable
checkpoint if safe completion fails. No reset credits, paid/API credits or assumed
future resets. A real reset requires segmented measurement preserving cumulative
job cost; it does not replenish the worker job ceiling.

A checker failure may justify one bounded diagnosis in scope. Runtime/schema/
workflow fixes or repeatedly running expensive irrelevant suites are not R work.
A missing historical checker path is a documented limitation, not permission to
create another file. Parent exact-head CI is separate from these local checks.

## Later admission and accounting closeout

| Later job | Estimate the entire boundary before admission |
| --- | --- |
| A | Typed core, forward migration and every producer hook, existing schema dispatch changes, retention/cursors, observation ingress, lifecycle recovery, real owner regressions, parent review/CI/accounting. |
| B | Complete internal SSE and H/K reconciliation, authorization/isolation/partial writes, A regressions, parent review/CI/accounting. No implicit public route or auth work. |
| T | All composed failures, slow consumers, deterministic concurrency/race/repetition, cold recovery and profile measurement, independent review, exact-head CI and closeout. Findings requiring runtime repair get explicit readmission. |

No numeric A/B/T allotment follows from R. Use accepted predecessor SHA and
measured test/runtime evidence to estimate the next whole job; preserve the
roadmap sprint ceiling of 1.9 B5 where applicable. No duplicate integration job:
T owns milestone composition; parent R review belongs to this R tail.

Handoff reports observed worker delta, uncertainty and conservative remaining
parent tail compactly outside Git. It must not claim final global reserve or
integrated actual cost until parent work and delayed posting are accounted for.
A clean local commit is a reviewable R result, not publication/CI acceptance.
