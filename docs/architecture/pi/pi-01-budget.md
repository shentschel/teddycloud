# PI-01 budget and admission

Status: R measured; B admitted with a spending cap; T reserved

Use the [PI-00 measurement rules](pi-00-budget.md). Personal usage and reset
timestamps stay in the ignored local automation state. The sprint limit is
1.9 B5; neither live quota window may consume its last 5%.

R uses the completed Astra refinement sample with a 50% uncertainty allowance.
T is reserved using the completed Sol debt sample with the same allowance.
These rounded account-wide observations are planning aids, not task invoices or
statistical bounds. The R sample includes read-only environment inspection.

A was completed in PI-00/A and incurs no second delivery reservation. Its review
against R is part of R. B is a first Astra architecture-delivery sample; it must
not inherit refinement cost as if it had been measured. Admit it with an explicit
local spending cap and intermediate checks; stop at a reviewable ADR checkpoint
if the remaining capacity cannot safely fund completion and T.

B was admitted after R completed and was published. Its first-sample cap and
the separate T allowance fit both live windows; exact observations remain local.
Check capacity after the ADR draft and before publication. Incomplete design
work remains incomplete when a cap requires a checkpoint.

After every completed slice, measure again and begin the next admitted slice if
safe. A six-hour timer is a wake-up mechanism, not a one-sprint-per-run limit.
Model handoff may be queued but must not be recorded as executed until there is
an actual start observation and work. Reuse its request ID on recovery.

## Calendar and milestone accounting

The weekly quota period remains the accounting boundary. PI-00 calibration and
PI-01 architecture are milestone labels, not evidence of a new weekly allowance.
Completing one milestone does not reset consumption or justify waiting for a
reset. Additional refined work can use the remaining current-week allowance,
with R/T reservations and the common 5% reserve intact. Continuations after a
real reset retain their unfinished sprint and per-window measurements.

The local state is authoritative for admission observations. Public plans record
the scope, sample quality and outcome only. A missed observation or a restart is
reported as uncertainty rather than reconstructed as precise consumption.
