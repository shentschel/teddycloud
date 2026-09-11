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
- `budget`: usage snapshots before and after every sprint.
- `review`: delivered outcome, tests, debt and replanning decision.

The first run uses [PI-00](pi-00-quota-calibration.md) to calibrate Codex Plus
quota consumption. No fixed conversion between five-hour and weekly usage may be
assumed.

## Local automation state

The optional daily automation stores the current weekly-window identifier and PI
status in the repository-local `.pi-automation-state.json`. The file survives a
host restart but is intentionally ignored by Git because it belongs to one Codex
host. A changed weekly `resetsAt` value starts the next PI; an unchanged completed
window remains silent, while an unchanged `in_progress` window may resume only
unfinished, idempotent work.
