# PI-03 budget and admission

Status: R refined; A/B/T not admitted by this document

PI-03 follows the [PI-00 measurement method](pi-00-budget.md). Exact account
usage, reset identifiers and agent run IDs remain in ignored local automation
state. Public documentation records only scope and admission rules.

All four PI-03 tasks use `gpt-5.6-sol`. No task may exceed `1.9 B5`; at least 5%
of both the current five-hour and weekly windows remains unspent. R completion
does not admit A automatically, and a candidate PI label does not create quota.

## Admission order

1. R is this bounded documentation refinement. It creates no product scaffold.
2. Reserve a conservative T allowance before admitting A.
3. Admit A as one reviewable scaffold slice. Stop after manifests, dependency
   direction and local build evidence if CI work would cross its cap.
4. Recheck both live windows. Admit B only after A's clean-build contract is
   accepted; B may checkpoint after deterministic local gates before remote CI.
5. Preserve T capacity. T may operate on an accepted A/B checkpoint, but the
   milestone stays incomplete until all required evidence exists.

Tool bootstrap and uncached CI can consume materially more than documentation.
Treat A and B as uncalibrated engineering work: use a small initial cap, observe
both windows after the first clean dependency restore/build and do not infer cost
from PI-02's Python/C fixture work. Network or CI queue time is not completion.

## Checkpoint rules

Every stopped slice records changed files, passing/failing commands, unresolved
decisions and the next idempotent action. A generated tree, dispatched agent or
queued workflow is not completion. Resume the same stable ID after a real reset;
do not duplicate its issue or restart completed work.

No reset credits, purchased credits or API billing are authorized. Release,
deployment and production validation remain outside this PI.

