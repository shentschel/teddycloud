# PI-00 plan

Status: in progress; R refinement complete, T reserved, optional D not admitted
Milestone: a defensible, model-specific quota calibration and executable PI-01 scope

## Scope and dependencies

Follow [PI-00 calibration](pi-00-quota-calibration.md) and the
[model-routing policy](../task-model-routing.md). The user explicitly started R
after a weekly reset; the missed automatic start must not be backdated.

| Sprint | Model | Outcome | Dependency | State |
| --- | --- | --- | --- | --- |
| R | `gpt-6-astra` | Refined PI-01 decisions, acceptance criteria and executable backlog | Existing explorations and source baseline | Complete; issue publication deferred to F-05 |
| T | `gpt-5.6-sol` | Documentation consistency review and deferred-issue audit | R published; actual execution routed to Sol | Reserved, not started |
| D | `gpt-5.6-sol` | One accepted ADR through review, if bounded enough for Sol | Remaining capacity; R/T observations; record any override of PI-01/B routing | Not admitted |

The cap is 1.9 five-hour quota windows per sprint, with at least 5% weekly
capacity left unused. This is a ceiling, not a minimum amount of work to consume.
Personal observations remain in ignored local state; see [budget](pi-00-budget.md).
Missing Sol calibration prevents committing to a fixed PI-01 delivery capacity.

## R acceptance and verification

- Charter distinguishes required behavior, proposals and unknowns.
- All seven enhancement owners and mandatory workflows are mapped.
- Lookup, Set, Custom Card, extension and migration gates are observable.
- PI-01 R/A/B/T have outputs, dependencies, models and executable acceptance checks.
- Deferred findings have local task IDs and issue-ready descriptions.
- Relative links, ownership coverage and `git diff --check` pass.
- Source baseline and unverified live-system assumptions are explicit.

GitHub currently returns HTTP 410 because Issues are disabled. The
[backlog](pi-01-backlog.md) is the temporary task registry; no GitHub issue numbers
are invented. Issue publication is a separate unresolved setup item.

## Rollback

Documentation-only. Revert the published commit; preserve the ignored local
measurement history for accurate resumption.
