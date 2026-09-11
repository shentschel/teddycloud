# PI-01 refinement: architecture charter

Status: prepared for PI-00 refinement
Candidate milestone: architecture charter approved

## Objective

Define the product boundary, target users, success measures and non-goals for
TeddyCloud Next so later architecture decisions can be evaluated consistently.

## Non-goals

- Implement production runtime code.
- Replace the existing C protocol gateway.
- Freeze low-level APIs or database tables.
- Migrate production TeddyCloud data.

## Inputs

- [Greenfield rewrite exploration](../greenfield-rewrite-exploration.md)
- [Set-Tonie metadata exploration](../set-tonie-metadata-exploration.md)
- [UI plugin extension exploration](../ui-plugin-extension-exploration.md)
- [Current-system dependency matrix](../current-system-dependency-matrix.md)
- [PI roadmap](../teddycloud-next-pi-roadmap.md)

## Refinement questions

1. Which current TeddyCloud hardware generations and workflows are mandatory?
2. Is the first product boundary a control plane using the legacy gateway?
3. Which plugins become core modules, UI extensions or isolated workers?
4. Which compatibility APIs must remain stable during migration?
5. What are the measurable data-loss, lookup-correctness and performance goals?
6. Which decisions require upstream coordination or licensing review?

## Proposed sprint outcomes

### R — refinement/exploration

- Confirm stakeholders, mandatory workflows and explicit exclusions.
- Prioritize risks and unresolved assumptions.
- Produce acceptance criteria for the two delivery slices.

### D1 — system context and boundaries

- Create a system context diagram.
- Define ownership boundaries for gateway, core, UI and workers.
- Record data and event flows and external dependencies.

### D2 — foundational ADRs

- Decide control-plane-first versus complete rewrite.
- Decide repository topology and supported deployment baseline.
- Record database/file ownership and compatibility strategy.

### T — technical debt/refactoring

- Normalize terminology across architecture documents.
- Remove contradictory estimates or assumptions.
- Link every deferred architectural concern to an issue.

## Acceptance criteria

- Product scope and non-goals are explicit.
- Every current plugin has one target ownership category.
- Hardware and API compatibility boundaries are documented.
- At least the control-plane sequencing decision is an accepted ADR.
- Risks have owners or linked follow-up issues.
- The next PI can be planned without reopening settled PI-01 decisions.

## Verification

- Review all relative links and Markdown formatting.
- Compare component ownership with the dependency matrix.
- Check that no ADR silently authorizes production migration or destructive work.
- Run repository documentation checks available in CI.

## Rollback and safety

This PI changes documentation only. Reverting its commit restores the previous
state; it must not alter a running TeddyCloud installation.

