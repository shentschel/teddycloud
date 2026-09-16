# PI-02 budget and admission

Status: R executed; A source-checkpoint slices admitted; B/T pending

Use the [measurement method](pi-00-budget.md) within the same current weekly
allowance as PI-01. The 1.9 B5 sprint cap and both 5% reserves remain in force.
Exact account usage and reset identifiers belong to the ignored local state.

R used the prior Astra refinement observation plus an uncertainty allowance.
Its actual start followed queued handoff time. Account-wide deltas are rounded
observations, not exact per-task costs.

Reserve T from the larger recent Sol debt sample plus 50% uncertainty. A/B
fixture engineering differs from documentation work: admit a small first slice
with an explicit cap, check usage after its first meaningful test and record a
new sample. Do not spend T's capacity on extending the optional corpus.

A's first source-fixture checkpoint overran its initial five-hour cap before
the agent was stopped; integration then corrected fixture syntax and shape.
Later checkpoints were independently bounded and published only after JSON,
link and CI checks. A checkpoint crossing a five-hour reset cannot be assigned
a valid before/after delta for that window. These are local measurement caveats,
not evidence of runtime compatibility. Admit B only after A's acceptance audit,
and admit B's first offline-harness slice conservatively rather than assuming
source-documentation cost predicts test engineering cost.

If a model handoff remains queued, say so. Do not treat dispatch success as
execution, and do not wait for a six-hour tick once the matching model is active
and the current slice has safe capacity. A milestone label does not reset quota.
