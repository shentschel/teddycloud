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

- [PI-02 execution plan](pi-02-plan.md)
- [PI-02 contract inventory](pi-02-contract-inventory.md)
- [PI-02 budget method](pi-02-budget.md)
- [PI-02 review](pi-02-review.md)
- [Pinned Web UI/plugin seam checkpoint](pi-02-webui-seam.md)

- [PI-00 plan](pi-00-plan.md)
- [PI-00 budget method](pi-00-budget.md)
- [PI-00 review](pi-00-review.md)
- [Refined PI-01 charter](pi-01-architecture-charter.md)
- [PI-01 execution plan](pi-01-plan.md)
- [PI-01 budget method](pi-01-budget.md)
- [PI-01 review](pi-01-review.md)
- [PI-01 reference environment](pi-01-reference-environment.md)
- [Gateway sequencing ADR](../adr/0001-control-plane-and-gateway-sequencing.md)
- [Technology/topology ADR](../adr/0002-technology-and-repository-topology.md)
- [Persistence/recovery ADR](../adr/0003-persistence-and-projection-recovery.md)
- [Issue-ready tasks and findings](pi-01-backlog.md)

GitHub Issues are enabled in this fork. The [PI-01 backlog](pi-01-backlog.md)
links the four open findings; the [PI-02 plan](pi-02-plan.md) links its three
next sprint tasks. Search by stable ID before creating any further issue.
Run `python3 scripts/check_architecture_docs.py` before publishing architecture
changes. The path-scoped documentation workflow runs the same check without
creating release artifacts.

## Local automation state

The optional six-hour automation stores the current quota-window identifiers and PI
status in the repository-local `.pi-automation-state.json`. The file survives a
host restart but is intentionally ignored by Git because it belongs to one Codex
host. It resumes the next unfinished idempotent sprint whenever both current
windows have safe capacity. An exhausted window sets `waiting_budget`; a verified
reset resumes that same work instead of skipping to a new PI. Reset timestamps
may drift by seconds, so a timestamp difference alone is not treated as proof of
a new weekly window. The fixed six-hour cadence checks each five-hour reset no
later than one cycle afterwards and is not changed by the automation itself. A
queued dispatch has a stable request ID and is not sent again while a matching
run is active or unresolved. A queued handoff is only a request: on a later run
with the correct model, check the actual task and repository state, then execute
the same request ID when no work has started. A completed sprint may be followed
by the next admitted sprint within the same run and quota week.
