# PI-05 budget and admission

Status: **R/A complete; B transaction/repository checkpoint recorded; remaining B/T require fresh admission**

Personal usage snapshots and agent identifiers remain only in the ignored
automation state. Repository documentation records estimates and outcomes, not
account details.

## Routing and calibrated estimate

All PI-05 slices route to `gpt-5.6-sol` with high reasoning. Prior comparable
Sol work used roughly 11–22 percentage points of a five-hour window and 2–4
points of the weekly window. PI-05 includes unfamiliar driver and failure-mode
work, so admission uses conservative caps:

| Slice | Five-hour cap | Weekly cap | Notes |
|---|---:|---:|---|
| R | 20 pp | 4 pp | Refinement and failure model |
| A | 24 pp | 4 pp | Driver experiment and migrations |
| B | 28 pp | 5 pp | Transactions, repositories and restore |
| T | 22 pp | 4 pp | Audit and failure hardening |
| Integration per delivery slice | 8 pp | 2 pp | Parent review, fixes, CI |

These are admission ceilings, not promises or a conversion formula. The failed
R subagent consumed its bounded interval without producing files; the parent
recovered the refinement. That attempt is not treated as delivered velocity.

## Admission policy

- Keep at least five percent free in both live windows.
- Never exceed 1.9 five-hour budgets for any one sprint; a PI uses the weekly
  admission boundary, not a new quota bucket. This corrects the earlier wording
  that incorrectly applied the sprint ceiling to the whole PI.
- Recheck both windows after each completed and published slice.
- Admit A, then B, then T only when its cap, integration allowance, remaining
  PI work and uncertainty reserve fit.
- A driver choice without executable evidence is not completion.
- Interrupted work keeps a checkpoint and stable request ID; it is not started
  twice.
- Reset credits, purchased credits and API billing are excluded.

If the next slice does not fit, publish the current evidence and wait for the
next verified reset rather than shrinking acceptance criteria.

PI-05/A was executed after the prior five-hour reset with its 24/4 cap. The
client did not expose live account percentages during execution, so the run is
not added as an actual-velocity sample. Acceptance instead uses the published
implementation, local checks and green remote CI. B and T retain their original
conservative estimates.


A bounded B checkpoint implemented the transaction port and representative
Content repository without reducing B acceptance. Its account-window snapshot
is kept only in the ignored automation state. Each remaining bounded B delivery
requires a fresh admission; earlier delivery estimates are not reset or reused
as a claim of unused capacity.

## Remaining B calibration

Published B checkpoints are separate verified deliveries, not repeated completion
of the same work. Prior account observations include orchestration, integration
and tooling recovery; they do not isolate implementation cost. Missing snapshots
are not imputed as zero. The original B cap is therefore not described as an
unused allowance across all checkpoints. Remaining upgrade fencing and lifecycle
restore each receive a fresh bounded admission; T and integration remain reserved.
The five-hour and weekly windows are checked independently after publication.
