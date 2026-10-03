# PI-07 budget and admission checkpoint

Status: provisional, uncalibrated design checkpoint; no delivery admitted.
Use [current routing](../task-model-routing.md): R/T Astra/high, A/B 6.1 Sol/high.
Five-hour and weekly percentage points are independent quota units, not hours.

| Work | Provisional integrated ceiling: five-hour / weekly points |
| --- | --- |
| R bounded checkpoint | 35 / 7, including agent ceiling 27 / 5 and parent/uncertainty 8 / 2 |
| A initial bounded checkpoint | 35 / 7 |
| B initial bounded checkpoint | 40 / 8 |
| T including #17 | 55 / 10 |

A/B numbers buy a reviewable checkpoint, not a promise to finish their entire
sprint. Re-estimate before dispatch from the largest comparable integrated
sample plus uncertainty until three usable model/work-class samples exist.
Do not use small codec samples to price filesystem durability work. Broader
scope requires renewed admission; each sprint remains at most 190 five-hour
points across real windows. A PI consumes at most 95 weekly points.

Admission: remaining five-hour capacity must cover current integrated work,
outstanding commitments and 5 points safety. Before final verification and
handoff, retain another 3 unspent points inside the integrated allowance.
Do not add parent costs twice. Future R/T costs are reserved only in the weekly
window: current integrated weekly estimate + outstanding work + remaining
mandatory R/T reserve + 5 points safety must fit. Future T reserve is 10 weekly
points; when T runs, count it as current work instead, including #17 once.

Sample both live windows before/after each checkpoint. The private orchestration
ledger owns actual observations, execution identifiers and reset times. Shared
account movement is not isolated agent usage. Missing readings are unknown, not
zero. No fixed weekly/five-hour conversion, API price proxy, reset credit or
future reset is assumed. Stop with coherent files before any tighter live or
authorized ceiling; incomplete acceptance remains explicitly open.
