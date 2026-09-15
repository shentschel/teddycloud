# PI-02 budget and admission

Status: R executed; A/B/T require fresh admission

Use the [measurement method](pi-00-budget.md) within the same current weekly
allowance as PI-01. The 1.9 B5 sprint cap and both 5% reserves remain in force.
Exact account usage and reset identifiers belong to the ignored local state.

R starts from the previous Astra refinement observation plus 50% uncertainty.
Record its actual start when execution begins, excluding queued handoff time.
Account-wide deltas are rounded observations, not exact per-task costs.

Reserve T from the larger recent Sol debt sample plus 50% uncertainty. A/B
fixture engineering differs from documentation work: admit a small first slice
with an explicit cap, check usage after its first meaningful test and record a
new sample. Do not spend T's capacity on extending the optional corpus.

If a model handoff remains queued, say so. Do not treat dispatch success as
execution, and do not wait for a six-hour tick once the matching model is active
and the current slice has safe capacity. A milestone label does not reset quota.
