# PI-00 budget method and admission

Status: initial R/T and Sol delivery calibration complete

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

- R: completed as bounded Astra refinement; actual observations are local-only.
- T: completed as bounded Sol documentation debt work; actual observations are
  local-only.
- A: completed as a bounded system-context and ownership documentation slice. It
  is the first Sol delivery sample and does not accept the Astra-routed
  foundational ADRs.
- PI-01: candidate backlog prepared. Admit R and reserve T first; admit at most
one delivery slice at a time after a fresh capacity check.

PI-01/R is the next dependency-safe candidate. It remains an Astra refinement;
its admission uses the local Astra R observation plus the temporary uncertainty
allowance and reserves PI-01/T before any delivery work.

One sample cannot establish a statistical confidence bound. After comparable
samples exist, use a conservative observed cost with a recorded uncertainty
allowance; do not convert elapsed hours or the number of open issues into quota.
Review again after three comparable samples or any model/effort change.

For the next matching R or T task, the local planner uses the relevant observed
weekly delta plus a 50% uncertainty allowance, rounded upward. This temporary
allowance is replaced after three comparable samples. No delivery task inherits
an R/T estimate merely because it uses the same model.
