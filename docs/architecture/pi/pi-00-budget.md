# PI-00 budget method and admission

Status: calibration in progress; no fixed PI-01 delivery capacity established

Account usage values, reset timestamps and host paths are private operational
data. Record snapshots in the ignored `.pi-automation-state.json`, not in this
public repository or public issue bodies. This document exposes only method,
sample quality and scope admission.

## Measurement rules

1. Record actual start/end observations for each sprint: time, model, requested
   effort, used percentages and reset identifiers for both quota windows.
2. Record actual effort only if verifiable; a requested setting is not proof.
3. Percentages are account-wide and rounded. Concurrent tasks and response
   overhead prevent exact task billing. A zero delta does not mean zero cost.
4. Subtract observations only within the same verified window. Across a reset,
   retain per-window segments; if a boundary sample is missing, mark the total
   unknown. A slightly changed reset timestamp alone does not prove a new window.
5. Keep at least 5% of weekly capacity unused and respect the sprint ceiling.
   Check admission against the remaining live allowance, including other work.
6. Keep separate Sol/Astra samples. Keep effort stable within a sample. A short
   refinement is not representative evidence for protocol or migration work.

## Admission decision

- R: admitted as bounded refinement; actual observations are local-only.
- T: required and reserved; perform a fresh preflight and route to Sol first.
- D: not admitted until R/T observations and remaining capacity are reviewed.
- PI-01: candidate backlog prepared; delivery count remains conditional.

One sample cannot establish a statistical confidence bound. After comparable
samples exist, use a conservative observed cost with a recorded uncertainty
allowance; do not convert elapsed hours or the number of open issues into quota.
Review again after three comparable samples or any model/effort change.
