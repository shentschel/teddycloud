# TeddyCloud Next PI execution

This folder contains refinements, quota records and completion reports for the
[PI roadmap](../teddycloud-next-pi-roadmap.md).

## Required files per PI

```text
pi-NN-plan.md
pi-NN-budget.md
pi-NN-review.md
```

- `plan`: admitted sprints, milestone, dependencies and acceptance criteria.
- `budget`: measurement method, sample quality and admission; personal before/after
  usage snapshots stay in the ignored local automation state.
- `review`: delivered outcome, tests, debt and replanning decision.

The first run uses [PI-00](pi-00-quota-calibration.md) to calibrate Codex Plus
quota consumption. No fixed conversion between five-hour and weekly usage may be
assumed.

Current execution artifacts:

- [PI-00 plan](pi-00-plan.md)
- [PI-00 budget method](pi-00-budget.md)
- [PI-00 review](pi-00-review.md)
- [Refined PI-01 charter](pi-01-architecture-charter.md)
- [Issue-ready tasks and findings](pi-01-backlog.md)

GitHub Issues are currently disabled in this fork. Until enabled, the backlog
provides stable local task IDs; these must not be reported as published issues.

## Local automation state

The optional daily automation stores the current weekly-window identifier and PI
status in the repository-local `.pi-automation-state.json`. The file survives a
host restart but is intentionally ignored by Git because it belongs to one Codex
host. A changed weekly `resetsAt` value starts the next PI; an unchanged completed
window remains silent, while an unchanged `in_progress` window may resume only
unfinished, idempotent work.
