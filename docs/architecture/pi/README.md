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

- [PI-07 plan checkpoint](pi-07-plan.md)
- [PI-07 budget checkpoint](pi-07-budget.md)
- [PI-07 review checkpoint](pi-07-review.md)
- [PI-07 TAF storage contract checkpoint](pi-07-taf-storage-contract.md)
- [PI-07/A1 TAF domain checkpoint](pi-07-a1-domain-checkpoint.md)
- [PI-07/A2 qualified content filesystem checkpoint](pi-07-a2-contentfs-checkpoint.md)
- PI-07/R is independently accepted after resolving all five design gates.
  A is accepted; B, T and the PI milestone remain open.

- [PI-06 execution plan](pi-06-plan.md)
- [PI-06 budget method](pi-06-budget.md)
- [PI-06 review](pi-06-review.md)
- [PI-06 Tag Registry contract](pi-06-tag-registry-contract.md)
- [PI-06/A1 identity checkpoint](pi-06-a1-identity-checkpoint.md)
- [PI-06/A1 internal decision-ID checkpoint](pi-06-a1-decision-checkpoint.md)
- [PI-06/A1 history-count checkpoint](pi-06-a1-counts-checkpoint.md)
- [PI-06/A1 internal encoding refinement](pi-06-a1-encoding-refinement.md)
- [PI-06/A1 counter/writer partial checkpoint](pi-06-a1-encoder-checkpoint.md)
- [PI-06/A1 acceptance audit](pi-06-a1-acceptance-audit.md)
- [PI-06/A2 identity schema checkpoint](pi-06-a2-schema-checkpoint.md)

- [PI-05 execution plan](pi-05-plan.md)
- [PI-05 budget method](pi-05-budget.md)
- [PI-05 review](pi-05-review.md)
- [PI-05/A SQLite driver decision](pi-05-a-sqlite-driver.md)
- [PI-05/B backup checkpoint](pi-05-b-backup-checkpoint.md)
- [PI-05/B contention checkpoint](pi-05-b-contention-checkpoint.md)
- [PI-05/B restart and bounded-access checkpoint](pi-05-b-restart-checkpoint.md)
- [PI-05/B verified upgrade backup checkpoint](pi-05-b-upgrade-checkpoint.md)
- [PI-05/B closed/fenced restore lifecycle checkpoint](pi-05-b-restore-checkpoint.md)
- [PI-05/T failure checkpoint](pi-05-t-failure-checkpoint.md)
- [PI-05/T persistence and milestone audit](pi-05-t-audit.md)

- [PI-04 execution plan](pi-04-plan.md)
- [PI-04 budget method](pi-04-budget.md)
- [PI-04 review](pi-04-review.md)
- [PI-04/T domain and migration-safety audit](pi-04-t-audit.md)

- [PI-03 execution plan](pi-03-plan.md)
- [PI-03 budget method](pi-03-budget.md)
- [PI-03 review](pi-03-review.md)

- [PI-02 execution plan](pi-02-plan.md)
- [PI-02 contract inventory](pi-02-contract-inventory.md)
- [PI-02 budget method](pi-02-budget.md)
- [PI-02 review](pi-02-review.md)
- [Pinned Web UI/plugin seam checkpoint](pi-02-webui-seam.md)
- [Status and framing checkpoint](pi-02-status-framing.md)
- [Enhancement compatibility disposition](pi-02-enhancement-coverage.md)
- [PI-02/A acceptance audit](pi-02-a-acceptance-audit.md)
- [PI-02/T final evidence audit](pi-02-t-audit.md)

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
completed sprint tasks, and the [PI-03 plan](pi-03-plan.md) links its three
delivery tasks; R/A/B/T are complete according to its review. The
[PI-04 plan](pi-04-plan.md) links its completed A/B/T tasks as issues #11-#13.
The [PI-05 plan](pi-05-plan.md) links completed persistence slices as issues
#14-#16; its review accepts the database-only upgrade/rollback milestone.
The [PI-06 plan](pi-06-plan.md) links its A/B/T tasks as issues #19–#21.
The PI-06 review records R/A/B/T and the transactional Tag milestone accepted.
PI-07/R design is accepted; delivery tasks are issues #22–#24 and #17 remains
the nested missing-media audit.
Search by stable ID before creating any further issue.
Run `python3 scripts/check_architecture_docs.py` before publishing architecture
changes. The path-scoped documentation workflow runs the same check without
creating release artifacts.

## Local automation state

The optional six-hour automation stores the current quota-window identifiers and PI
status in the repository-local `.pi-automation-state.json`. The file survives a
host restart but is intentionally ignored by Git because it belongs to one Codex
host. It resumes the next unfinished idempotent sprint whenever both current
windows have safe capacity under the roadmap's active window-specific admission
policy: future R/T reserves are weekly only; the five-hour gate includes current
work, integration, uncertainty and five-percent safety. Older budget text that
held future R/T capacity in each five-hour window is superseded by this policy. An exhausted window sets `waiting_budget`; a verified
reset resumes that same work instead of skipping to a new PI. Reset timestamps
may drift by seconds, so a timestamp difference alone is not treated as proof of
a new weekly window. The fixed six-hour cadence checks each five-hour reset no
later than one cycle afterwards while weekly capacity permits. When weekly
capacity cannot cover a safely completable task plus reserves, the same automation
moves to shortly after the actual weekly reset; after verifying the reset it
returns to six-hour cadence. Schedule changes require successful tool verification. A
queued dispatch has a stable request ID and is not sent again while a matching
run is active or unresolved. A queued handoff is only a request: on a later run
with the correct model, check the actual task and repository state, then execute
the same request ID when no work has started. A completed sprint may be followed
by the next admitted sprint within the same run and quota week.

Open persistence follow-up: [PI-07/T-01 missing external blob evidence](https://github.com/shentschel/teddycloud/issues/17).
[F-PERSIST-01 foreign-key snapshot validation](https://github.com/shentschel/teddycloud/issues/18)
is accepted in the PI-06 review and is a completed prerequisite, not repeat work.
